package workflow

import (
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestSnapshotTrackerIgnoresPanickingUpdateCallback(t *testing.T) {
	tracker := newSnapshotTracker(func(types.ToolResult) {
		panic("update sink failed")
	})
	tracker.setMeta(metaInfo{Name: "panic_safe", Description: "panic-safe updates"})
	id := tracker.startAgent("agent", "phase", "prompt")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("snapshot update callback panic should be contained, got %v", r)
		}
	}()

	tracker.finishAgent(id, statusDone, "ok", "")
	snapshot := tracker.complete("done")
	if snapshot.DoneCount != 1 || snapshot.AgentCount != 1 {
		t.Fatalf("unexpected snapshot counts after contained panic: %#v", snapshot)
	}
}
