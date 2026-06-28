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
	cwd       string
	readState *common.FileReadState
}

func NewTool(cwd string) types.Tool {
	return &WriteTool{cwd: cwd}
}

func NewTrackedTool(cwd string, readState *common.FileReadState) types.Tool {
	return &WriteTool{cwd: cwd, readState: readState}
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
- Only use emojis if the user explicitly requests them.
- The content argument is required; pass an explicit empty string only when the intended file content is empty.
- Reports whether the file was created or updated after the write succeeds.
- The path may be absolute or relative to the working directory. The file_path alias is accepted for Claude Code compatibility. Parent directories are created automatically.`
}

func (t *WriteTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to write to (absolute or relative to cwd)",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Alias for path, accepted for compatibility.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file",
			},
		},
		"required": []string{"content"},
		"anyOf": []map[string]any{
			{"required": []string{"path"}},
			{"required": []string{"file_path"}},
		},
	}
}

func (t *WriteTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	pathArg, _ := args["path"].(string)
	if pathArg == "" {
		pathArg, _ = args["file_path"].(string)
	}
	content, hasContent := args["content"].(string)

	if pathArg == "" {
		return common.ErrorResult("path is required"), nil
	}
	if !hasContent {
		return common.ErrorResult("content is required"), nil
	}

	resolved := common.ResolveToCwd(pathArg, t.cwd)
	writeType := "create"
	if info, err := os.Stat(resolved); err == nil {
		if info.IsDir() {
			return common.ErrorResult(fmt.Sprintf("%s is a directory, not a file. Use a file path for write.", pathArg)), nil
		}
		if result, ok := t.validateExistingFileReadState(pathArg, resolved); !ok {
			return result, nil
		}
		writeType = "update"
	} else if err != nil && !os.IsNotExist(err) {
		return common.ErrorResult(fmt.Sprintf("failed to stat file: %v", err)), nil
	}

	// Create parent directories
	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to create directories: %v", err)), nil
	}

	// Write the file
	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to write file: %v", err)), nil
	}
	t.recordWrittenState(resolved, content)

	bytes := len([]byte(content))
	message := fmt.Sprintf("File created successfully at: %s", pathArg)
	if writeType == "update" {
		message = fmt.Sprintf("The file %s has been updated successfully.", pathArg)
	}

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: message,
			},
		},
		Details: map[string]any{
			"path":  resolved,
			"bytes": bytes,
			"type":  writeType,
		},
	}, nil
}

func (t *WriteTool) validateExistingFileReadState(displayPath, path string) (types.ToolResult, bool) {
	if t.readState == nil {
		return types.ToolResult{}, true
	}
	record, ok := t.readState.Get(path)
	if !ok || record.Partial {
		return common.ErrorResult("File has not been read yet. Read it first before writing to it."), false
	}
	// Write fully rewrites the file, so always compare the current content to
	// what was read rather than trusting mtime: coarse-resolution filesystems
	// can leave mtime unchanged after an external edit, which would otherwise
	// let a stale overwrite slip through.
	current, err := os.ReadFile(path)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to read current file before write: %v", err)), false
	}
	if string(current) != record.Content {
		return common.ErrorResult(fmt.Sprintf("File has been modified since read: %s. Read it again before attempting to write it.", displayPath)), false
	}
	return types.ToolResult{}, true
}

func (t *WriteTool) recordWrittenState(path, content string) {
	if t.readState == nil {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	t.readState.Record(path, content, info.ModTime().UnixNano(), false)
}
