// examples/creative_team — 创作团队 Agent 协作示例
//
// 所有角色打包在同一个二进制，通过子命令启动不同进程：
//
//	# 1. 先启动 Mailbox Server（含 Dashboard）
//	go run ./examples/creative_team mailbox
//
//	# 2. 启动两个 worker（各自独立终端）
//	go run ./examples/creative_team topic-selector
//	go run ./examples/creative_team editor
//
//	# 3. 启动 Orchestrator 下发创作任务
//	go run ./examples/creative_team orchestrator "写一篇关于孤独与创造力的短文"
//
// 环境变量：
//
//	MAILBOX_ADDR   mailbox server 地址（默认 localhost:6382）
//	LMSTUDIO_URL   LM Studio 地址（默认 http://192.168.5.149:1234/v1）
//	LMSTUDIO_MODEL 模型名称（默认 zai-org/glm-4.7-flash）
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/mailbox"
	"github.com/crosszan/modu/pkg/mailbox/client"
	"github.com/crosszan/modu/pkg/mailbox/dashboard"
	"github.com/crosszan/modu/pkg/mailbox/server"
	"github.com/crosszan/modu/pkg/mailbox/sqlitestore"
	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/providers/openai"
	"github.com/crosszan/modu/pkg/types"
)

// ── 配置 ──────────────────────────────────────────────────────────────────────

func mailboxAddr() string {
	if v := os.Getenv("MAILBOX_ADDR"); v != "" {
		return v
	}
	return "localhost:6382"
}

func lmstudioURL() string {
	if v := os.Getenv("LMSTUDIO_URL"); v != "" {
		return v
	}
	return "http://192.168.5.149:1234/v1"
}

func lmstudioModel() string {
	if v := os.Getenv("LMSTUDIO_MODEL"); v != "" {
		return v
	}
	return "zai-org/glm-4.7-flash"
}

func setupModel() *types.Model {
	url := lmstudioURL()
	modelID := lmstudioModel()
	providers.Register(openai.New("lmstudio", openai.WithBaseURL(url)))
	log.Printf("[model] LM Studio %s @ %s", modelID, url)
	return &types.Model{ID: modelID, Name: modelID, ProviderID: "lmstudio"}
}

// ── LLM 工具函数 ──────────────────────────────────────────────────────────────

func newLLMAgent(model *types.Model, systemPrompt string) *agent.Agent {
	return agent.NewAgent(agent.AgentConfig{
		InitialState: &agent.AgentState{
			SystemPrompt: systemPrompt,
			Model:        model,
		},
	})
}

// llmCall 向 agent 发一条消息，等待完成后返回回复文本
func llmCall(ctx context.Context, a *agent.Agent, prompt string) string {
	if err := a.Prompt(ctx, prompt); err != nil {
		log.Printf("[llm] error: %v", err)
		return ""
	}
	a.WaitForIdle()
	msgs := a.GetState().Messages
	for i := len(msgs) - 1; i >= 0; i-- {
		var content []types.ContentBlock
		if m, ok := msgs[i].(types.AssistantMessage); ok {
			content = m.Content
		} else if m, ok := msgs[i].(*types.AssistantMessage); ok {
			content = m.Content
		}
		for _, c := range content {
			if tc, ok := c.(*types.TextContent); ok && tc.Text != "" {
				return tc.Text
			}
		}
	}
	return ""
}

func sendChat(ctx context.Context, c *client.MailboxClient, to, taskID, text string) {
	msg, _ := mailbox.NewChatMessage(c.AgentID(), taskID, text)
	if err := c.Send(ctx, to, msg); err != nil {
		log.Printf("[%s] send chat to %s: %v", c.AgentID(), to, err)
		return
	}
	fmt.Printf("  [%s → %s] %s\n", c.AgentID(), to, text)
}

// ── 子命令：mailbox ───────────────────────────────────────────────────────────

func cmdMailbox() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := mailboxAddr()
	dashAddr := "0.0.0.0:8082"

	store, err := sqlitestore.New("creative_team.db")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()

	s := server.NewMailboxServer(mailbox.WithStore(store))
	go func() {
		log.Printf("[mailbox] listening on %s", addr)
		if err := s.ListenAndServe(addr); err != nil {
			log.Printf("[mailbox] %v", err)
		}
	}()

	dash := dashboard.NewDashboard(s.Hub())
	go func() {
		if err := dash.Start(ctx, dashAddr); err != nil {
			log.Printf("[dashboard] %v", err)
		}
	}()

	fmt.Printf("Mailbox:   %s\n", addr)
	fmt.Printf("Dashboard: http://%s\n", dashAddr)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	fmt.Println("\nShutting down...")
}

// ── 子命令：worker 通用循环 ───────────────────────────────────────────────────

func runWorker(agentID, role, systemPrompt string) {
	model := setupModel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr := mailboxAddr()
	c := client.NewMailboxClient(agentID, addr)

	if err := c.Register(ctx); err != nil {
		log.Fatalf("[%s] register: %v", agentID, err)
	}
	_ = c.SetRole(ctx, role)
	fmt.Printf("[%s] registered → mailbox %s\n", agentID, addr)

	llm := newLLMAgent(model, systemPrompt)

	// 捕获 Ctrl+C 优雅退出
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		fmt.Printf("\n[%s] shutting down...\n", agentID)
		cancel()
	}()

	var currentFrom string // 当前任务的发送方（用于 chat 回复路由）

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		raw, err := c.Recv(ctx)
		if err != nil || raw == "" {
			time.Sleep(150 * time.Millisecond)
			continue
		}

		parsed, err := mailbox.ParseMessage(raw)
		if err != nil {
			continue
		}

		switch parsed.Type {

		case mailbox.MessageTypeTaskAssign:
			payload, _ := mailbox.ParseTaskAssignPayload(parsed)
			currentFrom = parsed.From
			taskID := parsed.TaskID

			fmt.Printf("\n[%s] ← task %s: %s\n", agentID, taskID, payload.Description)
			_ = c.StartTask(ctx, taskID)
			_ = c.SetStatus(ctx, "busy", taskID)

			// 立即告知已收到
			sendChat(ctx, c, currentFrom, taskID, "收到任务，正在处理中...")

			// LLM 处理任务
			result := llmCall(ctx, llm, payload.Description)
			if result == "" {
				result = fmt.Sprintf("[%s] 完成（无 LLM 输出）", agentID)
			}

			_ = c.CompleteTask(ctx, taskID, result)
			_ = c.SetStatus(ctx, "idle", "")
			sendChat(ctx, c, currentFrom, taskID, "任务已完成，请查收。")
			fmt.Printf("[%s] task %s done\n", agentID, taskID)
			currentFrom = "" // 清空，任务结束后不再响应 chat

		case mailbox.MessageTypeChat:
			// 只在有活跃任务时响应
			if currentFrom == "" {
				continue
			}
			chatPayload, _ := mailbox.ParseChatPayload(parsed)
			fmt.Printf("[%s ← %s] %s\n", agentID, parsed.From, chatPayload.Text)

			tid := parsed.TaskID
			reply := llmCall(ctx, llm, chatPayload.Text)
			if reply == "" {
				reply = "（正在忙碌中，稍后回复）"
			}
			replyTo := parsed.From
			if replyTo == "" {
				replyTo = currentFrom
			}
			sendChat(ctx, c, replyTo, tid, reply)
		}
	}
}

// ── 子命令：topic-selector ────────────────────────────────────────────────────

func cmdTopicSelector() {
	runWorker("topic-selector", "worker", `你是一名资深创作选题编辑。
接到任务后，你会从多个独特视角出发，提出3个有深度、有创意的选题方向。
每个选题包含：标题、核心角度、创作亮点（2-3句话）。
如有对话询问，请自然交流，分享你的选题思路。中文回复，简洁专业。`)
}

// ── 子命令：editor ────────────────────────────────────────────────────────────

func cmdEditor() {
	runWorker("editor", "worker", `你是一名专业的内容编辑和撰稿人。
接到任务后，你会创作一篇有深度、有文采的短文（约500字）。
你注重文章的结构感、语言节奏和思想深度。
如有对话询问，请自然交流，分享你的写作进展。中文回复，专业优雅。`)
}

// ── 子命令：orchestrator ──────────────────────────────────────────────────────

const orchestratorPrompt = `你是一名内容团队的 PMO（项目管理负责人）。
你负责协调选题编辑（topic-selector）和内容编辑（editor）完成创作。
你的职责：
1. 将需求整理为清晰的选题任务
2. 从选题结果中挑选最有潜力的方向，撰写创作简报
3. 下发给内容编辑，收到成稿后写编辑寄语
中文回复，简洁专业。`

func cmdOrchestrator(brief string) {
	model := setupModel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl+C 中断
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		cancel()
	}()

	addr := mailboxAddr()
	c := client.NewMailboxClient("orchestrator", addr)
	if err := c.Register(ctx); err != nil {
		log.Fatalf("[orchestrator] register: %v", err)
	}
	_ = c.SetRole(ctx, "orchestrator")
	fmt.Printf("[orchestrator] connected → %s\n", addr)
	fmt.Printf("[orchestrator] 创作主题：%s\n\n", brief)

	llm := newLLMAgent(model, orchestratorPrompt)

	// ── Phase 1: 生成选题任务 ─────────────────────────────────────────────
	fmt.Println("[orchestrator] === Phase 1: 向选题编辑下发任务 ===")
	_ = c.SetStatus(ctx, "busy", "")

	topicBrief := llmCall(ctx, llm,
		fmt.Sprintf("创作需求：%s\n\n请为选题编辑写一段任务简报，要求他提供3个有深度的选题方向，每个附带切入思路。", brief))
	fmt.Printf("[orchestrator] topic brief:\n%s\n\n", topicBrief)

	taskID1, _ := c.CreateTask(ctx, topicBrief)
	_ = c.AssignTask(ctx, taskID1, "topic-selector")
	msg1, _ := mailbox.NewTaskAssignMessage("orchestrator", taskID1, topicBrief)
	_ = c.Send(ctx, "topic-selector", msg1)
	fmt.Printf("[orchestrator] task %s → topic-selector\n", taskID1)

	topicResult := waitForTask(ctx, c, llm, taskID1, "topic-selector",
		[]string{
			"目前有哪些有趣的角度？希望有些新颖的切入点。",
			"选题进度如何？我们时间有点紧。",
		})
	fmt.Printf("[orchestrator] 选题结果:\n%s\n\n", topicResult)

	// ── Phase 2: 挑选选题，下发写作任务 ─────────────────────────────────
	fmt.Println("[orchestrator] === Phase 2: 挑选选题，向编辑下发写作任务 ===")

	editBrief := llmCall(ctx, llm,
		fmt.Sprintf("选题编辑提交了以下方案：\n%s\n\n请挑选最有潜力的一个，为内容编辑写详细创作简报：主题、切入角度、目标读者、结构建议、约500字。", topicResult))
	fmt.Printf("[orchestrator] edit brief:\n%s\n\n", editBrief)

	taskID2, _ := c.CreateTask(ctx, editBrief)
	_ = c.AssignTask(ctx, taskID2, "editor")
	msg2, _ := mailbox.NewTaskAssignMessage("orchestrator", taskID2, editBrief)
	_ = c.Send(ctx, "editor", msg2)
	fmt.Printf("[orchestrator] task %s → editor\n", taskID2)

	article := waitForTask(ctx, c, llm, taskID2, "editor",
		[]string{
			"文章进展如何？语言风格希望有一点文学性。",
			"结尾部分有没有一个有力的收束？",
		})
	fmt.Printf("[orchestrator] 成稿:\n%s\n\n", article)

	// ── Phase 3: 编辑寄语 ────────────────────────────────────────────────
	note := llmCall(ctx, llm,
		fmt.Sprintf("编辑完成了这篇文章：\n%s\n\n请写一段编辑寄语（2-3句话）。", article))

	_ = c.SetStatus(ctx, "idle", "")

	fmt.Println("\n" + strings.Repeat("═", 60))
	fmt.Println("✦  创作完成")
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println("\n── 正文 ──")
	fmt.Println(article)
	fmt.Println("\n── 编辑寄语 ──")
	fmt.Println(note)
	fmt.Println(strings.Repeat("═", 60))

	// 任务完成后继续监听 worker 的消息（仅记录，不自动回复），直到 Ctrl+C
	fmt.Println("\n[orchestrator] 任务完成，持续监听中（Ctrl+C 退出）...")
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		raw, _ := c.Recv(ctx)
		if raw == "" {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		parsed, err := mailbox.ParseMessage(raw)
		if err != nil {
			continue
		}
		if parsed.Type == mailbox.MessageTypeChat {
			p, _ := mailbox.ParseChatPayload(parsed)
			fmt.Printf("[orchestrator ← %s] %s\n", parsed.From, p.Text)
		}
	}
}

// waitForTask 等待任务完成，期间处理 chat 消息并定时向 worker 问询
func waitForTask(
	ctx context.Context,
	c *client.MailboxClient,
	llm *agent.Agent,
	taskID, workerID string,
	chatPrompts []string,
) string {
	deadline := time.Now().Add(10 * time.Minute)
	chatIdx := 0
	nextChatAt := time.Now().Add(6 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ""
		default:
		}

		task, err := c.GetTask(ctx, taskID)
		if err == nil {
			switch task.Status {
			case mailbox.TaskStatusCompleted:
				return task.Result
			case mailbox.TaskStatusFailed:
				return "任务失败：" + task.Error
			}
		}

		// 处理 worker 发来的 chat
		for {
			raw, _ := c.Recv(ctx)
			if raw == "" {
				break
			}
			parsed, err := mailbox.ParseMessage(raw)
			if err != nil {
				continue
			}
			if parsed.Type == mailbox.MessageTypeChat {
				p, _ := mailbox.ParseChatPayload(parsed)
				fmt.Printf("[orchestrator ← %s] %s\n", parsed.From, p.Text)
				reply := llmCall(ctx, llm, fmt.Sprintf("%s 说：%s\n请简短回复。", parsed.From, p.Text))
				if reply != "" {
					sendChat(ctx, c, parsed.From, taskID, reply)
				}
			}
		}

		// 定时发送问询
		if chatIdx < len(chatPrompts) && time.Now().After(nextChatAt) {
			sendChat(ctx, c, workerID, taskID, chatPrompts[chatIdx])
			chatIdx++
			nextChatAt = time.Now().Add(10 * time.Second)
		}

		time.Sleep(500 * time.Millisecond)
	}
	return "（等待超时）"
}

// ── 入口 ──────────────────────────────────────────────────────────────────────

const usage = `用法：creative_team <command> [args]

命令：
  mailbox              启动 Mailbox Server + Dashboard（需先运行）
  topic-selector       启动选题编辑 Agent（worker）
  editor               启动内容编辑 Agent（worker）
  orchestrator <主题>  启动 PMO，下发创作任务

环境变量：
  MAILBOX_ADDR    Mailbox 地址（默认 localhost:6382）
  LMSTUDIO_URL    LM Studio API 地址（默认 http://192.168.5.149:1234/v1）
  LMSTUDIO_MODEL  模型名称（默认 zai-org/glm-4.7-flash）
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "mailbox":
		cmdMailbox()
	case "topic-selector":
		cmdTopicSelector()
	case "editor":
		cmdEditor()
	case "orchestrator":
		brief := "写一篇关于AI时代人类创造力的短文"
		if len(os.Args) > 2 {
			brief = strings.Join(os.Args[2:], " ")
		}
		cmdOrchestrator(brief)
	default:
		fmt.Fprintf(os.Stderr, "未知命令：%s\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}
