package main

import (
	"encoding/json"
	"testing"
)

func TestDecodeModuTUIWorkflowSnapshotNormalizesRuntimeState(t *testing.T) {
	snapshot, ok := decodeModuTUIWorkflowSnapshot(map[string]any{
		"workflow": map[string]any{
			"runningCount":   json.Number("1"),
			"stoppedCount":   "2",
			"completedCount": float64(3),
			"failedCount":    4,
			"indicator":      "  running research  ",
			"runs": []any{
				map[string]any{
					"id":           "run-1",
					"name":         "research",
					"status":       "running",
					"agentCount":   float64(2),
					"doneCount":    1,
					"currentPhase": "review",
					"phases": []any{
						map[string]any{
							"title":        "review",
							"agentCount":   2,
							"runningCount": 1,
						},
					},
				},
			},
		},
	})
	if !ok {
		t.Fatal("workflow snapshot was not decoded")
	}
	if snapshot.RunningCount != 1 || snapshot.StoppedCount != 2 || snapshot.CompletedCount != 3 || snapshot.FailedCount != 4 {
		t.Fatalf("counts = %#v", snapshot)
	}
	if snapshot.Indicator != "running research" {
		t.Fatalf("indicator = %q", snapshot.Indicator)
	}
	if len(snapshot.Runs) != 1 {
		t.Fatalf("runs = %#v", snapshot.Runs)
	}
	run := snapshot.Runs[0]
	if run.ID != "run-1" || run.AgentCount != 2 || run.DoneCount != 1 || len(run.Phases) != 1 {
		t.Fatalf("run = %#v", run)
	}
	if phase := run.Phases[0]; phase.Title != "review" || phase.RunningCount != 1 {
		t.Fatalf("phase = %#v", phase)
	}
}

func TestDecodeModuTUIWorkflowSnapshotRejectsMissingState(t *testing.T) {
	for _, states := range []map[string]any{
		nil,
		{},
		{"workflow": "invalid"},
	} {
		if snapshot, ok := decodeModuTUIWorkflowSnapshot(states); ok {
			t.Fatalf("unexpected snapshot: %#v", snapshot)
		}
	}
}
