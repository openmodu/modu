package mailbox

import (
	"errors"
	"fmt"
	"log"
	"time"
)

// ── Adversarial validation ────────────────────────────────────────────────────

// validateTaskPrompt is the description injected into every auto-generated validate task.
const validateTaskPrompt = `[VALIDATE] Score the following task result on a scale of 0.0 to 1.0.

## Original Task
%s

## Submitted Result
%s%s
Pass threshold: %.2f — score ≥ threshold means the result is accepted.
Provide a numeric score and specific, actionable feedback.`

// PublishValidatedTask adds a task to the swarm queue that requires adversarial validation
// before it is considered done. A separate validator agent must score the result; if the
// score is below passThreshold the task is automatically re-queued up to maxRetries times.
//
// passThreshold defaults to 0.7 when ≤ 0. maxRetries must be ≥ 0.
func (h *Hub) PublishValidatedTask(creatorID, description string, maxRetries int, passThreshold float64, requiredCaps ...string) (string, error) {
	if passThreshold <= 0 {
		passThreshold = 0.7
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextTaskID()
	now := time.Now()
	task := &Task{
		ID:                  id,
		Description:         description,
		OriginalDescription: description,
		CreatedBy:           creatorID,
		Assignees:           []string{},
		AgentResults:        make(map[string]string),
		Status:              TaskStatusPending,
		SwarmOrigin:         true,
		RequiredCaps:        requiredCaps,
		ValidationRequired:  true,
		MaxRetries:          maxRetries,
		PassThreshold:       passThreshold,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	h.tasks[id] = task
	h.swarmQueue = append(h.swarmQueue, id)
	snapshot := *task
	h.publishLocked(Event{Type: EventTypeSwarmTaskPublished, TaskID: id, Data: snapshot})
	if err := h.store.SaveTask(snapshot); err != nil {
		log.Printf("[Hub] SaveTask %s (publish validated): %v", id, err)
	}
	return id, nil
}

// SubmitForValidation records the worker's result, transitions the task to "validating",
// and creates a new validate task in the swarm queue for a validator agent to review.
// Returns the validate task ID.
func (h *Hub) SubmitForValidation(taskID, agentID, result string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	task, ok := h.tasks[taskID]
	if !ok {
		return "", ErrTaskNotFound
	}
	if task.OwnerID != agentID {
		return "", fmt.Errorf("agent %s is not the owner of task %s", agentID, taskID)
	}
	if task.AgentResults == nil {
		task.AgentResults = make(map[string]string)
	}
	task.AgentResults[agentID] = result
	task.Result = result
	task.Status = TaskStatusValidating
	task.UpdatedAt = time.Now()

	// Release the worker — their part is done.
	if info, ok := h.agentInfos[agentID]; ok {
		info.Status = "idle"
		info.CurrentTask = ""
		h.publishLocked(Event{Type: EventTypeAgentUpdated, AgentID: agentID, Data: *info})
	}

	// Build the history section for the validate task description.
	historySection := ""
	if len(task.ValidationHistory) > 0 {
		historySection = "\n\n## Previous Attempts\n"
		for _, a := range task.ValidationHistory {
			historySection += fmt.Sprintf("- Attempt %d: score=%.2f, feedback=%q\n", a.AttemptNum, a.Score, a.Feedback)
		}
	}

	threshold := task.PassThreshold
	if threshold <= 0 {
		threshold = 0.7
	}
	baseDesc := task.OriginalDescription
	if baseDesc == "" {
		baseDesc = task.Description
	}
	validateDesc := fmt.Sprintf(validateTaskPrompt, baseDesc, result, historySection, threshold)

	// Create the validate task and push it to the swarm queue.
	validateID := h.nextTaskID()
	now := time.Now()
	validateTask := &Task{
		ID:           validateID,
		Description:  validateDesc,
		CreatedBy:    "system",
		Assignees:    []string{},
		AgentResults: make(map[string]string),
		Status:       TaskStatusPending,
		SwarmOrigin:  true,
		RequiredCaps: []string{"validate"},
		SourceTaskID: taskID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	h.tasks[validateID] = validateTask
	h.swarmQueue = append(h.swarmQueue, validateID)

	taskSnap := *task
	validateSnap := *validateTask
	h.publishLocked(Event{Type: EventTypeTaskUpdated, TaskID: taskID, Data: taskSnap})
	h.publishLocked(Event{Type: EventTypeSwarmTaskPublished, TaskID: validateID, Data: validateSnap})
	if err := h.store.SaveTask(taskSnap); err != nil {
		log.Printf("[Hub] SaveTask %s (submit for validation): %v", taskID, err)
	}
	if err := h.store.SaveTask(validateSnap); err != nil {
		log.Printf("[Hub] SaveTask %s (create validate task): %v", validateID, err)
	}
	return validateID, nil
}

// SubmitValidation processes a validator agent's judgment on a validate task.
//   - score ≥ passThreshold  → source task moves to "validated" (done)
//   - score < passThreshold and retries remain → source task re-queued with feedback context
//   - score < passThreshold and no retries left → source task marked "failed"
//
// A validator cannot review its own submitted work.
func (h *Hub) SubmitValidation(validateTaskID, validatorID string, score float64, feedback string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	validateTask, ok := h.tasks[validateTaskID]
	if !ok {
		return ErrTaskNotFound
	}
	if validateTask.SourceTaskID == "" {
		return fmt.Errorf("task %s is not a validation task", validateTaskID)
	}
	// Guard: only accept a judgment while the validate task is still running.
	if validateTask.Status != TaskStatusRunning {
		return fmt.Errorf("validate task %s is not running (status: %s)", validateTaskID, validateTask.Status)
	}
	// Guard: only the agent that claimed the task may submit the result.
	if validateTask.OwnerID != validatorID {
		return fmt.Errorf("agent %s did not claim validate task %s (owner: %s)", validatorID, validateTaskID, validateTask.OwnerID)
	}
	sourceTask, ok := h.tasks[validateTask.SourceTaskID]
	if !ok {
		return fmt.Errorf("source task %s not found", validateTask.SourceTaskID)
	}
	if sourceTask.OwnerID == validatorID {
		return errors.New("agent cannot validate its own work")
	}

	now := time.Now()

	// Record this attempt in the history.
	attempt := ValidationAttempt{
		AttemptNum:  sourceTask.RetryCount + 1,
		WorkerID:    sourceTask.OwnerID,
		Result:      sourceTask.Result,
		ValidatorID: validatorID,
		Score:       score,
		Feedback:    feedback,
		At:          now,
	}
	sourceTask.ValidationHistory = append(sourceTask.ValidationHistory, attempt)
	sourceTask.ValidationScore = score
	sourceTask.ValidationFeedback = feedback
	sourceTask.UpdatedAt = now

	// Mark the validate task as done and release the validator agent.
	validateTask.Status = TaskStatusCompleted
	validateTask.Result = fmt.Sprintf("score=%.2f feedback=%q", score, feedback)
	validateTask.UpdatedAt = now
	closedAt := now
	validateTask.DiscussionClosedAt = &closedAt
	h.releaseOwnerLocked(validateTask)

	threshold := sourceTask.PassThreshold
	if threshold <= 0 {
		threshold = 0.7
	}

	if score >= threshold {
		sourceTask.Status = TaskStatusValidated
		sourceTask.ValidationStatus = "passed"
		srcClosed := now
		sourceTask.DiscussionClosedAt = &srcClosed
		h.publishLocked(Event{Type: EventTypeTaskValidationPassed, TaskID: sourceTask.ID, AgentID: validatorID, Data: *sourceTask})
	} else {
		sourceTask.ValidationStatus = "failed"
		if sourceTask.RetryCount < sourceTask.MaxRetries {
			sourceTask.RetryCount++
			// Append feedback context to the description so the next worker sees it.
			baseDesc := sourceTask.OriginalDescription
			if baseDesc == "" {
				baseDesc = sourceTask.Description
			}
			sourceTask.Description = fmt.Sprintf(
				"%s\n\n[Retry %d/%d — Validator feedback: %s]",
				baseDesc, sourceTask.RetryCount, sourceTask.MaxRetries, feedback,
			)
			// Reset worker assignment so any agent can claim it again.
			sourceTask.Result = ""
			sourceTask.Status = TaskStatusPending
			sourceTask.OwnerID = ""
			sourceTask.AssignedTo = ""
			sourceTask.Assignees = []string{}
			h.swarmQueue = append(h.swarmQueue, sourceTask.ID)
			h.publishLocked(Event{Type: EventTypeTaskRetried, TaskID: sourceTask.ID, Data: *sourceTask})
		} else {
			srcClosed := now
			sourceTask.Status = TaskStatusFailed
			sourceTask.Error = fmt.Sprintf("validation failed after %d attempt(s), final score: %.2f", sourceTask.RetryCount+1, score)
			sourceTask.DiscussionClosedAt = &srcClosed
			h.publishLocked(Event{Type: EventTypeTaskValidationFailed, TaskID: sourceTask.ID, Data: *sourceTask})
		}
	}

	if err := h.store.SaveTask(*validateTask); err != nil {
		log.Printf("[Hub] SaveTask %s (validation result): %v", validateTaskID, err)
	}
	if err := h.store.SaveTask(*sourceTask); err != nil {
		log.Printf("[Hub] SaveTask %s (after validation): %v", sourceTask.ID, err)
	}
	return nil
}
