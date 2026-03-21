package agent

import (
	"context"

	"github.com/openmodu/modu/pkg/types"
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

// ParallelTool is an optional interface for tools that are safe to run
// concurrently with other parallel tools in the same LLM response turn.
// When multiple parallel tools appear in the same turn, they run in a
// sync.WaitGroup instead of sequentially.
type ParallelTool interface {
	Parallel() bool
}
