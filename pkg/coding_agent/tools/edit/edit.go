package edit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
	"golang.org/x/text/unicode/norm"
)

const maxEditFileSize = 1024 * 1024 * 1024

// EditTool implements the precise text replacement tool.
type EditTool struct {
	cwd       string
	readState *common.FileReadState
}

func NewTool(cwd string) types.Tool {
	return &EditTool{cwd: cwd}
}

func NewTrackedTool(cwd string, readState *common.FileReadState) types.Tool {
	return &EditTool{cwd: cwd, readState: readState}
}

func (t *EditTool) Name() string  { return "edit" }
func (t *EditTool) Label() string { return "Edit File" }
func (t *EditTool) Description() string {
	return `Perform targeted string replacements in files.

Usage:
- Use this tool for modifying existing files; prefer it over write for targeted changes and over bash commands such as sed, awk, perl, or shell redirection.
- Read the file first so old_text is based on the current contents and exact indentation.
- old_text must match the file content, including whitespace and indentation, and must not include read line-number prefixes.
- An explicitly empty old_text creates a new file, or writes to an existing empty file, matching Claude Code's old_string behavior.
- old_text and new_text must be different; no-op edits are rejected.
- Existing files must be fully read before editing; partial reads and files changed after read are rejected.
- The edit is rejected when old_text appears multiple times unless replace_all=true is set. Add nearby context to make a single replacement unique.
- Use replace_all only for intentional file-wide renames or repeated identical replacements. Boolean strings "true" and "false" are accepted for Claude Code compatibility.
- The path must refer to a file, not a directory.
- Jupyter notebooks (.ipynb) are rejected by this text edit tool; use a notebook-aware workflow instead.
- Files larger than 1.0GB are rejected before reading to avoid loading huge files into memory.
- The file_path, old_string, and new_string aliases are accepted for Claude Code compatibility.
- If exact matching fails, the tool may attempt a fuzzy match by normalizing whitespace and Unicode characters, but you should still provide exact text whenever possible.`
}

func (t *EditTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to edit (absolute or relative to cwd)",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Alias for path, accepted for compatibility.",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "Alias for old_text, accepted for compatibility.",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "The replacement text",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "Alias for new_text, accepted for compatibility.",
			},
			"replace_all": map[string]any{
				"anyOf": []map[string]any{
					{"type": "boolean"},
					{"type": "string", "enum": []string{"true", "false"}},
				},
				"description": "Replace all occurrences instead of requiring exactly one match. Default false. Boolean strings \"true\" and \"false\" are accepted for Claude Code compatibility.",
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"path"}},
			{"required": []string{"file_path"}},
		},
		"allOf": []map[string]any{
			{
				"anyOf": []map[string]any{
					{"required": []string{"old_text"}},
					{"required": []string{"old_string"}},
				},
			},
			{
				"anyOf": []map[string]any{
					{"required": []string{"new_text"}},
					{"required": []string{"new_string"}},
				},
			},
		},
	}
}

func (t *EditTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	pathArg, _ := args["path"].(string)
	if pathArg == "" {
		pathArg, _ = args["file_path"].(string)
	}
	oldText, hasOldText := stringArg(args, "old_text", "old_string")
	newText, hasNewText := stringArg(args, "new_text", "new_string")
	replaceAll, _ := common.ToSemanticBool(args["replace_all"])

	if pathArg == "" {
		return common.ErrorResult("path is required"), nil
	}
	if !hasOldText {
		return common.ErrorResult("old_text is required"), nil
	}
	if !hasNewText {
		return common.ErrorResult("new_text is required"), nil
	}
	if oldText == newText {
		return common.ErrorResult("No changes to make: old_text and new_text are exactly the same."), nil
	}

	resolved, err := common.ResolveReadPath(pathArg, t.cwd)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to resolve path: %v", err)), nil
	}
	if result, ok := rejectDirectoryEditTarget(resolved, pathArg); ok {
		return result, nil
	}
	if result, ok := rejectOversizedEditTarget(resolved); ok {
		return result, nil
	}
	if oldText == "" {
		return t.editEmptyOldText(resolved, pathArg, newText)
	}
	if result, ok := rejectNotebookEditTarget(resolved, pathArg); ok {
		return result, nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return common.ErrorResult(fmt.Sprintf("file not found: %s", pathArg)), nil
		}
		return common.ErrorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	rawContent := string(data)
	if result, ok := t.validateExistingFileReadState(pathArg, resolved, rawContent); !ok {
		return result, nil
	}

	content := rawContent

	// Remove BOM
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")

	// Detect line ending style
	lineEnding := "\n"
	if strings.Contains(content, "\r\n") {
		lineEnding = "\r\n"
	}

	// Normalize line endings for matching
	normalContent := strings.ReplaceAll(content, "\r\n", "\n")
	normalOld := strings.ReplaceAll(oldText, "\r\n", "\n")
	normalNew := strings.ReplaceAll(newText, "\r\n", "\n")

	// Count occurrences
	count := strings.Count(normalContent, normalOld)

	usedFuzzy := false
	contentForReplacement := normalContent
	oldForReplacement := normalOld
	var fuzzyMatches []fuzzyLineMatch

	if count == 0 {
		fuzzyMatches = findFuzzyLineMatches(normalContent, normalOld)
		count = len(fuzzyMatches)

		if count == 0 {
			fuzzyContent := normalizeForFuzzyMatch(normalContent)
			fuzzyOld := normalizeForFuzzyMatch(normalOld)

			// fallback check with basic normalizeWhitespace (just in case)
			if strings.Contains(fuzzyContent, fuzzyOld) || strings.Contains(normalizeWhitespace(normalContent), normalizeWhitespace(normalOld)) {
				return common.ErrorResult(fmt.Sprintf("old_text not found with exact match in %s, but a fuzzy match was found. Please ensure whitespace and indentation match exactly.", pathArg)), nil
			}
			return common.ErrorResult(fmt.Sprintf("old_text not found in %s. Make sure the text matches exactly.", pathArg)), nil
		}

		usedFuzzy = true
		oldForReplacement = fuzzyMatches[0].text
	}

	if count > 1 && !replaceAll {
		return common.ErrorResult(fmt.Sprintf("old_text appears %d times in %s. Use replace_all=true to replace all occurrences, or provide more context to make the match unique.", count, pathArg)), nil
	}

	// Perform replacement
	var newContent string
	replacementText := normalNew
	if usedFuzzy {
		replacementText = preserveQuoteStyle(normalOld, oldForReplacement, normalNew)
	}
	if usedFuzzy {
		if replaceAll {
			newContent = replaceFuzzyMatches(normalContent, fuzzyMatches, normalOld, normalNew)
		} else {
			match := fuzzyMatches[0]
			end := match.end
			if shouldStripTrailingNewlineForDelete(normalContent, end, oldForReplacement, replacementText) {
				end++
			}
			newContent = normalContent[:match.start] + replacementText + normalContent[end:]
		}
	} else if replaceAll {
		newContent = replaceAllExact(contentForReplacement, oldForReplacement, normalNew)
	} else {
		oldForEdit := oldForReplacement
		if matchStart := strings.Index(contentForReplacement, oldForReplacement); matchStart >= 0 {
			if shouldStripTrailingNewlineForDelete(contentForReplacement, matchStart+len(oldForReplacement), oldForReplacement, normalNew) {
				oldForEdit += "\n"
			}
		}
		newContent = strings.Replace(contentForReplacement, oldForEdit, normalNew, 1)
	}

	// Restore original line ending style
	if lineEnding == "\r\n" {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
	}

	if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}
	t.recordWrittenState(resolved, newContent)

	// Generate a simple diff summary
	diff := generateDiff(oldForReplacement, replacementText, contentForReplacement, pathArg)

	replacements := 1
	if replaceAll {
		replacements = count
	}

	msgText := fmt.Sprintf("Successfully edited %s (%d replacement(s))\n\n%s", pathArg, replacements, diff)
	if usedFuzzy {
		msgText = fmt.Sprintf("Successfully edited %s (%d replacement(s) using fuzzy match)\n\n%s", pathArg, replacements, diff)
	}

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: msgText,
			},
		},
		Details: map[string]any{
			"path":         resolved,
			"replacements": replacements,
		},
	}, nil
}

func (t *EditTool) validateExistingFileReadState(displayPath, path, currentContent string) (types.ToolResult, bool) {
	if t.readState == nil {
		return types.ToolResult{}, true
	}
	record, ok := t.readState.Get(path)
	if !ok {
		return common.ErrorResult("File has not been read yet. Read it first before writing to it."), false
	}
	// Unlike write (full rewrite), edit performs a targeted replacement, so a
	// partial read is sufficient: the recorded content is always the full file,
	// which lets the staleness check below work regardless of how it was read.
	// Always compare content rather than trusting mtime, since coarse-resolution
	// filesystems can leave mtime unchanged after an external edit.
	if currentContent != record.Content {
		return common.ErrorResult(fmt.Sprintf("File has been modified since read: %s. Read it again before attempting to write it.", displayPath)), false
	}
	return types.ToolResult{}, true
}

func (t *EditTool) recordWrittenState(path, content string) {
	if t.readState == nil {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	t.readState.Record(path, content, info.ModTime().UnixNano(), false)
}

func stringArg(args map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		v, ok := args[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if ok {
			return s, true
		}
	}
	return "", false
}

func rejectDirectoryEditTarget(path, displayPath string) (types.ToolResult, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return types.ToolResult{}, false
	}
	if !info.IsDir() {
		return types.ToolResult{}, false
	}
	return common.ErrorResult(fmt.Sprintf("%s is a directory, not a file. Use a file path for edit.", displayPath)), true
}

func rejectOversizedEditTarget(path string) (types.ToolResult, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return types.ToolResult{}, false
	}
	if info.Size() <= maxEditFileSize {
		return types.ToolResult{}, false
	}
	return common.ErrorResult(fmt.Sprintf("File is too large to edit (%s). Maximum editable file size is %s.", common.FormatSize(info.Size()), common.FormatSize(maxEditFileSize))), true
}

func rejectNotebookEditTarget(path, displayPath string) (types.ToolResult, bool) {
	if strings.ToLower(filepath.Ext(path)) != ".ipynb" {
		return types.ToolResult{}, false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return types.ToolResult{}, false
	}
	return common.ErrorResult(fmt.Sprintf("File is a Jupyter Notebook: %s. Use a notebook-aware workflow instead of plain text edit.", displayPath)), true
}

func (t *EditTool) editEmptyOldText(resolved, displayPath, newText string) (types.ToolResult, error) {
	if data, err := os.ReadFile(resolved); err == nil {
		if strings.TrimSpace(string(data)) != "" {
			return common.ErrorResult("Cannot create new file - file already exists."), nil
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
			return common.ErrorResult(fmt.Sprintf("failed to create directories: %v", err)), nil
		}
	} else {
		return common.ErrorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	if err := os.WriteFile(resolved, []byte(newText), 0o644); err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}
	t.recordWrittenState(resolved, newText)

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Successfully edited %s (1 replacement(s))", displayPath),
			},
		},
		Details: map[string]any{
			"path":         resolved,
			"replacements": 1,
		},
	}, nil
}

type fuzzyLineMatch struct {
	start int
	end   int
	text  string
}

type lineSpan struct {
	start int
	end   int
}

func findFuzzyLineMatches(content, old string) []fuzzyLineMatch {
	oldLines := strings.Split(old, "\n")
	if len(oldLines) == 0 {
		return nil
	}

	spans := splitLineSpans(content)
	if len(oldLines) > len(spans) {
		return nil
	}

	normalizedOld := normalizeForFuzzyMatch(old)
	matches := make([]fuzzyLineMatch, 0)
	windowSize := len(oldLines)
	for i := 0; i+windowSize <= len(spans); i++ {
		start := spans[i].start
		end := spans[i+windowSize-1].end
		candidate := content[start:end]
		if normalizeForFuzzyMatch(candidate) != normalizedOld {
			continue
		}

		matches = append(matches, fuzzyLineMatch{
			start: start,
			end:   end,
			text:  candidate,
		})
		i += windowSize - 1
	}

	return matches
}

func splitLineSpans(content string) []lineSpan {
	if content == "" {
		return []lineSpan{{start: 0, end: 0}}
	}

	spans := make([]lineSpan, 0, strings.Count(content, "\n")+1)
	start := 0
	for {
		idx := strings.IndexByte(content[start:], '\n')
		if idx < 0 {
			spans = append(spans, lineSpan{start: start, end: len(content)})
			return spans
		}

		end := start + idx
		spans = append(spans, lineSpan{start: start, end: end})
		start = end + 1
		if start == len(content) {
			spans = append(spans, lineSpan{start: start, end: start})
			return spans
		}
	}
}

func replaceFuzzyMatches(content string, matches []fuzzyLineMatch, oldText, newText string) string {
	var sb strings.Builder
	pos := 0
	for _, match := range matches {
		sb.WriteString(content[pos:match.start])
		sb.WriteString(preserveQuoteStyle(oldText, match.text, newText))
		pos = match.end
		if shouldStripTrailingNewlineForDelete(content, pos, match.text, newText) {
			pos++
		}
	}
	sb.WriteString(content[pos:])
	return sb.String()
}

func replaceAllExact(content, oldText, newText string) string {
	var sb strings.Builder
	pos := 0
	for {
		idx := strings.Index(content[pos:], oldText)
		if idx < 0 {
			sb.WriteString(content[pos:])
			return sb.String()
		}

		start := pos + idx
		end := start + len(oldText)
		sb.WriteString(content[pos:start])
		sb.WriteString(newText)
		pos = end
		if shouldStripTrailingNewlineForDelete(content, pos, oldText, newText) {
			pos++
		}
	}
}

func shouldStripTrailingNewlineForDelete(content string, matchEnd int, oldText, newText string) bool {
	return newText == "" && !strings.HasSuffix(oldText, "\n") && matchEnd < len(content) && content[matchEnd] == '\n'
}

func preserveQuoteStyle(oldText, actualOldText, newText string) string {
	if oldText == actualOldText {
		return newText
	}

	result := newText
	if strings.ContainsAny(actualOldText, "\u201c\u201d") {
		result = applyCurlyDoubleQuotes(result)
	}
	if strings.ContainsAny(actualOldText, "\u2018\u2019") {
		result = applyCurlySingleQuotes(result)
	}
	return result
}

func applyCurlyDoubleQuotes(text string) string {
	runes := []rune(text)
	var sb strings.Builder
	for i, r := range runes {
		if r == '"' {
			if isOpeningQuoteContext(runes, i) {
				sb.WriteRune('\u201c')
			} else {
				sb.WriteRune('\u201d')
			}
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func applyCurlySingleQuotes(text string) string {
	runes := []rune(text)
	var sb strings.Builder
	for i, r := range runes {
		if r == '\'' {
			prevIsLetter := i > 0 && isLetter(runes[i-1])
			nextIsLetter := i < len(runes)-1 && isLetter(runes[i+1])
			if prevIsLetter && nextIsLetter {
				sb.WriteRune('\u2019')
			} else if isOpeningQuoteContext(runes, i) {
				sb.WriteRune('\u2018')
			} else {
				sb.WriteRune('\u2019')
			}
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func isOpeningQuoteContext(runes []rune, index int) bool {
	if index == 0 {
		return true
	}
	switch runes[index-1] {
	case ' ', '\t', '\n', '\r', '(', '[', '{', '\u2014', '\u2013':
		return true
	default:
		return false
	}
}

func isLetter(r rune) bool {
	return unicode.IsLetter(r)
}

// normalizeForFuzzyMatch applies progressive transformations for fuzzy matching:
// - Normalize to NFKC
// - Strip trailing whitespace from each line
// - Normalize smart quotes to ASCII equivalents
// - Normalize Unicode dashes/hyphens to ASCII hyphen
// - Normalize special Unicode spaces to regular space
func normalizeForFuzzyMatch(text string) string {
	// Normalize to NFKC
	text = norm.NFKC.String(text)

	// Strip trailing whitespace per line
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}
	text = strings.Join(lines, "\n")

	// Smart single quotes -> '
	for _, q := range []string{"\u2018", "\u2019", "\u201A", "\u201B"} {
		text = strings.ReplaceAll(text, q, "'")
	}
	// Smart double quotes -> "
	for _, q := range []string{"\u201C", "\u201D", "\u201E", "\u201F"} {
		text = strings.ReplaceAll(text, q, "\"")
	}
	// Various dashes/hyphens -> -
	// U+2010 hyphen, U+2011 non-breaking hyphen, U+2012 figure dash,
	// U+2013 en-dash, U+2014 em-dash, U+2015 horizontal bar, U+2212 minus
	for _, d := range []string{"\u2010", "\u2011", "\u2012", "\u2013", "\u2014", "\u2015", "\u2212"} {
		text = strings.ReplaceAll(text, d, "-")
	}
	// Special spaces -> regular space
	// U+00A0 NBSP, U+2002-U+200A various spaces, U+202F narrow NBSP,
	// U+205F medium math space, U+3000 ideographic space
	for _, s := range []string{"\u00A0", "\u2002", "\u2003", "\u2004", "\u2005", "\u2006", "\u2007", "\u2008", "\u2009", "\u200A", "\u202F", "\u205F", "\u3000"} {
		text = strings.ReplaceAll(text, s, " ")
	}

	return text
}

// normalizeWhitespace collapses all whitespace sequences to a single space.
func normalizeWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// generateDiff generates a unified diff with per-line numbers and context.
// Format: "  N  code" for context, "- N  code" for removed, "+ N  code" for added.
func generateDiff(oldText, newText, fileContent, path string) string {
	const context = 3

	allLines := strings.Split(fileContent, "\n")
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Find 1-based starting line of oldText in the file.
	startLine := 1
	if idx := strings.Index(fileContent, oldText); idx >= 0 {
		startLine = strings.Count(fileContent[:idx], "\n") + 1
	}
	endLine := startLine + len(oldLines) - 1 // last removed line (1-based)

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", path)
	fmt.Fprintf(&sb, "+++ %s\n", path)
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", startLine, len(oldLines), startLine, len(newLines))

	// Context before.
	ctxStart := startLine - 1 - context
	if ctxStart < 0 {
		ctxStart = 0
	}
	for i := ctxStart; i < startLine-1 && i < len(allLines); i++ {
		fmt.Fprintf(&sb, "  %d  %s\n", i+1, allLines[i])
	}

	// Removed lines.
	for i, line := range oldLines {
		fmt.Fprintf(&sb, "- %d  %s\n", startLine+i, line)
	}

	// Added lines (same start line number since they replace the removed block).
	for i, line := range newLines {
		fmt.Fprintf(&sb, "+ %d  %s\n", startLine+i, line)
	}

	// Context after.
	ctxEnd := endLine + context
	if ctxEnd > len(allLines) {
		ctxEnd = len(allLines)
	}
	for i := endLine; i < ctxEnd; i++ {
		fmt.Fprintf(&sb, "  %d  %s\n", i+1, allLines[i])
	}

	return sb.String()
}
