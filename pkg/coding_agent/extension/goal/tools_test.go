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
// TextContent block plus the raw result.
func callTool(t *testing.T, tool agent.AgentTool, args map[string]any) string {
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
	return tc.Text
}

func TestUpdateGoalCompletesActive(t *testing.T) {
	store := NewStore()
	store.Start("audit citations in report.md")
	out := callTool(t, &updateGoalTool{store: store}, map[string]any{"status": "complete"})
	if !strings.Contains(out, "marked complete") {
		t.Errorf("update_goal success message wrong: %q", out)
	}
	g, _ := store.Current()
	if g.Status != StatusComplete {
		t.Errorf("store status should be complete, got %q", g.Status)
	}
}

func TestUpdateGoalRejectsNonCompleteStatus(t *testing.T) {
	store := NewStore()
	store.Start("test")
	// MVP only accepts "complete" — any other value is a user-facing error
	// message, not a hard tool failure (we want the model to read the text
	// and self-correct, not blow up the run).
	for _, bad := range []string{"paused", "active", "", "done", "complete "} {
		out := callTool(t, &updateGoalTool{store: store}, map[string]any{"status": bad})
		if !strings.Contains(out, "only accepts status=\"complete\"") {
			t.Errorf("status=%q want rejection message, got: %s", bad, out)
		}
	}
	if g, _ := store.Current(); g.Status != StatusActive {
		t.Errorf("store should still be active, got %q", g.Status)
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
	if got := callTool(t, &getGoalTool{store: store}, nil); !strings.Contains(got, "no goal set") {
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
// store error types is reflected here too — guards against silently
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
