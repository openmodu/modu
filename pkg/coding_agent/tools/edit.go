package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/types"
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
	return `Perform exact string replacements in files. The old_text must match exactly (including whitespace and indentation). If old_text appears multiple times, the edit will be rejected as ambiguous. Use replace_all=true to replace all occurrences.`
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

	if count == 0 {
		// Try fuzzy match (normalize whitespace)
		fuzzyContent := normalizeWhitespace(normalContent)
		fuzzyOld := normalizeWhitespace(normalOld)
		if strings.Contains(fuzzyContent, fuzzyOld) {
			return errorResult(fmt.Sprintf("old_text not found with exact match in %s, but a fuzzy match was found. Please ensure whitespace and indentation match exactly.", pathArg)), nil
		}
		return errorResult(fmt.Sprintf("old_text not found in %s. Make sure the text matches exactly.", pathArg)), nil
	}

	if count > 1 && !replaceAll {
		return errorResult(fmt.Sprintf("old_text appears %d times in %s. Use replace_all=true to replace all occurrences, or provide more context to make the match unique.", count, pathArg)), nil
	}

	// Perform replacement
	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(normalContent, normalOld, normalNew)
	} else {
		newContent = strings.Replace(normalContent, normalOld, normalNew, 1)
	}

	// Restore original line ending style
	if lineEnding == "\r\n" {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
	}

	if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return errorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	// Generate a simple diff summary
	diff := generateDiff(normalOld, normalNew, pathArg)

	replacements := 1
	if replaceAll {
		replacements = count
	}

	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Successfully edited %s (%d replacement(s))\n\n%s", pathArg, replacements, diff),
			},
		},
		Details: map[string]any{
			"path":         resolved,
			"replacements": replacements,
		},
	}, nil
}

// normalizeWhitespace collapses all whitespace sequences to a single space.
func normalizeWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// generateDiff generates a unified diff-like output.
func generateDiff(oldText, newText, path string) string {
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", path)
	fmt.Fprintf(&sb, "+++ %s\n", path)

	for _, line := range oldLines {
		fmt.Fprintf(&sb, "- %s\n", line)
	}
	for _, line := range newLines {
		fmt.Fprintf(&sb, "+ %s\n", line)
	}

	return sb.String()
}
