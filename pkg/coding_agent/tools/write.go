package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/providers"
)

// WriteTool implements the file writing tool.
type WriteTool struct {
	cwd string
}

func NewWriteTool(cwd string) *WriteTool {
	return &WriteTool{cwd: cwd}
}

func (t *WriteTool) Name() string  { return "write" }
func (t *WriteTool) Label() string { return "Write File" }
func (t *WriteTool) Description() string {
	return `Write content to a file at the given path. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories as needed.`
}

func (t *WriteTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to write to (absolute or relative to cwd)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	pathArg, _ := args["path"].(string)
	content, _ := args["content"].(string)

	if pathArg == "" {
		return errorResult("path is required"), nil
	}

	resolved := ResolveToCwd(pathArg, t.cwd)

	// Create parent directories
	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errorResult(fmt.Sprintf("failed to create directories: %v", err)), nil
	}

	// Write the file
	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return errorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	bytes := len([]byte(content))

	return agent.AgentToolResult{
		Content: []providers.ContentBlock{
			providers.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Successfully wrote %s to %s", FormatSize(int64(bytes)), pathArg),
			},
		},
		Details: map[string]any{
			"path":  resolved,
			"bytes": bytes,
		},
	}, nil
}
