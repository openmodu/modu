package common

import (
	"regexp"
	"strconv"

	"github.com/openmodu/modu/pkg/types"
)

var semanticNumberPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

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

// ToSemanticInt converts numeric inputs and decimal numeric string literals.
func ToSemanticInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case string:
		if !semanticNumberPattern.MatchString(n) {
			return 0, false
		}
		value, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return 0, false
		}
		return int(value), true
	default:
		return 0, false
	}
}

// ToSemanticBool converts booleans and the exact string literals "true"/"false".
func ToSemanticBool(v any) (bool, bool) {
	switch b := v.(type) {
	case bool:
		return b, true
	case string:
		switch b {
		case "true":
			return true, true
		case "false":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}
