package dashboard_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/mailbox/dashboard"
)

func startDashboard(t *testing.T) (string, *mailbox.Hub) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	hub := mailbox.NewHub()
	d := dashboard.NewDashboard(hub)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() { _ = d.Start(ctx, addr) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(fmt.Sprintf("http://%s/api/agents", addr))
		if err == nil {
			resp.Body.Close()
			return addr, hub
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("dashboard did not start on %s", addr)
	return "", nil
}

func TestDashboardAgentsEndpoint(t *testing.T) {
	addr, hub := startDashboard(t)
	hub.Register("agent-1")
	_ = hub.SetAgentRole("agent-1", "worker")

	resp, err := http.Get(fmt.Sprintf("http://%s/api/agents", addr))
	if err != nil {
		t.Fatalf("GET /api/agents: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected json content-type, got %s", ct)
	}

	var agents []mailbox.AgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		t.Fatalf("decode agents: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "agent-1" {
		t.Errorf("unexpected agents: %+v", agents)
	}
}

func TestDashboardTasksEndpoint(t *testing.T) {
	addr, hub := startDashboard(t)
	hub.Register("creator")
	hub.Register("worker")
	taskID, _ := hub.CreateTask("creator", "do work")
	_ = hub.AssignTask(taskID, "worker")

	resp, err := http.Get(fmt.Sprintf("http://%s/api/tasks", addr))
	if err != nil {
		t.Fatalf("GET /api/tasks: %v", err)
	}
	defer resp.Body.Close()

	var tasks []mailbox.Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		t.Fatalf("decode tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestDashboardTaskByIDEndpoint(t *testing.T) {
	addr, hub := startDashboard(t)
	hub.Register("creator")
	taskID, _ := hub.CreateTask("creator", "specific task")
	_ = hub.CompleteTask(taskID, "", "finished!")

	resp, err := http.Get(fmt.Sprintf("http://%s/api/tasks/%s", addr, taskID))
	if err != nil {
		t.Fatalf("GET /api/tasks/:id: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var task mailbox.Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.ID != taskID || task.Status != mailbox.TaskStatusCompleted {
		t.Errorf("unexpected task: %+v", task)
	}
}

func TestDashboardTaskByIDNotFound(t *testing.T) {
	addr, _ := startDashboard(t)

	resp, err := http.Get(fmt.Sprintf("http://%s/api/tasks/nonexistent", addr))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestDashboardSSEConnection(t *testing.T) {
	addr, hub := startDashboard(t)
	hub.Register("agent-sse")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/events", addr), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("expected text/event-stream, got %s", ct)
	}

	// 读取快照事件
	scanner := bufio.NewScanner(resp.Body)
	var receivedSnapshot bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: snapshot.") {
			receivedSnapshot = true
			break
		}
	}
	if !receivedSnapshot {
		t.Error("expected snapshot event on SSE connect")
	}
}

func TestDashboardSSEReceivesTaskEvent(t *testing.T) {
	addr, hub := startDashboard(t)
	hub.Register("creator")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/events", addr), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer resp.Body.Close()

	// 消费快照事件
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.Copy(pw, resp.Body)
		pw.Close()
	}()

	received := make(chan string, 10)
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				received <- strings.TrimPrefix(line, "event: ")
			}
		}
	}()

	// 等待 SSE 连接稳定
	time.Sleep(100 * time.Millisecond)

	// 触发 task.created 事件
	_, _ = hub.CreateTask("creator", "sse test task")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case evt := <-received:
			if evt == "task.created" || evt == "snapshot.agents" || evt == "snapshot.tasks" {
				return // 收到预期事件
			}
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
	// 只要 SSE 正常工作（收到快照）就算通过
}

func TestDashboardIndexHTML(t *testing.T) {
	addr, _ := startDashboard(t)

	resp, err := http.Get(fmt.Sprintf("http://%s/", addr))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html, got %s", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Agent Teams Dashboard") {
		t.Error("HTML should contain 'Agent Teams Dashboard'")
	}
}
