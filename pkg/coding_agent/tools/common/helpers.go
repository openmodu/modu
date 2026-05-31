package common

import (
	"github.com/openmodu/modu/pkg/types"
)

// ErrorResult creates an error ToolResult with the given message.
func ErrorResult(msg string) types.ToolResult {
	return types.ToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: msg,
			},
		},
	}
}

// ToInt converts various numeric types to int.
func ToInt(v any) int {
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
