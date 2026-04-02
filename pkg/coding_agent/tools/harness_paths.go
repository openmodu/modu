package tools

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type HarnessPathsProvider interface {
	HarnessPathsMap() map[string]any
}

type HarnessPathsTool struct {
	provider HarnessPathsProvider
}

func NewHarnessPathsTool(provider HarnessPathsProvider) *HarnessPathsTool {
	return &HarnessPathsTool{provider: provider}
}

func (t *HarnessPathsTool) Name() string  { return "harness_paths" }
func (t *HarnessPathsTool) Label() string { return "Harness Paths" }
func (t *HarnessPathsTool) Description() string {
	return "Return internal harness-managed runtime paths such as sessions, plans, worktrees, tool results, and memory directories."
}

func (t *HarnessPathsTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *HarnessPathsTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.provider == nil {
		return textResult("harness paths provider is not configured"), nil
	}
	details := t.provider.HarnessPathsMap()
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{
			Type: "text",
			Text: fmt.Sprintf("Harness runtime paths are available in details. Use read/ls with the absolute paths if needed."),
		}},
		Details: details,
	}, nil
}
