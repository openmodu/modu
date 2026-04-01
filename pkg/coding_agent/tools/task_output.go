package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type BackgroundTask struct {
	ID      string
	Kind    string
	Status  string
	Summary string
	Output  string
	Error   string
}

type BackgroundTaskStore interface {
	Create(kind, summary string) string
	Complete(id, output string)
	Fail(id, errMsg string)
	Get(id string) (BackgroundTask, bool)
	List() []BackgroundTask
}

type TaskOutputTool struct {
	store BackgroundTaskStore
}

func NewTaskOutputTool(store BackgroundTaskStore) *TaskOutputTool {
	return &TaskOutputTool{store: store}
}

func (t *TaskOutputTool) Name() string  { return "task_output" }
func (t *TaskOutputTool) Label() string { return "Task Output" }
func (t *TaskOutputTool) Description() string {
	return `Inspect the output of a background task created earlier in the current session.
Provide task_id to fetch one task, or omit it to list all known background tasks.`
}

func (t *TaskOutputTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "Optional background task ID to inspect",
			},
		},
	}
}

func (t *TaskOutputTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.store == nil {
		return taskOutputResult("background task store is not configured"), nil
	}

	taskID, _ := args["task_id"].(string)
	if strings.TrimSpace(taskID) != "" {
		task, ok := t.store.Get(taskID)
		if !ok {
			return taskOutputResult(fmt.Sprintf("task %s not found", taskID)), nil
		}
		text := fmt.Sprintf("Task %s\nkind: %s\nstatus: %s\nsummary: %s", task.ID, task.Kind, task.Status, task.Summary)
		if task.Output != "" {
			text += "\n\noutput:\n" + task.Output
		}
		if task.Error != "" {
			text += "\n\nerror:\n" + task.Error
		}
		return taskOutputResult(text), nil
	}

	tasks := t.store.List()
	if len(tasks) == 0 {
		return taskOutputResult("no background tasks"), nil
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	var lines []string
	for _, task := range tasks {
		lines = append(lines, fmt.Sprintf("%s [%s] %s", task.ID, task.Status, task.Summary))
	}
	return taskOutputResult(strings.Join(lines, "\n")), nil
}

func taskOutputResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
	}
}
