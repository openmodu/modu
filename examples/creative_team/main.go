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
	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
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

func newCodingSession(model *types.Model, systemPrompt, agentDir string) *coding_agent.CodingSession {
	// Use agentDir as Cwd too so that CodingSession doesn't pick up CLAUDE.md
	// or other context files from the repo root and pollute the system prompt.
	cs, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:                agentDir,
		AgentDir:           agentDir,
		Model:              model,
		CustomSystemPrompt: systemPrompt,
		Tools:              []agent.AgentTool{}, // empty: no file tools, LLM must respond as text
	})
	if err != nil {
		log.Fatalf("[coding_agent] NewCodingSession: %v", err)
	}
	return cs
}

// llmCall 向 session 发一条消息，等待完成后返回回复文本
func llmCall(ctx context.Context, cs *coding_agent.CodingSession, prompt string) string {
	if err := cs.Prompt(ctx, prompt); err != nil {
		log.Printf("[llm] error: %v", err)
		return ""
	}
	cs.WaitForIdle()
	return cs.GetLastAssistantText()
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

	ws, err := NewWorkspace(workspaceRoot())
	if err != nil {
		log.Fatalf("[%s] workspace: %v", agentID, err)
	}
	ws.UpdateAgent(agentID, role, "idle")

	addr := mailboxAddr()
	c := client.NewMailboxClient(agentID, addr)

	if err := c.Register(ctx); err != nil {
		log.Fatalf("[%s] register: %v", agentID, err)
	}
	_ = c.SetRole(ctx, role)
	fmt.Printf("[%s] registered → mailbox %s\n", agentID, addr)
	fmt.Printf("[%s] workspace  → %s\n", agentID, workspaceRoot())

	llm := newCodingSession(model, systemPrompt, ws.AgentDir(agentID))

	// 捕获 Ctrl+C 优雅退出
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		fmt.Printf("\n[%s] shutting down...\n", agentID)
		ws.UpdateAgent(agentID, role, "offline")
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

			fmt.Printf("\n[%s] ← task %s\n", agentID, taskID)
			_ = c.StartTask(ctx, taskID)
			_ = c.SetStatus(ctx, "busy", taskID)
			ws.UpdateAgent(agentID, role, "busy")
			ws.IncrTaskCount(agentID)

			// 在任务讨论区发布开始通知
			sendChat(ctx, c, currentFrom, taskID, "已收到任务，开始处理中...")

			// LLM 处理任务
			taskPrompt := "请根据以下任务简报，立即输出完整内容。直接给出最终成果，不要说「正在处理」「稍后分享」等过渡语。\n\n" + payload.Description
			result := llmCall(ctx, llm, taskPrompt)
			if result == "" {
				result = fmt.Sprintf("[%s] 完成（无 LLM 输出）", agentID)
			}

			ws.SaveDoc(agentID, taskID, fmt.Sprintf("%s 的任务产出", agentID), result)

			// 完成前先在讨论区发布摘要，让 orchestrator 可以先看到
			preview := result
			if len([]rune(preview)) > 150 {
				preview = string([]rune(preview)[:150]) + "..."
			}
			sendChat(ctx, c, currentFrom, taskID, "已完成，摘要：\n"+preview)

			_ = c.CompleteTask(ctx, taskID, result)
			_ = c.SetStatus(ctx, "idle", "")
			ws.UpdateAgent(agentID, role, "idle")
			fmt.Printf("[%s] task %s done → workspace/docs/%s-%s.md\n", agentID, taskID, agentID, taskID)
			currentFrom = ""

		case mailbox.MessageTypeChat:
			if currentFrom == "" {
				continue
			}
			chatPayload, _ := mailbox.ParseChatPayload(parsed)
			fmt.Printf("[%s ← %s] %s\n", agentID, parsed.From, chatPayload.Text)

			// 根据角色判断是否参与讨论
			reply := llmCall(ctx, llm, fmt.Sprintf(
				"任务讨论中，%s 发言：%s\n根据你的角色，判断是否需要回复。如需回复则直接输出内容，无需回复则输出「[skip]」。",
				parsed.From, chatPayload.Text,
			))
			if reply != "" && reply != "[skip]" {
				sendChat(ctx, c, parsed.From, parsed.TaskID, reply)
			}
		}
	}
}

// ── 子命令：topic-selector ────────────────────────────────────────────────────

func cmdTopicSelector() {
	runWorker("topic-selector", "worker", `你是一名资深创作选题编辑。
收到任务简报后，直接输出3个有深度、有创意的选题方向（每个包含标题、核心角度、创作亮点2-3句），不要说「正在处理」等过渡语，直接给出完整选题内容。
收到问询消息时，简短回复即可。中文回复，简洁专业。`)
}

// ── 子命令：editor ────────────────────────────────────────────────────────────

func cmdEditor() {
	runWorker("editor", "worker", `你是一名专业的内容编辑和撰稿人。
收到创作简报后，直接输出一篇有深度、有文采的短文（约500字），不要说「正在创作」「稍后分享」等过渡语，直接给出完整文章正文。
收到问询消息时，简短回复即可。中文回复，专业优雅。`)
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

	ws, err := NewWorkspace(workspaceRoot())
	if err != nil {
		log.Fatalf("[orchestrator] workspace: %v", err)
	}
	ws.UpdateAgent("orchestrator", "orchestrator", "busy")

	// Ctrl+C 中断
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		ws.UpdateAgent("orchestrator", "orchestrator", "offline")
		cancel()
	}()

	addr := mailboxAddr()
	c := client.NewMailboxClient("orchestrator", addr)
	if err := c.Register(ctx); err != nil {
		log.Fatalf("[orchestrator] register: %v", err)
	}
	_ = c.SetRole(ctx, "orchestrator")
	fmt.Printf("[orchestrator] connected → %s\n", addr)
	fmt.Printf("[orchestrator] 创作主题：%s\n", brief)
	fmt.Printf("[orchestrator] workspace  → %s\n\n", workspaceRoot())

	llm := newCodingSession(model, orchestratorPrompt, ws.AgentDir("orchestrator"))

	// ── Phase 1: 生成选题任务 ─────────────────────────────────────────────
	fmt.Println("[orchestrator] === Phase 1: 向选题编辑下发任务 ===")
	_ = c.SetStatus(ctx, "busy", "")

	topicBrief := llmCall(ctx, llm,
		fmt.Sprintf("创作需求：%s\n\n请为选题编辑写一段任务简报，要求他提供3个有深度的选题方向，每个附带切入思路。", brief))
	fmt.Printf("[orchestrator] topic brief:\n%s\n\n", topicBrief)

	taskID1, _ := c.CreateTask(ctx, topicBrief)
	ws.SaveDoc("orchestrator", taskID1, "选题任务简报", topicBrief)
	_ = c.AssignTask(ctx, taskID1, "topic-selector")
	msg1, _ := mailbox.NewTaskAssignMessage("orchestrator", taskID1, topicBrief)
	_ = c.Send(ctx, "topic-selector", msg1)
	fmt.Printf("[orchestrator] task %s → topic-selector\n", taskID1)

	topicResult := waitForTask(ctx, c, llm, taskID1, "topic-selector")
	fmt.Printf("[orchestrator] 选题结果:\n%s\n\n", topicResult)

	// ── Phase 2: 挑选选题，下发写作任务 ─────────────────────────────────
	fmt.Println("[orchestrator] === Phase 2: 挑选选题，向编辑下发写作任务 ===")

	editBrief := llmCall(ctx, llm,
		fmt.Sprintf("选题编辑提交了以下方案：\n%s\n\n请挑选最有潜力的一个，为内容编辑写详细创作简报：主题、切入角度、目标读者、结构建议、约500字。", topicResult))
	fmt.Printf("[orchestrator] edit brief:\n%s\n\n", editBrief)

	taskID2, _ := c.CreateTask(ctx, editBrief)
	ws.SaveDoc("orchestrator", taskID2, "创作简报", editBrief)
	_ = c.AssignTask(ctx, taskID2, "editor")
	msg2, _ := mailbox.NewTaskAssignMessage("orchestrator", taskID2, editBrief)
	_ = c.Send(ctx, "editor", msg2)
	fmt.Printf("[orchestrator] task %s → editor\n", taskID2)

	article := waitForTask(ctx, c, llm, taskID2, "editor")
	fmt.Printf("[orchestrator] 成稿:\n%s\n\n", article)

	// ── Phase 3: 编辑寄语 ────────────────────────────────────────────────
	note := llmCall(ctx, llm,
		fmt.Sprintf("编辑完成了这篇文章：\n%s\n\n请写一段编辑寄语（2-3句话）。", article))

	finalPath := ws.SaveFinal(brief, article, note)
	ws.UpdateAgent("orchestrator", "orchestrator", "idle")
	_ = c.SetStatus(ctx, "idle", "")

	fmt.Println("\n" + strings.Repeat("═", 60))
	fmt.Println("✦  创作完成")
	fmt.Println(strings.Repeat("═", 60))
	fmt.Println("\n── 正文 ──")
	fmt.Println(article)
	fmt.Println("\n── 编辑寄语 ──")
	fmt.Println(note)
	fmt.Printf("\n已保存至：%s\n", finalPath)
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

// waitForTask 等待任务完成。
// 讨论区有新消息时，orchestrator LLM 根据 PMO 角色判断是否参与讨论。
// 不使用定时器——一切由消息驱动。
func waitForTask(
	ctx context.Context,
	c *client.MailboxClient,
	llm *coding_agent.CodingSession,
	taskID, workerID string,
) string {
	deadline := time.Now().Add(10 * time.Minute)

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

		// 处理讨论区消息：LLM 根据 PMO 角色判断是否参与
		for {
			raw, _ := c.Recv(ctx)
			if raw == "" {
				break
			}
			parsed, err := mailbox.ParseMessage(raw)
			if err != nil || parsed.Type != mailbox.MessageTypeChat {
				continue
			}
			p, _ := mailbox.ParseChatPayload(parsed)
			fmt.Printf("[orchestrator ← %s] %s\n", parsed.From, p.Text)

			reply := llmCall(ctx, llm, fmt.Sprintf(
				"任务讨论区，%s 发言：%s\n作为 PMO，根据你的职责判断是否需要介入。如需回复则直接输出内容，无需则输出「[skip]」。",
				parsed.From, p.Text,
			))
			if reply != "" && reply != "[skip]" {
				sendChat(ctx, c, workerID, taskID, reply)
			}
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
