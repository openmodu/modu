package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/mailbox"
	"github.com/crosszan/modu/pkg/mailbox/client"
	"github.com/crosszan/modu/pkg/types"
)

// NewMailboxTools returns the mailbox tools for a given client.
func NewMailboxTools(c *client.MailboxClient) []agent.AgentTool {
	return []agent.AgentTool{
		&getDiscussionTool{c: c},
		&postMessageTool{c: c},
		&getTaskTool{c: c},
		&completeTaskTool{c: c},
		&listAgentsTool{c: c},
		&listProjectsTool{c: c},
		&getProjectTool{c: c},
	}
}

// ── mailbox_get_discussion ────────────────────────────────────────────────────

type getDiscussionTool struct{ c *client.MailboxClient }

func (t *getDiscussionTool) Name() string  { return "mailbox_get_discussion" }
func (t *getDiscussionTool) Label() string { return "Get Task Discussion" }
func (t *getDiscussionTool) Description() string {
	return "获取指定任务下的完整讨论历史（所有参与者的发言记录）。"
}
func (t *getDiscussionTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "任务 ID",
			},
		},
		"required": []string{"task_id"},
	}
}
func (t *getDiscussionTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return toolText("task_id is required"), nil
	}
	entries, err := t.c.GetConversation(ctx, taskID)
	if err != nil {
		return toolText(fmt.Sprintf("error: %v", err)), nil
	}
	if len(entries) == 0 {
		return toolText("（暂无讨论记录）"), nil
	}
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] %s → %s: %s\n",
			e.At.Format("15:04:05"), e.From, e.To, e.Content))
	}
	return toolText(sb.String()), nil
}

// ── mailbox_post_message ──────────────────────────────────────────────────────

type postMessageTool struct{ c *client.MailboxClient }

func (t *postMessageTool) Name() string  { return "mailbox_post_message" }
func (t *postMessageTool) Label() string { return "Post Discussion Message" }
func (t *postMessageTool) Description() string {
	return "向任务讨论区发布一条消息（发给指定参与者，消息会记录在任务讨论历史中）。"
}
func (t *postMessageTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to": map[string]any{
				"type":        "string",
				"description": "接收消息的 agent ID",
			},
			"task_id": map[string]any{
				"type":        "string",
				"description": "任务 ID",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "消息内容",
			},
		},
		"required": []string{"to", "task_id", "text"},
	}
}
func (t *postMessageTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	to, _ := args["to"].(string)
	taskID, _ := args["task_id"].(string)
	text, _ := args["text"].(string)
	if to == "" || taskID == "" || text == "" {
		return toolText("to, task_id, text are all required"), nil
	}
	if to == t.c.AgentID() {
		return toolText("不能给自己发消息，请指定其他 agent 的 ID"), nil
	}
	msg, err := mailbox.NewChatMessage(t.c.AgentID(), taskID, text)
	if err != nil {
		return toolText(fmt.Sprintf("error building message: %v", err)), nil
	}
	if err := t.c.Send(ctx, to, msg); err != nil {
		return toolText(fmt.Sprintf("error sending: %v", err)), nil
	}
	fmt.Printf("  [%s → %s] %s\n", t.c.AgentID(), to, text)
	return toolText("消息已发送"), nil
}

// ── mailbox_list_projects ─────────────────────────────────────────────────────

type listProjectsTool struct{ c *client.MailboxClient }

func (t *listProjectsTool) Name() string  { return "mailbox_list_projects" }
func (t *listProjectsTool) Label() string { return "List Projects" }
func (t *listProjectsTool) Description() string {
	return "列出所有创作项目及其包含的任务 ID。"
}
func (t *listProjectsTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *listProjectsTool) Execute(ctx context.Context, _ string, _ map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	projects, err := t.c.ListProjects(ctx)
	if err != nil {
		return toolText(fmt.Sprintf("error: %v", err)), nil
	}
	if len(projects) == 0 {
		return toolText("（暂无项目）"), nil
	}
	var sb strings.Builder
	for _, p := range projects {
		sb.WriteString(fmt.Sprintf("- %s  name=%s  tasks=%d  status=%s\n",
			p.ID, p.Name, len(p.TaskIDs), p.Status))
	}
	return toolText(sb.String()), nil
}

// ── mailbox_get_project ───────────────────────────────────────────────────────

type getProjectTool struct{ c *client.MailboxClient }

func (t *getProjectTool) Name() string  { return "mailbox_get_project" }
func (t *getProjectTool) Label() string { return "Get Project Info" }
func (t *getProjectTool) Description() string {
	return "查询项目详情，包括所有任务 ID 和状态。"
}
func (t *getProjectTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project_id": map[string]any{
				"type":        "string",
				"description": "项目 ID",
			},
		},
		"required": []string{"project_id"},
	}
}
func (t *getProjectTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	projectID, _ := args["project_id"].(string)
	if projectID == "" {
		return toolText("project_id is required"), nil
	}
	proj, err := t.c.GetProject(ctx, projectID)
	if err != nil {
		return toolText(fmt.Sprintf("error: %v", err)), nil
	}
	data, _ := json.MarshalIndent(map[string]any{
		"id":       proj.ID,
		"name":     proj.Name,
		"status":   proj.Status,
		"task_ids": proj.TaskIDs,
	}, "", "  ")
	return toolText(string(data)), nil
}

// ── mailbox_get_task ──────────────────────────────────────────────────────────

type getTaskTool struct{ c *client.MailboxClient }

func (t *getTaskTool) Name() string  { return "mailbox_get_task" }
func (t *getTaskTool) Label() string { return "Get Task Info" }
func (t *getTaskTool) Description() string {
	return "查询任务的当前状态、描述和结果。"
}
func (t *getTaskTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "任务 ID",
			},
		},
		"required": []string{"task_id"},
	}
}
func (t *getTaskTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return toolText("task_id is required"), nil
	}
	task, err := t.c.GetTask(ctx, taskID)
	if err != nil {
		return toolText(fmt.Sprintf("error: %v", err)), nil
	}
	info := map[string]any{
		"id":           task.ID,
		"status":       task.Status,
		"description":  task.Description,
		"assignees":    task.Assignees,
		"result":       task.Result,
		"created_at":   task.CreatedAt.Format(time.RFC3339),
	}
	if task.ProjectID != "" {
		info["project_id"] = task.ProjectID
	}
	if len(task.AgentResults) > 0 {
		info["agent_results"] = task.AgentResults
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	return toolText(string(data)), nil
}

// ── mailbox_complete_task ─────────────────────────────────────────────────────

type completeTaskTool struct{ c *client.MailboxClient }

func (t *completeTaskTool) Name() string  { return "mailbox_complete_task" }
func (t *completeTaskTool) Label() string { return "Complete Task" }
func (t *completeTaskTool) Description() string {
	return "提交任务成果，将任务标记为已完成。result 是完整的最终成果内容。"
}
func (t *completeTaskTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "任务 ID",
			},
			"result": map[string]any{
				"type":        "string",
				"description": "完整的任务成果",
			},
		},
		"required": []string{"task_id", "result"},
	}
}
func (t *completeTaskTool) Execute(ctx context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	taskID, _ := args["task_id"].(string)
	result, _ := args["result"].(string)
	if taskID == "" || result == "" {
		return toolText("task_id and result are required"), nil
	}
	if err := t.c.CompleteTask(ctx, taskID, result); err != nil {
		return toolText(fmt.Sprintf("error: %v", err)), nil
	}
	_ = t.c.SetStatus(ctx, "idle", "")
	return toolText("任务已完成"), nil
}

// ── mailbox_list_agents ───────────────────────────────────────────────────────

type listAgentsTool struct{ c *client.MailboxClient }

func (t *listAgentsTool) Name() string  { return "mailbox_list_agents" }
func (t *listAgentsTool) Label() string { return "List Agents" }
func (t *listAgentsTool) Description() string {
	return "列出当前所有在线的 agent 及其角色和状态。"
}
func (t *listAgentsTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *listAgentsTool) Execute(ctx context.Context, _ string, _ map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	ids, err := t.c.ListAgents(ctx)
	if err != nil {
		return toolText(fmt.Sprintf("error: %v", err)), nil
	}
	var sb strings.Builder
	for _, id := range ids {
		info, err := t.c.GetAgentInfo(ctx, id)
		if err != nil {
			sb.WriteString(fmt.Sprintf("- %s (info unavailable)\n", id))
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s  role=%s  status=%s\n", id, info.Role, info.Status))
	}
	return toolText(sb.String()), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func toolText(s string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: s}},
	}
}
