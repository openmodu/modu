package mailbox

import (
	"strings"
	"testing"
	"time"
)

// drainPipelineEvent drains events until one matches the given pipeline event type,
// or the deadline expires.
func drainPipelineEvent(sub <-chan Event, want EventType, timeout time.Duration) (Event, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case e := <-sub:
			if e.Type == want {
				return e, true
			}
		case <-deadline:
			return Event{}, false
		}
	}
}

// setupPipelineHub creates a hub with two workers: one with "research" cap and
// one with "write" cap.
func setupPipelineHub(t *testing.T) *Hub {
	t.Helper()
	h := NewHub()
	h.Register("researcher")
	h.Register("writer")
	_ = h.SetCapabilities("researcher", []string{"research"})
	_ = h.SetCapabilities("writer", []string{"write"})
	return h
}

// TestPipelineTwoSteps: 2-step pipeline advances automatically.
func TestPipelineTwoSteps(t *testing.T) {
	h := setupPipelineHub(t)

	steps := []PipelineStep{
		{DescriptionTemplate: "step 0 task", RequiredCaps: []string{"research"}},
		{DescriptionTemplate: "step 1 task based on: {{.PrevResult}}", RequiredCaps: []string{"write"}},
	}
	pipelineID, err := h.PublishPipeline("creator", steps)
	if err != nil {
		t.Fatalf("PublishPipeline: %v", err)
	}

	// Claim and complete step 0.
	task0, ok := h.ClaimTask("researcher")
	if !ok {
		t.Fatal("expected step 0 task")
	}
	if task0.PipelineStepIdx != 0 {
		t.Errorf("expected step 0, got %d", task0.PipelineStepIdx)
	}
	if err := h.CompleteTask(task0.ID, "researcher", "result-0"); err != nil {
		t.Fatalf("CompleteTask step 0: %v", err)
	}

	// Step 1 should now be in the swarm queue.
	task1, ok := h.ClaimTask("writer")
	if !ok {
		t.Fatal("expected step 1 task after step 0 completed")
	}
	if task1.PipelineStepIdx != 1 {
		t.Errorf("expected PipelineStepIdx=1, got %d", task1.PipelineStepIdx)
	}
	if !strings.Contains(task1.Description, "result-0") {
		t.Errorf("expected step 1 description to contain previous result, got %q", task1.Description)
	}

	// Complete step 1 → pipeline should be done.
	if err := h.CompleteTask(task1.ID, "writer", "result-1"); err != nil {
		t.Fatalf("CompleteTask step 1: %v", err)
	}

	p, ok := h.GetPipeline(pipelineID)
	if !ok {
		t.Fatal("GetPipeline: not found")
	}
	if p.Status != "completed" {
		t.Errorf("expected pipeline completed, got %s", p.Status)
	}
	if len(p.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(p.Results))
	}
}

// TestPipelineThreeSteps: result propagates through all three steps.
func TestPipelineThreeSteps(t *testing.T) {
	h := setupPipelineHub(t)

	steps := []PipelineStep{
		{DescriptionTemplate: "collect data", RequiredCaps: []string{"research"}},
		{DescriptionTemplate: "draft: {{.PrevResult}}", RequiredCaps: []string{"write"}},
		{DescriptionTemplate: "polish: {{.PrevResult}}", RequiredCaps: []string{"write"}},
	}
	pipelineID, err := h.PublishPipeline("creator", steps)
	if err != nil {
		t.Fatalf("PublishPipeline: %v", err)
	}

	results := []string{"data-collected", "draft-written", "polished-text"}
	workers := []string{"researcher", "writer", "writer"}

	for i, worker := range workers {
		h.Register(worker) // re-register to reset busy state if needed
		task, ok := h.ClaimTask(worker)
		if !ok {
			t.Fatalf("step %d: ClaimTask failed for %s", i, worker)
		}
		if task.PipelineStepIdx != i {
			t.Errorf("step %d: expected PipelineStepIdx=%d, got %d", i, i, task.PipelineStepIdx)
		}
		if i > 0 && !strings.Contains(task.Description, results[i-1]) {
			t.Errorf("step %d: expected description to contain %q, got %q",
				i, results[i-1], task.Description)
		}
		if err := h.CompleteTask(task.ID, worker, results[i]); err != nil {
			t.Fatalf("step %d: CompleteTask: %v", i, err)
		}
	}

	p, _ := h.GetPipeline(pipelineID)
	if p.Status != "completed" {
		t.Errorf("expected completed, got %s", p.Status)
	}
	for i, r := range p.Results {
		if r != results[i] {
			t.Errorf("result[%d]: expected %q, got %q", i, results[i], r)
		}
	}
}

// TestPipelinePrevResultInjection: {{.PrevResult}} is substituted correctly.
func TestPipelinePrevResultInjection(t *testing.T) {
	h := NewHub()
	h.Register("a")
	h.Register("b")
	_ = h.SetCapabilities("a", []string{"cap"})
	_ = h.SetCapabilities("b", []string{"cap"})

	steps := []PipelineStep{
		{DescriptionTemplate: "step0", RequiredCaps: []string{"cap"}},
		{DescriptionTemplate: "use this: {{.PrevResult}} done", RequiredCaps: []string{"cap"}},
	}
	_, err := h.PublishPipeline("x", steps)
	if err != nil {
		t.Fatal(err)
	}

	task0, _ := h.ClaimTask("a")
	_ = h.CompleteTask(task0.ID, "a", "MY_OUTPUT")

	task1, ok := h.ClaimTask("b")
	if !ok {
		t.Fatal("expected step 1 task")
	}
	if task1.Description != "use this: MY_OUTPUT done" {
		t.Errorf("unexpected description: %q", task1.Description)
	}
}

// TestPipelineLastStep: PipelineCompleted event is emitted; Results has all entries.
func TestPipelineLastStep(t *testing.T) {
	h := NewHub()
	events := h.Subscribe()
	defer h.Unsubscribe(events)

	h.Register("w")
	_ = h.SetCapabilities("w", []string{"cap"})

	steps := []PipelineStep{
		{DescriptionTemplate: "s0", RequiredCaps: []string{"cap"}},
		{DescriptionTemplate: "s1 {{.PrevResult}}", RequiredCaps: []string{"cap"}},
	}
	pipelineID, _ := h.PublishPipeline("x", steps)

	task0, _ := h.ClaimTask("w")
	_ = h.CompleteTask(task0.ID, "w", "r0")

	h.Register("w") // reset busy
	task1, _ := h.ClaimTask("w")
	_ = h.CompleteTask(task1.ID, "w", "r1")

	e, found := drainPipelineEvent(events, EventTypePipelineCompleted, 500*time.Millisecond)
	if !found {
		t.Fatal("expected pipeline.completed event")
	}
	if e.PipelineID != pipelineID {
		t.Errorf("wrong pipeline ID in event: %s", e.PipelineID)
	}

	p, _ := h.GetPipeline(pipelineID)
	if p.Status != "completed" {
		t.Errorf("expected completed, got %s", p.Status)
	}
	if len(p.Results) != 2 || p.Results[0] != "r0" || p.Results[1] != "r1" {
		t.Errorf("unexpected Results: %v", p.Results)
	}
}

// TestPublishPipelineValidation: fewer than 2 steps should return an error.
func TestPublishPipelineValidation(t *testing.T) {
	h := NewHub()
	_, err := h.PublishPipeline("x", []PipelineStep{
		{DescriptionTemplate: "only one step"},
	})
	if err == nil {
		t.Error("expected error for single-step pipeline")
	}
	_, err = h.PublishPipeline("x", nil)
	if err == nil {
		t.Error("expected error for nil steps")
	}
}

// TestGetPipeline: pipeline is findable and CurrentStep advances as tasks complete.
func TestGetPipeline(t *testing.T) {
	h := NewHub()
	h.Register("w1")
	h.Register("w2")
	_ = h.SetCapabilities("w1", []string{"cap"})
	_ = h.SetCapabilities("w2", []string{"cap"})

	steps := []PipelineStep{
		{DescriptionTemplate: "s0", RequiredCaps: []string{"cap"}},
		{DescriptionTemplate: "s1 {{.PrevResult}}", RequiredCaps: []string{"cap"}},
		{DescriptionTemplate: "s2 {{.PrevResult}}", RequiredCaps: []string{"cap"}},
	}
	pipelineID, _ := h.PublishPipeline("creator", steps)

	p, ok := h.GetPipeline(pipelineID)
	if !ok {
		t.Fatal("GetPipeline: not found")
	}
	if p.CurrentStep != 0 {
		t.Errorf("expected CurrentStep=0, got %d", p.CurrentStep)
	}

	task0, _ := h.ClaimTask("w1")
	_ = h.CompleteTask(task0.ID, "w1", "r0")

	p, _ = h.GetPipeline(pipelineID)
	if p.CurrentStep != 1 {
		t.Errorf("expected CurrentStep=1, got %d", p.CurrentStep)
	}

	h.Register("w1")
	task1, _ := h.ClaimTask("w1")
	_ = h.CompleteTask(task1.ID, "w1", "r1")

	p, _ = h.GetPipeline(pipelineID)
	if p.CurrentStep != 2 {
		t.Errorf("expected CurrentStep=2, got %d", p.CurrentStep)
	}

	h.Register("w2")
	task2, _ := h.ClaimTask("w2")
	_ = h.CompleteTask(task2.ID, "w2", "r2")

	p, _ = h.GetPipeline(pipelineID)
	if p.Status != "completed" {
		t.Errorf("expected completed, got %s", p.Status)
	}
	if len(p.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(p.Results))
	}
}
