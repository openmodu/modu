package coding_agent

import (
	"fmt"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/taskoutput"
)

type BackgroundTask = taskoutput.Task
type BackgroundTaskStore = taskoutput.Store

type backgroundTaskManager struct {
	mu       sync.RWMutex
	nextID   int64
	tasks    map[string]taskoutput.Task
	onChange func()
}

func newBackgroundTaskManager() *backgroundTaskManager {
	return &backgroundTaskManager{
		tasks: make(map[string]taskoutput.Task),
	}
}

func (m *backgroundTaskManager) SetOnChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = fn
}

func (m *backgroundTaskManager) Create(kind, summary string) string {
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("task-%d", m.nextID)
	now := time.Now().UnixMilli()
	m.tasks[id] = taskoutput.Task{
		ID:        id,
		Kind:      kind,
		Status:    "running",
		Summary:   summary,
		CreatedAt: now,
		UpdatedAt: now,
	}
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
	return id
}

func (m *backgroundTaskManager) Complete(id, output string) {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	task.Status = "completed"
	task.Output = output
	task.UpdatedAt = time.Now().UnixMilli()
	m.tasks[id] = task
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

func (m *backgroundTaskManager) Fail(id, errMsg string) {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	task.Status = "failed"
	task.Error = errMsg
	task.UpdatedAt = time.Now().UnixMilli()
	m.tasks[id] = task
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

func (m *backgroundTaskManager) Get(id string) (taskoutput.Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[id]
	return task, ok
}

func (m *backgroundTaskManager) List() []taskoutput.Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]taskoutput.Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		out = append(out, task)
	}
	return out
}

// GetBackgroundTasks returns a snapshot of session background tasks.
func (s *CodingSession) GetBackgroundTasks() []BackgroundTask {
	if s.taskManager == nil {
		return nil
	}
	return s.taskManager.List()
}
