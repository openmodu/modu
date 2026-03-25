package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
	"golang.org/x/text/unicode/norm"
)

// EditTool implements the precise text replacement tool.
type EditTool struct {
	cwd string
}

func NewEditTool(cwd string) *EditTool {
	return &EditTool{cwd: cwd}
}

func (t *EditTool) Name() string  { return "edit" }
func (t *EditTool) Label() string { return "Edit File" }
func (t *EditTool) Description() string {
	return `Perform exact string replacements in files. The old_text must match exactly (including whitespace and indentation). If exact match fails, it will attempt a fuzzy match by normalizing whitespace and Unicode characters. If old_text appears multiple times, the edit will be rejected as ambiguous. Use replace_all=true to replace all occurrences.`
}

func (t *EditTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to edit (absolute or relative to cwd)",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "The replacement text",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Replace all occurrences instead of requiring exactly one match. Default false.",
			},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
}

func (t *EditTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	pathArg, _ := args["path"].(string)
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)
	replaceAll, _ := args["replace_all"].(bool)

	if pathArg == "" {
		return errorResult("path is required"), nil
	}
	if oldText == "" {
		return errorResult("old_text is required"), nil
	}

	resolved, err := ResolveReadPath(pathArg, t.cwd)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to resolve path: %v", err)), nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResult(fmt.Sprintf("file not found: %s", pathArg)), nil
		}
		return errorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	content := string(data)

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

	if count == 0 {
		// Try advanced fuzzy match
		fuzzyContent := normalizeForFuzzyMatch(normalContent)
		fuzzyOld := normalizeForFuzzyMatch(normalOld)
		count = strings.Count(fuzzyContent, fuzzyOld)

		if count == 0 {
			// fallback check with basic normalizeWhitespace (just in case)
			if strings.Contains(normalizeWhitespace(normalContent), normalizeWhitespace(normalOld)) {
				return errorResult(fmt.Sprintf("old_text not found with exact match in %s, but a fuzzy match was found. Please ensure whitespace and indentation match exactly.", pathArg)), nil
			}
			return errorResult(fmt.Sprintf("old_text not found in %s. Make sure the text matches exactly.", pathArg)), nil
		}

		usedFuzzy = true
		contentForReplacement = fuzzyContent
		oldForReplacement = fuzzyOld
	}

	if count > 1 && !replaceAll {
		return errorResult(fmt.Sprintf("old_text appears %d times in %s. Use replace_all=true to replace all occurrences, or provide more context to make the match unique.", count, pathArg)), nil
	}

	// Perform replacement
	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(contentForReplacement, oldForReplacement, normalNew)
	} else {
		newContent = strings.Replace(contentForReplacement, oldForReplacement, normalNew, 1)
	}

	// Restore original line ending style
	if lineEnding == "\r\n" {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
	}

	if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return errorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	// Generate a simple diff summary
	diff := generateDiff(oldForReplacement, normalNew, contentForReplacement, pathArg)

	replacements := 1
	if replaceAll {
		replacements = count
	}

	msgText := fmt.Sprintf("Successfully edited %s (%d replacement(s))\n\n%s", pathArg, replacements, diff)
	if usedFuzzy {
		msgText = fmt.Sprintf("Successfully edited %s (%d replacement(s) using fuzzy match)\n\n%s", pathArg, replacements, diff)
	}

	return agent.AgentToolResult{
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
