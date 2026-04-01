package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// TodoItem represents a session task entry.
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// TodoStore provides session-scoped todo persistence.
type TodoStore interface {
	GetTodos() []TodoItem
	SetTodos([]TodoItem)
}

// TodoWriteTool replaces the current session todo list.
type TodoWriteTool struct {
	store TodoStore
}

func NewTodoWriteTool(store TodoStore) *TodoWriteTool {
	return &TodoWriteTool{store: store}
}

func (t *TodoWriteTool) Name() string  { return "todo_write" }
func (t *TodoWriteTool) Label() string { return "Todo Write" }
func (t *TodoWriteTool) Description() string {
	return `Replace the current session todo list with an updated set of tasks.
Use this to track progress on multi-step work. Each todo item must have:
- content: the task description
- status: one of pending, in_progress, completed
Keep exactly one item in_progress when actively working.`
}

func (t *TodoWriteTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"todos": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content": map[string]any{"type": "string"},
						"status": map[string]any{
							"type": "string",
							"enum": []string{"pending", "in_progress", "completed"},
						},
					},
					"required": []string{"content", "status"},
				},
			},
		},
		"required": []string{"todos"},
	}
}

func (t *TodoWriteTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.store == nil {
		return todoResult("todo store is not configured", true), nil
	}

	rawTodos, ok := args["todos"].([]any)
	if !ok {
		if typed, ok2 := args["todos"].([]map[string]any); ok2 {
			rawTodos = make([]any, len(typed))
			for i := range typed {
				rawTodos[i] = typed[i]
			}
		} else {
			return todoResult("todos must be an array", true), nil
		}
	}

	todos := make([]TodoItem, 0, len(rawTodos))
	inProgress := 0
	for _, raw := range rawTodos {
		itemMap, ok := raw.(map[string]any)
		if !ok {
			return todoResult("each todo must be an object", true), nil
		}
		content, _ := itemMap["content"].(string)
		status, _ := itemMap["status"].(string)
		content = strings.TrimSpace(content)
		status = strings.TrimSpace(status)
		if content == "" {
			return todoResult("todo content cannot be empty", true), nil
		}
		switch status {
		case "pending", "in_progress", "completed":
		default:
			return todoResult(fmt.Sprintf("invalid todo status: %s", status), true), nil
		}
		if status == "in_progress" {
			inProgress++
		}
		todos = append(todos, TodoItem{Content: content, Status: status})
	}
	if inProgress > 1 {
		return todoResult("at most one todo may be in_progress", true), nil
	}

	t.store.SetTodos(todos)
	return todoResult(fmt.Sprintf("updated todo list with %d item(s)", len(todos)), false), nil
}

func todoResult(text string, isError bool) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
		Details: map[string]any{
			"isError": isError,
		},
	}
}
