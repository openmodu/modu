package grep

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

const defaultGrepLimit = 250

var vcsDirectoriesToExclude = []string{".git", ".svn", ".hg", ".bzr", ".jj", ".sl"}

const (
	outputModeContent          = "content"
	outputModeFilesWithMatches = "files_with_matches"
	outputModeCount            = "count"
)

type grepOptions struct {
	outputMode  string
	ignoreCase  bool
	literal     bool
	beforeLines int
	afterLines  int
	lineNumbers bool
	limit       int
	unlimited   bool
	offset      int
	fileType    string
	multiline   bool
}

// GrepTool implements the content search tool.
type GrepTool struct {
	cwd       string
	artifacts *common.ArtifactStore
}

func NewTool(cwd string) types.Tool {
	return &GrepTool{cwd: cwd}
}

func NewToolWithArtifacts(cwd string, artifacts *common.ArtifactStore) types.Tool {
	return &GrepTool{cwd: cwd, artifacts: artifacts}
}

func (t *GrepTool) Name() string  { return "grep" }
func (t *GrepTool) Label() string { return "Search Content" }
func (t *GrepTool) Description() string {
	return `Search file contents using regex patterns.

Usage:
- Use this tool for content search; prefer it over running grep, rg, awk, or sed through bash.
- Uses ripgrep (rg) when available and falls back to a built-in implementation.
- Searches hidden files while excluding VCS directories such as .git and .hg.
- Respects .gitignore when using ripgrep. The built-in fallback skips only VCS metadata directories.
- Defaults to output_mode="files_with_matches"; use output_mode="content" for matching lines or "count" for per-file counts.
- files_with_matches results are sorted by modification time, newest first.
- Long matching lines are capped at 500 columns to avoid base64 or minified content flooding the context.
- Use path, glob, type, head_limit, and offset to narrow or page broad searches. Glob accepts space- or comma-separated patterns.
- head_limit defaults to 250 across output modes. Pass head_limit=0 only when you need unlimited output.
- The optional path must exist and may be a file or directory.
- Matched paths under the working directory are shown relative to the working directory, even when path narrows the search.
- The -i, -n, -A, -B, -C, head_limit, offset, type, and multiline parameters are accepted for Claude Code compatibility.
- Boolean strings "true" and "false" are accepted for -i, -n, literal, and multiline; numeric strings such as "10" are accepted for context, -A, -B, -C, head_limit, and offset.
- Use literal=true when searching for an exact string rather than a regex.`
}

func (t *GrepTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory or file to search in (default: cwd)",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "Glob pattern(s) to filter files (e.g., '*.go', '*.{ts,tsx}', '*.go,*.md')",
			},
			"output_mode": map[string]any{
				"type":        "string",
				"enum":        []string{outputModeContent, outputModeFilesWithMatches, outputModeCount},
				"description": "Output mode: content shows matching lines, files_with_matches shows only matching file paths, count shows per-file match counts.",
			},
			"ignore_case": map[string]any{
				"anyOf":       semanticBooleanSchema(),
				"description": "Case insensitive search",
			},
			"-i": map[string]any{
				"anyOf":       semanticBooleanSchema(),
				"description": "Alias for ignore_case, accepted for compatibility.",
			},
			"-n": map[string]any{
				"anyOf":       semanticBooleanSchema(),
				"description": "Show line numbers when output_mode is content. Defaults to true.",
			},
			"literal": map[string]any{
				"anyOf":       semanticBooleanSchema(),
				"description": "Treat pattern as literal string, not regex. Boolean strings \"true\" and \"false\" are accepted for Claude Code compatibility.",
			},
			"context": map[string]any{
				"anyOf":       semanticIntegerSchema(),
				"description": "Number of context lines before and after each match",
			},
			"-C": map[string]any{
				"anyOf":       semanticIntegerSchema(),
				"description": "Alias for context, accepted for compatibility.",
			},
			"-B": map[string]any{
				"anyOf":       semanticIntegerSchema(),
				"description": "Number of lines to show before each match when output_mode is content.",
			},
			"-A": map[string]any{
				"anyOf":       semanticIntegerSchema(),
				"description": "Number of lines to show after each match when output_mode is content.",
			},
			"limit": map[string]any{
				"anyOf":       semanticIntegerSchema(),
				"description": "Maximum number of results (default 250; 0 means unlimited)",
			},
			"head_limit": map[string]any{
				"anyOf":       semanticIntegerSchema(),
				"description": "Alias for limit, accepted for compatibility. Defaults to 250; 0 means unlimited.",
			},
			"offset": map[string]any{
				"anyOf":       semanticIntegerSchema(),
				"description": "Number of results to skip before applying limit.",
				"minimum":     0,
			},
			"type": map[string]any{
				"type":        "string",
				"description": "File type to search, equivalent to ripgrep --type for common types.",
			},
			"multiline": map[string]any{
				"anyOf":       semanticBooleanSchema(),
				"description": "Enable multiline regex mode.",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return common.ErrorResult("pattern is required"), nil
	}

	searchPath := t.cwd
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = common.ResolveToCwd(p, t.cwd)
	}
	if _, err := os.Stat(searchPath); err != nil {
		if os.IsNotExist(err) {
			return common.ErrorResult(fmt.Sprintf("path does not exist: %s", searchPath)), nil
		}
		return common.ErrorResult(fmt.Sprintf("failed to stat path: %v", err)), nil
	}

	glob, _ := args["glob"].(string)
	globPatterns := parseGrepGlobPatterns(glob)
	opts, errResult := parseGrepOptions(args)
	if errResult != nil {
		return *errResult, nil
	}

	// Try ripgrep first
	if rgPath, err := exec.LookPath("rg"); err == nil {
		return t.executeRipgrep(ctx, rgPath, pattern, searchPath, globPatterns, opts, toolCallID)
	}

	// Fallback to built-in
	return t.executeBuiltin(ctx, pattern, searchPath, globPatterns, opts, toolCallID)
}

func parseGrepOptions(args map[string]any) (grepOptions, *types.ToolResult) {
	opts := grepOptions{
		outputMode:  outputModeFilesWithMatches,
		lineNumbers: true,
		limit:       defaultGrepLimit,
	}

	mode, _ := args["output_mode"].(string)
	if mode == "" {
		mode = outputModeFilesWithMatches
	}
	switch mode {
	case outputModeContent, outputModeFilesWithMatches, outputModeCount:
		opts.outputMode = mode
	default:
		result := common.ErrorResult(fmt.Sprintf("invalid output_mode %q. Use content, files_with_matches, or count.", mode))
		return opts, &result
	}

	opts.ignoreCase, _ = common.ToSemanticBool(args["ignore_case"])
	if !opts.ignoreCase {
		opts.ignoreCase, _ = common.ToSemanticBool(args["-i"])
	}
	opts.literal, _ = common.ToSemanticBool(args["literal"])
	opts.fileType, _ = args["type"].(string)
	opts.multiline, _ = common.ToSemanticBool(args["multiline"])
	if v, ok := args["-n"]; ok {
		lineNumbers, ok := common.ToSemanticBool(v)
		if ok {
			opts.lineNumbers = lineNumbers
		}
	}

	contextLines := 0
	if v, ok := args["context"]; ok {
		contextLines, _ = common.ToSemanticInt(v)
	} else if v, ok := args["-C"]; ok {
		contextLines, _ = common.ToSemanticInt(v)
	}
	opts.beforeLines = contextLines
	opts.afterLines = contextLines
	if v, ok := args["-B"]; ok {
		opts.beforeLines, _ = common.ToSemanticInt(v)
	}
	if v, ok := args["-A"]; ok {
		opts.afterLines, _ = common.ToSemanticInt(v)
	}
	if opts.beforeLines < 0 {
		opts.beforeLines = 0
	}
	if opts.afterLines < 0 {
		opts.afterLines = 0
	}

	if v, ok := args["limit"]; ok {
		limit, _ := common.ToSemanticInt(v)
		parseGrepLimit(&opts, limit)
	} else if v, ok := args["head_limit"]; ok {
		limit, _ := common.ToSemanticInt(v)
		parseGrepLimit(&opts, limit)
	}
	if opts.limit < 0 {
		opts.limit = defaultGrepLimit
		opts.unlimited = false
	}

	if v, ok := args["offset"]; ok {
		opts.offset, _ = common.ToSemanticInt(v)
	}
	if opts.offset < 0 {
		opts.offset = 0
	}

	return opts, nil
}

func semanticBooleanSchema() []map[string]any {
	return []map[string]any{
		{"type": "boolean"},
		{"type": "string", "enum": []string{"true", "false"}},
	}
}

func semanticIntegerSchema() []map[string]any {
	return []map[string]any{
		{"type": "integer"},
		{"type": "string", "pattern": `^-?\d+(\.\d+)?$`},
	}
}

func parseGrepLimit(opts *grepOptions, limit int) {
	if limit == 0 {
		opts.limit = 0
		opts.unlimited = true
		return
	}
	opts.limit = limit
	opts.unlimited = false
}

func (t *GrepTool) executeRipgrep(ctx context.Context, rgPath, pattern, searchPath string, globPatterns []string, opts grepOptions, toolCallID string) (types.ToolResult, error) {
	args := []string{
		"--color", "never",
		"--hidden",
		"--max-columns", strconv.Itoa(common.GrepMaxLineLen),
	}
	for _, dir := range vcsDirectoriesToExclude {
		args = append(args, "--glob", "!"+dir)
	}
	switch opts.outputMode {
	case outputModeFilesWithMatches:
		args = append(args, "--files-with-matches")
	case outputModeCount:
		args = append(args, "--count", "--with-filename")
	default:
		args = append(args, "--no-heading")
		if opts.lineNumbers {
			args = append(args, "--line-number")
		}
	}

	if opts.ignoreCase {
		args = append(args, "--ignore-case")
	}
	if opts.literal {
		args = append(args, "--fixed-strings")
	}
	if opts.outputMode == outputModeContent {
		if opts.beforeLines > 0 {
			args = append(args, fmt.Sprintf("--before-context=%d", opts.beforeLines))
		}
		if opts.afterLines > 0 {
			args = append(args, fmt.Sprintf("--after-context=%d", opts.afterLines))
		}
	}
	for _, globPattern := range globPatterns {
		args = append(args, "--glob", globPattern)
	}
	if opts.fileType != "" {
		args = append(args, "--type", opts.fileType)
	}
	if opts.multiline {
		args = append(args, "--multiline", "--multiline-dotall")
	}

	if strings.HasPrefix(pattern, "-") {
		args = append(args, "-e", pattern)
	} else {
		args = append(args, pattern)
	}
	args = append(args, searchPath)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = t.cwd

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				// No matches
				return types.ToolResult{
					Content: []types.ContentBlock{
						&types.TextContent{Type: "text", Text: noGrepMatchesMessage(opts.outputMode)},
					},
				}, nil
			}
		}
		return common.ErrorResult(fmt.Sprintf("ripgrep error: %v", err)), nil
	}

	visible := formatRipgrepOutput(output, searchPath, t.cwd, opts)
	if t.artifacts == nil {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: visible},
			},
			Details: map[string]any{
				"path":          searchPath,
				"matched_paths": extractMatchedPathsFromRipgrepOutput(output, searchPath),
			},
		}, nil
	}
	raw := formatRipgrepOutput(output, searchPath, t.cwd, completeGrepOptions(opts))
	preview := common.PreviewTextFrom(raw, visible, common.TextPreviewOptions{
		ToolCallID:    toolCallID,
		ArtifactName:  "grep",
		ArtifactStore: t.artifacts,
		Strategy:      common.PreviewHead,
		MaxLines:      grepPreviewLines(opts),
		MaxBytes:      common.DefaultMaxBytes,
	})

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: preview.Text},
		},
		Details: map[string]any{
			"path":          searchPath,
			"matched_paths": extractMatchedPathsFromRipgrepOutput(output, searchPath),
			"output":        preview.Details["output"],
		},
	}, nil
}

func (t *GrepTool) executeBuiltin(ctx context.Context, pattern, searchPath string, globPatterns []string, opts grepOptions, toolCallID string) (types.ToolResult, error) {
	if opts.literal {
		pattern = regexp.QuoteMeta(pattern)
	}

	flags := ""
	if opts.ignoreCase {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("invalid regex pattern: %v", err)), nil
	}

	var results []string
	matchFiles := make(map[string]struct{})
	matchCounts := make(map[string]int)
	matchCount := 0

	err = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info.IsDir() {
			name := info.Name()
			if shouldSkipGrepDir(name) {
				return filepath.SkipDir
			}
			return nil
		}

		displayPath := relativeGrepPath(path, searchPath, t.cwd)

		// Apply glob filter
		if len(globPatterns) > 0 && !matchesAnyGlob(globPatterns, displayPath, info.Name()) {
			return nil
		}

		if opts.fileType != "" && !matchesFileType(path, opts.fileType) {
			return nil
		}

		// Skip oversized files before loading them into memory (applies to both
		// the multiline and line-by-line paths).
		if info.Size() > 10*1024*1024 { // Skip files > 10MB
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		if opts.multiline {
			indexes := re.FindAllStringIndex(content, -1)
			if len(indexes) == 0 {
				return nil
			}
			matchFiles[displayPath] = struct{}{}
			matchCounts[displayPath] = len(indexes)
			matchCount += len(indexes)
			if opts.outputMode == outputModeContent {
				for _, match := range indexes {
					lineNum := strings.Count(content[:match[0]], "\n") + 1
					text := common.TruncateLine(strings.ReplaceAll(content[match[0]:match[1]], "\n", "\\n"), common.GrepMaxLineLen)
					if opts.lineNumbers {
						results = append(results, fmt.Sprintf("%s:%d:%s", displayPath, lineNum, text))
					} else {
						results = append(results, fmt.Sprintf("%s:%s", displayPath, text))
					}
				}
			}
			return nil
		}

		lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
		includedLines := make(map[int]struct{})
		for i, line := range lines {
			if re.MatchString(line) {
				matchCount++
				matchFiles[displayPath] = struct{}{}
				matchCounts[displayPath]++
				if opts.outputMode != outputModeContent {
					continue
				}

				start := i - opts.beforeLines
				if start < 0 {
					start = 0
				}
				end := i + opts.afterLines
				if end >= len(lines) {
					end = len(lines) - 1
				}
				for lineIndex := start; lineIndex <= end; lineIndex++ {
					if _, ok := includedLines[lineIndex]; ok {
						continue
					}
					includedLines[lineIndex] = struct{}{}
					truncatedLine := common.TruncateLine(lines[lineIndex], common.GrepMaxLineLen)
					if opts.lineNumbers {
						results = append(results, fmt.Sprintf("%s:%d:%s", displayPath, lineIndex+1, truncatedLine))
					} else {
						results = append(results, fmt.Sprintf("%s:%s", displayPath, truncatedLine))
					}
				}
			}
		}

		return nil
	})

	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		return common.ErrorResult(fmt.Sprintf("search error: %v", err)), nil
	}

	if len(results) == 0 && len(matchCounts) == 0 {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: noGrepMatchesMessage(opts.outputMode)},
			},
		}, nil
	}

	visible := formatBuiltinOutput(results, matchFiles, matchCounts, opts, matchCount, searchPath, t.cwd)
	if t.artifacts == nil {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: visible},
			},
			Details: map[string]any{
				"path":          searchPath,
				"matched_paths": sortedKeys(matchFiles, 20),
			},
		}, nil
	}
	raw := formatBuiltinOutput(results, matchFiles, matchCounts, completeGrepOptions(opts), matchCount, searchPath, t.cwd)
	preview := common.PreviewTextFrom(raw, visible, common.TextPreviewOptions{
		ToolCallID:    toolCallID,
		ArtifactName:  "grep",
		ArtifactStore: t.artifacts,
		Strategy:      common.PreviewHead,
		MaxLines:      grepPreviewLines(opts),
		MaxBytes:      common.DefaultMaxBytes,
	})

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: preview.Text},
		},
		Details: map[string]any{
			"path":          searchPath,
			"matched_paths": sortedKeys(matchFiles, 20),
			"output":        preview.Details["output"],
		},
	}, nil
}

func formatRipgrepOutput(output []byte, searchPath, cwd string, opts grepOptions) string {
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return noGrepMatchesMessage(opts.outputMode)
	}

	switch opts.outputMode {
	case outputModeFilesWithMatches:
		paths := make([]string, 0, len(lines))
		for _, line := range lines {
			if line == "" {
				continue
			}
			paths = append(paths, line)
		}
		sortGrepPathsByModTime(paths, searchPath, cwd)
		paths, truncated := applyGrepWindow(paths, opts)
		if len(paths) == 0 {
			return noGrepMatchesMessage(opts.outputMode)
		}
		displayPaths := make([]string, 0, len(paths))
		for _, path := range paths {
			displayPaths = append(displayPaths, relativeGrepPath(path, searchPath, cwd))
		}
		text := fmt.Sprintf("Found %d file(s)\n%s", len(displayPaths), strings.Join(displayPaths, "\n"))
		if truncated {
			text += grepWindowMessage(opts)
		}
		return text
	case outputModeCount:
		countLines := make([]string, 0, len(lines))
		for _, line := range lines {
			if line == "" {
				continue
			}
			path, countText, ok := strings.Cut(line, ":")
			if !ok {
				countLines = append(countLines, common.TruncateLine(line, common.GrepMaxLineLen))
				continue
			}
			countLines = append(countLines, fmt.Sprintf("%s:%s", relativeGrepPath(path, searchPath, cwd), countText))
		}
		countLines, truncated := applyGrepWindow(countLines, opts)
		total, files := summarizeGrepCountLines(countLines)
		text := fmt.Sprintf("%s\n\nFound %d total occurrence(s) across %d file(s).", strings.Join(countLines, "\n"), total, files)
		if truncated {
			text += grepWindowMessage(opts)
		}
		return text
	default:
		for i, line := range lines {
			lines[i] = common.TruncateLine(relativeGrepContentLine(line, searchPath, cwd), common.GrepMaxLineLen)
		}
		lines, truncated := applyGrepWindow(lines, opts)
		text := strings.Join(lines, "\n")
		if truncated {
			text += grepWindowMessage(opts)
		}
		return text
	}
}

func completeGrepOptions(opts grepOptions) grepOptions {
	opts.offset = 0
	opts.limit = 0
	opts.unlimited = true
	return opts
}

func grepPreviewLines(opts grepOptions) int {
	if opts.unlimited || opts.limit <= 0 {
		return common.DefaultMaxLines
	}
	switch opts.outputMode {
	case outputModeFilesWithMatches, outputModeCount:
		return opts.limit + 3
	default:
		return opts.limit
	}
}

func formatBuiltinOutput(results []string, matchFiles map[string]struct{}, matchCounts map[string]int, opts grepOptions, matchCount int, searchPath, cwd string) string {
	switch opts.outputMode {
	case outputModeFilesWithMatches:
		paths := make([]string, 0, len(matchCounts))
		for path := range matchCounts {
			paths = append(paths, path)
		}
		sortGrepPathsByModTime(paths, searchPath, cwd)
		paths, truncated := applyGrepWindow(paths, opts)
		text := fmt.Sprintf("Found %d file(s)\n%s", len(paths), strings.Join(paths, "\n"))
		if truncated {
			text += grepWindowMessage(opts)
		}
		return text
	case outputModeCount:
		paths := make([]string, 0, len(matchCounts))
		for path := range matchCounts {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		lines := make([]string, 0, len(paths))
		for _, path := range paths {
			count := matchCounts[path]
			lines = append(lines, fmt.Sprintf("%s:%d", path, count))
		}
		lines, truncated := applyGrepWindow(lines, opts)
		total, files := summarizeGrepCountLines(lines)
		text := fmt.Sprintf("%s\n\nFound %d total occurrence(s) across %d file(s).", strings.Join(lines, "\n"), total, files)
		if truncated {
			text += grepWindowMessage(opts)
		}
		return text
	default:
		lines, truncated := applyGrepWindow(results, opts)
		text := strings.Join(lines, "\n")
		if truncated || (!opts.unlimited && matchCount > opts.limit) {
			text += grepWindowMessage(opts)
		}
		return text
	}
}

func sortGrepPathsByModTime(paths []string, searchPath, cwd string) {
	sort.Slice(paths, func(i, j int) bool {
		leftTime := grepPathModTime(paths[i], searchPath, cwd)
		rightTime := grepPathModTime(paths[j], searchPath, cwd)
		if leftTime == rightTime {
			return paths[i] < paths[j]
		}
		return leftTime > rightTime
	})
}

func grepPathModTime(path, searchPath, cwd string) int64 {
	for _, candidate := range grepPathStatCandidates(path, searchPath, cwd) {
		info, err := os.Stat(candidate)
		if err == nil {
			return info.ModTime().UnixNano()
		}
	}
	return 0
}

func grepPathStatCandidates(path, searchPath, cwd string) []string {
	if filepath.IsAbs(path) {
		return []string{path}
	}

	var candidates []string
	if cwd != "" {
		candidates = append(candidates, filepath.Join(cwd, path))
	}
	if searchPath != "" {
		candidates = append(candidates, filepath.Join(searchPath, path))
	}
	candidates = append(candidates, path)
	return candidates
}

func summarizeGrepCountLines(lines []string) (int, int) {
	total := 0
	files := 0
	for _, line := range lines {
		_, countText, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		count, err := strconv.Atoi(countText)
		if err != nil {
			continue
		}
		total += count
		files++
	}
	return total, files
}

func applyGrepWindow(lines []string, opts grepOptions) ([]string, bool) {
	if opts.offset >= len(lines) {
		return []string{}, opts.offset > 0 && len(lines) > 0
	}
	start := opts.offset
	if opts.unlimited {
		return lines[start:], start > 0
	}
	end := start + opts.limit
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start:end], start > 0 || end < len(lines)
}

func grepWindowMessage(opts grepOptions) string {
	if opts.unlimited {
		return fmt.Sprintf("\n\n... (results offset by %d item(s); use a lower offset to see earlier results)", opts.offset)
	}
	return fmt.Sprintf("\n\n... (results limited to %d item(s); use offset to continue)", opts.limit)
}

func noGrepMatchesMessage(outputMode string) string {
	if outputMode == outputModeFilesWithMatches {
		return "No files found"
	}
	return "No matches found."
}

func relativeGrepPath(path, searchPath, cwd string) string {
	if rel, err := filepath.Rel(cwd, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return rel
	}
	if path == searchPath {
		return filepath.Base(path)
	}
	if rel, err := filepath.Rel(searchPath, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return strings.TrimPrefix(path, searchPath+string(filepath.Separator))
}

func relativeGrepContentLine(line, searchPath, cwd string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return line
	}
	return fmt.Sprintf("%s:%s", relativeGrepPath(parts[0], searchPath, cwd), parts[1])
}

func parseGrepGlobPatterns(glob string) []string {
	var patterns []string
	for _, rawPattern := range strings.Fields(glob) {
		if strings.Contains(rawPattern, "{") && strings.Contains(rawPattern, "}") {
			patterns = append(patterns, rawPattern)
			continue
		}
		for _, pattern := range strings.Split(rawPattern, ",") {
			pattern = strings.TrimSpace(pattern)
			if pattern != "" {
				patterns = append(patterns, pattern)
			}
		}
	}
	return patterns
}

func shouldSkipGrepDir(name string) bool {
	for _, dir := range vcsDirectoriesToExclude {
		if name == dir {
			return true
		}
	}
	return false
}

func matchesAnyGlob(patterns []string, relPath, name string) bool {
	for _, pattern := range patterns {
		if matchesGlob(pattern, relPath, name) {
			return true
		}
	}
	return false
}

func matchesGlob(pattern, relPath, name string) bool {
	normalized := filepath.ToSlash(relPath)
	pattern = filepath.ToSlash(pattern)
	for _, expanded := range expandBraceGlob(pattern) {
		if matched, _ := filepath.Match(expanded, normalized); matched {
			return true
		}
		if matched, _ := filepath.Match(expanded, name); matched {
			return true
		}
	}
	return false
}

func expandBraceGlob(pattern string) []string {
	start := strings.IndexByte(pattern, '{')
	end := strings.IndexByte(pattern, '}')
	if start < 0 || end <= start {
		return []string{pattern}
	}

	prefix := pattern[:start]
	suffix := pattern[end+1:]
	parts := strings.Split(pattern[start+1:end], ",")
	expanded := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			expanded = append(expanded, prefix+part+suffix)
		}
	}
	if len(expanded) == 0 {
		return []string{pattern}
	}
	return expanded
}

func matchesFileType(path, fileType string) bool {
	fileType = strings.TrimPrefix(strings.ToLower(fileType), ".")
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if ext == fileType {
		return true
	}

	switch fileType {
	case "go":
		return ext == "go"
	case "js":
		return ext == "js" || ext == "jsx" || ext == "mjs" || ext == "cjs"
	case "ts":
		return ext == "ts" || ext == "tsx"
	case "py", "python":
		return ext == "py"
	case "java":
		return ext == "java"
	case "rust", "rs":
		return ext == "rs"
	case "ruby", "rb":
		return ext == "rb"
	case "php":
		return ext == "php"
	case "html":
		return ext == "html" || ext == "htm"
	case "css":
		return ext == "css"
	case "json":
		return ext == "json"
	case "md", "markdown":
		return ext == "md" || ext == "markdown"
	case "yaml", "yml":
		return ext == "yaml" || ext == "yml"
	}
	return false
}

func extractMatchedPathsFromRipgrepOutput(output []byte, searchPath string) []string {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	seen := make(map[string]struct{})
	var paths []string
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}
		path := parts[0]
		if !filepath.IsAbs(path) {
			path = filepath.Join(searchPath, path)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
		if len(paths) >= 20 {
			break
		}
	}
	return paths
}

func sortedKeys(set map[string]struct{}, limit int) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}
