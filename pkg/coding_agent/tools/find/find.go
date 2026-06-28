package find

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

const defaultFindLimit = 100

const truncatedMessage = "(Results are truncated. Consider using a more specific path or pattern.)"

var vcsDirectoriesToExclude = []string{".git", ".svn", ".hg", ".bzr", ".jj", ".sl"}

// FindTool implements the file search tool using glob patterns.
type FindTool struct {
	cwd string
}

func NewTool(cwd string) types.Tool {
	return &FindTool{cwd: cwd}
}

func (t *FindTool) Name() string  { return "find" }
func (t *FindTool) Label() string { return "Find Files" }
func (t *FindTool) Description() string {
	return `Find files by glob pattern.

Usage:
- Use this tool when you need to locate files by name or path pattern; prefer it over running shell find or ls through bash.
- Supports patterns such as "**/*.go", "src/*.ts", or "*_test.go".
- Absolute patterns such as "/tmp/project/**/*.go" are accepted; the static directory prefix is used as the search root.
- Uses fd when available and falls back to a built-in filesystem walk.
- Searches hidden files and ordinary files ignored by .gitignore while excluding VCS metadata directories such as .git and .hg.
- The optional path must be a directory; use read for known files.
- Returns file paths relative to the working directory, sorted by modification time and capped at 100 results by default. Numeric strings such as "10" are accepted for limit. Narrow broad searches with path and pattern.`
}

func (t *FindTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern to match files (e.g., '**/*.go', 'src/*.ts')",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory to search in (default: cwd)",
			},
			"limit": map[string]any{
				"anyOf":       semanticFindIntegerSchema(),
				"description": "Maximum number of results (default 100)",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *FindTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return common.ErrorResult("pattern is required"), nil
	}

	searchPath := t.cwd
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = common.ResolveToCwd(p, t.cwd)
	}
	pattern, searchPath = normalizeFindPatternAndPath(pattern, searchPath)

	info, err := os.Stat(searchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return common.ErrorResult(fmt.Sprintf("directory not found: %s", searchPath)), nil
		}
		return common.ErrorResult(fmt.Sprintf("failed to stat directory: %v", err)), nil
	}
	if !info.IsDir() {
		return common.ErrorResult(fmt.Sprintf("path is not a directory: %s", searchPath)), nil
	}

	limit := defaultFindLimit
	if v, ok := args["limit"]; ok {
		limit, _ = common.ToSemanticInt(v)
		if limit <= 0 {
			limit = defaultFindLimit
		}
	}

	// Try fd first
	if fdPath, err := exec.LookPath("fd"); err == nil {
		return t.executeFd(ctx, fdPath, pattern, searchPath, limit)
	}

	// Fallback to built-in
	return t.executeBuiltin(ctx, pattern, searchPath, limit)
}

func semanticFindIntegerSchema() []map[string]any {
	return []map[string]any{
		{"type": "integer"},
		{"type": "string", "pattern": `^-?\d+$`},
	}
}

func normalizeFindPatternAndPath(pattern, searchPath string) (string, string) {
	if !filepath.IsAbs(pattern) {
		return pattern, searchPath
	}

	baseDir, relativePattern := extractFindBaseDirectory(pattern)
	if baseDir == "" {
		return pattern, searchPath
	}
	return relativePattern, baseDir
}

func extractFindBaseDirectory(pattern string) (string, string) {
	globIndex := strings.IndexAny(pattern, "*?[{")
	if globIndex == -1 {
		return filepath.Dir(pattern), filepath.Base(pattern)
	}

	staticPrefix := pattern[:globIndex]
	lastSepIndex := strings.LastIndex(staticPrefix, string(filepath.Separator))
	if filepath.Separator != '/' {
		lastSepIndex = max(lastSepIndex, strings.LastIndex(staticPrefix, "/"))
	}
	if lastSepIndex == -1 {
		return "", pattern
	}
	if lastSepIndex == 0 {
		return string(filepath.Separator), pattern[1:]
	}
	return staticPrefix[:lastSepIndex], pattern[lastSepIndex+1:]
}

func (t *FindTool) executeFd(ctx context.Context, fdPath, pattern, searchPath string, limit int) (types.ToolResult, error) {
	args := []string{
		"--type", "f",
		"--color", "never",
		"--hidden",
		"--no-ignore",
		"--glob", pattern,
	}
	for _, dir := range vcsDirectoriesToExclude {
		args = append(args, "--exclude", dir)
	}

	cmd := exec.CommandContext(ctx, fdPath, args...)
	cmd.Dir = searchPath

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return types.ToolResult{
				Content: []types.ContentBlock{
					&types.TextContent{Type: "text", Text: "No files found"},
				},
			}, nil
		}
		// fd might not support all options, fall back
		return t.executeBuiltin(ctx, pattern, searchPath, limit)
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "No files found"},
			},
		}, nil
	}

	lines := strings.Split(result, "\n")
	matches := make([]fileMatch, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		abs := filepath.Join(searchPath, line)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		matches = append(matches, fileMatch{
			rel:   displayFindPath(abs, t.cwd),
			abs:   abs,
			mtime: info.ModTime().UnixNano(),
		})
	}

	return findResult(searchPath, matches, limit), nil
}

type fileMatch struct {
	rel   string
	abs   string
	mtime int64
}

func findResult(searchPath string, matches []fileMatch, limit int) types.ToolResult {
	if len(matches) == 0 {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "No files found"},
			},
			Details: map[string]any{
				"path":          searchPath,
				"matched_paths": []string{},
			},
		}
	}

	// Newest first, matching grep's ordering, so that when results are capped at
	// limit the most recently modified files are kept rather than dropped.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].mtime == matches[j].mtime {
			return matches[i].rel < matches[j].rel
		}
		return matches[i].mtime > matches[j].mtime
	})

	truncated := len(matches) > limit
	if truncated {
		matches = matches[:limit]
	}

	lines := make([]string, 0, len(matches))
	matchedPaths := make([]string, 0, min(len(matches), 20))
	for _, match := range matches {
		lines = append(lines, match.rel)
		if len(matchedPaths) < 20 {
			matchedPaths = append(matchedPaths, match.abs)
		}
	}

	text := strings.Join(lines, "\n")
	if truncated {
		text += "\n\n" + truncatedMessage
	}

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
		Details: map[string]any{
			"path":          searchPath,
			"matched_paths": matchedPaths,
		},
	}
}

func (t *FindTool) executeBuiltin(ctx context.Context, pattern, searchPath string, limit int) (types.ToolResult, error) {
	var results []fileMatch
	skipDirs := findVCSDirSet()

	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(searchPath, path)
		if relPath == "" {
			relPath = path
		}

		if matchFindPattern(pattern, relPath, info.Name()) {
			results = append(results, fileMatch{
				rel:   displayFindPath(path, t.cwd),
				abs:   path,
				mtime: info.ModTime().UnixNano(),
			})
		}

		return nil
	})

	if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		return common.ErrorResult(fmt.Sprintf("search error: %v", err)), nil
	}

	if len(results) == 0 {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "No files found"},
			},
		}, nil
	}

	return findResult(searchPath, results, limit), nil
}

func displayFindPath(path, cwd string) string {
	if rel, err := filepath.Rel(cwd, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func findVCSDirSet() map[string]bool {
	dirs := make(map[string]bool, len(vcsDirectoriesToExclude))
	for _, dir := range vcsDirectoriesToExclude {
		dirs[dir] = true
	}
	return dirs
}

func matchFindPattern(pattern, relPath, baseName string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	relPath = filepath.ToSlash(relPath)
	if pattern == "" || relPath == "" {
		return false
	}

	if !strings.Contains(pattern, "/") {
		if matched, _ := filepath.Match(pattern, baseName); matched {
			return true
		}
	}
	if matched, _ := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(relPath)); matched {
		return true
	}
	if !strings.Contains(pattern, "**") {
		return false
	}
	return matchFindSegments(strings.Split(pattern, "/"), strings.Split(relPath, "/"))
}

func matchFindSegments(patternSegments, pathSegments []string) bool {
	if len(patternSegments) == 0 {
		return len(pathSegments) == 0
	}
	if patternSegments[0] == "**" {
		if matchFindSegments(patternSegments[1:], pathSegments) {
			return true
		}
		return len(pathSegments) > 0 && matchFindSegments(patternSegments, pathSegments[1:])
	}
	if len(pathSegments) == 0 {
		return false
	}
	matched, err := filepath.Match(patternSegments[0], pathSegments[0])
	if err != nil || !matched {
		return false
	}
	return matchFindSegments(patternSegments[1:], pathSegments[1:])
}
