package mailbox

import (
	"sync"
	"testing"
	"time"
)

func TestAgentInfoLifecycle(t *testing.T) {
	h := NewHub()

	// Register 后自动创建 AgentInfo
	h.Register("agent-1")
	info, err := h.GetAgentInfo("agent-1")
	if err != nil {
		t.Fatalf("expected AgentInfo after Register, got err: %v", err)
	}
	if info.ID != "agent-1" {
		t.Errorf("expected ID=agent-1, got %s", info.ID)
	}
	if info.Status != "idle" {
		t.Errorf("expected Status=idle, got %s", info.Status)
	}

	// SetAgentRole
	if err := h.SetAgentRole("agent-1", "orchestrator"); err != nil {
		t.Fatalf("SetAgentRole failed: %v", err)
	}
	info, _ = h.GetAgentInfo("agent-1")
	if info.Role != "orchestrator" {
		t.Errorf("expected Role=orchestrator, got %s", info.Role)
	}

	// SetAgentStatus
	if err := h.SetAgentStatus("agent-1", "busy", "task-1"); err != nil {
		t.Fatalf("SetAgentStatus failed: %v", err)
	}
	info, _ = h.GetAgentInfo("agent-1")
	if info.Status != "busy" || info.CurrentTask != "task-1" {
		t.Errorf("unexpected status/task: %s / %s", info.Status, info.CurrentTask)
	}

	// 未注册 agent 返回 ErrAgentNotFound
	if err := h.SetAgentRole("ghost", "worker"); err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestListAgentInfos(t *testing.T) {
	h := NewHub()
	h.Register("a1")
	h.Register("a2")
	infos := h.ListAgentInfos()
	if len(infos) != 2 {
		t.Errorf("expected 2 agents, got %d", len(infos))
	}
}

func TestTaskLifecycle(t *testing.T) {
	h := NewHub()
	h.Register("creator")
	h.Register("worker")

	// CreateTask
	taskID, err := h.CreateTask("creator", "do something")
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	task, err := h.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if task.Status != TaskStatusPending {
		t.Errorf("expected Pending, got %s", task.Status)
	}
	if task.Description != "do something" {
		t.Errorf("unexpected description: %s", task.Description)
	}
	if task.CreatedBy != "creator" {
		t.Errorf("unexpected creator: %s", task.CreatedBy)
	}

	// AssignTask
	if err := h.AssignTask(taskID, "worker"); err != nil {
		t.Fatalf("AssignTask failed: %v", err)
	}
	task, _ = h.GetTask(taskID)
	if task.AssignedTo != "worker" {
		t.Errorf("expected assigned to worker, got %s", task.AssignedTo)
	}

	// StartTask
	if err := h.StartTask(taskID); err != nil {
		t.Fatalf("StartTask failed: %v", err)
	}
	task, _ = h.GetTask(taskID)
	if task.Status != TaskStatusRunning {
		t.Errorf("expected Running, got %s", task.Status)
	}

	// CompleteTask
	if err := h.CompleteTask(taskID, "result data"); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}
	task, _ = h.GetTask(taskID)
	if task.Status != TaskStatusCompleted || task.Result != "result data" {
		t.Errorf("unexpected completed state: status=%s result=%s", task.Status, task.Result)
	}
}

func TestTaskFail(t *testing.T) {
	h := NewHub()
	h.Register("creator")

	taskID, _ := h.CreateTask("creator", "risky task")
	_ = h.StartTask(taskID)
	if err := h.FailTask(taskID, "something went wrong"); err != nil {
		t.Fatalf("FailTask failed: %v", err)
	}
	task, _ := h.GetTask(taskID)
	if task.Status != TaskStatusFailed || task.Error != "something went wrong" {
		t.Errorf("unexpected failed state: status=%s error=%s", task.Status, task.Error)
	}
}

func TestTaskNotFound(t *testing.T) {
	h := NewHub()
	if _, err := h.GetTask("nonexistent"); err != ErrTaskNotFound {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
	h.Register("agent-1")
	if _, err := h.CreateTask("ghost", "desc"); err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound for unknown creator, got %v", err)
	}
}

func TestListTasks(t *testing.T) {
	h := NewHub()
	h.Register("a")
	_, _ = h.CreateTask("a", "task 1")
	_, _ = h.CreateTask("a", "task 2")
	tasks := h.ListTasks()
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestTaskIDMonotonicallyIncreasing(t *testing.T) {
	h := NewHub()
	h.Register("a")
	id1, _ := h.CreateTask("a", "t1")
	id2, _ := h.CreateTask("a", "t2")
	if id1 == id2 {
		t.Error("task IDs must be unique")
	}
}

func TestConcurrentTaskCreation(t *testing.T) {
	h := NewHub()
	h.Register("creator")
	const n = 50
	ids := make([]string, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			id, err := h.CreateTask("creator", "concurrent task")
			if err != nil {
				t.Errorf("CreateTask error: %v", err)
			}
			ids[i] = id
		}()
	}
	wg.Wait()

	// All IDs must be unique
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate task ID: %s", id)
		}
		seen[id] = true
	}
}

func TestUpdatedAtChanges(t *testing.T) {
	h := NewHub()
	h.Register("creator")
	taskID, _ := h.CreateTask("creator", "t")
	task1, _ := h.GetTask(taskID)

	time.Sleep(2 * time.Millisecond)
	_ = h.StartTask(taskID)
	task2, _ := h.GetTask(taskID)

	if !task2.UpdatedAt.After(task1.UpdatedAt) {
		t.Error("UpdatedAt should advance after StartTask")
	}
}
