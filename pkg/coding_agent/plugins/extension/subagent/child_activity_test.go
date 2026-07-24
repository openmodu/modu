package subagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

func TestChildActivityRebuildFromTasks(t *testing.T) {
	dir := t.TempDir()
	sessionFile := filepath.Join(dir, "session.jsonl")
	// Two assistant turns (120 + 30 tokens) and one failed tool result.
	lines := `{"role":"user","content":[]}
{"role":"assistant","usage":{"input":100,"output":20}}
{"role":"tool","toolName":"bash","isError":true}
{"role":"assistant","usage":{"input":25,"output":5}}
`
	if err := os.WriteFile(sessionFile, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newChildActivityRegistry()
	// A task already tallied live must not be overwritten by the rebuild.
	r.handle(childEvent("live", string(types.EventTypeTurnEnd), "", false, 999))

	r.rebuildFromTasks([]extension.TaskSnapshot{
		{ID: "t1", Kind: "subagent", SessionFile: sessionFile},
		{ID: "skip-kind", Kind: "other", SessionFile: sessionFile},
		{ID: "live", Kind: "subagent", SessionFile: sessionFile},
	})

	got, ok := r.get("t1")
	if !ok {
		t.Fatal("expected rebuilt activity for t1")
	}
	if got.Turns != 2 {
		t.Errorf("Turns = %d, want 2", got.Turns)
	}
	if got.Tokens != 150 {
		t.Errorf("Tokens = %d, want 150", got.Tokens)
	}
	if got.FailedTools != 1 {
		t.Errorf("FailedTools = %d, want 1", got.FailedTools)
	}
	if _, ok := r.get("skip-kind"); ok {
		t.Error("non-subagent task should be skipped")
	}
	if live, _ := r.get("live"); live.Tokens != 999 {
		t.Errorf("live task overwritten by rebuild: Tokens = %d, want 999", live.Tokens)
	}
}

func childEvent(taskID, reason, tool string, isErr bool, tokens int) types.Event {
	return types.Event{
		Type:     "subagent_child_event",
		TaskID:   taskID,
		Reason:   reason,
		ToolName: tool,
		IsError:  isErr,
		Message:  &types.AssistantMessage{Usage: types.AgentUsage{Input: tokens}},
	}
}

func TestChildActivityRegistryTallies(t *testing.T) {
	r := newChildActivityRegistry()

	r.handle(childEvent("t1", string(types.EventTypeTurnEnd), "", false, 100))
	r.handle(childEvent("t1", string(types.EventTypeToolExecutionEnd), "bash", true, 0))
	r.handle(childEvent("t1", string(types.EventTypeToolExecutionEnd), "read", false, 0)) // success: not counted
	r.handle(childEvent("t1", string(types.EventTypeTurnEnd), "", false, 50))

	got, ok := r.get("t1")
	if !ok {
		t.Fatal("expected activity for t1")
	}
	if got.Turns != 2 {
		t.Errorf("turns = %d, want 2", got.Turns)
	}
	if got.FailedTools != 1 {
		t.Errorf("failedTools = %d, want 1", got.FailedTools)
	}
	if got.Tokens != 150 {
		t.Errorf("tokens = %d, want 150", got.Tokens)
	}
}

func TestChildActivityRegistryIsolatesTasksAndIgnoresUntagged(t *testing.T) {
	r := newChildActivityRegistry()
	r.handle(childEvent("a", string(types.EventTypeTurnEnd), "", false, 10))
	r.handle(childEvent("b", string(types.EventTypeTurnEnd), "", false, 20))
	r.handle(childEvent("", string(types.EventTypeTurnEnd), "", false, 999)) // no task id: dropped

	a, _ := r.get("a")
	b, _ := r.get("b")
	if a.Turns != 1 || a.Tokens != 10 {
		t.Errorf("task a wrong: %+v", a)
	}
	if b.Turns != 1 || b.Tokens != 20 {
		t.Errorf("task b wrong: %+v", b)
	}
	if _, ok := r.get(""); ok {
		t.Error("empty task id should not be tracked")
	}
}
