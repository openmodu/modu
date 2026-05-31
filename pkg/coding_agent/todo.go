package coding_agent

import (
	"github.com/openmodu/modu/pkg/coding_agent/services/todo"
	"github.com/openmodu/modu/pkg/coding_agent/tools/planning"
	"github.com/openmodu/modu/pkg/types"
)

// TodoItem aliases the todo service's item type so existing callers (and the
// runtime-state snapshot) keep working unchanged.
type TodoItem = todo.Item

// todoStoreAdapter bridges the session todo store to the planning todo tool,
// converting between the two TodoItem shapes.
type todoStoreAdapter struct {
	session *engine
}

func (a todoStoreAdapter) GetTodos() []planning.TodoItem {
	if a.session == nil {
		return nil
	}
	items := a.session.GetTodos()
	out := make([]planning.TodoItem, len(items))
	for i, item := range items {
		out[i] = planning.TodoItem{Content: item.Content, Status: item.Status}
	}
	return out
}

func (a todoStoreAdapter) SetTodos(items []planning.TodoItem) {
	if a.session == nil {
		return
	}
	out := make([]TodoItem, len(items))
	for i, item := range items {
		out[i] = TodoItem{Content: item.Content, Status: item.Status}
	}
	a.session.SetTodos(out)
}

func (s *engine) replaceTodoTool() {
	if !s.config.FeatureTodoTool() {
		s.activeTools = removeToolByName(s.activeTools, "todo_write")
		s.agent.SetTools(removeToolByName(s.agent.GetState().Tools, "todo_write"))
		return
	}
	todoTool := planning.NewTodoWriteTool(todoStoreAdapter{session: s})
	s.activeTools = replaceTool(s.activeTools, todoTool)
	s.agent.SetTools(replaceTool(s.agent.GetState().Tools, todoTool))
}

func replaceTool(list []types.Tool, replacement types.Tool) []types.Tool {
	out := make([]types.Tool, 0, len(list))
	replaced := false
	for _, tool := range list {
		if tool.Name() == replacement.Name() {
			if !replaced {
				out = append(out, replacement)
				replaced = true
			}
			continue
		}
		out = append(out, tool)
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}

// GetTodos returns the current session todo list.
func (s *engine) GetTodos() []TodoItem { return s.todos.Get() }

// SetTodos replaces the current session todo list.
func (s *engine) SetTodos(items []TodoItem) { s.todos.Set(items) }
