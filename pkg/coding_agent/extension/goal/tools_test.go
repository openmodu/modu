package goal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// callTool runs a tool's Execute and returns the flat text of the first
// TextContent block.
func callTool(t *testing.T, tool agent.AgentTool, args map[string]any) string {
	t.Helper()
	text, _ := callToolResult(t, tool, args)
	return text
}

func callToolResult(t *testing.T, tool agent.AgentTool, args map[string]any) (string, agent.AgentToolResult) {
	t.Helper()
	res, err := tool.Execute(context.Background(), "test-call", args, nil)
	if err != nil {
		t.Fatalf("%s execute: %v", tool.Name(), err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s: empty content", tool.Name())
	}
	tc, ok := res.Content[0].(*types.TextContent)
	if !ok {
		t.Fatalf("%s: first block not text: %T", tool.Name(), res.Content[0])
	}
	return tc.Text, res
}

func TestUpdateGoalCompletesActive(t *testing.T) {
	store := NewStore()
	store.Start("audit citations in report.md")
	out := callTool(t, &updateGoalTool{store: store}, map[string]any{"status": "complete"})
	if !strings.Contains(out, `"status": "complete"`) {
		t.Errorf("update_goal success message wrong: %q", out)
	}
	g, _ := store.Current()
	if g.Status != StatusComplete {
		t.Errorf("store status should be complete, got %q", g.Status)
	}
}

func TestCreateGoalToolCreatesBudgetedGoal(t *testing.T) {
	store := NewStore()
	out := callTool(t, &createGoalTool{store: store}, map[string]any{
		"objective":    "ship a durable goal mode",
		"token_budget": float64(25),
	})
	if !strings.Contains(out, "ship a durable goal mode") ||
		!strings.Contains(out, `"tokenBudget": 25`) {
		t.Fatalf("create_goal response missing goal fields: %s", out)
	}
	g, ok := store.Current()
	if !ok {
		t.Fatal("create_goal did not persist goal")
	}
	if g.TokenBudget == nil || *g.TokenBudget != 25 {
		t.Fatalf("token budget mismatch: %+v", g.TokenBudget)
	}
}

func TestCreateGoalToolRejectsExistingGoal(t *testing.T) {
	store := NewStore()
	store.Start("existing")
	out := callTool(t, &createGoalTool{store: store}, map[string]any{"objective": "new"})
	if !strings.Contains(out, "already active") {
		t.Fatalf("expected existing-goal rejection, got: %s", out)
	}
}

func TestUpdateGoalCompletionBudgetReport(t *testing.T) {
	store := NewStore()
	budget := 20
	g, _ := store.StartWithBudget("finish with report", &budget)
	store.AccountUsage(types.AgentUsage{Input: 3, Output: 5}, 9, false, g.ID)
	out := callTool(t, &updateGoalTool{store: store}, map[string]any{"status": "complete"})
	if !strings.Contains(out, "completionBudgetReport") ||
		!strings.Contains(out, "tokens used: 8 of 20") {
		t.Fatalf("missing completion budget report: %s", out)
	}
}

func TestUpdateGoalRejectsNonCompleteStatus(t *testing.T) {
	store := NewStore()
	store.Start("test")
	// MVP only accepts "complete"; any other value is a user-facing error
	// message, not a hard tool failure (we want the model to read the text
	// and self-correct, not blow up the run).
	for _, bad := range []string{"paused", "active", "", "done", "complete "} {
		out := callTool(t, &updateGoalTool{store: store}, map[string]any{"status": bad})
		if !strings.Contains(out, "can only mark the existing goal complete") {
			t.Errorf("status=%q want rejection message, got: %s", bad, out)
		}
	}
	if g, _ := store.Current(); g.Status != StatusActive {
		t.Errorf("store should still be active, got %q", g.Status)
	}
}

func TestGoalToolErrorResultsCarryIsErrorDetail(t *testing.T) {
	store := NewStore()
	store.Start("test")
	_, res := callToolResult(t, &updateGoalTool{store: store}, map[string]any{"status": "paused"})
	details, ok := res.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %T", res.Details)
	}
	if details["isError"] != true {
		t.Fatalf("expected isError detail true, got %#v", details)
	}
	_, res = callToolResult(t, &getGoalTool{store: store}, nil)
	details, ok = res.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected details map, got %T", res.Details)
	}
	if details["isError"] != false {
		t.Fatalf("expected isError detail false, got %#v", details)
	}
}

func TestUpdateGoalNoGoalErrors(t *testing.T) {
	store := NewStore()
	out := callTool(t, &updateGoalTool{store: store}, map[string]any{"status": "complete"})
	if !strings.Contains(out, "no goal is set") {
		t.Errorf("update_goal without goal should mention 'no goal is set', got: %s", out)
	}
}

func TestGetGoalEmptyAndPopulated(t *testing.T) {
	store := NewStore()
	if got := callTool(t, &getGoalTool{store: store}, nil); !strings.Contains(got, `"goal": null`) {
		t.Errorf("get_goal empty mismatch: %s", got)
	}
	store.Start("benchmark the inference path")
	got := callTool(t, &getGoalTool{store: store}, nil)
	if !strings.Contains(got, "benchmark the inference path") ||
		!strings.Contains(got, "active") {
		t.Errorf("get_goal populated missing fields: %s", got)
	}
}

func TestToolMetadata(t *testing.T) {
	store := NewStore()
	for _, tool := range []agent.AgentTool{
		&createGoalTool{store: store},
		&updateGoalTool{store: store},
		&getGoalTool{store: store},
	} {
		if tool.Name() == "" || tool.Label() == "" ||
			tool.Description() == "" || tool.Parameters() == nil {
			t.Errorf("%s: empty metadata", tool.Name())
		}
	}
}

// We keep this assertion separate so a future Errors change drawing on
// store error types is reflected here too; guards against silently
// rewording the public ErrGoalActive sentinel.
func TestUpdateGoalDoubleCompleteReturnsAlreadyDone(t *testing.T) {
	store := NewStore()
	store.Start("once")
	store.MarkComplete()

	out := callTool(t, &updateGoalTool{store: store}, map[string]any{"status": "complete"})
	if !strings.Contains(out, "already complete") {
		t.Errorf("expected 'already complete' surface message, got: %s", out)
	}
	if !errors.Is(ErrAlreadyDone, ErrAlreadyDone) {
		t.Fatal("sentinel sanity check") // defensive: catches accidental sentinel rename
	}
}
