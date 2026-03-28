package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

// ContentConfig holds LLM connection settings for the wechat content team.
type ContentConfig struct {
	APIURL  string // OpenAI-compatible base URL (e.g. http://localhost:1234/v1)
	APIKey  string // API key; empty is fine for LM Studio
	ModelID string // model identifier as known by the server
	WorkDir string // workspace root for saving articles
}

func defaultContentConfig() ContentConfig {
	url := os.Getenv("CONTENT_API_URL")
	if url == "" {
		url = "http://localhost:1234/v1"
	}
	model := os.Getenv("CONTENT_MODEL")
	if model == "" {
		model = "local-model"
	}
	return ContentConfig{
		APIURL:  url,
		APIKey:  os.Getenv("CONTENT_API_KEY"),
		ModelID: model,
		WorkDir: "./workspace",
	}
}

const wechatProviderID = "wechat-lmstudio"

var wechatAgentDefs = []struct {
	id     string
	role   string
	prompt string
}{
	{"wc-editor", "主编（协调者）", WechatEditorPrompt},
	{"wc-researcher", "热点研究员", WechatResearcherPrompt},
	{"wc-copywriter", "主笔", WechatCopywriterPrompt},
	{"wc-reviewer", "审稿编辑", WechatReviewerPrompt},
}

// startWechatTeam registers all content agents with the hub and spawns their goroutines.
func startWechatTeam(ctx context.Context, hub *mailbox.Hub, cfg ContentConfig) {
	providers.Register(openai.New(wechatProviderID,
		openai.WithBaseURL(cfg.APIURL),
		openai.WithAPIKey(cfg.APIKey),
	))

	for _, a := range wechatAgentDefs {
		hub.Register(a.id)
		_ = hub.SetAgentRole(a.id, a.role)
	}
	for _, a := range wechatAgentDefs {
		go runContentAgent(ctx, hub, a.id, a.prompt, cfg)
	}
	log.Printf("[wechat] team started (api=%s model=%s)", cfg.APIURL, cfg.ModelID)
}

// runContentAgent is the main loop for one content agent.
// It handles two message types:
//   - task_assign: coordinator role — the agent owns the task end-to-end
//   - delegate:    worker role — the agent performs sub-work and replies back
func runContentAgent(ctx context.Context, hub *mailbox.Hub, agentID, systemPrompt string, cfg ContentConfig) {
	agentWorkDir := filepath.Join(cfg.WorkDir, "agents", agentID)
	_ = os.MkdirAll(agentWorkDir, 0o755)
	log.Printf("[%s] ready", agentID)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		raw, ok := hub.Recv(agentID)
		if !ok || raw == "" {
			time.Sleep(120 * time.Millisecond)
			continue
		}

		parsed, err := mailbox.ParseMessage(raw)
		if err != nil {
			continue
		}

		switch parsed.Type {
		case mailbox.MessageTypeTaskAssign:
			// Coordinator mode: the agent owns this task and will orchestrate others.
			payload, _ := mailbox.ParseTaskAssignPayload(parsed)
			taskID := parsed.TaskID
			log.Printf("[%s] ← task_assign %s", agentID, taskID)
			_ = hub.StartTask(taskID)
			_ = hub.SetAgentStatus(agentID, "busy", taskID)

			if err := runCodingSession(ctx, hub, agentID, taskID, payload.Description,
				systemPrompt, coordinatorInstructions, cfg, agentWorkDir); err != nil {
				log.Printf("[%s] task %s error: %v", agentID, taskID, err)
				_ = hub.FailTask(taskID, err.Error())
				_ = hub.SetAgentStatus(agentID, "idle", "")
			}
			// On success, mailbox_complete marks the task done and sets agent idle.

		case mailbox.MessageTypeDelegate:
			// Worker mode: perform delegated sub-work and reply to the delegator.
			payload, _ := mailbox.ParseDelegatePayload(parsed)
			taskID := parsed.TaskID
			delegatorID := payload.DelegatorID
			log.Printf("[%s] ← delegate from %s (task %s)", agentID, delegatorID, taskID)
			_ = hub.SetAgentStatus(agentID, "busy", taskID)

			hub.PostForumMessage(agentID, taskID,
				fmt.Sprintf("👷 %s 开始处理委托工作…", agentID))

			if err := runCodingSession(ctx, hub, agentID, taskID, payload.Description,
				systemPrompt, workerInstructions(delegatorID), cfg, agentWorkDir); err != nil {
				log.Printf("[%s] delegate error: %v", agentID, err)
				// Reply with error so delegator doesn't hang
				hub.PostDelegateResult(taskID, agentID, delegatorID,
					fmt.Sprintf("工作失败：%v", err))
				_ = hub.SetAgentStatus(agentID, "idle", "")
			}
			// On success, mailbox_reply unblocks the delegator and sets agent idle.
		}
	}
}

// runCodingSession creates a fresh CodingSession for the given work and runs it to completion.
// instrFn returns role-specific instructions to append to the system prompt.
func runCodingSession(
	ctx context.Context,
	hub *mailbox.Hub,
	agentID, taskID, description, systemPrompt string,
	instrFn func(agentID, taskID, outputFile string) string,
	cfg ContentConfig,
	agentWorkDir string,
) error {
	absWorkDir, err := filepath.Abs(agentWorkDir)
	if err != nil {
		absWorkDir = agentWorkDir
	}
	outputFile := filepath.Join(absWorkDir, agentID+"-"+taskID+".md")

	model := &types.Model{
		ID:         cfg.ModelID,
		Name:       cfg.ModelID,
		ProviderID: wechatProviderID,
		Api:        types.KnownApiOpenAIChatCompletions,
		MaxTokens:  8192,
	}

	log.Printf("[%s] creating CodingSession (cwd=%s)", agentID, absWorkDir)
	cs, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:                absWorkDir,
		Model:              model,
		CustomTools:        hubMailboxTools(hub, agentID),
		CustomSystemPrompt: systemPrompt + instrFn(agentID, taskID, outputFile),
		GetAPIKey: func(provider string) (string, error) {
			if provider == wechatProviderID {
				return cfg.APIKey, nil
			}
			return "", fmt.Errorf("no API key for provider %s", provider)
		},
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	log.Printf("[%s] CodingSession ready, sending prompt to LLM", agentID)

	// Subscribe to agent events for live logging
	cs.Subscribe(func(e agent.AgentEvent) {
		switch e.Type {
		case agent.EventTypeTurnStart:
			log.Printf("[%s] ▶ LLM turn start", agentID)
		case agent.EventTypeToolExecutionStart:
			log.Printf("[%s] 🔧 tool call: %s", agentID, e.ToolName)
		case agent.EventTypeAgentEnd:
			log.Printf("[%s] ✓ agent done", agentID)
		}
	})

	userPrompt := fmt.Sprintf(
		"任务ID：%s\n\n%s\n\n请直接开始工作，不要以「好的」「以下是」等废话开头。",
		taskID, description)

	// Heartbeat: post a "still working" message every 30s so the forum doesn't look frozen.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				hub.PostForumMessage(agentID, taskID, "⏳ 仍在处理中…")
			}
		}
	}()

	if err := cs.Prompt(ctx, userPrompt); err != nil {
		close(done)
		return fmt.Errorf("prompt: %w", err)
	}
	cs.WaitForIdle()
	close(done)
	return nil
}

// coordinatorInstructions returns the workflow instructions for a coordinator agent.
// The coordinator owns the task end-to-end: delegates to peers, compiles results, completes the task.
func coordinatorInstructions(agentID, taskID, outputFile string) string {
	return fmt.Sprintf(`

---
## 工作流程（协调者模式）

你是本次任务的负责人，负责协调整个工作流程。

**可用的 mailbox 工具：**
- mailbox_delegate(to, task_id, request)：委托其他 agent 完成子工作，**阻塞等待其回复**
- mailbox_post(task_id, text)：向任务论坛发布消息（进展、想法、讨论）
- mailbox_complete(task_id, file_path)：提交最终成果，标记任务完成（仅在一切就绪后调用）

**执行规则：**
1. 通过 mailbox_delegate 委托各方工作，等待其结果后再推进下一步
2. 关键进展用 mailbox_post 发到论坛（让所有人知道进度）
3. 最终成果写入文件后调用 mailbox_complete：
   - task_id: %s
   - file_path: %s

**委托对象参考：**
- wc-researcher：热点调研、选题分析
- wc-copywriter：文章撰写、修改
- wc-reviewer：审稿、质量把关
`, taskID, outputFile)
}

// workerInstructions returns the workflow instructions for a worker agent (responding to a delegation).
func workerInstructions(delegatorID string) func(agentID, taskID, outputFile string) string {
	return func(agentID, taskID, outputFile string) string {
		return fmt.Sprintf(`

---
## 工作流程（执行者模式）

你正在响应 %s 的委托，完成一项子工作。

**执行规则：**
1. 全力完成所委托的工作，输出完整内容
2. 使用 Write 工具将完整成果写入文件：
   路径：%s
3. 调用 mailbox_reply 回传成果：
   - to: %s
   - task_id: %s
   - file_path: %s

**禁止跳过以上步骤**，否则委托方将无限等待。
`, delegatorID, outputFile, delegatorID, taskID, outputFile)
	}
}
