package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/types"
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

func NewReadTool(cwd string) *ReadTool {
	return &ReadTool{cwd: cwd}
}

func (t *ReadTool) Name() string  { return "read" }
func (t *ReadTool) Label() string { return "Read File" }
func (t *ReadTool) Description() string {
	return `Read the contents of a file at the given path. The path must be an absolute path or relative to the working directory. By default reads up to 2000 lines from the beginning. Use offset and limit to read specific sections. Images are returned as base64-encoded content.`
}

func (t *ReadTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to read (absolute or relative to cwd)",
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
		"required": []string{"path"},
	}
}

func (t *ReadTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	pathArg, _ := args["path"].(string)
	if pathArg == "" {
		return errorResult("path is required"), nil
	}

	resolved, err := ResolveReadPath(pathArg, t.cwd)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to resolve path: %v", err)), nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResult(fmt.Sprintf("file not found: %s", pathArg)), nil
		}
		return errorResult(fmt.Sprintf("failed to stat file: %v", err)), nil
	}

	if info.IsDir() {
		return errorResult(fmt.Sprintf("%s is a directory, not a file. Use ls to list directory contents.", pathArg)), nil
	}

	ext := strings.ToLower(filepath.Ext(resolved))
	if mimeType, isImage := imageExtensions[ext]; isImage {
		return t.readImage(resolved, mimeType)
	}

	return t.readText(resolved, info, args)
}

func (t *ReadTool) readImage(path, mimeType string) (agent.AgentToolResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read image: %v", err)), nil
	}

	// Detect MIME type from content if needed
	if mimeType == "" {
		mimeType = mime.TypeByExtension(filepath.Ext(path))
		if mimeType == "" {
			mimeType = "image/png"
		}
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			types.ImageContent{
				Type:     "image",
				Data:     encoded,
				MimeType: mimeType,
			},
		},
	}, nil
}

func (t *ReadTool) readText(path string, info os.FileInfo, args map[string]any) (agent.AgentToolResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	content := string(data)

	// Handle BOM
	content = strings.TrimPrefix(content, "\xef\xbb\xbf")

	lines := strings.Split(content, "\n")

	// Parse offset and limit
	offset := 0
	if v, ok := args["offset"]; ok {
		offset = toInt(v)
		if offset > 0 {
			offset-- // Convert to 0-based
		}
	}
	limit := ReadMaxLines
	if v, ok := args["limit"]; ok {
		limit = toInt(v)
		if limit <= 0 {
			limit = ReadMaxLines
		}
	}

	// Apply offset
	if offset > 0 && offset < len(lines) {
		lines = lines[offset:]
	} else if offset >= len(lines) {
		return agent.AgentToolResult{
			Content: []types.ContentBlock{
				types.TextContent{
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
		fmt.Fprintf(&sb, "%6d\t%s\n", startLine+i, line)
	}

	result := sb.String()

	if truncated {
		result += fmt.Sprintf("\n... (%d lines truncated, showing lines %d-%d of %d total)",
			totalLines-len(lines), startLine, startLine+len(lines)-1, totalLines+offset)
	}

	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			types.TextContent{
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

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}

func errorResult(msg string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			types.TextContent{
				Type: "text",
				Text: msg,
			},
		},
	}
}
