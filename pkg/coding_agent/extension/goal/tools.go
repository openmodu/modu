package goal

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// updateGoalTool lets the model declare the active goal complete. Upstream
// pi-goal accepts the broader COMPLETABLE_GOAL_STATUS_VALUES; in MVP we only
// honour "complete" — pausing / cancelling go through user-driven slash
// commands so the model can't unilaterally stop a goal mid-flight.
type updateGoalTool struct{ store *Store }

func (t *updateGoalTool) Name() string  { return "update_goal" }
func (t *updateGoalTool) Label() string { return "Update Goal" }
func (t *updateGoalTool) Description() string {
	return `Mark the active thread goal as complete. Call this only after performing a completion audit and verifying every required artifact actually exists with the expected content. The only accepted status is "complete".`
}

func (t *updateGoalTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"complete"},
				"description": "Set to \"complete\" only when the objective is achieved and no required work remains.",
			},
		},
		"required": []string{"status"},
	}
}

func (t *updateGoalTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	status, _ := args["status"].(string)
	if status != "complete" {
		return textResult(fmt.Sprintf("update_goal only accepts status=\"complete\" in this build, got %q", status)), nil
	}
	g, err := t.store.MarkComplete()
	if err != nil {
		return textResult(fmt.Sprintf("update_goal failed: %v", err)), nil
	}
	return textResult(fmt.Sprintf("Goal %s marked complete. The continuation loop will stop after this turn.", g.ID[:8])), nil
}

// getGoalTool lets the model peek at the current goal — useful when an audit
// step needs to recheck what the original objective said without trusting
// summarized memory.
type getGoalTool struct{ store *Store }

func (t *getGoalTool) Name() string  { return "get_goal" }
func (t *getGoalTool) Label() string { return "Get Goal" }
func (t *getGoalTool) Description() string {
	return `Inspect the active thread goal: status, objective text, timestamps. Returns "(no goal set)" if none is active.`
}

func (t *getGoalTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *getGoalTool) Execute(_ context.Context, _ string, _ map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	return textResult(t.store.Summary()), nil
}

func textResult(s string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: s},
		},
	}
}
