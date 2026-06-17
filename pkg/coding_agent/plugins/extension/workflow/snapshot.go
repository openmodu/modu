package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

type metaInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	WhenToUse   string      `json:"whenToUse,omitempty"`
	Phases      []phaseInfo `json:"phases,omitempty"`
}

type phaseInfo struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Model  string `json:"model,omitempty"`
}

type agentStatus string

const (
	statusQueued  agentStatus = "queued"
	statusRunning agentStatus = "running"
	statusDone    agentStatus = "done"
	statusError   agentStatus = "error"
	statusSkipped agentStatus = "skipped"
)

type agentSnapshot struct {
	ID            int         `json:"id"`
	Label         string      `json:"label"`
	Phase         string      `json:"phase,omitempty"`
	Prompt        string      `json:"prompt"`
	Status        agentStatus `json:"status"`
	ResultPreview string      `json:"resultPreview,omitempty"`
	Error         string      `json:"error,omitempty"`
}

type workflowSnapshot struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	Phases       []string        `json:"phases"`
	CurrentPhase string          `json:"currentPhase,omitempty"`
	Logs         []string        `json:"logs"`
	Agents       []agentSnapshot `json:"agents"`
	AgentCount   int             `json:"agentCount"`
	RunningCount int             `json:"runningCount"`
	DoneCount    int             `json:"doneCount"`
	ErrorCount   int             `json:"errorCount"`
	DurationMs   int64           `json:"durationMs,omitempty"`
	Result       any             `json:"result,omitempty"`
	Meta         *metaInfo       `json:"meta,omitempty"`
}

type snapshotTracker struct {
	mu       sync.Mutex
	started  time.Time
	snapshot workflowSnapshot
	onUpdate types.ToolUpdateCallback
}

func newSnapshotTracker(onUpdate types.ToolUpdateCallback) *snapshotTracker {
	return &snapshotTracker{
		started: time.Now(),
		snapshot: workflowSnapshot{
			Phases: []string{},
			Logs:   []string{},
			Agents: []agentSnapshot{},
		},
		onUpdate: onUpdate,
	}
}

func (s *snapshotTracker) setMeta(meta metaInfo) {
	s.mu.Lock()
	s.snapshot.Name = meta.Name
	s.snapshot.Description = meta.Description
	s.snapshot.Meta = &meta
	s.mu.Unlock()
	s.emit(false)
}

func (s *snapshotTracker) addPhase(title string) {
	s.mu.Lock()
	s.snapshot.CurrentPhase = title
	found := false
	for _, p := range s.snapshot.Phases {
		if p == title {
			found = true
			break
		}
	}
	if !found {
		s.snapshot.Phases = append(s.snapshot.Phases, title)
	}
	s.mu.Unlock()
	s.emit(false)
}

func (s *snapshotTracker) addLog(text string) {
	s.mu.Lock()
	s.snapshot.Logs = append(s.snapshot.Logs, text)
	s.mu.Unlock()
	s.emit(false)
}

func (s *snapshotTracker) startAgent(label, phase, prompt string) int {
	s.mu.Lock()
	id := len(s.snapshot.Agents) + 1
	s.snapshot.Agents = append(s.snapshot.Agents, agentSnapshot{
		ID:     id,
		Label:  label,
		Phase:  phase,
		Prompt: prompt,
		Status: statusRunning,
	})
	s.recomputeLocked(0, nil)
	s.mu.Unlock()
	s.emit(false)
	return id
}

func (s *snapshotTracker) finishAgent(id int, status agentStatus, result any, errText string) {
	s.mu.Lock()
	for i := range s.snapshot.Agents {
		if s.snapshot.Agents[i].ID != id {
			continue
		}
		s.snapshot.Agents[i].Status = status
		s.snapshot.Agents[i].ResultPreview = preview(result, 120)
		s.snapshot.Agents[i].Error = errText
		break
	}
	s.recomputeLocked(0, nil)
	s.mu.Unlock()
	s.emit(false)
}

func (s *snapshotTracker) complete(result any) workflowSnapshot {
	s.mu.Lock()
	duration := time.Since(s.started).Milliseconds()
	s.recomputeLocked(duration, result)
	out := s.snapshot
	s.mu.Unlock()
	s.emitSnapshot(out, true)
	return out
}

func (s *snapshotTracker) skipRunning(reason string) {
	s.mu.Lock()
	for i := range s.snapshot.Agents {
		if s.snapshot.Agents[i].Status == statusRunning || s.snapshot.Agents[i].Status == statusQueued {
			s.snapshot.Agents[i].Status = statusSkipped
			s.snapshot.Agents[i].Error = reason
		}
	}
	s.recomputeLocked(0, nil)
	s.mu.Unlock()
	s.emit(false)
}

func (s *snapshotTracker) recomputeLocked(durationMs int64, result any) {
	running := 0
	done := 0
	errs := 0
	for _, a := range s.snapshot.Agents {
		switch a.Status {
		case statusRunning:
			running++
		case statusDone:
			done++
		case statusError:
			errs++
		}
	}
	s.snapshot.AgentCount = len(s.snapshot.Agents)
	s.snapshot.RunningCount = running
	s.snapshot.DoneCount = done
	s.snapshot.ErrorCount = errs
	if durationMs > 0 {
		s.snapshot.DurationMs = durationMs
	}
	if result != nil {
		s.snapshot.Result = result
	}
}

func (s *snapshotTracker) emit(completed bool) {
	s.mu.Lock()
	snapshot := s.snapshot
	s.mu.Unlock()
	s.emitSnapshot(snapshot, completed)
}

func (s *snapshotTracker) emitSnapshot(snapshot workflowSnapshot, completed bool) {
	if s.onUpdate == nil || snapshot.Name == "" {
		return
	}
	s.onUpdate(types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: renderSnapshot(snapshot, completed)}},
		Details: snapshot,
	})
}

func renderSnapshot(snapshot workflowSnapshot, completed bool) string {
	state := "Workflow running"
	if completed {
		state = "Workflow completed"
	}
	return fmt.Sprintf("%s\nWorkflow: %s (%d/%d done, %d running, %d errors)",
		state, snapshot.Name, snapshot.DoneCount, snapshot.AgentCount, snapshot.RunningCount, snapshot.ErrorCount)
}

func preview(value any, max int) string {
	if value == nil {
		return ""
	}
	var text string
	switch v := value.(type) {
	case string:
		text = v
	default:
		data, err := json.Marshal(v)
		if err != nil {
			text = fmt.Sprint(v)
		} else {
			text = string(data)
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= max {
		return text
	}
	if max <= 1 {
		return text[:max]
	}
	return text[:max-1] + "..."
}
