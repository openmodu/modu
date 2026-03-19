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

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/mailbox/dashboard"
	"github.com/openmodu/modu/pkg/mailbox/server"
	"github.com/openmodu/modu/pkg/mailbox/sqlitestore"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
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

func newCodingSession(model *types.Model, systemPrompt, agentDir string, tools []agent.AgentTool) *coding_agent.CodingSession {
	// Use agentDir as Cwd too so that CodingSession doesn't pick up CLAUDE.md
	// or other context files from the repo root and pollute the system prompt.
	cs, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:                agentDir,
		AgentDir:           agentDir,
		Model:              model,
		CustomSystemPrompt: systemPrompt,
		Tools:              tools,
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

	mbTools := NewMailboxTools(c)
	llm := newCodingSession(model, systemPrompt, ws.AgentDir(agentID), mbTools)

	// 捕获 Ctrl+C 优雅退出
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		fmt.Printf("\n[%s] shutting down...\n", agentID)
		ws.UpdateAgent(agentID, role, "offline")
		cancel()
	}()

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
			taskID := parsed.TaskID
			from := parsed.From

			fmt.Printf("\n[%s] ← task %s from %s\n", agentID, taskID, from)
			_ = c.StartTask(ctx, taskID)
			_ = c.SetStatus(ctx, "busy", taskID)
			ws.UpdateAgent(agentID, role, "busy")
			ws.IncrTaskCount(agentID)

			// 每个任务开始前重置上下文，避免历史 tool call 积累导致 LLM 循环
			llm.GetAgent().Reset()

			// Step 1: 发确认（单次 tool call：mailbox_post_message）
			llmCall(ctx, llm, fmt.Sprintf(
				"你收到了来自 %s 的任务（ID: %s）。\n任务内容：\n%s\n\n"+
					"请用 mailbox_post_message 向 %s 确认已收到（一句话即可）。只需这一步。",
				from, taskID, payload.Description, from,
			))

			// Step 2: 纯文本生成（不调 tool，直接输出完整成果）
			result := llmCall(ctx, llm,
				"现在根据任务要求，直接输出完整的任务成果。不要说「正在处理」，直接给出最终内容。")
			if result == "" {
				result = fmt.Sprintf("[%s] 完成（无 LLM 输出）", agentID)
			}

			// Step 3: 保存文件，以文件路径提交成果，LLM 发通知
			filePath := ws.SaveDoc(agentID, taskID, fmt.Sprintf("%s 的任务产出", agentID), result)
			_ = c.CompleteTask(ctx, taskID, filePath)
			llmCall(ctx, llm, fmt.Sprintf(
				"任务已提交。用 mailbox_post_message 向 %s 发一条简短完成通知（不超过50字）。", from,
			))

			_ = c.SetStatus(ctx, "idle", "")
			ws.UpdateAgent(agentID, role, "idle")
			// 任务完成后也重置，释放上下文
			llm.GetAgent().Reset()
			fmt.Printf("[%s] task %s done → workspace/docs/%s-%s.md\n", agentID, taskID, agentID, taskID)

		case mailbox.MessageTypeChat:
			if parsed.From == agentID {
				continue // 跳过自己发给自己的消息
			}
			chatPayload, _ := mailbox.ParseChatPayload(parsed)
			fmt.Printf("[%s ← %s] %s\n", agentID, parsed.From, chatPayload.Text)

			// 直接基于消息内容回复，不再调用 mailbox_get_discussion（避免循环）
			llmCall(ctx, llm, fmt.Sprintf(
				"%s 说（任务 %s）：%s\n"+
					"根据你的角色判断是否需要回复。如需回复，用 mailbox_post_message 发给 %s（50字以内）；"+
					"如无需回复，直接输出「不需要回复」即可。不要调用其他工具。",
				parsed.From, parsed.TaskID, chatPayload.Text, parsed.From,
			))
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

const orchestratorPrompt = `你是一名内容团队的 PMO（项目管理负责人），负责确保最终成果达到高质量标准。
你的职责：
1. 为各成员制定清晰的任务简报
2. 评审交付成果——不达标时明确指出问题并要求修改，达标时接受并推进
3. 选取最优选题，指导内容创作方向
4. 收到满意成稿后，写一段有温度的编辑寄语

评审标准：内容深度、结构清晰、语言质量、与任务简报的契合度。
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

	// 创建本次创作项目，将所有任务归入同一 project
	projectID, err := c.CreateProject(ctx, brief)
	if err != nil {
		log.Printf("[orchestrator] 创建项目失败（继续执行）: %v", err)
		projectID = ""
	} else {
		fmt.Printf("[orchestrator] 创建项目 %s\n", projectID)
	}

	mbTools := NewMailboxTools(c)
	llm := newCodingSession(model, orchestratorPrompt, ws.AgentDir("orchestrator"), mbTools)

	// ── Phase 1: 选题 ────────────────────────────────────────────────────
	fmt.Println("[orchestrator] === Phase 1: 选题 ===")
	_ = c.SetStatus(ctx, "busy", "")

	topicBrief := llmCall(ctx, llm,
		fmt.Sprintf("创作需求：%s\n\n请为选题编辑写一段任务简报，要求他提供3个有深度的选题方向，每个附带切入思路。", brief))
	fmt.Printf("[orchestrator] topic brief:\n%s\n\n", topicBrief)

	taskID1 := assignTask(ctx, c, ws, topicBrief, "topic-selector", projectID)
	topicResult := waitForTaskWithReview(ctx, c, llm, ws, taskID1, "topic-selector", projectID, "选题方案", 2)
	fmt.Printf("[orchestrator] 选题结果:\n%s\n\n", topicResult)

	// ── Phase 2: 写作 ────────────────────────────────────────────────────
	fmt.Println("[orchestrator] === Phase 2: 写作 ===")
	// 阶段间重置上下文，避免选题阶段的 tool 历史影响写作阶段
	llm.GetAgent().Reset()

	editBrief := llmCall(ctx, llm,
		fmt.Sprintf("选题编辑提交了以下方案：\n%s\n\n请挑选最有潜力的一个，为内容编辑写详细创作简报：主题、切入角度、目标读者、结构建议、约500字。", topicResult))
	fmt.Printf("[orchestrator] edit brief:\n%s\n\n", editBrief)

	taskID2 := assignTask(ctx, c, ws, editBrief, "editor", projectID)
	article := waitForTaskWithReview(ctx, c, llm, ws, taskID2, "editor", projectID, "文章", 2)
	fmt.Printf("[orchestrator] 成稿:\n%s\n\n", article)

	// ── Phase 3: 编辑寄语 ────────────────────────────────────────────────
	note := llmCall(ctx, llm,
		fmt.Sprintf("以下文章已通过评审：\n%s\n\n请写一段有温度的编辑寄语（2-3句话）。", article))

	finalPath := ws.SaveFinal(brief, article, note)

	// 标记项目已完成
	if projectID != "" {
		if err := c.CompleteProject(ctx, projectID); err != nil {
			log.Printf("[orchestrator] CompleteProject: %v", err)
		} else {
			fmt.Printf("[orchestrator] project %s 已完成\n", projectID)
		}
	}

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

// assignTask 创建任务、保存到 workspace 并发给指定 worker，返回 taskID。
func assignTask(ctx context.Context, c *client.MailboxClient, ws *Workspace, brief, workerID, projectID string) string {
	taskID, _ := c.CreateTask(ctx, brief, projectID)
	ws.SaveDoc("orchestrator", taskID, fmt.Sprintf("任务简报→%s", workerID), brief)
	_ = c.AssignTask(ctx, taskID, workerID)
	msg, _ := mailbox.NewTaskAssignMessage("orchestrator", taskID, brief)
	_ = c.Send(ctx, workerID, msg)
	fmt.Printf("[orchestrator] task %s → %s (project: %s)\n", taskID, workerID, projectID)
	return taskID
}

// reviewResult 让 orchestrator LLM 评审成果。
// 返回 (true, "") 表示接受；返回 (false, feedback) 表示退回，feedback 是具体修改意见。
func reviewResult(ctx context.Context, llm *coding_agent.CodingSession, stage, result string) (bool, string) {
	evaluation := llmCall(ctx, llm, fmt.Sprintf(
		"请评审以下「%s」成果：\n\n%s\n\n"+
			"评审标准：内容深度、结构清晰、语言质量、与任务简报的契合度。\n"+
			"如果达标，仅输出「[accept]」；\n"+
			"如果需要改进，输出「[revise]: 」后跟具体修改意见（说明问题和期望）。",
		stage, result,
	))
	if strings.HasPrefix(strings.TrimSpace(evaluation), "[accept]") {
		return true, ""
	}
	feedback := strings.TrimPrefix(strings.TrimSpace(evaluation), "[revise]:")
	feedback = strings.TrimPrefix(feedback, "[revise]: ")
	return false, strings.TrimSpace(feedback)
}

// waitForTaskWithReview 等待任务完成后由 orchestrator LLM 评审。
// 不达标则退回修改（最多 maxRevisions 轮），达标后返回最终成果。
func waitForTaskWithReview(
	ctx context.Context,
	c *client.MailboxClient,
	llm *coding_agent.CodingSession,
	ws *Workspace,
	initialTaskID, workerID, projectID, stage string,
	maxRevisions int,
) string {
	taskID := initialTaskID

	for attempt := 0; attempt <= maxRevisions; attempt++ {
		result := waitForTask(ctx, c, llm, taskID, workerID)
		if result == "" || strings.HasPrefix(result, "任务失败") || strings.HasPrefix(result, "（等待超时）") {
			return result
		}

		fmt.Printf("[orchestrator] 评审「%s」(第 %d/%d 次)\n", stage, attempt+1, maxRevisions+1)
		accepted, feedback := reviewResult(ctx, llm, stage, result)

		if accepted {
			fmt.Printf("[orchestrator] ✓ 「%s」已接受\n", stage)
			return result
		}

		if attempt == maxRevisions {
			fmt.Printf("[orchestrator] 已达最大修改轮次，接受当前版本\n")
			return result
		}

		fmt.Printf("[orchestrator] ✗ 退回「%s」，修改意见：%s\n", stage, feedback)
		revisionBrief := fmt.Sprintf(
			"你提交的成果需要修改，请根据以下反馈改进：\n\n**修改意见**：\n%s\n\n**原成果**：\n%s",
			feedback, result,
		)
		taskID = assignTask(ctx, c, ws, revisionBrief, workerID, projectID)
	}
	return "（未能在规定轮次内完成）"
}

// waitForTask 等待任务完成。
// 讨论区有新消息时，将完整讨论上下文交给 orchestrator LLM，由它通过 mailbox tools 决定是否参与。
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
				// result 存的是文件路径，读取文件内容供 LLM 评审
				result := task.Result
				if data, ferr := os.ReadFile(result); ferr == nil {
					result = string(data)
				}
				return result
			case mailbox.TaskStatusFailed:
				return "任务失败：" + task.Error
			}
		}

		// 收到讨论区消息时，直接基于消息内容决定是否介入（不调 mailbox_get_discussion 避免循环）
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

			llmCall(ctx, llm, fmt.Sprintf(
				"%s 说（任务 %s）：%s\n"+
					"作为 PMO，判断是否需要介入。如需要，用 mailbox_post_message 发给 %s；如无需介入，直接输出「不介入」。不要调用其他工具。",
				parsed.From, taskID, p.Text, workerID,
			))
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
