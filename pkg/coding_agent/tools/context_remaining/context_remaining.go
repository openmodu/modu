package context_remaining

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/types"
)

// Provider supplies a read-only view of the active context budget.
type Provider interface {
	TokensUntilCompaction() (int, bool)
}

// Tool reports how many tokens remain before automatic compaction.
type Tool struct {
	provider Provider
}

func New(provider Provider) types.Tool {
	return &Tool{provider: provider}
}

func (t *Tool) Name() string {
	return "get_context_remaining"
}

func (t *Tool) Label() string {
	return "Context Remaining"
}

func (t *Tool) Description() string {
	return "Get the remaining tokens in the current context window before automatic compaction."
}

func (t *Tool) Parameters() any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

func (t *Tool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	var (
		tokensLeft any
		text       string
	)
	if t.provider == nil {
		tokensLeft = nil
		text = "Remaining context tokens before compaction are unknown."
	} else if remaining, ok := t.provider.TokensUntilCompaction(); ok {
		tokensLeft = remaining
		text = fmt.Sprintf("Remaining context tokens before compaction: %d", remaining)
	} else {
		tokensLeft = nil
		text = "Remaining context tokens before compaction are unknown."
	}

	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		Details: map[string]any{
			"tokens_left": tokensLeft,
		},
	}, nil
}
