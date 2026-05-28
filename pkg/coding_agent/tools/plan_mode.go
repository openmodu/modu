package tools

import (
	"context"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type PlanModeManager interface {
	EnterPlanMode()
	IsPlanMode() bool
	// SubmitPlan presents the plan to the user for approval and returns the
	// message to feed back to the model. On approval it exits plan mode and
	// seeds the todo list from steps; on rejection it stays in plan mode and
	// relays the user's feedback.
	SubmitPlan(ctx context.Context, plan string, steps []string) string
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
func (t *EnterPlanModeTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.ToolUpdateCallback) (agent.ToolResult, error) {
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
	return "Present the finished implementation plan to the user for approval. " +
		"Call this only after research is complete. The user must approve before " +
		"any changes are made: on approval plan mode is exited, the steps become " +
		"the todo list, and you execute them in order. If the user rejects, you " +
		"remain in plan mode — revise the plan from their feedback and call this again."
}
func (t *ExitPlanModeTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"plan": map[string]any{
				"type":        "string",
				"description": "The plan summary in markdown, shown to the user for approval",
			},
			"steps": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Ordered sub-tasks to execute after approval; each becomes a todo item",
			},
		},
		"required": []string{"plan"},
	}
}
func (t *ExitPlanModeTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.ToolUpdateCallback) (agent.ToolResult, error) {
	plan, _ := args["plan"].(string)
	var steps []string
	if raw, ok := args["steps"].([]any); ok {
		for _, s := range raw {
			if str, ok := s.(string); ok && str != "" {
				steps = append(steps, str)
			}
		}
	}
	if t.manager == nil {
		return planToolResult("plan recorded"), nil
	}
	return planToolResult(t.manager.SubmitPlan(ctx, plan, steps)), nil
}

func planToolResult(text string) agent.ToolResult {
	return agent.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
}
