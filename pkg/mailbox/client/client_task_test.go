package client_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/crosszan/modu/pkg/mailbox"
	"github.com/crosszan/modu/pkg/mailbox/client"
	"github.com/crosszan/modu/pkg/mailbox/server"
)

func startTestMailbox(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	s := server.NewMailboxServer()
	go func() { _ = s.ListenAndServe(addr) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start on %s", addr)
	return ""
}

func TestClientSetRole(t *testing.T) {
	addr := startTestMailbox(t)
	ctx := context.Background()

	c := client.NewMailboxClient("orchestrator", addr)
	if err := c.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := c.SetRole(ctx, "orchestrator"); err != nil {
		t.Fatalf("SetRole: %v", err)
	}

	info, err := c.GetAgentInfo(ctx, "orchestrator")
	if err != nil {
		t.Fatalf("GetAgentInfo: %v", err)
	}
	if info.Role != "orchestrator" {
		t.Errorf("expected role=orchestrator, got %s", info.Role)
	}
}

func TestClientSetStatus(t *testing.T) {
	addr := startTestMailbox(t)
	ctx := context.Background()

	c := client.NewMailboxClient("worker-1", addr)
	_ = c.Register(ctx)

	if err := c.SetStatus(ctx, "busy", "task-42"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	info, _ := c.GetAgentInfo(ctx, "worker-1")
	if info.Status != "busy" || info.CurrentTask != "task-42" {
		t.Errorf("unexpected status: %+v", info)
	}

	if err := c.SetStatus(ctx, "idle", ""); err != nil {
		t.Fatalf("SetStatus idle: %v", err)
	}
	info, _ = c.GetAgentInfo(ctx, "worker-1")
	if info.Status != "idle" {
		t.Errorf("expected idle, got %s", info.Status)
	}
}

func TestClientTaskLifecycle(t *testing.T) {
	addr := startTestMailbox(t)
	ctx := context.Background()

	orch := client.NewMailboxClient("orch", addr)
	worker := client.NewMailboxClient("worker", addr)
	_ = orch.Register(ctx)
	_ = worker.Register(ctx)

	// CreateTask
	taskID, err := orch.CreateTask(ctx, "process data")
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if taskID == "" {
		t.Fatal("expected non-empty task ID")
	}

	// AssignTask
	if err := orch.AssignTask(ctx, taskID, "worker"); err != nil {
		t.Fatalf("AssignTask: %v", err)
	}

	// StartTask
	if err := worker.StartTask(ctx, taskID); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	// CompleteTask
	if err := worker.CompleteTask(ctx, taskID, "processed 100 records"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	// GetTask
	task, err := orch.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != mailbox.TaskStatusCompleted || task.Result != "processed 100 records" {
		t.Errorf("unexpected task: %+v", task)
	}
	if task.AssignedTo != "worker" {
		t.Errorf("expected assigned to worker, got %s", task.AssignedTo)
	}
}

func TestClientFailTask(t *testing.T) {
	addr := startTestMailbox(t)
	ctx := context.Background()

	c := client.NewMailboxClient("agent", addr)
	_ = c.Register(ctx)
	taskID, _ := c.CreateTask(ctx, "risky")
	_ = c.StartTask(ctx, taskID)

	if err := c.FailTask(ctx, taskID, "something exploded"); err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	task, _ := c.GetTask(ctx, taskID)
	if task.Status != mailbox.TaskStatusFailed || task.Error != "something exploded" {
		t.Errorf("unexpected failed task: %+v", task)
	}
}

func TestClientListTasks(t *testing.T) {
	addr := startTestMailbox(t)
	ctx := context.Background()

	c := client.NewMailboxClient("lister", addr)
	_ = c.Register(ctx)
	_, _ = c.CreateTask(ctx, "task A")
	_, _ = c.CreateTask(ctx, "task B")

	tasks, err := c.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}
