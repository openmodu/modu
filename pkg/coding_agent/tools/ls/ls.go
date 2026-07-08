package ls

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

const defaultLsLimit = 500

// LsTool implements the directory listing tool.
type LsTool struct {
	cwd       string
	artifacts *common.ArtifactStore
}

func NewTool(cwd string) types.Tool {
	return &LsTool{cwd: cwd}
}

func NewToolWithArtifacts(cwd string, artifacts *common.ArtifactStore) types.Tool {
	return &LsTool{cwd: cwd, artifacts: artifacts}
}

func (t *LsTool) Name() string  { return "ls" }
func (t *LsTool) Label() string { return "List Directory" }
func (t *LsTool) Description() string {
	return `List the contents of a directory.

Usage:
- Use this tool to inspect a directory you have not seen yet; prefer it over running ls through bash for ordinary directory exploration.
- Use find when you need glob pattern matching across a tree.
- Use ignore to hide shallow entries that match glob patterns such as "*.log", "build/", or "vendor/**"; pass either one pattern or an array of patterns.
- Returns file and directory names, with directories suffixed with "/".
- The path must be a directory; use read for known files.
- Results are sorted alphabetically case-insensitively and capped by limit. Numeric strings such as "10" are accepted for limit.`
}

func (t *LsTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Directory path to list (default: cwd)",
			},
			"limit": map[string]any{
				"anyOf":       semanticLsIntegerSchema(),
				"description": "Maximum number of entries to return (default 500)",
			},
			"ignore": map[string]any{
				"anyOf": []map[string]any{
					{"type": "string"},
					{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
				},
				"description": "Optional shallow glob pattern or patterns to hide from the listing, such as \"*.log\", \"build/\", or \"vendor/**\".",
			},
		},
	}
}

func (t *LsTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	dirPath := t.cwd
	if p, ok := args["path"].(string); ok && p != "" {
		dirPath = common.ResolveToCwd(p, t.cwd)
	}

	limit := defaultLsLimit
	if v, ok := args["limit"]; ok {
		limit, _ = common.ToSemanticInt(v)
		if limit <= 0 {
			limit = defaultLsLimit
		}
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return common.ErrorResult(fmt.Sprintf("directory not found: %s", dirPath)), nil
		}
		return common.ErrorResult(fmt.Sprintf("failed to stat directory: %v", err)), nil
	}
	if !info.IsDir() {
		return common.ErrorResult(fmt.Sprintf("path is not a directory: %s", dirPath)), nil
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to read directory: %v", err)), nil
	}

	ignorePatterns := parseIgnorePatterns(args["ignore"])
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if isIgnoredEntry(name, entry.IsDir(), ignorePatterns) {
			continue
		}
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}

	// Sort case-insensitive
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})

	totalNames := len(names)
	if t.artifacts == nil {
		truncated := false
		if len(names) > limit {
			names = names[:limit]
			truncated = true
		}
		text := strings.Join(names, "\n")
		if truncated {
			text += fmt.Sprintf("\n\n... (%d entries total, showing first %d)", totalNames, limit)
		}
		if text == "" {
			text = "(empty directory)"
		}
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: text},
			},
			Details: map[string]any{
				"path": dirPath,
			},
		}, nil
	}

	preview := common.PreviewText(strings.Join(names, "\n"), common.TextPreviewOptions{
		ToolCallID:    toolCallID,
		ArtifactName:  "ls",
		ArtifactStore: t.artifacts,
		Strategy:      common.PreviewHead,
		MaxLines:      limit,
		MaxBytes:      common.DefaultMaxBytes,
	})
	text := preview.Text
	if meta, ok := preview.Details["output"].(map[string]any); ok {
		meta["totalEntries"] = totalNames
	}

	if text == "" {
		text = "(empty directory)"
	}

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
		Details: map[string]any{
			"path":   dirPath,
			"output": preview.Details["output"],
		},
	}, nil
}

func semanticLsIntegerSchema() []map[string]any {
	return []map[string]any{
		{"type": "integer"},
		{"type": "string", "pattern": `^-?\d+$`},
	}
}

func parseIgnorePatterns(v any) []string {
	switch patterns := v.(type) {
	case []string:
		return cleanIgnorePatterns(patterns)
	case []any:
		out := make([]string, 0, len(patterns))
		for _, item := range patterns {
			if pattern, ok := item.(string); ok {
				out = append(out, pattern)
			}
		}
		return cleanIgnorePatterns(out)
	case string:
		return cleanIgnorePatterns([]string{patterns})
	default:
		return nil
	}
}

func cleanIgnorePatterns(patterns []string) []string {
	out := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(filepath.ToSlash(pattern))
		pattern = strings.TrimPrefix(pattern, "./")
		if pattern != "" {
			out = append(out, pattern)
		}
	}
	return out
}

func isIgnoredEntry(name string, isDir bool, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}

	candidates := []string{filepath.ToSlash(name)}
	if isDir {
		candidates = append(candidates, filepath.ToSlash(name)+"/")
	}

	for _, pattern := range patterns {
		for _, candidate := range candidates {
			if pattern == candidate || strings.TrimSuffix(pattern, "/") == strings.TrimSuffix(candidate, "/") {
				return true
			}
			if strings.HasSuffix(pattern, "/**") {
				root := strings.TrimSuffix(pattern, "/**")
				if root == strings.TrimSuffix(candidate, "/") {
					return true
				}
			}
			if matched, err := filepath.Match(filepath.FromSlash(pattern), filepath.FromSlash(candidate)); err == nil && matched {
				return true
			}
		}
	}
	return false
}
