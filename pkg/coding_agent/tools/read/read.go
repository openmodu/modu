package read

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

var imageExtensions = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".svg":  "image/svg+xml",
}

// ReadTool implements the file reading tool.
type ReadTool struct {
	cwd string
}

func NewTool(cwd string) types.Tool {
	return &ReadTool{cwd: cwd}
}

func (t *ReadTool) Name() string  { return "read" }
func (t *ReadTool) Label() string { return "Read File" }
func (t *ReadTool) Description() string {
	return `Read a file from the local filesystem.

Usage:
- Use this tool to inspect known files; prefer it over bash commands such as cat, head, tail, or sed.
- The path may be absolute or relative to the working directory.
- By default it reads up to 2000 lines from the beginning. Use offset and limit when you only need a specific section of a large file.
- Results are returned with 1-based line numbers in "line<TAB>content" format. Do not include the line-number prefix when later using edit old_text.
- This tool reads files only, not directories. Use ls to inspect a directory.
- Images are returned as base64-encoded image content.`
}

func (t *ReadTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to read (absolute or relative to cwd)",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Alias for path, accepted for compatibility.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Line number to start reading from (1-based). Optional.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read. Optional, defaults to 2000.",
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"path"}},
			{"required": []string{"file_path"}},
		},
	}
}

func (t *ReadTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	pathArg, _ := args["path"].(string)
	if pathArg == "" {
		pathArg, _ = args["file_path"].(string)
	}
	if pathArg == "" {
		return common.ErrorResult("path is required"), nil
	}

	resolved, err := common.ResolveReadPath(pathArg, t.cwd)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to resolve path: %v", err)), nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return common.ErrorResult(fmt.Sprintf("file not found: %s", pathArg)), nil
		}
		return common.ErrorResult(fmt.Sprintf("failed to stat file: %v", err)), nil
	}

	if info.IsDir() {
		return common.ErrorResult(fmt.Sprintf("%s is a directory, not a file. Use ls to list directory contents.", pathArg)), nil
	}

	ext := strings.ToLower(filepath.Ext(resolved))
	if mimeType, isImage := imageExtensions[ext]; isImage {
		return t.readImage(resolved, mimeType)
	}

	return t.readText(resolved, info, args)
}

func (t *ReadTool) readImage(path, mimeType string) (types.ToolResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to read image: %v", err)), nil
	}

	// Detect MIME type from content if needed
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(path))
		if mimeType == "" {
			mimeType = "image/png"
		}
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.ImageContent{
				Type:     "image",
				Data:     encoded,
				MimeType: mimeType,
			},
		},
	}, nil
}

func (t *ReadTool) readText(path string, info os.FileInfo, args map[string]any) (types.ToolResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return common.ErrorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	content := string(data)

	// Handle BOM
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")

	lines := strings.Split(content, "\n")

	// Parse offset and limit
	offset := 0
	if v, ok := args["offset"]; ok {
		offset = common.ToInt(v)
		if offset > 0 {
			offset-- // Convert to 0-based
		}
	}
	limit := common.ReadMaxLines
	if v, ok := args["limit"]; ok {
		limit = common.ToInt(v)
		if limit <= 0 {
			limit = common.ReadMaxLines
		}
	}

	// Apply offset
	if offset > 0 && offset < len(lines) {
		lines = lines[offset:]
	} else if offset >= len(lines) {
		return types.ToolResult{
			Content: []types.ContentBlock{
				&types.TextContent{
					Type: "text",
					Text: fmt.Sprintf("offset %d is beyond end of file (%d lines)", offset+1, len(lines)),
				},
			},
		}, nil
	}

	totalLines := len(lines)
	truncated := false

	// Apply limit
	if len(lines) > limit {
		lines = lines[:limit]
		truncated = true
	}

	// Format with line numbers
	var sb strings.Builder
	startLine := offset + 1
	for i, line := range lines {
		fmt.Fprintf(&sb, "%d\t%s\n", startLine+i, line)
	}

	result := sb.String()

	if truncated {
		result += fmt.Sprintf("\n... (%d lines truncated, showing lines %d-%d of %d total)",
			totalLines-len(lines), startLine, startLine+len(lines)-1, totalLines+offset)
	}

	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: result,
			},
		},
		Details: map[string]any{
			"path":      path,
			"size":      info.Size(),
			"lines":     totalLines + offset,
			"truncated": truncated,
		},
	}, nil
}
