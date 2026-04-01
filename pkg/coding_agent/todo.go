package coding_agent

import (
	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/tools"
)

// TodoItem represents one task tracked during a coding session.
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type todoStoreAdapter struct {
	session *CodingSession
}

func (a todoStoreAdapter) GetTodos() []tools.TodoItem {
	if a.session == nil {
		return nil
	}
	items := a.session.GetTodos()
	out := make([]tools.TodoItem, len(items))
	for i, item := range items {
		out[i] = tools.TodoItem{Content: item.Content, Status: item.Status}
	}
	return out
}

func (a todoStoreAdapter) SetTodos(items []tools.TodoItem) {
	if a.session == nil {
		return
	}
	out := make([]TodoItem, len(items))
	for i, item := range items {
		out[i] = TodoItem{Content: item.Content, Status: item.Status}
	}
	a.session.SetTodos(out)
}

func (s *CodingSession) replaceTodoTool() {
	todoTool := tools.NewTodoWriteTool(todoStoreAdapter{session: s})
	s.activeTools = replaceAgentTool(s.activeTools, todoTool)
	s.agent.SetTools(replaceAgentTool(s.agent.GetState().Tools, todoTool))
}

func replaceAgentTool(list []agent.AgentTool, replacement agent.AgentTool) []agent.AgentTool {
	out := make([]agent.AgentTool, 0, len(list))
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
func (s *CodingSession) GetTodos() []TodoItem {
	s.todoMu.RLock()
	defer s.todoMu.RUnlock()
	out := make([]TodoItem, len(s.todos))
	copy(out, s.todos)
	return out
}

// SetTodos replaces the current session todo list.
func (s *CodingSession) SetTodos(items []TodoItem) {
	s.todoMu.Lock()
	defer s.todoMu.Unlock()
	s.todos = make([]TodoItem, len(items))
	copy(s.todos, items)
}
