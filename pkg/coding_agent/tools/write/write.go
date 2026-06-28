package write

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

// WriteTool implements the file writing tool.
type WriteTool struct {
	cwd string
}

func NewTool(cwd string) types.Tool {
	return &WriteTool{cwd: cwd}
}

func (t *WriteTool) Name() string  { return "write" }
func (t *WriteTool) Label() string { return "Write File" }
func (t *WriteTool) Description() string {
	return `Write complete content to a file.

Usage:
- Use this tool primarily to create new files or to completely rewrite a file when that is explicitly required.
- Prefer edit for targeted changes to existing files because edit keeps the diff smaller and easier to review.
- If overwriting an existing file, read it first and make sure a full rewrite is necessary.
- Do not create documentation files, READMEs, examples, or broad scaffolding unless the user explicitly asks for them.
- The path may be absolute or relative to the working directory. Parent directories are created automatically.`
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

func (t *WriteTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	pathArg, _ := args["path"].(string)
	content, _ := args["content"].(string)

	if pathArg == "" {
		return common.ErrorResult("path is required"), nil
	}

	resolved := common.ResolveToCwd(pathArg, t.cwd)

	// Create parent directories
	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to create directories: %v", err)), nil
	}

	// Write the file
	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	bytes := len([]byte(content))

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Successfully wrote %s to %s", common.FormatSize(int64(bytes)), pathArg),
			},
		},
		Details: map[string]any{
			"path":  resolved,
			"bytes": bytes,
		},
	}, nil
}
