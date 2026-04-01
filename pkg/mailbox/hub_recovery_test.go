package mailbox

import (
	"testing"
	"time"
)

// simulateEviction manually expires an agent's heartbeat and runs eviction.
func simulateEviction(h *Hub, agentID string) {
	h.mu.Lock()
	h.lastSeen[agentID] = time.Now().Add(-60 * time.Second)
	h.mu.Unlock()
	h.evictOfflineAgents()
}

// TestRecoverSwarmTask: swarm task is re-queued when its agent is evicted.
func TestRecoverSwarmTask(t *testing.T) {
	h := NewHub()
	h.Register("worker-1")
	_ = h.SetCapabilities("worker-1", []string{"compute"})

	taskID, err := h.PublishTask("external", "do some work", "compute")
	if err != nil {
		t.Fatalf("PublishTask: %v", err)
	}

	task, ok := h.ClaimTask("worker-1")
	if !ok {
		t.Fatalf("ClaimTask: expected task")
	}
	if task.Status != TaskStatusRunning {
		t.Fatalf("expected running, got %s", task.Status)
	}

	simulateEviction(h, "worker-1")

	// Task should be back in the queue as pending.
	h.mu.RLock()
	recovered := h.tasks[taskID]
	queueLen := len(h.swarmQueue)
	h.mu.RUnlock()

	if recovered.Status != TaskStatusPending {
		t.Errorf("expected pending after recovery, got %s", recovered.Status)
	}
	if recovered.RecoveryCount != 1 {
		t.Errorf("expected RecoveryCount=1, got %d", recovered.RecoveryCount)
	}
	if recovered.OwnerID != "" {
		t.Errorf("expected OwnerID cleared, got %q", recovered.OwnerID)
	}
	if queueLen != 1 {
		t.Errorf("expected swarmQueue length 1, got %d", queueLen)
	}
}

// TestRecoverSwarmTask_MaxRetries: once RecoveryCount reaches the limit the task is failed.
func TestRecoverSwarmTask_MaxRetries(t *testing.T) {
	h := NewHub(WithMaxTaskRecoveries(2))

	for i := 1; i <= 3; i++ {
		workerID := "worker-1"
		h.Register(workerID)
		_ = h.SetCapabilities(workerID, []string{"compute"})
	}

	taskID, err := h.PublishTask("external", "work", "compute")
	if err != nil {
		t.Fatalf("PublishTask: %v", err)
	}

	// Two successful recoveries.
	for round := 1; round <= 2; round++ {
		h.Register("worker-1")
		_ = h.SetCapabilities("worker-1", []string{"compute"})
		task, ok := h.ClaimTask("worker-1")
		if !ok {
			t.Fatalf("round %d: ClaimTask: no task", round)
		}
		if task.ID != taskID {
			t.Fatalf("round %d: unexpected task ID %s", round, task.ID)
		}
		simulateEviction(h, "worker-1")

		h.mu.RLock()
		status := h.tasks[taskID].Status
		count := h.tasks[taskID].RecoveryCount
		h.mu.RUnlock()

		if status != TaskStatusPending {
			t.Fatalf("round %d: expected pending, got %s", round, status)
		}
		if count != round {
			t.Fatalf("round %d: expected RecoveryCount=%d, got %d", round, round, count)
		}
	}

	// Third eviction: max exceeded → should fail.
	h.Register("worker-1")
	_ = h.SetCapabilities("worker-1", []string{"compute"})
	_, ok := h.ClaimTask("worker-1")
	if !ok {
		t.Fatal("third round: ClaimTask: no task")
	}
	simulateEviction(h, "worker-1")

	h.mu.RLock()
	final := h.tasks[taskID]
	h.mu.RUnlock()

	if final.Status != TaskStatusFailed {
		t.Errorf("expected failed after max recoveries, got %s", final.Status)
	}
	if final.Error == "" {
		t.Errorf("expected error message, got empty")
	}
}

// TestRecoverExplicitTask: explicitly-assigned (non-swarm) tasks are failed on eviction, not re-queued.
func TestRecoverExplicitTask(t *testing.T) {
	h := NewHub()
	h.Register("orchestrator")
	h.Register("worker-1")

	taskID, err := h.CreateTask("orchestrator", "explicit task")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := h.AssignTask(taskID, "worker-1"); err != nil {
		t.Fatalf("AssignTask: %v", err)
	}
	if err := h.StartTask(taskID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	simulateEviction(h, "worker-1")

	h.mu.RLock()
	task := h.tasks[taskID]
	queueLen := len(h.swarmQueue)
	h.mu.RUnlock()

	if task.Status != TaskStatusFailed {
		t.Errorf("expected failed, got %s", task.Status)
	}
	if queueLen != 0 {
		t.Errorf("expected empty swarm queue, got %d", queueLen)
	}
}

// TestRecoverTask_AlreadyCompleted: evicting an agent whose task is already completed is a no-op.
func TestRecoverTask_AlreadyCompleted(t *testing.T) {
	h := NewHub()
	h.Register("worker-1")
	_ = h.SetCapabilities("worker-1", []string{"compute"})

	taskID, _ := h.PublishTask("external", "work", "compute")
	_, _ = h.ClaimTask("worker-1")
	_ = h.CompleteTask(taskID, "worker-1", "done")

	// Now evict — the task is already completed; it must stay completed.
	simulateEviction(h, "worker-1")

	h.mu.RLock()
	task := h.tasks[taskID]
	h.mu.RUnlock()

	if task.Status != TaskStatusCompleted {
		t.Errorf("expected completed to remain unchanged, got %s", task.Status)
	}
	if task.RecoveryCount != 0 {
		t.Errorf("expected RecoveryCount=0, got %d", task.RecoveryCount)
	}
}

// TestEventTaskRecovered: eviction of a running swarm task emits task.recovered event.
func TestEventTaskRecovered(t *testing.T) {
	h := NewHub()
	events := h.Subscribe()
	defer h.Unsubscribe(events)

	h.Register("worker-1")
	_ = h.SetCapabilities("worker-1", []string{"compute"})
	taskID, _ := h.PublishTask("external", "work", "compute")
	_, _ = h.ClaimTask("worker-1")

	simulateEviction(h, "worker-1")

	// Drain events looking for task.recovered.
	deadline := time.After(200 * time.Millisecond)
	var found bool
	for !found {
		select {
		case ev := <-events:
			if ev.Type == EventTypeTaskRecovered && ev.TaskID == taskID {
				found = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for task.recovered event")
		}
	}
}
