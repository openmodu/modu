package mailbox

import (
	"strings"
	"testing"
)

// helpers shared across adversarial tests

func setupAdversarialHub(t *testing.T) (*Hub, string, string, string) {
	t.Helper()
	h := NewHub()
	h.Register("worker-1")
	h.Register("validator-1")
	h.Register("publisher")
	_ = h.SetCapabilities("worker-1", []string{"text-processing"})
	_ = h.SetCapabilities("validator-1", []string{"validate"})
	taskID, err := h.PublishValidatedTask("publisher", "describe photosynthesis", 2, 0.7, "text-processing")
	if err != nil {
		t.Fatalf("PublishValidatedTask: %v", err)
	}
	return h, taskID, "worker-1", "validator-1"
}

// claimAndSubmit is a test shortcut: worker claims the task, submits result for validation.
// Returns the validate task ID.
func claimAndSubmit(t *testing.T, h *Hub, workerID, result string) (taskID, validateID string) {
	t.Helper()
	task, ok := h.ClaimTask(workerID)
	if !ok {
		t.Fatalf("ClaimTask: no task available for %s", workerID)
	}
	taskID = task.ID
	validateID, err := h.SubmitForValidation(taskID, workerID, result)
	if err != nil {
		t.Fatalf("SubmitForValidation: %v", err)
	}
	return taskID, validateID
}

// claimValidateTask claims the validate task as validatorID.
func claimValidateTask(t *testing.T, h *Hub, validatorID string) string {
	t.Helper()
	vt, ok := h.ClaimTask(validatorID)
	if !ok {
		t.Fatalf("ClaimTask (validator): no validate task available")
	}
	return vt.ID
}

// ── Bug 1: validate task must survive restart ─────────────────────────────────

func TestValidateTaskSwarmOrigin(t *testing.T) {
	h, _, workerID, _ := setupAdversarialHub(t)

	_, validateID := claimAndSubmit(t, h, workerID, "some result")

	vt, err := h.GetTask(validateID)
	if err != nil {
		t.Fatalf("GetTask(validateID): %v", err)
	}
	if !vt.SwarmOrigin {
		t.Error("validate task must have SwarmOrigin=true so it survives a restart")
	}
}

func TestLoadFromStoreRestoresValidateTask(t *testing.T) {
	h, _, workerID, _ := setupAdversarialHub(t)
	_, validateID := claimAndSubmit(t, h, workerID, "my result")

	// Simulate a restart before the validator claims the task:
	// grab the stored tasks and reload into a fresh hub.
	tasks := h.ListTasks()
	h2 := NewHub()
	for _, tc := range tasks {
		tc := tc
		h2.tasks[tc.ID] = &tc
	}
	// loadFromStore equivalent: rebuild swarm queue.
	for _, tc := range h2.tasks {
		if tc.SwarmOrigin && tc.Status == TaskStatusPending && len(tc.Assignees) == 0 {
			h2.swarmQueue = append(h2.swarmQueue, tc.ID)
		}
	}

	found := false
	for _, id := range h2.swarmQueue {
		if id == validateID {
			found = true
		}
	}
	if !found {
		t.Errorf("validate task %s was not restored to swarm queue after restart", validateID)
	}
}

// ── Bug 2a: only the owner may submit validation ──────────────────────────────

func TestSubmitValidationOwnershipCheck(t *testing.T) {
	h, _, workerID, validatorID := setupAdversarialHub(t)

	_, validateID := claimAndSubmit(t, h, workerID, "result A")
	_ = claimValidateTask(t, h, validatorID) // validator-1 claims it

	// A rogue agent that did NOT claim the task tries to submit.
	h.Register("rogue")
	err := h.SubmitValidation(validateID, "rogue", 0.9, "looks great")
	if err == nil {
		t.Error("expected error when non-owner tries to submit validation, got nil")
	}
}

// ── Bug 2b: cannot re-submit after the validate task is completed ─────────────

func TestSubmitValidationIdempotencyGuard(t *testing.T) {
	h, _, workerID, validatorID := setupAdversarialHub(t)

	_, validateID := claimAndSubmit(t, h, workerID, "result A")
	_ = claimValidateTask(t, h, validatorID)

	// First submission — should succeed (pass).
	if err := h.SubmitValidation(validateID, validatorID, 0.9, "good"); err != nil {
		t.Fatalf("first SubmitValidation: %v", err)
	}

	// Second submission on the already-completed validate task — must be rejected.
	err := h.SubmitValidation(validateID, validatorID, 0.1, "bad")
	if err == nil {
		t.Error("expected error on duplicate SubmitValidation, got nil")
	}
}

// ── Bug 3: validated status closes the discussion ────────────────────────────

func TestValidatedStatusClosesDiscussion(t *testing.T) {
	h, _, workerID, validatorID := setupAdversarialHub(t)

	taskID, validateID := claimAndSubmit(t, h, workerID, "result")
	_ = claimValidateTask(t, h, validatorID)
	if err := h.SubmitValidation(validateID, validatorID, 0.9, "perfect"); err != nil {
		t.Fatalf("SubmitValidation: %v", err)
	}

	task, _ := h.GetTask(taskID)
	if task.Status != TaskStatusValidated {
		t.Fatalf("expected status=validated, got %s", task.Status)
	}

	// EnsureTaskOpen should now return an error.
	if err := h.EnsureTaskOpen(taskID); err == nil {
		t.Error("EnsureTaskOpen should return error for a validated task")
	}

	// UpdateTaskSummary should also be blocked.
	if err := h.UpdateTaskSummary(taskID, "sneaky update"); err == nil {
		t.Error("UpdateTaskSummary should return error for a validated task")
	}
}

// ── Additional: happy path and retry loop ────────────────────────────────────

func TestAdversarialValidationHappyPath(t *testing.T) {
	h, _, workerID, validatorID := setupAdversarialHub(t)

	taskID, validateID := claimAndSubmit(t, h, workerID, "photosynthesis is cool")
	_ = claimValidateTask(t, h, validatorID)
	if err := h.SubmitValidation(validateID, validatorID, 0.85, "well done"); err != nil {
		t.Fatalf("SubmitValidation: %v", err)
	}

	task, _ := h.GetTask(taskID)
	if task.Status != TaskStatusValidated {
		t.Errorf("expected validated, got %s", task.Status)
	}
	if task.ValidationStatus != "passed" {
		t.Errorf("expected ValidationStatus=passed, got %s", task.ValidationStatus)
	}
}

func TestAdversarialValidationRetryOnFailure(t *testing.T) {
	h, _, workerID, validatorID := setupAdversarialHub(t)

	taskID, validateID := claimAndSubmit(t, h, workerID, "weak answer")
	_ = claimValidateTask(t, h, validatorID)
	if err := h.SubmitValidation(validateID, validatorID, 0.4, "needs more detail"); err != nil {
		t.Fatalf("SubmitValidation: %v", err)
	}

	task, _ := h.GetTask(taskID)
	if task.Status != TaskStatusPending {
		t.Fatalf("expected task re-queued as pending, got %s", task.Status)
	}
	if task.RetryCount != 1 {
		t.Errorf("expected RetryCount=1, got %d", task.RetryCount)
	}
	if !strings.Contains(task.Description, "[Retry 1/2") {
		t.Errorf("expected retry feedback in description, got: %s", task.Description)
	}

	queueLen := h.SwarmQueueLen()
	if queueLen == 0 {
		t.Error("expected source task to be back in swarm queue")
	}
}

func TestAdversarialValidationExhaustedRetries(t *testing.T) {
	h, _, workerID, validatorID := setupAdversarialHub(t)
	// MaxRetries=2 — fail all three attempts.
	for attempt := 0; attempt < 3; attempt++ {
		_, validateID := claimAndSubmit(t, h, workerID, "still weak")
		vtID := claimValidateTask(t, h, validatorID)
		if vtID != validateID {
			t.Fatalf("attempt %d: claimed wrong validate task", attempt)
		}
		if err := h.SubmitValidation(validateID, validatorID, 0.3, "still not good enough"); err != nil {
			t.Fatalf("attempt %d SubmitValidation: %v", attempt, err)
		}
	}

	// After 3 failures (0 + 2 retries) the source task must be failed.
	tasks := h.ListTasks()
	var sourceTasks []Task
	for _, tt := range tasks {
		if !tt.SwarmOrigin || tt.SourceTaskID != "" {
			continue
		}
		sourceTasks = append(sourceTasks, tt)
	}
	if len(sourceTasks) != 1 {
		t.Fatalf("expected 1 source task, got %d", len(sourceTasks))
	}
	if sourceTasks[0].Status != TaskStatusFailed {
		t.Errorf("expected failed after exhausted retries, got %s", sourceTasks[0].Status)
	}
}

func TestAgentReleasedAfterCompleteTask(t *testing.T) {
	h := NewHub()
	h.Register("w")
	_ = h.SetCapabilities("w", []string{"x"})
	_, _ = h.PublishTask("sys", "do something", "x")

	task, ok := h.ClaimTask("w")
	if !ok {
		t.Fatal("ClaimTask failed")
	}
	info, _ := h.GetAgentInfo("w")
	if info.Status != "busy" {
		t.Fatal("expected busy after claim")
	}
	if err := h.CompleteTask(task.ID, "w", "done"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	info, _ = h.GetAgentInfo("w")
	if info.Status != "idle" {
		t.Errorf("expected idle after CompleteTask, got %s", info.Status)
	}
	if info.CurrentTask != "" {
		t.Errorf("expected empty CurrentTask, got %s", info.CurrentTask)
	}
}

func TestBusyAgentCannotClaimSecondTask(t *testing.T) {
	h := NewHub()
	h.Register("w")
	_ = h.SetCapabilities("w", []string{"x"})
	_, _ = h.PublishTask("sys", "task 1", "x")
	_, _ = h.PublishTask("sys", "task 2", "x")

	_, ok := h.ClaimTask("w")
	if !ok {
		t.Fatal("first ClaimTask failed")
	}
	_, ok = h.ClaimTask("w")
	if ok {
		t.Error("busy agent should not be able to claim a second task")
	}
}

func TestSwarmQueueNotPollutedByCreateTask(t *testing.T) {
	h := NewHub()
	h.Register("creator")
	// Regular CreateTask — must NOT appear in swarm queue.
	_, err := h.CreateTask("creator", "internal task")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if h.SwarmQueueLen() != 0 {
		t.Errorf("expected empty swarm queue, got %d items", h.SwarmQueueLen())
	}
}
