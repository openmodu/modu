package utils

import (
	"testing"

	"github.com/crosszan/modu/pkg/llm"
)

func TestValidateToolCall(t *testing.T) {
	tool := llm.ToolDefinition{
		Name:        "greet",
		Description: "greet tool",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
	}
	call := llm.ToolCall{
		Name:      "greet",
		Arguments: map[string]any{"name": "modu"},
	}
	_, err := ValidateToolCall([]llm.ToolDefinition{tool}, call)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateToolCallInvalid(t *testing.T) {
	tool := llm.ToolDefinition{
		Name:        "greet",
		Description: "greet tool",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
			"required": []any{"name"},
		},
	}
	call := llm.ToolCall{
		Name:      "greet",
		Arguments: map[string]any{"name": 123},
	}
	_, err := ValidateToolCall([]llm.ToolDefinition{tool}, call)
	if err == nil {
		t.Fatalf("expected error")
	}
}
