package coding_agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/mailbox"
	"github.com/openmodu/modu/pkg/mailbox/client"
	"github.com/openmodu/modu/pkg/types"
)

const (
	defaultSpawnPollInterval = 500 * time.Millisecond
	defaultSpawnTimeout      = 5 * time.Minute
)

// SpawnAgentTool 允许 orchestrator agent 将任务委派给另一个 agent，
// 并等待该任务完成后返回结果。
//
// 执行流程：
//  1. TASK.CREATE（以自身为 creator）
//  2. TASK.ASSIGN → target agent
//  3. MSG.SEND → target agent（JSON 格式的 task_assign 消息）
//  4. 轮询 TASK.GET 直到 status = completed | failed（或超时）
//  5. 返回 task.Result 或 error
type SpawnAgentTool struct {
	mailbox      *client.MailboxClient
	pollInterval time.Duration
	timeout      time.Duration
}

// SpawnAgentOption 用于配置 SpawnAgentTool
type SpawnAgentOption func(*SpawnAgentTool)

// WithPollInterval 设置轮询间隔（默认 500ms）
func WithPollInterval(d time.Duration) SpawnAgentOption {
	return func(t *SpawnAgentTool) { t.pollInterval = d }
}

// WithSpawnTimeout 设置等待超时（默认 5min）
func WithSpawnTimeout(d time.Duration) SpawnAgentOption {
	return func(t *SpawnAgentTool) { t.timeout = d }
}

// NewSpawnAgentTool 创建一个 SpawnAgentTool 实例
func NewSpawnAgentTool(mc *client.MailboxClient, opts ...SpawnAgentOption) *SpawnAgentTool {
	t := &SpawnAgentTool{
		mailbox:      mc,
		pollInterval: defaultSpawnPollInterval,
		timeout:      defaultSpawnTimeout,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *SpawnAgentTool) Name() string  { return "spawn_agent" }
func (t *SpawnAgentTool) Label() string { return "Spawn Agent Task" }
func (t *SpawnAgentTool) Description() string {
	return `Delegate a task to another agent and wait for its completion.
The target agent must be registered in the mailbox. The tool creates a tracked task,
sends a task_assign message to the target, and polls until the task is completed or failed.
Returns the task result on success, or an error message on failure.`
}

func (t *SpawnAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target_agent_id": map[string]any{
				"type":        "string",
				"description": "The ID of the agent to delegate the task to (must be registered in mailbox)",
			},
			"task_description": map[string]any{
				"type":        "string",
				"description": "A clear description of what the target agent should do",
			},
		},
		"required": []string{"target_agent_id", "task_description"},
	}
}

func (t *SpawnAgentTool) Execute(
	ctx context.Context,
	toolCallID string,
	args map[string]any,
	onUpdate agent.AgentToolUpdateCallback,
) (agent.AgentToolResult, error) {
	targetID, _ := args["target_agent_id"].(string)
	description, _ := args["task_description"].(string)

	if targetID == "" {
		return spawnErrorResult("target_agent_id is required"), nil
	}
	if description == "" {
		return spawnErrorResult("task_description is required"), nil
	}

	// 1. 创建任务
	onUpdate(agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: fmt.Sprintf("Creating task for agent %s...", targetID)}},
	})

	taskID, err := t.mailbox.CreateTask(ctx, description)
	if err != nil {
		return spawnErrorResult(fmt.Sprintf("failed to create task: %v", err)), nil
	}

	// 2. 分配任务
	if err := t.mailbox.AssignTask(ctx, taskID, targetID); err != nil {
		return spawnErrorResult(fmt.Sprintf("failed to assign task %s to %s: %v", taskID, targetID, err)), nil
	}

	// 3. 发送 task_assign 消息
	msgStr, err := mailbox.NewTaskAssignMessage(t.mailbox.AgentID(), taskID, description)
	if err != nil {
		return spawnErrorResult(fmt.Sprintf("failed to build message: %v", err)), nil
	}
	if err := t.mailbox.Send(ctx, targetID, msgStr); err != nil {
		return spawnErrorResult(fmt.Sprintf("failed to send task message to %s: %v", targetID, err)), nil
	}

	onUpdate(agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: fmt.Sprintf("Task %s assigned to %s, waiting for completion...", taskID, targetID)}},
	})

	// 4. 轮询等待结果
	result, pollErr := t.pollForCompletion(ctx, taskID)
	if pollErr != nil {
		return spawnErrorResult(fmt.Sprintf("task %s failed: %v", taskID, pollErr)), nil
	}

	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: result}},
		Details: map[string]string{"task_id": taskID, "assigned_to": targetID},
	}, nil
}

func (t *SpawnAgentTool) pollForCompletion(ctx context.Context, taskID string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return "", fmt.Errorf("timeout waiting for task %s", taskID)
		case <-ticker.C:
			task, err := t.mailbox.GetTask(timeoutCtx, taskID)
			if err != nil {
				// 暂时性错误，继续轮询
				continue
			}
			switch task.Status {
			case mailbox.TaskStatusCompleted:
				return task.Result, nil
			case mailbox.TaskStatusFailed:
				return "", errors.New(task.Error)
			}
		}
	}
}

func spawnErrorResult(msg string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: msg}},
	}
}
