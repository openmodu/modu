package toolresult

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/tools/common"
	"github.com/openmodu/modu/pkg/types"
)

type Tool struct {
	store *common.ArtifactStore
}

func NewTool(store *common.ArtifactStore) types.Tool {
	return &Tool{store: store}
}

func (t *Tool) Name() string  { return "read_tool_result" }
func (t *Tool) Label() string { return "Read Tool Result" }
func (t *Tool) Description() string {
	return `Read a paged slice from a truncated tool-result artifact.

Usage:
- Use this when a previous tool result was truncated and you need lines that were not in the preview.
- call_id must be the original tool call id whose output metadata included an artifact.
- offset is a 1-based line number and limit is the maximum number of lines to return.
- offset and limit are required so full artifacts are not loaded back into model context.`
}

func (t *Tool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"call_id": map[string]any{
				"type":        "string",
				"description": "The original tool call id.",
			},
			"tool_call_id": map[string]any{
				"type":        "string",
				"description": "Alias for call_id.",
			},
			"offset": map[string]any{
				"anyOf":       semanticIntegerSchema(1),
				"description": "1-based line number to start reading from.",
			},
			"limit": map[string]any{
				"anyOf":       semanticIntegerSchema(1),
				"description": "Maximum number of lines to return.",
			},
		},
		"required": []string{"offset", "limit"},
		"anyOf": []map[string]any{
			{"required": []string{"call_id"}},
			{"required": []string{"tool_call_id"}},
		},
	}
}

func semanticIntegerSchema(minimum int) []map[string]any {
	return []map[string]any{
		{"type": "integer", "minimum": minimum},
		{"type": "string", "pattern": `^[1-9]\d*$`},
	}
}

func (t *Tool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	if t.store == nil {
		return errorResult("artifact store is not configured"), nil
	}
	callID, _ := args["call_id"].(string)
	if callID == "" {
		callID, _ = args["tool_call_id"].(string)
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return errorResult("call_id is required"), nil
	}
	offset, ok := requiredPositiveInt(args, "offset")
	if !ok {
		return errorResult("offset is required and must be >= 1"), nil
	}
	limit, ok := requiredPositiveInt(args, "limit")
	if !ok {
		return errorResult("limit is required and must be >= 1"), nil
	}

	ref, err := t.store.Find(callID)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	lines, returned, hasMore, err := readLineWindow(ctx, ref.Path, offset, limit)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to read artifact: %v", err)), nil
	}
	if returned == 0 {
		lines = []string{fmt.Sprintf("(offset %d is beyond the artifact output)", offset)}
	}
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: strings.Join(lines, "\n")}},
		Details: map[string]any{
			"artifactId":    ref.ID,
			"artifactPath":  ref.Path,
			"artifactBytes": ref.Bytes,
			"callId":        callID,
			"offset":        offset,
			"limit":         limit,
			"returnedLines": returned,
			"hasMore":       hasMore,
		},
	}, nil
}

func requiredPositiveInt(args map[string]any, name string) (int, bool) {
	value, ok := common.ToSemanticInt(args[name])
	return value, ok && value > 0
}

func readLineWindow(ctx context.Context, path string, offset, limit int) ([]string, int, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	lineNo := 0
	out := make([]string, 0, limit)
	for {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, err
		}
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			if err == io.EOF {
				return out, len(out), false, nil
			}
			return nil, 0, false, err
		}
		lineNo++
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if lineNo >= offset {
			out = append(out, fmt.Sprintf("%d\t%s", lineNo, line))
			if len(out) == limit {
				hasMore := err != io.EOF
				if !hasMore {
					if next, nextErr := reader.Peek(1); nextErr == nil && len(next) > 0 {
						hasMore = true
					}
				}
				return out, len(out), hasMore, nil
			}
		}
		if err == io.EOF {
			return out, len(out), false, nil
		}
	}
}

func errorResult(msg string) types.ToolResult {
	result := common.ErrorResult(msg)
	result.IsError = true
	return result
}
