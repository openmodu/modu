package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/crosszan/modu/pkg/mailbox"
	"github.com/crosszan/modu/pkg/mailbox/server"
	"github.com/redis/go-redis/v9"
)

// startTestServer 在随机端口启动一个测试用的 MailboxServer，返回地址
func startTestServer(t *testing.T) string {
	t.Helper()
	// 找一个空闲端口
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	s := server.NewMailboxServer()
	go func() {
		_ = s.ListenAndServe(addr)
	}()
	// 等待服务启动
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return addr
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start in time on %s", addr)
	return ""
}

func newTestClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: addr})
}

func TestServerPing(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()

	ctx := context.Background()
	res, err := rdb.Do(ctx, "PING").Result()
	if err != nil || res != "PONG" {
		t.Fatalf("expected PONG, got %v %v", res, err)
	}
}

func TestServerAgentRegAndList(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, err := rdb.Do(ctx, "AGENT.REG", "agent-a").Result()
	if err != nil {
		t.Fatalf("AGENT.REG: %v", err)
	}

	res, err := rdb.Do(ctx, "AGENT.LIST").StringSlice()
	if err != nil {
		t.Fatalf("AGENT.LIST: %v", err)
	}
	if len(res) != 1 || res[0] != "agent-a" {
		t.Errorf("unexpected agents: %v", res)
	}
}

func TestServerAgentSetRole(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, _ = rdb.Do(ctx, "AGENT.REG", "orch").Result()

	res, err := rdb.Do(ctx, "AGENT.SETROLE", "orch", "orchestrator").Result()
	if err != nil || res != "OK" {
		t.Fatalf("AGENT.SETROLE: %v %v", res, err)
	}
}

func TestServerAgentSetStatus(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, _ = rdb.Do(ctx, "AGENT.REG", "worker").Result()

	// with task ID
	res, err := rdb.Do(ctx, "AGENT.SETSTATUS", "worker", "busy", "task-1").Result()
	if err != nil || res != "OK" {
		t.Fatalf("AGENT.SETSTATUS with task: %v %v", res, err)
	}

	// without task ID
	res, err = rdb.Do(ctx, "AGENT.SETSTATUS", "worker", "idle").Result()
	if err != nil || res != "OK" {
		t.Fatalf("AGENT.SETSTATUS without task: %v %v", res, err)
	}
}

func TestServerAgentInfo(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, _ = rdb.Do(ctx, "AGENT.REG", "spy").Result()
	_, _ = rdb.Do(ctx, "AGENT.SETROLE", "spy", "researcher").Result()

	raw, err := rdb.Do(ctx, "AGENT.INFO", "spy").Result()
	if err != nil {
		t.Fatalf("AGENT.INFO: %v", err)
	}
	var info mailbox.AgentInfo
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &info); err != nil {
		t.Fatalf("unmarshal AgentInfo: %v", err)
	}
	if info.ID != "spy" || info.Role != "researcher" {
		t.Errorf("unexpected info: %+v", info)
	}
}

func TestServerTaskCreate(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, _ = rdb.Do(ctx, "AGENT.REG", "boss").Result()

	taskID, err := rdb.Do(ctx, "TASK.CREATE", "boss", "do something").Result()
	if err != nil {
		t.Fatalf("TASK.CREATE: %v", err)
	}
	if taskID == "" {
		t.Error("expected non-empty task ID")
	}
}

func TestServerTaskLifecycle(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, _ = rdb.Do(ctx, "AGENT.REG", "creator").Result()
	_, _ = rdb.Do(ctx, "AGENT.REG", "exec").Result()

	taskID, _ := rdb.Do(ctx, "TASK.CREATE", "creator", "build something").Result()
	tid := fmt.Sprintf("%s", taskID)

	// TASK.ASSIGN
	res, err := rdb.Do(ctx, "TASK.ASSIGN", tid, "exec").Result()
	if err != nil || res != "OK" {
		t.Fatalf("TASK.ASSIGN: %v %v", res, err)
	}

	// TASK.START
	res, err = rdb.Do(ctx, "TASK.START", tid).Result()
	if err != nil || res != "OK" {
		t.Fatalf("TASK.START: %v %v", res, err)
	}

	// TASK.DONE
	res, err = rdb.Do(ctx, "TASK.DONE", tid, "result data").Result()
	if err != nil || res != "OK" {
		t.Fatalf("TASK.DONE: %v %v", res, err)
	}

	// TASK.GET
	raw, err := rdb.Do(ctx, "TASK.GET", tid).Result()
	if err != nil {
		t.Fatalf("TASK.GET: %v", err)
	}
	var task mailbox.Task
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &task); err != nil {
		t.Fatalf("unmarshal Task: %v", err)
	}
	if task.Status != mailbox.TaskStatusCompleted || task.Result != "result data" {
		t.Errorf("unexpected task state: %+v", task)
	}
}

func TestServerTaskFail(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, _ = rdb.Do(ctx, "AGENT.REG", "risky").Result()
	taskID, _ := rdb.Do(ctx, "TASK.CREATE", "risky", "dangerous op").Result()
	tid := fmt.Sprintf("%s", taskID)

	_, _ = rdb.Do(ctx, "TASK.START", tid).Result()
	res, err := rdb.Do(ctx, "TASK.FAIL", tid, "boom").Result()
	if err != nil || res != "OK" {
		t.Fatalf("TASK.FAIL: %v %v", res, err)
	}

	raw, _ := rdb.Do(ctx, "TASK.GET", tid).Result()
	var task mailbox.Task
	_ = json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &task)
	if task.Status != mailbox.TaskStatusFailed || task.Error != "boom" {
		t.Errorf("unexpected failed state: %+v", task)
	}
}

func TestServerTaskList(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, _ = rdb.Do(ctx, "AGENT.REG", "lister").Result()
	_, _ = rdb.Do(ctx, "TASK.CREATE", "lister", "t1").Result()
	_, _ = rdb.Do(ctx, "TASK.CREATE", "lister", "t2").Result()

	raw, err := rdb.Do(ctx, "TASK.LIST").Result()
	if err != nil {
		t.Fatalf("TASK.LIST: %v", err)
	}
	var tasks []mailbox.Task
	if err := json.Unmarshal([]byte(fmt.Sprintf("%s", raw)), &tasks); err != nil {
		t.Fatalf("unmarshal tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestServerUnknownCommand(t *testing.T) {
	addr := startTestServer(t)
	rdb := newTestClient(addr)
	defer rdb.Close()
	ctx := context.Background()

	_, err := rdb.Do(ctx, "UNKNOWN.CMD").Result()
	if err == nil {
		t.Error("expected error for unknown command")
	}
}
