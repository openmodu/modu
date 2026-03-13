package sqlitestore_test

import (
	"os"
	"testing"
	"time"

	"github.com/crosszan/modu/pkg/mailbox"
	"github.com/crosszan/modu/pkg/mailbox/sqlitestore"
)

func newTempStore(t *testing.T) *sqlitestore.SQLiteStore {
	t.Helper()
	f, err := os.CreateTemp("", "mailbox-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	s, err := sqlitestore.New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveAndLoadTask(t *testing.T) {
	s := newTempStore(t)

	now := time.Now().Truncate(time.Nanosecond)
	task := mailbox.Task{
		ID:          "task-1",
		Description: "do something",
		CreatedBy:   "orchestrator",
		AssignedTo:  "worker-1",
		Status:      mailbox.TaskStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	tasks, err := s.LoadTasks()
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.ID != "task-1" || got.Description != "do something" {
		t.Errorf("unexpected task: %+v", got)
	}
	if got.Status != mailbox.TaskStatusPending {
		t.Errorf("expected Pending, got %s", got.Status)
	}
}

func TestUpdateTask(t *testing.T) {
	s := newTempStore(t)

	now := time.Now()
	task := mailbox.Task{
		ID:        "task-2",
		CreatedBy: "orch",
		Status:    mailbox.TaskStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = s.SaveTask(task)

	// 更新为 completed
	task.Status = mailbox.TaskStatusCompleted
	task.Result = "done!"
	task.AssignedTo = "worker"
	task.UpdatedAt = now.Add(time.Second)
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask (update): %v", err)
	}

	tasks, _ := s.LoadTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != mailbox.TaskStatusCompleted {
		t.Errorf("expected Completed, got %s", tasks[0].Status)
	}
	if tasks[0].Result != "done!" {
		t.Errorf("expected result=done!, got %s", tasks[0].Result)
	}
	if tasks[0].AssignedTo != "worker" {
		t.Errorf("expected assigned_to=worker, got %s", tasks[0].AssignedTo)
	}
}

func TestLoadTasksOrder(t *testing.T) {
	s := newTempStore(t)

	base := time.Now()
	for i := 3; i >= 1; i-- {
		_ = s.SaveTask(mailbox.Task{
			ID:        "task-" + string(rune('0'+i)),
			CreatedBy: "a",
			Status:    mailbox.TaskStatusPending,
			CreatedAt: base.Add(time.Duration(i) * time.Second),
			UpdatedAt: base,
		})
	}

	tasks, _ := s.LoadTasks()
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	// Should be ordered by created_at ASC
	if tasks[0].CreatedAt.After(tasks[1].CreatedAt) || tasks[1].CreatedAt.After(tasks[2].CreatedAt) {
		t.Error("tasks should be ordered by created_at ASC")
	}
}

func TestSaveAndLoadAgentRole(t *testing.T) {
	s := newTempStore(t)

	if err := s.SaveAgentRole("agent-1", "orchestrator"); err != nil {
		t.Fatalf("SaveAgentRole: %v", err)
	}
	if err := s.SaveAgentRole("agent-2", "worker"); err != nil {
		t.Fatalf("SaveAgentRole: %v", err)
	}

	roles, err := s.LoadAgentRoles()
	if err != nil {
		t.Fatalf("LoadAgentRoles: %v", err)
	}
	if len(roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(roles))
	}
	if roles["agent-1"] != "orchestrator" {
		t.Errorf("expected orchestrator, got %s", roles["agent-1"])
	}
	if roles["agent-2"] != "worker" {
		t.Errorf("expected worker, got %s", roles["agent-2"])
	}
}

func TestUpdateAgentRole(t *testing.T) {
	s := newTempStore(t)

	_ = s.SaveAgentRole("agent-x", "worker")
	_ = s.SaveAgentRole("agent-x", "senior-worker") // update

	roles, _ := s.LoadAgentRoles()
	if roles["agent-x"] != "senior-worker" {
		t.Errorf("expected senior-worker after update, got %s", roles["agent-x"])
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	f, _ := os.CreateTemp("", "mailbox-reopen-*.db")
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	now := time.Now()

	// Write
	{
		s, err := sqlitestore.New(path)
		if err != nil {
			t.Fatalf("open 1: %v", err)
		}
		_ = s.SaveTask(mailbox.Task{
			ID: "persist-1", Description: "survive reopen",
			CreatedBy: "test", Status: mailbox.TaskStatusCompleted,
			Result: "ok", CreatedAt: now, UpdatedAt: now,
		})
		_ = s.SaveAgentRole("survivor", "tester")
		s.Close()
	}

	// Read after reopen
	{
		s, err := sqlitestore.New(path)
		if err != nil {
			t.Fatalf("open 2: %v", err)
		}
		defer s.Close()

		tasks, _ := s.LoadTasks()
		if len(tasks) != 1 || tasks[0].ID != "persist-1" || tasks[0].Result != "ok" {
			t.Errorf("unexpected tasks after reopen: %+v", tasks)
		}

		roles, _ := s.LoadAgentRoles()
		if roles["survivor"] != "tester" {
			t.Errorf("expected tester, got %s", roles["survivor"])
		}
	}
}

func TestHubWithSQLiteStore(t *testing.T) {
	f, _ := os.CreateTemp("", "hub-sqlite-*.db")
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	store, err := sqlitestore.New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 第一次运行：创建任务
	hub1 := mailbox.NewHub(mailbox.WithStore(store))
	hub1.Register("creator")
	hub1.Register("worker")
	_ = hub1.SetAgentRole("creator", "orchestrator")
	_ = hub1.SetAgentRole("worker", "coder")

	taskID, _ := hub1.CreateTask("creator", "build feature X")
	_ = hub1.AssignTask(taskID, "worker")
	_ = hub1.StartTask(taskID)
	_ = hub1.CompleteTask(taskID, "feature X shipped")
	store.Close()

	// 第二次运行：验证数据恢复
	store2, _ := sqlitestore.New(path)
	defer store2.Close()
	hub2 := mailbox.NewHub(mailbox.WithStore(store2))

	tasks := hub2.ListTasks()
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task after reload, got %d", len(tasks))
	}
	if tasks[0].Status != mailbox.TaskStatusCompleted || tasks[0].Result != "feature X shipped" {
		t.Errorf("unexpected task state: %+v", tasks[0])
	}

	// 角色在重新注册时恢复
	hub2.Register("creator")
	info, _ := hub2.GetAgentInfo("creator")
	if info.Role != "orchestrator" {
		t.Errorf("expected role=orchestrator after reload, got %s", info.Role)
	}
}
