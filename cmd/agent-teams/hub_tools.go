package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/types"
)

// hubMailboxTools returns the tools that agents use to communicate via the in-process hub.
//
//   - mailbox_delegate   — coordinator only: delegate work to a peer agent and wait for the result
//   - mailbox_reply      — worker only: reply to the delegator with completed work
//   - mailbox_post       — any agent: post a free-form message to the task's forum thread
//   - mailbox_pin        — owner/coordinator: update the pinned summary for the task
//   - mailbox_complete   — coordinator only: mark the whole task as done with the final deliverable
func hubMailboxTools(hub *mailbox.Hub, agentID string) []agent.AgentTool {
	return []agent.AgentTool{
		&hubDelegateTool{hub: hub, agentID: agentID},
		&hubReplyTool{hub: hub, agentID: agentID},
		&hubPostTool{hub: hub, agentID: agentID},
		&hubPinTool{hub: hub, agentID: agentID},
		&hubCompleteTool{hub: hub, agentID: agentID},
	}
}

// ── mailbox_delegate ──────────────────────────────────────────────────────────

type hubDelegateTool struct {
	hub     *mailbox.Hub
	agentID string
}

func (t *hubDelegateTool) Name() string  { return "mailbox_delegate" }
func (t *hubDelegateTool) Label() string { return "Delegate Work" }
func (t *hubDelegateTool) Description() string {
	return "邀请另一个 agent 参与当前任务，并阻塞等待其回复（最长 15 分钟）。协作请求和结果都会记录在任务论坛中。"
}
func (t *hubDelegateTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to":      map[string]any{"type": "string", "description": "被委托的 agent ID（如 wc-researcher）"},
			"task_id": map[string]any{"type": "string", "description": "当前任务 ID"},
			"request": map[string]any{"type": "string", "description": "委托内容，描述需要对方完成的工作"},
		},
		"required": []string{"to", "task_id", "request"},
	}
}
func (t *hubDelegateTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	to, _ := args["to"].(string)
	taskID, _ := args["task_id"].(string)
	request, _ := args["request"].(string)
	if to == "" || taskID == "" || request == "" {
		return hubText("to, task_id, request 均为必填"), nil
	}
	if to == t.agentID {
		return hubText("不能委托给自己"), nil
	}
	if err := t.hub.EnsureTaskOpen(taskID); err != nil {
		return hubText(err.Error()), nil
	}
	if err := t.hub.AssignTask(taskID, to); err != nil {
		return hubText(fmt.Sprintf("加入协作者失败: %v", err)), nil
	}

	// Register the reply channel before sending (avoid race)
	replyCh := t.hub.RegisterDelegate(taskID, to, t.agentID)

	// Post collaboration request to forum so it's visible
	t.hub.PostForumMessageKind(t.agentID, taskID, mailbox.ConversationKindDecision,
		fmt.Sprintf("📋 邀请 %s 协作：%s", to, previewText(request, 180)))

	// Send delegate message to target agent
	msg, err := mailbox.NewDelegateMessage(t.agentID, taskID, request)
	if err != nil {
		return hubText(fmt.Sprintf("build msg: %v", err)), nil
	}
	if err := t.hub.Send(to, msg); err != nil {
		return hubText(fmt.Sprintf("send: %v", err)), nil
	}

	// Wait for reply (with context + 15 min hard deadline)
	deadline := time.After(15 * time.Minute)
	select {
	case <-ctx.Done():
		return hubText("已取消"), nil
	case <-deadline:
		return hubText(fmt.Sprintf("等待 %s 回复超时（15分钟）", to)), nil
	case result := <-replyCh:
		return hubText(result), nil
	}
}

// ── mailbox_reply ─────────────────────────────────────────────────────────────

type hubReplyTool struct {
	hub     *mailbox.Hub
	agentID string
}

func (t *hubReplyTool) Name() string  { return "mailbox_reply" }
func (t *hubReplyTool) Label() string { return "Reply to Delegation" }
func (t *hubReplyTool) Description() string {
	return "将工作成果回传给委托方。传入产出文件路径（file_path），系统会读取内容并通知委托方。成果也会记录在任务论坛中。"
}
func (t *hubReplyTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to":        map[string]any{"type": "string", "description": "委托方的 agent ID"},
			"task_id":   map[string]any{"type": "string", "description": "任务 ID"},
			"file_path": map[string]any{"type": "string", "description": "产出文件路径（Write 工具创建的 .md 文件）"},
			"text":      map[string]any{"type": "string", "description": "直接回复文本（无文件时使用）"},
		},
		"required": []string{"to", "task_id"},
	}
}
func (t *hubReplyTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	to, _ := args["to"].(string)
	taskID, _ := args["task_id"].(string)
	filePath, _ := args["file_path"].(string)
	text, _ := args["text"].(string)

	if to == "" || taskID == "" {
		return hubText("to 和 task_id 均为必填"), nil
	}
	if err := t.hub.EnsureTaskOpen(taskID); err != nil {
		return hubText(err.Error()), nil
	}

	content := text
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return hubText(fmt.Sprintf("读取文件失败 %s: %v", filePath, err)), nil
		}
		content = string(data)
	}
	if content == "" {
		return hubText("file_path 或 text 必须提供其中一个"), nil
	}

	// Post to forum so it's visible
	chars := len([]rune(content))
	msg := fmt.Sprintf("✅ 协作内容已提交（%d字），同步给 %s", chars, to)
	if filePath != "" {
		msg += fmt.Sprintf(" · 文件 %s", filepath.Base(filePath))
	}
	t.hub.PostForumMessageKind(t.agentID, taskID, mailbox.ConversationKindDeliverable, msg)

	// Unblock the waiting mailbox_delegate call
	if !t.hub.PostDelegateResult(taskID, t.agentID, to, content) {
		// No one waiting — post as a regular chat message instead
		chatMsg, _ := mailbox.NewChatMessage(t.agentID, taskID, content)
		_ = t.hub.Send(to, chatMsg)
	}

	_ = t.hub.SetAgentStatus(t.agentID, "idle", "")
	return hubText(fmt.Sprintf("已回传给 %s（%d字）", to, chars)), nil
}

// ── mailbox_post ──────────────────────────────────────────────────────────────

type hubPostTool struct {
	hub     *mailbox.Hub
	agentID string
}

func (t *hubPostTool) Name() string  { return "mailbox_post" }
func (t *hubPostTool) Label() string { return "Post to Forum" }
func (t *hubPostTool) Description() string {
	return "向任务论坛发布一条消息（所有参与者可见）。用于分享进展、想法或讨论。"
}
func (t *hubPostTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "任务 ID"},
			"text":    map[string]any{"type": "string", "description": "消息内容"},
			"kind": map[string]any{
				"type":        "string",
				"description": "消息类型：general | progress | idea | decision | risk | deliverable | system",
			},
		},
		"required": []string{"task_id", "text"},
	}
}
func (t *hubPostTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	taskID, _ := args["task_id"].(string)
	text, _ := args["text"].(string)
	kindStr, _ := args["kind"].(string)
	if taskID == "" || text == "" {
		return hubText("task_id 和 text 均为必填"), nil
	}
	if err := t.hub.EnsureTaskOpen(taskID); err != nil {
		return hubText(err.Error()), nil
	}
	t.hub.PostForumMessageKind(t.agentID, taskID, mailbox.ConversationKind(kindStr), text)
	return hubText("消息已发布"), nil
}

// ── mailbox_pin ───────────────────────────────────────────────────────────────

type hubPinTool struct {
	hub     *mailbox.Hub
	agentID string
}

func (t *hubPinTool) Name() string  { return "mailbox_pin" }
func (t *hubPinTool) Label() string { return "Pin Task Summary" }
func (t *hubPinTool) Description() string {
	return "更新任务顶部摘要，沉淀当前共识、阶段结论或下一步安排。"
}
func (t *hubPinTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "任务 ID"},
			"summary": map[string]any{"type": "string", "description": "最新摘要"},
		},
		"required": []string{"task_id", "summary"},
	}
}
func (t *hubPinTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	taskID, _ := args["task_id"].(string)
	summary, _ := args["summary"].(string)
	if taskID == "" || summary == "" {
		return hubText("task_id 和 summary 均为必填"), nil
	}
	if err := t.hub.UpdateTaskSummary(taskID, summary); err != nil {
		return hubText(err.Error()), nil
	}
	t.hub.PostForumMessageKind(t.agentID, taskID, mailbox.ConversationKindDecision, "📌 已更新任务摘要："+previewText(summary, 160))
	return hubText("任务摘要已更新"), nil
}

// ── mailbox_complete ──────────────────────────────────────────────────────────

type hubCompleteTool struct {
	hub     *mailbox.Hub
	agentID string
}

func (t *hubCompleteTool) Name() string  { return "mailbox_complete" }
func (t *hubCompleteTool) Label() string { return "Complete Task" }
func (t *hubCompleteTool) Description() string {
	return "提交最终成果并将任务标记为已完成。传入产出文件路径（file_path），系统会读取文件内容存入系统。仅任务负责人（协调者）调用。"
}
func (t *hubCompleteTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string", "description": "任务 ID"},
			"file_path": map[string]any{
				"type":        "string",
				"description": "最终产出文件路径",
			},
			"result": map[string]any{
				"type":        "string",
				"description": "直接传入成果文本（无文件时使用）",
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "任务完成后的最终摘要，可选",
			},
		},
		"required": []string{"task_id"},
	}
}
func (t *hubCompleteTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	taskID, _ := args["task_id"].(string)
	filePath, _ := args["file_path"].(string)
	result, _ := args["result"].(string)
	summary, _ := args["summary"].(string)

	if taskID == "" {
		return hubText("task_id 是必填项"), nil
	}

	content := result
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return hubText(fmt.Sprintf("读取文件失败 %s: %v", filePath, err)), nil
		}
		content = string(data)
	}
	if content == "" {
		return hubText("file_path 或 result 必须提供其中一个"), nil
	}
	if summary != "" {
		if err := t.hub.UpdateTaskSummary(taskID, summary); err != nil {
			return hubText(fmt.Sprintf("update summary: %v", err)), nil
		}
	}

	if err := t.hub.CompleteTask(taskID, t.agentID, content); err != nil {
		return hubText(fmt.Sprintf("complete task: %v", err)), nil
	}
	_ = t.hub.SetAgentStatus(t.agentID, "idle", "")

	chars := len([]rune(content))
	msg := fmt.Sprintf("🎉 任务完成！最终成果 %d 字", chars)
	if filePath != "" {
		msg += fmt.Sprintf(" · 文件 %s", filepath.Base(filePath))
	}
	t.hub.ForcePostForumMessageKind(t.agentID, taskID, mailbox.ConversationKindDeliverable, msg)
	return hubText(fmt.Sprintf("任务 %s 已完成，成果 %d 字", taskID, chars)), nil
}

// ── helper ────────────────────────────────────────────────────────────────────

func hubText(s string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: s}},
	}
}

func previewText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}
