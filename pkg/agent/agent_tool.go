package agent

import (
	"context"

	"github.com/crosszan/modu/pkg/types"
)

type AgentToolResult struct {
	Content []types.ContentBlock `json:"content"`
	Details any                  `json:"details"`
}

type AgentToolUpdateCallback func(partial AgentToolResult)

type AgentTool interface {
	Name() string
	Label() string
	Description() string
	Parameters() any
	Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error)
}
