package tools

import (
	"context"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type PlanModeManager interface {
	EnterPlanMode()
	ExitPlanMode(plan string)
	IsPlanMode() bool
}

type EnterPlanModeTool struct {
	manager PlanModeManager
}

func NewEnterPlanModeTool(manager PlanModeManager) *EnterPlanModeTool {
	return &EnterPlanModeTool{manager: manager}
}

func (t *EnterPlanModeTool) Name() string  { return "enter_plan_mode" }
func (t *EnterPlanModeTool) Label() string { return "Enter Plan Mode" }
func (t *EnterPlanModeTool) Description() string {
	return "Enter planning mode for the current session before making changes."
}
func (t *EnterPlanModeTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *EnterPlanModeTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.manager != nil {
		t.manager.EnterPlanMode()
	}
	return planToolResult("plan mode enabled"), nil
}

type ExitPlanModeTool struct {
	manager PlanModeManager
}

func NewExitPlanModeTool(manager PlanModeManager) *ExitPlanModeTool {
	return &ExitPlanModeTool{manager: manager}
}

func (t *ExitPlanModeTool) Name() string  { return "exit_plan_mode" }
func (t *ExitPlanModeTool) Label() string { return "Exit Plan Mode" }
func (t *ExitPlanModeTool) Description() string {
	return "Exit planning mode and record the prepared plan summary."
}
func (t *ExitPlanModeTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan": map[string]any{"type": "string", "description": "The plan summary to record"},
		},
		"required": []string{"plan"},
	}
}
func (t *ExitPlanModeTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	plan, _ := args["plan"].(string)
	if t.manager != nil {
		t.manager.ExitPlanMode(plan)
	}
	return planToolResult("plan mode disabled"), nil
}

func planToolResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
}
