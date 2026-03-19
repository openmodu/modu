// examples/agent_teams 演示 Agent Teams 协作模式：
// 1 个 orchestrator + 2 个 worker，通过 mailbox 完成任务委派与结果聚合。
// orchestrator 会定期向 worker 发 chat 消息询问进展，worker 也会主动回报状态。
//
// 运行方式：
//
//	go run ./examples/agent_teams
//
// Dashboard 在 http://localhost:8080 可查看实时状态及对话记录。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/mailbox/dashboard"
	"github.com/openmodu/modu/pkg/mailbox/server"
	"github.com/openmodu/modu/pkg/mailbox/sqlitestore"
)

const (
	mailboxAddr   = "localhost:6381"
	dashboardAddr = "0.0.0.0:8080"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. 启动 Mailbox Server（SQLite 持久化）
	store, err := sqlitestore.New("mailbox.db")
	if err != nil {
		log.Fatalf("open sqlite store: %v", err)
	}
	defer store.Close()

	s := server.NewMailboxServer(mailbox.WithStore(store))
	go func() {
		if err := s.ListenAndServe(mailboxAddr); err != nil {
			log.Printf("[server] error: %v", err)
		}
	}()

	// 等待 server 就绪
	time.Sleep(200 * time.Millisecond)

	// 2. 启动 Dashboard
	dash := dashboard.NewDashboard(s.Hub())
	go func() {
		if err := dash.Start(ctx, dashboardAddr); err != nil {
			log.Printf("[dashboard] error: %v", err)
		}
	}()
	fmt.Printf("Dashboard: http://%s\n\n", dashboardAddr)

	// 3. 启动 2 个 worker
	var wg sync.WaitGroup
	wg.Add(2)
	go runWorker(ctx, &wg, "worker-1", mailboxAddr)
	go runWorker(ctx, &wg, "worker-2", mailboxAddr)

	// 小等让 worker 注册完成
	time.Sleep(300 * time.Millisecond)

	// 4. 启动 orchestrator
	runOrchestrator(ctx, mailboxAddr)

	wg.Wait()
	fmt.Println("\nAll done!")
	fmt.Printf("Dashboard still running at http://%s — press Ctrl+C to exit\n", dashboardAddr)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	fmt.Println("\nShutting down...")
}

// runOrchestrator 创建任务、委派给 worker，并通过 chat 消息与 worker 交流
func runOrchestrator(ctx context.Context, addr string) {
	c := client.NewMailboxClient("orchestrator", addr)
	if err := c.Register(ctx); err != nil {
		log.Fatalf("[orchestrator] register failed: %v", err)
	}
	if err := c.SetRole(ctx, "orchestrator"); err != nil {
		log.Printf("[orchestrator] SetRole: %v", err)
	}
	fmt.Println("[orchestrator] registered")

	tasks := []struct {
		worker string
		desc   string
	}{
		{"worker-1", "Summarize the latest Go release notes"},
		{"worker-2", "List top 5 trending GitHub repos today"},
	}

	taskIDs := make([]string, len(tasks))

	// 创建并委派所有任务
	for i, t := range tasks {
		taskID, err := c.CreateTask(ctx, t.desc)
		if err != nil {
			log.Fatalf("[orchestrator] CreateTask: %v", err)
		}
		if err := c.AssignTask(ctx, taskID, t.worker); err != nil {
			log.Fatalf("[orchestrator] AssignTask: %v", err)
		}
		msgStr, err := mailbox.NewTaskAssignMessage("orchestrator", taskID, t.desc)
		if err != nil {
			log.Fatalf("[orchestrator] build message: %v", err)
		}
		if err := c.Send(ctx, t.worker, msgStr); err != nil {
			log.Fatalf("[orchestrator] send to %s: %v", t.worker, err)
		}
		taskIDs[i] = taskID
		fmt.Printf("[orchestrator] task %s → %s: %q\n", taskID, t.worker, t.desc)
	}
	_ = c.SetStatus(ctx, "busy", taskIDs[0])

	// 等待所有任务完成，期间定期发 chat 消息询问进展
	fmt.Println("[orchestrator] waiting for results...")
	results := make([]string, len(taskIDs))
	completed := make([]bool, len(taskIDs))
	deadline := time.Now().Add(30 * time.Second)
	checkTicker := time.NewTicker(2 * time.Second)
	defer checkTicker.Stop()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-checkTicker.C:
		}

		allDone := true
		for i, taskID := range taskIDs {
			if completed[i] {
				continue
			}
			task, err := c.GetTask(ctx, taskID)
			if err != nil {
				allDone = false
				continue
			}
			switch task.Status {
			case mailbox.TaskStatusCompleted:
				results[i] = task.Result
				completed[i] = true
				fmt.Printf("[orchestrator] task %s completed\n", taskID)
			case mailbox.TaskStatusFailed:
				results[i] = "ERROR: " + task.Error
				completed[i] = true
				fmt.Printf("[orchestrator] task %s failed: %s\n", taskID, task.Error)
			case mailbox.TaskStatusRunning:
				// 向 worker 发 chat 询问进展
				msg, _ := mailbox.NewChatMessage("orchestrator", taskID,
					fmt.Sprintf("任务进展怎么样了？(%s)", taskID))
				_ = c.Send(ctx, tasks[i].worker, msg)
				allDone = false
			default:
				allDone = false
			}
		}
		if allDone {
			break
		}

		// 处理 worker 发回来的 chat 回复
		for {
			raw, err := c.Recv(ctx)
			if err != nil || raw == "" {
				break
			}
			parsed, err := mailbox.ParseMessage(raw)
			if err != nil {
				continue
			}
			if parsed.Type == mailbox.MessageTypeChat {
				p, _ := mailbox.ParseChatPayload(parsed)
				fmt.Printf("[orchestrator] ← %s [%s]: %s\n", parsed.From, parsed.TaskID, p.Text)
			}
		}
	}

	_ = c.SetStatus(ctx, "idle", "")

	// 聚合输出
	fmt.Println("\n=== Orchestrator Results ===")
	for i, t := range tasks {
		fmt.Printf("[%s] %s\n  → %s\n", t.worker, t.desc, results[i])
	}
}

// runWorker 模拟分阶段执行任务，响应 orchestrator 的 chat 询问
func runWorker(ctx context.Context, wg *sync.WaitGroup, id, addr string) {
	defer wg.Done()

	c := client.NewMailboxClient(id, addr)
	if err := c.Register(ctx); err != nil {
		log.Printf("[%s] register failed: %v", id, err)
		return
	}
	if err := c.SetRole(ctx, "worker"); err != nil {
		log.Printf("[%s] SetRole: %v", id, err)
	}
	fmt.Printf("[%s] registered\n", id)

	deadline := time.Now().Add(30 * time.Second)
	var currentTaskID string
	progress := 0

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msg, err := c.Recv(ctx)
		if err != nil || msg == "" {
			// 模拟分阶段推进任务进度
			if currentTaskID != "" && progress < 100 {
				time.Sleep(800 * time.Millisecond)
				progress += 30
				if progress > 100 {
					progress = 100
				}
				if progress == 100 {
					result := fmt.Sprintf("[%s] task done (simulated result)", id)
					_ = c.CompleteTask(ctx, currentTaskID, result)
					_ = c.SetStatus(ctx, "idle", "")
					fmt.Printf("[%s] task %s done\n", id, currentTaskID)
					return
				}
			} else {
				time.Sleep(100 * time.Millisecond)
			}
			continue
		}

		parsed, err := mailbox.ParseMessage(msg)
		if err != nil {
			log.Printf("[%s] parse message error: %v", id, err)
			continue
		}

		switch parsed.Type {
		case mailbox.MessageTypeTaskAssign:
			payload, err := mailbox.ParseTaskAssignPayload(parsed)
			if err != nil {
				log.Printf("[%s] parse payload error: %v", id, err)
				continue
			}
			currentTaskID = parsed.TaskID
			progress = 0
			fmt.Printf("[%s] received task %s: %q\n", id, currentTaskID, payload.Description)
			_ = c.StartTask(ctx, currentTaskID)
			_ = c.SetStatus(ctx, "busy", currentTaskID)
			// 主动告知已开始
			reply, _ := mailbox.NewChatMessage(id, currentTaskID, "收到任务，开始处理...")
			_ = c.Send(ctx, parsed.From, reply)

		case mailbox.MessageTypeChat:
			// 响应 orchestrator 的询问
			if currentTaskID == "" {
				continue
			}
			p, _ := mailbox.ParseChatPayload(parsed)
			fmt.Printf("[%s] ← orchestrator: %s\n", id, p.Text)
			reply, _ := mailbox.NewChatMessage(id, currentTaskID,
				fmt.Sprintf("当前进度 %d%%，正在处理中...", progress))
			_ = c.Send(ctx, parsed.From, reply)
		}
	}
}
