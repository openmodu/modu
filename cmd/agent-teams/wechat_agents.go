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
//   - delegate:    collaborator role — the agent contributes work inside the same task
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

			if _, err := runCodingSession(ctx, hub, agentID, taskID, payload.Description,
				systemPrompt, coordinatorInstructions, cfg, agentWorkDir); err != nil {
				log.Printf("[%s] task %s error: %v", agentID, taskID, err)
				_ = hub.FailTask(taskID, err.Error())
				_ = hub.SetAgentStatus(agentID, "idle", "")
			}
			// On success, mailbox_complete marks the task done and sets agent idle.

		case mailbox.MessageTypeDelegate:
			// Collaborator mode: contribute sub-work inside the same task and reply to the delegator.
			payload, _ := mailbox.ParseDelegatePayload(parsed)
			taskID := parsed.TaskID
			delegatorID := payload.DelegatorID
			log.Printf("[%s] ← delegate from %s (task %s)", agentID, delegatorID, taskID)
			_ = hub.SetAgentStatus(agentID, "busy", taskID)

			lastText, err := runCodingSession(ctx, hub, agentID, taskID, payload.Description,
				systemPrompt, workerInstructions(delegatorID), cfg, agentWorkDir)
			if err != nil {
				log.Printf("[%s] delegate error: %v", agentID, err)
				hub.PostDelegateResult(taskID, agentID, delegatorID,
					fmt.Sprintf("工作失败：%v", err))
			} else {
				// Safety net: if the LLM forgot to call mailbox_reply, unblock the delegator
				// with whatever text the agent produced. PostDelegateResult is a no-op if
				// mailbox_reply already sent the result.
				if hub.PostDelegateResult(taskID, agentID, delegatorID, lastText) {
					hub.PostForumMessageKind(agentID, taskID, mailbox.ConversationKindDeliverable,
						fmt.Sprintf("⚠️ 未显式调用 mailbox_reply，系统已按最后输出回传摘要：%s", previewAgentText(lastText, 120)))
					log.Printf("[%s] safety-net: unblocked %s (mailbox_reply was not called)", agentID, delegatorID)
				}
			}
			_ = hub.SetAgentStatus(agentID, "idle", "")
		}
	}
}

// runCodingSession creates a fresh CodingSession for the given work and runs it to completion.
// instrFn returns role-specific instructions to append to the system prompt.
// Returns the last assistant text produced by the session alongside any error.
func runCodingSession(
	ctx context.Context,
	hub *mailbox.Hub,
	agentID, taskID, description, systemPrompt string,
	instrFn func(agentID, taskID, outputFile string) string,
	cfg ContentConfig,
	agentWorkDir string,
) (string, error) {
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
		return "", fmt.Errorf("create session: %w", err)
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

	if err := cs.Prompt(ctx, userPrompt); err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	cs.WaitForIdle()
	return cs.GetLastAssistantText(), nil
}

func previewAgentText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}

// coordinatorInstructions returns the workflow instructions for a coordinator agent.
// The coordinator owns the task end-to-end: delegates to peers, compiles results, completes the task.
func coordinatorInstructions(agentID, taskID, outputFile string) string {
	return fmt.Sprintf(`

---
## 工作流程（协调者模式）

你是本次任务的负责人，负责协调整个工作流程。

**可用的 mailbox 工具：**
- mailbox_delegate(to, task_id, request)：邀请其他 agent 参与当前任务，**阻塞等待其回复**
- mailbox_post(task_id, text, kind)：向任务论坛发布消息，kind 可用 progress / idea / decision / risk / deliverable
- mailbox_pin(task_id, summary)：更新任务顶部摘要，沉淀当前共识与下一步
- mailbox_complete(task_id, file_path, summary)：提交最终成果并结束任务

**执行规则：**
1. 始终围绕同一个 task 推进，通过 mailbox_delegate 邀请协作者参与，不要新建平行任务
2. 关键进展和想法用 mailbox_post 发到论坛，并带上合适的 kind
3. 在阶段结论明确后，用 mailbox_pin 更新“当前共识”
4. 最终成果写入文件后调用 mailbox_complete：
   - task_id: %s
   - file_path: %s

**论坛约束：**
- 论坛只发摘要，不要把完整创作简报、整篇文章、整段审稿意见原样贴到论坛
- 给协作者的完整材料放在 mailbox_delegate 的 request 里，论坛里只同步一句阶段结论
- 如果需要同步交付，只写“已完成什么 + 下一步是什么”

**委托对象参考：**
- wc-researcher：热点调研、选题分析
- wc-copywriter：文章撰写、修改（在 request 中附上完整创作简报）
- wc-reviewer：审稿、质量把关（在 request 中附上完整文章内容，不要只给文件路径）

**重要：委托审稿时，必须将完整文章内容直接写入 request 字段，因为审稿人无法访问主笔的工作目录。**
`, taskID, outputFile)
}

// workerInstructions returns the workflow instructions for a worker agent (responding to a delegation).
func workerInstructions(delegatorID string) func(agentID, taskID, outputFile string) string {
	return func(agentID, taskID, outputFile string) string {
		return fmt.Sprintf(`

---
## 工作流程（执行者模式）

你正在响应 %s 的协作请求，在同一个任务里补充一部分工作。
**委托内容已经包含在上方的任务描述中，不要去搜索文件，直接使用描述里的内容开始工作。**

**必须严格按以下顺序执行，缺少任何一步整个流程都会卡死：**

第一步：完成工作，产出完整内容
第二步：用 Write 工具将成果写入文件：
   路径：%s
第三步：【⚠️ 这步是强制的，不能省略】调用 mailbox_reply 回传成果：
   - to: %s
   - task_id: %s
   - file_path: %s

如果你不调用 mailbox_reply，委托方 %s 将永久阻塞等待，整个任务流程无法继续。
完成 mailbox_reply 后即可结束。
`, delegatorID, outputFile, delegatorID, taskID, outputFile, delegatorID)
	}
}
