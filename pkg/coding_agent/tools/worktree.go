package tools

import (
	"context"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type WorktreeManager interface {
	EnterWorktree() (string, error)
	ExitWorktree() error
	ActiveWorktree() string
}

type EnterWorktreeTool struct {
	manager WorktreeManager
}

func NewEnterWorktreeTool(manager WorktreeManager) *EnterWorktreeTool {
	return &EnterWorktreeTool{manager: manager}
}

func (t *EnterWorktreeTool) Name() string  { return "enter_worktree" }
func (t *EnterWorktreeTool) Label() string { return "Enter Worktree" }
func (t *EnterWorktreeTool) Description() string {
	return "Create a temporary git worktree for isolated editing and switch the session into it."
}
func (t *EnterWorktreeTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *EnterWorktreeTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.manager == nil {
		return worktreeToolResult("worktree manager is not configured"), nil
	}
	path, err := t.manager.EnterWorktree()
	if err != nil {
		return worktreeToolResult(err.Error()), nil
	}
	return worktreeToolResult("entered worktree: " + path), nil
}

type ExitWorktreeTool struct {
	manager WorktreeManager
}

func NewExitWorktreeTool(manager WorktreeManager) *ExitWorktreeTool {
	return &ExitWorktreeTool{manager: manager}
}

func (t *ExitWorktreeTool) Name() string  { return "exit_worktree" }
func (t *ExitWorktreeTool) Label() string { return "Exit Worktree" }
func (t *ExitWorktreeTool) Description() string {
	return "Exit the active temporary worktree and restore the original working directory."
}
func (t *ExitWorktreeTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *ExitWorktreeTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.manager == nil {
		return worktreeToolResult("worktree manager is not configured"), nil
	}
	if err := t.manager.ExitWorktree(); err != nil {
		return worktreeToolResult(err.Error()), nil
	}
	return worktreeToolResult("exited worktree"), nil
}

func worktreeToolResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
}
