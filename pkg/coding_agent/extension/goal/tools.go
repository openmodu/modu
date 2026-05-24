package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type createGoalTool struct{ store *Store }

func (t *createGoalTool) Name() string  { return "create_goal" }
func (t *createGoalTool) Label() string { return "Create Goal" }
func (t *createGoalTool) Description() string {
	return `Create a goal only when explicitly requested by the user or system/developer instructions; do not infer goals from ordinary tasks. Set token_budget only when an explicit token budget is requested. Fails if a goal exists; use update_goal only for status.`
}

func (t *createGoalTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"objective": map[string]any{
				"type":        "string",
				"description": "Required. The concrete objective to start pursuing.",
			},
			"token_budget": map[string]any{
				"type":        "integer",
				"description": "Optional positive token budget for the new active goal.",
			},
		},
		"required":             []string{"objective"},
		"additionalProperties": false,
	}
}

func (t *createGoalTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	objective, _ := args["objective"].(string)
	budget, err := optionalPositiveInt(args["token_budget"])
	if err != nil {
		return textResult(fmt.Sprintf("create_goal failed: %v", err), true), nil
	}
	g, err := t.store.StartWithBudget(objective, budget)
	if err != nil {
		return textResult(fmt.Sprintf("create_goal failed: %v", err), true), nil
	}
	return textResult(formatGoalToolResponse(&g, false), false), nil
}

type updateGoalTool struct {
	store      *Store
	onComplete func(Goal)
}

func (t *updateGoalTool) Name() string  { return "update_goal" }
func (t *updateGoalTool) Label() string { return "Update Goal" }
func (t *updateGoalTool) Description() string {
	return `Update the existing goal. Set status to "complete" only when the objective has actually been achieved and no required work remains. Pause, resume, and budget-limited status changes are controlled by the user or system.`
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
		"required":             []string{"status"},
		"additionalProperties": false,
	}
}

func (t *updateGoalTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	status, _ := args["status"].(string)
	if status != "complete" {
		return textResult("update_goal can only mark the existing goal complete; pause, resume, and budget-limited status changes are controlled by the user or system", true), nil
	}
	g, err := t.store.MarkComplete()
	if err != nil {
		return textResult(fmt.Sprintf("update_goal failed: %v", err), true), nil
	}
	if t.onComplete != nil {
		t.onComplete(g)
	}
	return textResult(formatGoalToolResponse(&g, true), false), nil
}

type getGoalTool struct{ store *Store }

func (t *getGoalTool) Name() string  { return "get_goal" }
func (t *getGoalTool) Label() string { return "Get Goal" }
func (t *getGoalTool) Description() string {
	return `Get the current goal for this thread, including status, budgets, token and elapsed-time usage, and remaining token budget.`
}

func (t *getGoalTool) Parameters() any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

func (t *getGoalTool) Execute(_ context.Context, _ string, _ map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	g, ok := t.store.Current()
	if !ok {
		return textResult(formatGoalToolResponse(nil, false), false), nil
	}
	return textResult(formatGoalToolResponse(&g, false), false), nil
}

type goalToolSnapshot struct {
	ThreadID        string `json:"threadId,omitempty"`
	Objective       string `json:"objective"`
	Status          Status `json:"status"`
	TokenBudget     *int   `json:"tokenBudget,omitempty"`
	TokensUsed      int    `json:"tokensUsed"`
	TimeUsedSeconds int64  `json:"timeUsedSeconds"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type goalToolResponse struct {
	Goal                   *goalToolSnapshot `json:"goal"`
	RemainingTokens        *int              `json:"remainingTokens"`
	CompletionBudgetReport *string           `json:"completionBudgetReport"`
}

func formatGoalToolResponse(g *Goal, includeCompletionBudgetReport bool) string {
	response := goalToolResponse{}
	if g != nil {
		response.Goal = &goalToolSnapshot{
			ThreadID:        g.ThreadID,
			Objective:       g.Objective,
			Status:          g.Status,
			TokenBudget:     cloneIntPtr(g.TokenBudget),
			TokensUsed:      g.TokensUsed,
			TimeUsedSeconds: g.TimeUsedSeconds,
			CreatedAt:       g.CreatedAt,
			UpdatedAt:       g.UpdatedAt,
		}
		if g.TokenBudget != nil {
			remaining := *g.TokenBudget - g.TokensUsed
			if remaining < 0 {
				remaining = 0
			}
			response.RemainingTokens = &remaining
		}
		if includeCompletionBudgetReport && g.Status == StatusComplete {
			if report := completionBudgetReport(g); report != "" {
				response.CompletionBudgetReport = &report
			}
		}
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

func completionBudgetReport(g *Goal) string {
	parts := ""
	if g.TokenBudget != nil {
		parts = fmt.Sprintf("tokens used: %d of %d", g.TokensUsed, *g.TokenBudget)
	}
	if g.TimeUsedSeconds > 0 {
		if parts != "" {
			parts += "; "
		}
		parts += fmt.Sprintf("time used: %d seconds", g.TimeUsedSeconds)
	}
	if parts == "" {
		return ""
	}
	return "Goal achieved. Report final budget usage to the user: " + parts + "."
}

func optionalPositiveInt(v any) (*int, error) {
	if v == nil {
		return nil, nil
	}
	var out int
	switch n := v.(type) {
	case int:
		out = n
	case int64:
		if n > int64(math.MaxInt) {
			return nil, ErrInvalidBudget
		}
		out = int(n)
	case float64:
		if math.Trunc(n) != n || n > float64(math.MaxInt) {
			return nil, ErrInvalidBudget
		}
		out = int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil || i > int64(math.MaxInt) {
			return nil, ErrInvalidBudget
		}
		out = int(i)
	default:
		return nil, ErrInvalidBudget
	}
	if out <= 0 {
		return nil, ErrInvalidBudget
	}
	return &out, nil
}

func textResult(s string, isError bool) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: s},
		},
		Details: map[string]any{
			"isError": isError,
		},
	}
}
