package coding_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	cancel   map[string]context.CancelFunc
	path     string
	runRoot  string
	onChange func()
}

func newBackgroundTaskManager() *backgroundTaskManager {
	return &backgroundTaskManager{
		tasks:  make(map[string]taskoutput.Task),
		cancel: make(map[string]context.CancelFunc),
	}
}

func (m *backgroundTaskManager) SetStorePath(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.path = path
	if strings.TrimSpace(path) != "" {
		m.runRoot = filepath.Join(filepath.Dir(path), "async-subagent-runs")
	}
	return m.loadLocked()
}

func (m *backgroundTaskManager) SetOnChange(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = fn
}

func (m *backgroundTaskManager) Create(kind, summary string) string {
	return m.CreateWithMetadata(kind, summary, "", "", "", "")
}

func (m *backgroundTaskManager) CreateWithMetadata(kind, summary, agentName, task, parentID, outputFile string) string {
	return m.CreateWithMetadataInDir(kind, summary, agentName, task, parentID, outputFile, "")
}

// CreateWithMetadataInDir is like CreateWithMetadata but lets the caller
// override the per-task run dir parent. When runDirParent is empty the
// manager falls back to its global runRoot, preserving the default layout
// (status.json / session.jsonl land under runRoot/<id>/). When set, the
// task's RunDir becomes runDirParent/<id> and status / session files land
// there. Used by extension callers that want to keep child session files
// adjacent to a caller-controlled directory.
func (m *backgroundTaskManager) CreateWithMetadataInDir(kind, summary, agentName, task, parentID, outputFile, runDirParent string) string {
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("task-%d", m.nextID)
	now := time.Now().UnixMilli()
	taskRecord := taskoutput.Task{
		ID:         id,
		Kind:       kind,
		Status:     "running",
		Summary:    summary,
		Agent:      agentName,
		Task:       task,
		ParentID:   parentID,
		OutputFile: outputFile,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if strings.TrimSpace(runDirParent) != "" {
		taskRecord.RunDir = filepath.Join(runDirParent, id)
	}
	taskRecord = m.withRunPathsLocked(taskRecord)
	m.tasks[id] = taskRecord
	m.persistLocked()
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
	return id
}

func (m *backgroundTaskManager) RegisterCancel(id string, cancel context.CancelFunc) {
	if cancel == nil {
		return
	}
	m.mu.Lock()
	if m.cancel == nil {
		m.cancel = make(map[string]context.CancelFunc)
	}
	m.cancel[id] = cancel
	m.mu.Unlock()
}

func (m *backgroundTaskManager) UnregisterCancel(id string) {
	m.mu.Lock()
	delete(m.cancel, id)
	m.mu.Unlock()
}

func (m *backgroundTaskManager) Complete(id, output string) {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	if task.Status == "interrupted" {
		m.mu.Unlock()
		return
	}
	task.Status = "completed"
	task.Output = output
	task.UpdatedAt = time.Now().UnixMilli()
	m.tasks[id] = task
	m.persistLocked()
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
	if task.Status == "interrupted" {
		m.mu.Unlock()
		return
	}
	task.Status = "failed"
	task.Error = errMsg
	task.UpdatedAt = time.Now().UnixMilli()
	m.tasks[id] = task
	m.persistLocked()
	onChange := m.onChange
	m.mu.Unlock()
	if onChange != nil {
		onChange()
	}
}

func (m *backgroundTaskManager) Interrupt(id, reason string) (taskoutput.Task, bool) {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return taskoutput.Task{}, false
	}
	cancel := m.cancel[id]
	if cancel == nil || task.Status != "running" {
		m.mu.Unlock()
		return task, false
	}
	if strings.TrimSpace(reason) == "" {
		reason = "interrupted"
	}
	task.Status = "interrupted"
	task.Error = reason
	task.UpdatedAt = time.Now().UnixMilli()
	m.tasks[id] = task
	delete(m.cancel, id)
	m.persistLocked()
	onChange := m.onChange
	m.mu.Unlock()
	cancel()
	if onChange != nil {
		onChange()
	}
	return task, true
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

func (m *backgroundTaskManager) loadLocked() error {
	if strings.TrimSpace(m.path) == "" {
		return nil
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.loadRunStatusesLocked()
			return nil
		}
		return err
	}
	var tasks []taskoutput.Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return err
	}
	m.tasks = make(map[string]taskoutput.Task, len(tasks))
	var maxID int64
	for _, task := range tasks {
		if strings.TrimSpace(task.ID) == "" {
			continue
		}
		task = m.withRunPathsLocked(task)
		m.tasks[task.ID] = task
		if n := numericTaskID(task.ID); n > maxID {
			maxID = n
		}
	}
	m.loadRunStatusesLocked()
	if maxID > m.nextID {
		m.nextID = maxID
	}
	return nil
}

func (m *backgroundTaskManager) persistLocked() {
	if strings.TrimSpace(m.path) == "" {
		return
	}
	m.ensureRunPathsLocked()
	tasks := make([]taskoutput.Task, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
		m.persistRunStatusLocked(task)
	}
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, m.path)
}

func (m *backgroundTaskManager) ensureRunPathsLocked() {
	for id, task := range m.tasks {
		m.tasks[id] = m.withRunPathsLocked(task)
	}
}

func (m *backgroundTaskManager) withRunPathsLocked(task taskoutput.Task) taskoutput.Task {
	if strings.TrimSpace(task.ID) == "" || task.Kind != "subagent" {
		return task
	}
	if strings.TrimSpace(task.RunDir) == "" {
		// Default: derive from the manager's runRoot. When neither RunDir
		// nor runRoot is set there's nowhere to place the per-task files;
		// callers (e.g. SessionDir override) can still pre-set RunDir to
		// land the artifacts under a custom parent.
		if strings.TrimSpace(m.runRoot) == "" {
			return task
		}
		task.RunDir = filepath.Join(m.runRoot, task.ID)
	}
	if strings.TrimSpace(task.StatusFile) == "" {
		task.StatusFile = filepath.Join(task.RunDir, "status.json")
	}
	if strings.TrimSpace(task.SessionFile) == "" {
		task.SessionFile = filepath.Join(task.RunDir, "session.jsonl")
	}
	return task
}

func (m *backgroundTaskManager) persistRunStatusLocked(task taskoutput.Task) {
	if strings.TrimSpace(task.StatusFile) == "" {
		return
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(task.StatusFile), 0o755); err != nil {
		return
	}
	tmp := task.StatusFile + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, task.StatusFile)
}

func (m *backgroundTaskManager) loadRunStatusesLocked() {
	if strings.TrimSpace(m.runRoot) == "" {
		return
	}
	entries, err := os.ReadDir(m.runRoot)
	if err != nil {
		return
	}
	if m.tasks == nil {
		m.tasks = make(map[string]taskoutput.Task)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		statusFile := filepath.Join(m.runRoot, entry.Name(), "status.json")
		data, err := os.ReadFile(statusFile)
		if err != nil {
			continue
		}
		var task taskoutput.Task
		if err := json.Unmarshal(data, &task); err != nil || strings.TrimSpace(task.ID) == "" {
			continue
		}
		task = m.withRunPathsLocked(task)
		m.tasks[task.ID] = task
		if n := numericTaskID(task.ID); n > m.nextID {
			m.nextID = n
		}
	}
}

func numericTaskID(id string) int64 {
	raw := strings.TrimPrefix(id, "task-")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// GetBackgroundTasks returns a snapshot of session background tasks.
func (s *CodingSession) GetBackgroundTasks() []BackgroundTask {
	if s.taskManager == nil {
		return nil
	}
	return s.taskManager.List()
}
