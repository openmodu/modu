package subagent

import (
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

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
