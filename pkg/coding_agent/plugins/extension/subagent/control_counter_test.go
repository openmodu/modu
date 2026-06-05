package subagent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

func failedToolEvent(taskID string) types.Event {
	return types.Event{
		Type:    "subagent_child_event",
		TaskID:  taskID,
		Reason:  string(types.EventTypeToolExecutionEnd),
		IsError: true,
	}
}

func turnEvent(taskID string, tokens int) types.Event {
	return types.Event{
		Type:    "subagent_child_event",
		TaskID:  taskID,
		Reason:  string(types.EventTypeTurnEnd),
		Message: &types.AssistantMessage{Usage: types.AgentUsage{Input: tokens}},
	}
}

func TestControlCounterFailedToolThresholdFiresOnce(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{"x": frontmatterBody("x", "x")})
	ext.controlCounters.register("batch-1", "parallel", &controlOptions{enabled: true, failedToolAttemptsBeforeAttention: 2})

	ext.onChildEvent(failedToolEvent("batch-1")) // 1 — below threshold
	if got := api.noticesSnapshot(); len(got) != 0 {
		t.Fatalf("no notice expected at 1 failed tool, got %#v", got)
	}
	ext.onChildEvent(failedToolEvent("batch-1")) // 2 — fires
	snap := api.noticesSnapshot()
	if len(snap) != 1 || !strings.Contains(snap[0], "failed tool attempts (≥ 2)") {
		t.Fatalf("expected one needs-attention notice, got %#v", snap)
	}
	ext.onChildEvent(failedToolEvent("batch-1")) // 3 — latched, no new notice
	if got := api.noticesSnapshot(); len(got) != 1 {
		t.Fatalf("threshold should latch and fire once, got %#v", got)
	}
}

func TestControlCounterTurnsAndTokens(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{"x": frontmatterBody("x", "x")})
	ext.controlCounters.register("b", "chain", &controlOptions{
		enabled:                 true,
		activeNoticeAfterTurns:  2,
		activeNoticeAfterTokens: 150,
	})

	ext.onChildEvent(turnEvent("b", 100)) // turns=1 tokens=100
	if got := api.noticesSnapshot(); len(got) != 0 {
		t.Fatalf("no notice yet, got %#v", got)
	}
	ext.onChildEvent(turnEvent("b", 100)) // turns=2 (fires turns) tokens=200 (fires tokens)
	snap := api.noticesSnapshot()
	if len(snap) != 2 {
		t.Fatalf("expected turns + tokens notices, got %#v", snap)
	}
}

func TestControlCounterRespectsNotifyOn(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{"x": frontmatterBody("x", "x")})
	// Only allow needs_attention; the turns threshold (active_long_running)
	// must stay silent.
	ext.controlCounters.register("b", "parallel", &controlOptions{
		enabled:                           true,
		activeNoticeAfterTurns:            1,
		failedToolAttemptsBeforeAttention: 1,
		notifyOn:                          []string{"needs_attention"},
	})

	ext.onChildEvent(turnEvent("b", 0))       // would trip active_long_running — filtered out
	ext.onChildEvent(failedToolEvent("b"))    // trips needs_attention — allowed
	snap := api.noticesSnapshot()
	if len(snap) != 1 || !strings.Contains(snap[0], "failed tool attempts") {
		t.Fatalf("expected only the needs-attention notice, got %#v", snap)
	}
}

func TestControlCounterUnregisterStopsNotices(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{"x": frontmatterBody("x", "x")})
	ext.controlCounters.register("b", "parallel", &controlOptions{enabled: true, failedToolAttemptsBeforeAttention: 1})
	ext.controlCounters.unregister("b")
	ext.onChildEvent(failedToolEvent("b"))
	if got := api.noticesSnapshot(); len(got) != 0 {
		t.Fatalf("unregistered task should not notify, got %#v", got)
	}
}

// TestBatchAsyncTagsChildrenWithBatchID locks the id mapping: a batch async
// dispatch must tag every child's ForkOptions.BubbleTaskID with the batch id,
// so the host bubbles all children's events under that id and the batch's
// control counters aggregate across them.
func TestBatchAsyncTagsChildrenWithBatchID(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{"x": frontmatterBody("x", "x")})
	tool := toolOf(t, api)

	var mu sync.Mutex
	var bubbleIDs []string
	done := make(chan struct{}, 2)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		bubbleIDs = append(bubbleIDs, opts.BubbleTaskID)
		mu.Unlock()
		done <- struct{}{}
		return "ok", nil
	}

	res, err := tool.Execute(context.Background(), "batch", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "x", "task": "a"},
			map[string]any{"agent": "x", "task": "b"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("batch dispatch failed: err=%v res=%+v", err, res)
	}
	batchID := parseBatchTaskID(textOf(res))
	if batchID == "" {
		t.Fatalf("could not parse batch task id from reply: %q", textOf(res))
	}

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for batch children to fork")
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bubbleIDs) != 2 {
		t.Fatalf("expected 2 child forks, got %d", len(bubbleIDs))
	}
	for _, got := range bubbleIDs {
		if got != batchID {
			t.Fatalf("child BubbleTaskID = %q, want batch id %q", got, batchID)
		}
	}
}

func parseBatchTaskID(reply string) string {
	const marker = "task_id="
	i := strings.Index(reply, marker)
	if i < 0 {
		return ""
	}
	rest := reply[i+len(marker):]
	if j := strings.IndexAny(rest, " \t\n."); j >= 0 {
		return rest[:j]
	}
	return rest
}
