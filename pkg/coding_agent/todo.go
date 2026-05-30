package coding_agent

import (
	"sync"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/tools/planning"
)

// TodoItem represents one task tracked during a coding session.
type TodoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

type todoStoreAdapter struct {
	session *CodingSession
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

func (s *CodingSession) replaceTodoTool() {
	if !s.config.FeatureTodoTool() {
		s.activeTools = removeToolByName(s.activeTools, "todo_write")
		s.agent.SetTools(removeToolByName(s.agent.GetState().Tools, "todo_write"))
		return
	}
	todoTool := planning.NewTodoWriteTool(todoStoreAdapter{session: s})
	s.activeTools = replaceTool(s.activeTools, todoTool)
	s.agent.SetTools(replaceTool(s.agent.GetState().Tools, todoTool))
}

func replaceTool(list []agent.Tool, replacement agent.Tool) []agent.Tool {
	out := make([]agent.Tool, 0, len(list))
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

// todoStore owns the session todo list. It is self-contained: state changes
// notify the host through onChange rather than reaching back into the session.
type todoStore struct {
	mu       sync.RWMutex
	items    []TodoItem
	onChange func()
}

func newTodoStore() *todoStore { return &todoStore{} }

func (t *todoStore) Get() []TodoItem {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]TodoItem, len(t.items))
	copy(out, t.items)
	return out
}

func (t *todoStore) Set(items []TodoItem) {
	t.mu.Lock()
	t.items = make([]TodoItem, len(items))
	copy(t.items, items)
	t.mu.Unlock()
	if t.onChange != nil {
		t.onChange()
	}
}

// GetTodos returns the current session todo list.
func (s *CodingSession) GetTodos() []TodoItem { return s.todos.Get() }

// SetTodos replaces the current session todo list.
func (s *CodingSession) SetTodos(items []TodoItem) { s.todos.Set(items) }
