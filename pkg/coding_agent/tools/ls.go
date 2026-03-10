package tools

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/types"
)

const defaultLsLimit = 500

// LsTool implements the directory listing tool.
type LsTool struct {
	cwd string
}

func NewLsTool(cwd string) *LsTool {
	return &LsTool{cwd: cwd}
}

func (t *LsTool) Name() string  { return "ls" }
func (t *LsTool) Label() string { return "List Directory" }
func (t *LsTool) Description() string {
	return `List the contents of a directory. Returns file and directory names, with directories suffixed with '/'. Sorted alphabetically (case-insensitive).`
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
				"type":        "integer",
				"description": "Maximum number of entries to return (default 500)",
			},
		},
	}
}

func (t *LsTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	dirPath := t.cwd
	if p, ok := args["path"].(string); ok && p != "" {
		dirPath = ResolveToCwd(p, t.cwd)
	}

	limit := defaultLsLimit
	if v, ok := args["limit"]; ok {
		limit = toInt(v)
		if limit <= 0 {
			limit = defaultLsLimit
		}
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResult(fmt.Sprintf("directory not found: %s", dirPath)), nil
		}
		return errorResult(fmt.Sprintf("failed to read directory: %v", err)), nil
	}

	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}

	// Sort case-insensitive
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})

	truncated := false
	if len(names) > limit {
		names = names[:limit]
		truncated = true
	}

	text := strings.Join(names, "\n")
	if truncated {
		text += fmt.Sprintf("\n\n... (%d entries total, showing first %d)", len(entries), limit)
	}

	if text == "" {
		text = "(empty directory)"
	}

	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
	}, nil
}
