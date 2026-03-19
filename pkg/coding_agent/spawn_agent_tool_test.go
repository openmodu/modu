package coding_agent_test

import (
	"context"
	"net"
	"testing"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/mailbox/server"
)

func startTestMailboxServer(t *testing.T) (string, *mailbox.Hub) {
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
			return addr, s.Hub()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not start on %s", addr)
	return "", nil
}

func noop(_ agent.AgentToolResult) {}

func TestSpawnAgentToolInterface(t *testing.T) {
	mc := client.NewMailboxClient("orchestrator", "127.0.0.1:9999")
	tool := coding_agent.NewSpawnAgentTool(mc)

	if tool.Name() != "spawn_agent" {
		t.Errorf("unexpected name: %s", tool.Name())
	}
	if tool.Label() == "" {
		t.Error("label should not be empty")
	}
	if tool.Description() == "" {
		t.Error("description should not be empty")
	}
	params, ok := tool.Parameters().(map[string]any)
	if !ok {
		t.Fatal("Parameters should return map[string]any")
	}
	props, _ := params["properties"].(map[string]any)
	if _, ok := props["target_agent_id"]; !ok {
		t.Error("parameters should have target_agent_id")
	}
	if _, ok := props["task_description"]; !ok {
		t.Error("parameters should have task_description")
	}
}

func TestSpawnAgentToolMissingArgs(t *testing.T) {
	mc := client.NewMailboxClient("orch", "127.0.0.1:9999")
	tool := coding_agent.NewSpawnAgentTool(mc)

	ctx := context.Background()
	result, err := tool.Execute(ctx, "id1", map[string]any{}, noop)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) == 0 {
		t.Error("expected error message in result content")
	}
}

func TestSpawnAgentToolSuccess(t *testing.T) {
	addr, _ := startTestMailboxServer(t)
	ctx := context.Background()

	orch := client.NewMailboxClient("orchestrator", addr)
	if err := orch.Register(ctx); err != nil {
		t.Fatalf("register orchestrator: %v", err)
	}

	worker := client.NewMailboxClient("worker-1", addr)
	if err := worker.Register(ctx); err != nil {
		t.Fatalf("register worker: %v", err)
	}

	// worker 模拟接收 task_assign 消息并完成任务
	go func() {
		for {
			msg, err := worker.Recv(ctx)
			if err != nil || msg == "" {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			parsed, err := mailbox.ParseMessage(msg)
			if err != nil || parsed.Type != mailbox.MessageTypeTaskAssign {
				continue
			}
			_ = worker.StartTask(ctx, parsed.TaskID)
			time.Sleep(50 * time.Millisecond)
			_ = worker.CompleteTask(ctx, parsed.TaskID, "worker result: done!")
			return
		}
	}()

	tool := coding_agent.NewSpawnAgentTool(orch,
		coding_agent.WithPollInterval(50*time.Millisecond),
		coding_agent.WithSpawnTimeout(5*time.Second),
	)

	result, err := tool.Execute(ctx, "tool-call-1", map[string]any{
		"target_agent_id":  "worker-1",
		"task_description": "process some data",
	}, noop)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected non-empty result content")
	}
}

func TestSpawnAgentToolTimeout(t *testing.T) {
	addr, _ := startTestMailboxServer(t)
	ctx := context.Background()

	orch := client.NewMailboxClient("orch-timeout", addr)
	_ = orch.Register(ctx)

	// 注册但从不处理任务的 worker
	w := client.NewMailboxClient("lazy-worker", addr)
	_ = w.Register(ctx)

	tool := coding_agent.NewSpawnAgentTool(orch,
		coding_agent.WithPollInterval(50*time.Millisecond),
		coding_agent.WithSpawnTimeout(300*time.Millisecond),
	)

	result, err := tool.Execute(ctx, "tid", map[string]any{
		"target_agent_id":  "lazy-worker",
		"task_description": "do something",
	}, noop)
	if err != nil {
		t.Fatalf("unexpected error (should return result with error message): %v", err)
	}
	if len(result.Content) == 0 {
		t.Error("expected result content describing timeout")
	}
}
