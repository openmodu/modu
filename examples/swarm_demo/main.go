// swarm_demo 展示 Agent Swarm 的核心特性：
//
//   - 去中心化：无固定 Orchestrator，外部 publisher 直接向队列发布任务
//   - 竞争认领：多个 agent 轮询并原子地抢占任务（先到先得）
//   - 能力匹配：任务声明所需能力，只有具备该能力的 agent 才能认领
//   - 自动伸缩：Swarm 管理器监控队列深度，按需 spawn / despawn agent
//   - Dead Agent Recovery：agent 心跳超时被驱逐后，其 running 任务自动重新入队
//   - Task Pipeline：多步顺序工作流，每步结果自动注入下一步描述
//
// 运行方式：
//
//	go run ./examples/swarm_demo/
//
// Dashboard: http://localhost:8083
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/mailbox/dashboard"
	"github.com/openmodu/modu/pkg/mailbox/server"
	"github.com/openmodu/modu/pkg/swarm"
)

const mailboxAddr = "127.0.0.1:16381"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. 启动 mailbox server（Redis 兼容协议）
	srv := server.NewMailboxServer()
	go func() {
		if err := srv.ListenAndServe(mailboxAddr); err != nil {
			log.Printf("[main] mailbox server: %v", err)
		}
	}()
	time.Sleep(100 * time.Millisecond) // 等待 server 就绪

	hub := srv.Hub()

	// 2. 启动 Dashboard
	dash := dashboard.NewDashboard(hub)
	go func() {
		if err := dash.Start(ctx, ":8083"); err != nil && ctx.Err() == nil {
			log.Printf("[main] dashboard: %v", err)
		}
	}()

	// 3. 创建 Swarm：最少 2 个 agent，最多 5 个，具备 text-processing 能力
	//    AgentFactory 使用 inProcessFactory，在当前进程内以 goroutine 形式 spawn agent
	factory := &inProcessFactory{addr: mailboxAddr}
	sw := swarm.New(hub, factory, swarm.SpawnPolicy{
		MinAgents:     2,
		MaxAgents:     5,
		Capabilities:  []string{"text-processing"},
		ScaleUpRatio:  1.5, // 队列积压超过空闲 agent 数的 1.5 倍时扩容
		CheckInterval: 2 * time.Second,
	})
	sw.Start()
	defer sw.Stop()

	// 等待初始 agent 注册完成
	time.Sleep(600 * time.Millisecond)

	// 4. 常规 Swarm 任务：publisher 向队列批量投递独立任务
	go publishTasks(ctx)

	// 5. Dead Agent Recovery 演示：8s 后启动一个额外 agent，给它发布任务，
	//    然后停止它的心跳使其被驱逐，观察任务自动重新入队
	go demoDeadAgentRecovery(ctx)

	// 6. Task Pipeline 演示：12s 后发布一条三步流水线，
	//    每步的结果自动注入下一步描述，无需外部 orchestrator 轮询
	go demoPipeline(ctx)

	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Println("Agent Swarm Demo 运行中")
	log.Println("Dashboard:  http://localhost:8083")
	log.Println("功能：Swarm 任务 | Dead Agent Recovery | Task Pipeline")
	log.Println("按 Ctrl+C 停止")
	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	<-ctx.Done()
	log.Println("[main] 正在关闭...")
}

// ── 常规 Swarm 任务 ────────────────────────────────────────────────────────────

// publishTasks 模拟外部系统向 swarm 队列批量发布任务
func publishTasks(ctx context.Context) {
	// Publisher 注册为普通 agent，但主要使用 PublishTask 发布任务
	c := client.NewMailboxClient("publisher", mailboxAddr)
	if err := c.Register(ctx); err != nil {
		log.Printf("[Publisher] register: %v", err)
		return
	}
	if err := c.SetRole(ctx, "publisher"); err != nil {
		log.Printf("[Publisher] set role: %v", err)
	}

	tasks := []string{
		"分析情感倾向：'今天天气真好，心情愉快'",
		"翻译成英文：'人工智能正在改变世界'",
		"提取关键词：深度学习、神经网络、自然语言处理",
		"生成机器学习简介（50字以内）",
		"检查语法错误：'我昨天吃了一个苹果昨天'",
		"分类评论（正面/负面/中性）：'这个产品还行吧'",
		"总结要点：大模型在代码生成领域的应用与挑战",
		"改写成正式语气：'这玩意儿挺好用的，就是有点贵'",
		"判断是否含有敏感信息：'请联系我的私人邮箱'",
		"扩写成 200 字：'AI 助手改变了人们的工作方式'",
	}

	for i, desc := range tasks {
		select {
		case <-ctx.Done():
			return
		default:
		}

		taskID, err := c.PublishTask(ctx, desc, "text-processing")
		if err != nil {
			log.Printf("[Publisher] publish task %d: %v", i+1, err)
		} else {
			log.Printf("[Publisher] ▶ 发布任务 #%d (%s): %.30s...", i+1, taskID, desc)
		}

		// 错开发布时间，让 Swarm 有机会展示动态伸缩
		select {
		case <-ctx.Done():
			return
		case <-time.After(1200 * time.Millisecond):
		}
	}

	log.Println("[Publisher] 所有任务已发布，等待 agents 处理完成...")
}

// ── Dead Agent Recovery 演示 ──────────────────────────────────────────────────

// demoDeadAgentRecovery 演示：启动一个 agent，给它发布一个任务，
// 然后切断其心跳使其被驱逐，观察任务自动重新入队被另一个 agent 接手。
func demoDeadAgentRecovery(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(8 * time.Second):
	}

	log.Println("\n[Recovery Demo] ── 启动 Dead Agent Recovery 演示 ──")

	// 用一个可取消的子 ctx 控制这个 agent 的生命周期
	agentCtx, killAgent := context.WithCancel(ctx)

	// 发布一个慢任务（5s），确保 agent 被驱逐时任务仍在 running
	pub := client.NewMailboxClient("recovery-pub", mailboxAddr)
	if err := pub.Register(ctx); err != nil {
		log.Printf("[Recovery Demo] register publisher: %v", err)
		killAgent()
		return
	}
	taskID, err := pub.PublishTask(ctx, "耗时处理：对「量子计算」做深度调研（模拟5秒）", "text-processing")
	if err != nil {
		log.Printf("[Recovery Demo] publish task: %v", err)
		killAgent()
		return
	}
	log.Printf("[Recovery Demo] ▶ 发布慢任务 %s", taskID)

	// 启动专门的 doomed-agent，它认领任务后会一直持有（永不完成）
	go runDoomedAgent(agentCtx, "doomed-agent", mailboxAddr)
	time.Sleep(2 * time.Second) // 等待 agent 认领任务

	// 切断心跳 → 等待 30s 超时驱逐（为 demo 加速，直接取消 ctx）
	log.Println("[Recovery Demo] 🔪 切断 doomed-agent 心跳，等待驱逐...")
	killAgent()

	// Hub 的 evictionLoop 每 5s 检查一次，超时 30s 驱逐
	// 为加速 demo，这里等 35s 即可看到效果；实际生产中无需等待
	select {
	case <-ctx.Done():
	case <-time.After(35 * time.Second):
		log.Printf("[Recovery Demo] ✔ doomed-agent 被驱逐，任务 %s 应已重新入队", taskID)
	}
}

// runDoomedAgent 注册、声明能力、认领一个任务，但永远不完成它
// （模拟 agent 挂起或处理超时）。ctx 取消时停止心跳，触发驱逐。
func runDoomedAgent(ctx context.Context, agentID, addr string) {
	c := client.NewMailboxClient(agentID, addr)
	for {
		if err := c.Register(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	_ = c.SetRole(ctx, "swarm-worker")
	_ = c.SetCapabilities(ctx, "text-processing")
	_ = c.SetStatus(ctx, "idle", "")
	log.Printf("[%s] 上线（将在认领任务后假死）", agentID)

	// 认领一个任务
	for {
		task, err := c.ClaimTask(ctx)
		if err != nil || task == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(400 * time.Millisecond):
			}
			continue
		}
		log.Printf("[%s] ✓ 认领任务 %s — 现在假死（停止心跳）", agentID, task.ID)
		// 阻塞直到 ctx 取消（心跳随 ctx 取消而停止）
		<-ctx.Done()
		log.Printf("[%s] 心跳已停止，等待被驱逐", agentID)
		return
	}
}

// ── Task Pipeline 演示 ────────────────────────────────────────────────────────

// demoPipeline 演示三步流水线：调研 → 撰写 → 润色
// 每步结果通过 {{.PrevResult}} 自动注入下一步，无需外部轮询。
func demoPipeline(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(12 * time.Second):
	}

	log.Println("\n[Pipeline Demo] ── 启动 Task Pipeline 演示 ──")

	pub := client.NewMailboxClient("pipeline-pub", mailboxAddr)
	if err := pub.Register(ctx); err != nil {
		log.Printf("[Pipeline Demo] register: %v", err)
		return
	}

	steps := []mailbox.PipelineStep{
		{
			DescriptionTemplate: "调研主题「大语言模型的应用现状」，输出结构化摘要：主要场景、代表产品、核心挑战（各 2-3 点）。",
			RequiredCaps:        []string{"text-processing"},
		},
		{
			DescriptionTemplate: "基于以下调研摘要，撰写一篇 200 字科普短文：\n\n{{.PrevResult}}",
			RequiredCaps:        []string{"text-processing"},
		},
		{
			DescriptionTemplate: "润色并优化以下短文，使语言更流畅、适合大众读者：\n\n{{.PrevResult}}",
			RequiredCaps:        []string{"text-processing"},
		},
	}

	pipelineID, err := pub.PublishPipeline(ctx, "pipeline-pub", steps)
	if err != nil {
		log.Printf("[Pipeline Demo] PublishPipeline: %v", err)
		return
	}
	log.Printf("[Pipeline Demo] ▶ 流水线已启动 %s（3步，全部复用现有 text-processing agents）", pipelineID)

	// 轮询直到 pipeline 完成
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
		p, err := pub.GetPipeline(ctx, pipelineID)
		if err != nil || p == nil {
			continue
		}
		log.Printf("[Pipeline Demo] 进度 step %d/%d  status=%s", p.CurrentStep+1, len(p.Steps), p.Status)
		if p.Status == "completed" {
			log.Println("[Pipeline Demo] ✔ 流水线完成！最终产出：")
			for i, r := range p.Results {
				preview := r
				if len(preview) > 100 {
					preview = preview[:100] + "..."
				}
				log.Printf("  step %d: %s", i, preview)
			}
			return
		}
	}
}

// ── Swarm Agent 运行时 ────────────────────────────────────────────────────────

// inProcessFactory 以 goroutine 方式在当前进程内 spawn agent
type inProcessFactory struct {
	addr string
}

func (f *inProcessFactory) Spawn(ctx context.Context, agentID string, caps []string) error {
	go runSwarmAgent(ctx, agentID, f.addr, caps)
	return nil
}

// runSwarmAgent 是 swarm agent 的主循环：
//  1. 向 mailbox 注册，声明能力
//  2. 循环轮询 ClaimTask，原子认领队列中匹配自身能力的任务
//  3. 处理任务（此处为模拟，替换 processTask 为真实 LLM 调用即可）
//  4. 提交结果，回到空闲状态
func runSwarmAgent(ctx context.Context, agentID, addr string, caps []string) {
	c := client.NewMailboxClient(agentID, addr)

	// 等待 mailbox server 就绪，重试注册
	for {
		if err := c.Register(ctx); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}

	_ = c.SetRole(ctx, "swarm-worker")
	_ = c.SetCapabilities(ctx, caps...)
	_ = c.SetStatus(ctx, "idle", "")

	log.Printf("[%s] 上线，能力: %v", agentID, caps)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] 收到停止信号，退出", agentID)
			return
		default:
		}

		// 尝试从 swarm 队列中原子认领一个任务
		task, err := c.ClaimTask(ctx)
		if err != nil {
			// 网络异常，稍后重试
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		if task == nil {
			// 队列暂无任务，轮询等待
			select {
			case <-ctx.Done():
				return
			case <-time.After(400 * time.Millisecond):
			}
			continue
		}

		pipelineTag := ""
		if task.PipelineID != "" {
			pipelineTag = fmt.Sprintf(" [pipeline=%s step=%d]", task.PipelineID, task.PipelineStepIdx)
		}
		log.Printf("[%s] ✓ 认领 %s%s: %.35s...", agentID, task.ID, pipelineTag, task.Description)

		// 处理任务（模拟 LLM 调用）
		result := processTask(ctx, agentID, task.ID, task.Description)
		if ctx.Err() != nil {
			return
		}

		// Submit result — Hub resets agent status to idle automatically.
		if err := c.CompleteTask(ctx, task.ID, result); err != nil {
			log.Printf("[%s] 完成任务 %s 失败: %v", agentID, task.ID, err)
		} else {
			log.Printf("[%s] ✔ 完成 %s", agentID, task.ID)
		}
	}
}

// processTask 模拟任务处理（实际使用时替换为 LLM 调用）
func processTask(ctx context.Context, agentID, taskID, description string) string {
	// 模拟 0.8~2s 的处理时间
	delay := time.Duration(800+len(taskID)*150) * time.Millisecond
	if delay > 2*time.Second {
		delay = 2 * time.Second
	}
	select {
	case <-ctx.Done():
		return ""
	case <-time.After(delay):
	}
	return fmt.Sprintf("[%s 处理结果] 已完成：%s", agentID, description)
}
