package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type workflowRunStatus string

const (
	workflowStatusRunning   workflowRunStatus = "running"
	workflowStatusCompleted workflowRunStatus = "completed"
	workflowStatusFailed    workflowRunStatus = "failed"
	workflowStatusStopped   workflowRunStatus = "stopped"
)

type liveWorkflowRun struct {
	ID         string
	ScriptPath string
	RunDir     string
	Status     workflowRunStatus
	Snapshot   *workflowSnapshot
	Error      string
	StartedAt  time.Time
	UpdatedAt  time.Time
	Exec       workflowExecution
	cancel     context.CancelFunc
}

type workflowAgentControlAction string

const (
	workflowAgentActionStop    workflowAgentControlAction = "stop"
	workflowAgentActionRestart workflowAgentControlAction = "restart"
)

type liveWorkflowAgentControl struct {
	RunID   string
	AgentID int
	cancel  context.CancelFunc
	action  workflowAgentControlAction
}

type workflowRunStatusFile struct {
	ID         string            `json:"id"`
	Status     workflowRunStatus `json:"status"`
	ScriptPath string            `json:"scriptPath,omitempty"`
	RunDir     string            `json:"runDir,omitempty"`
	Error      string            `json:"error,omitempty"`
	StartedAt  time.Time         `json:"startedAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

type workflowRegistry struct {
	mu     sync.Mutex
	runs   map[string]*liveWorkflowRun
	agents map[string]*liveWorkflowAgentControl
}

func newWorkflowRegistry() *workflowRegistry {
	return &workflowRegistry{runs: map[string]*liveWorkflowRun{}, agents: map[string]*liveWorkflowAgentControl{}}
}

func (r *workflowRegistry) start(id, scriptPath, runDir string, cancel context.CancelFunc, exec workflowExecution) {
	if r == nil || strings.TrimSpace(id) == "" {
		return
	}
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = map[string]*liveWorkflowRun{}
	}
	if r.agents == nil {
		r.agents = map[string]*liveWorkflowAgentControl{}
	}
	r.runs[id] = &liveWorkflowRun{
		ID:         id,
		ScriptPath: scriptPath,
		RunDir:     runDir,
		Status:     workflowStatusRunning,
		StartedAt:  now,
		UpdatedAt:  now,
		Exec:       exec,
		cancel:     cancel,
	}
}

func persistWorkflowRunStatus(runDir string, status workflowRunStatus, errText string) error {
	runDir = strings.TrimSpace(runDir)
	if runDir == "" {
		return nil
	}
	id := workflowRunID(runDir)
	now := time.Now()
	current, _, _ := readWorkflowRunStatus(filepath.Join(runDir, "status.json"))
	startedAt := now
	if current != nil && !current.StartedAt.IsZero() {
		startedAt = current.StartedAt
	}
	if current != nil && current.Status == workflowStatusStopped && status != workflowStatusRunning {
		status = workflowStatusStopped
		if strings.TrimSpace(current.Error) != "" {
			errText = current.Error
		}
	}
	data, err := json.MarshalIndent(workflowRunStatusFile{
		ID:         id,
		Status:     status,
		ScriptPath: filepath.Join(runDir, "script.lua"),
		RunDir:     runDir,
		Error:      errText,
		StartedAt:  startedAt,
		UpdatedAt:  now,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("persist workflow run status: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "status.json"), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("persist workflow run status: %w", err)
	}
	return nil
}

func readWorkflowRunStatus(path string) (*workflowRunStatusFile, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return nil, time.Time{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, info.ModTime(), err
	}
	var status workflowRunStatusFile
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, info.ModTime(), err
	}
	return &status, info.ModTime(), nil
}

func (r *workflowRegistry) update(id string, snapshot workflowSnapshot) {
	if r == nil || strings.TrimSpace(id) == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil {
		return
	}
	s := snapshot
	run.Snapshot = &s
	if run.ScriptPath == "" {
		run.ScriptPath = snapshot.ScriptPath
	}
	if run.RunDir == "" {
		run.RunDir = snapshot.RunDir
	}
	run.UpdatedAt = time.Now()
}

func (r *workflowRegistry) updateAgentActivity(id string, agentID int, activity workflowAgentActivity) {
	if r == nil || strings.TrimSpace(id) == "" || agentID <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil || run.Snapshot == nil {
		return
	}
	for i := range run.Snapshot.Agents {
		if run.Snapshot.Agents[i].ID != agentID {
			continue
		}
		applyAgentActivity(&run.Snapshot.Agents[i], activity)
		run.UpdatedAt = time.Now()
		return
	}
}

func (r *workflowRegistry) finish(id string, status workflowRunStatus, snapshot workflowSnapshot, errText string) (workflowRunStatus, string) {
	if r == nil || strings.TrimSpace(id) == "" {
		return status, errText
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil {
		return status, errText
	}
	if snapshot.Name != "" || snapshot.AgentCount > 0 || snapshot.ScriptPath != "" {
		s := snapshot
		run.Snapshot = &s
	}
	if run.Status == workflowStatusStopped {
		status = workflowStatusStopped
		if strings.TrimSpace(run.Error) != "" {
			errText = run.Error
		}
	}
	run.Status = status
	run.Error = errText
	run.UpdatedAt = time.Now()
	run.cancel = nil
	return status, errText
}

func (r *workflowRegistry) stop(id, reason string) bool {
	if r == nil || strings.TrimSpace(id) == "" {
		return false
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "stop requested"
	}
	r.mu.Lock()
	run := r.runs[id]
	if run == nil || run.Status != workflowStatusRunning || run.cancel == nil {
		r.mu.Unlock()
		return false
	}
	cancel := run.cancel
	run.Status = workflowStatusStopped
	run.Error = reason
	run.UpdatedAt = time.Now()
	r.mu.Unlock()
	cancel()
	return true
}

func (r *workflowRegistry) registerAgentControl(runID string, agentID int, cancel context.CancelFunc) func() workflowAgentControlAction {
	if r == nil || strings.TrimSpace(runID) == "" || agentID <= 0 || cancel == nil {
		return func() workflowAgentControlAction { return "" }
	}
	key := workflowAgentControlKey(runID, agentID)
	r.mu.Lock()
	if r.agents == nil {
		r.agents = map[string]*liveWorkflowAgentControl{}
	}
	r.agents[key] = &liveWorkflowAgentControl{
		RunID:   runID,
		AgentID: agentID,
		cancel:  cancel,
	}
	r.mu.Unlock()
	return func() workflowAgentControlAction {
		r.mu.Lock()
		defer r.mu.Unlock()
		control := r.agents[key]
		delete(r.agents, key)
		if control == nil {
			return ""
		}
		return control.action
	}
}

func (r *workflowRegistry) requestAgentAction(runID string, agentID int, action workflowAgentControlAction) bool {
	if r == nil || strings.TrimSpace(runID) == "" || agentID <= 0 || action == "" {
		return false
	}
	key := workflowAgentControlKey(runID, agentID)
	r.mu.Lock()
	control := r.agents[key]
	if control == nil || control.cancel == nil {
		r.mu.Unlock()
		return false
	}
	control.action = action
	cancel := control.cancel
	r.mu.Unlock()
	cancel()
	return true
}

func workflowAgentControlKey(runID string, agentID int) string {
	return fmt.Sprintf("%s:%d", strings.TrimSpace(runID), agentID)
}

func (r *workflowRegistry) resume(id string, cancel context.CancelFunc) (workflowExecution, bool, string) {
	if r == nil || strings.TrimSpace(id) == "" {
		return workflowExecution{}, false, "Workflow run is not available for resume: " + id
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	run := r.runs[id]
	if run == nil {
		return workflowExecution{}, false, "Workflow run is not available for resume in this session: " + id
	}
	if run.Status == workflowStatusRunning {
		return workflowExecution{}, false, "Workflow run is already running: " + id
	}
	if run.Status == workflowStatusCompleted {
		return workflowExecution{}, false, "Workflow run is already completed: " + id
	}
	if strings.TrimSpace(run.Exec.Script) == "" || run.Exec.AgentCache == nil {
		return workflowExecution{}, false, "Workflow run cannot be resumed after process recreation; restart it instead: " + id
	}
	run.Status = workflowStatusRunning
	run.Error = ""
	run.UpdatedAt = time.Now()
	run.cancel = cancel
	exec := run.Exec
	exec.Resume = true
	exec.ScriptPath = run.ScriptPath
	exec.RunDir = run.RunDir
	run.Exec = exec
	return exec, true, ""
}

func (r *workflowRegistry) list() []liveWorkflowRun {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]liveWorkflowRun, 0, len(r.runs))
	for _, run := range r.runs {
		cp := *run
		if run.Snapshot != nil {
			s := *run.Snapshot
			cp.Snapshot = &s
		}
		out = append(out, cp)
	}
	return out
}
