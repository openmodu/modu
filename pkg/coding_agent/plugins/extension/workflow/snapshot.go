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
	ID              int                        `json:"id"`
	Label           string                     `json:"label"`
	Phase           string                     `json:"phase,omitempty"`
	Prompt          string                     `json:"prompt"`
	Status          agentStatus                `json:"status"`
	ResultPreview   string                     `json:"resultPreview,omitempty"`
	Error           string                     `json:"error,omitempty"`
	StartedAt       time.Time                  `json:"startedAt,omitempty"`
	EndedAt         time.Time                  `json:"endedAt,omitempty"`
	DurationMs      int64                      `json:"durationMs,omitempty"`
	EstimatedTokens int                        `json:"estimatedTokens,omitempty"`
	TurnTokens      int                        `json:"turnTokens,omitempty"`
	Cost            float64                    `json:"cost,omitempty"`
	FailedToolCalls int                        `json:"failedToolCalls,omitempty"`
	RecentToolCalls []workflowToolCallSnapshot `json:"recentToolCalls,omitempty"`
	Transcript      []workflowTranscriptEntry  `json:"transcript,omitempty"`
	Cached          bool                       `json:"cached,omitempty"`
}

type phaseSummary struct {
	Title           string  `json:"title"`
	AgentCount      int     `json:"agentCount"`
	RunningCount    int     `json:"runningCount"`
	DoneCount       int     `json:"doneCount"`
	ErrorCount      int     `json:"errorCount"`
	EstimatedTokens int     `json:"estimatedTokens,omitempty"`
	Cost            float64 `json:"cost,omitempty"`
	DurationMs      int64   `json:"durationMs,omitempty"`
}

type workflowSnapshot struct {
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	ScriptPath     string          `json:"scriptPath,omitempty"`
	RunDir         string          `json:"runDir,omitempty"`
	Phases         []string        `json:"phases"`
	PhaseSummaries []phaseSummary  `json:"phaseSummaries,omitempty"`
	CurrentPhase   string          `json:"currentPhase,omitempty"`
	Logs           []string        `json:"logs"`
	Agents         []agentSnapshot `json:"agents"`
	AgentCount     int             `json:"agentCount"`
	RunningCount   int             `json:"runningCount"`
	DoneCount      int             `json:"doneCount"`
	ErrorCount     int             `json:"errorCount"`
	Cost           float64         `json:"cost,omitempty"`
	DurationMs     int64           `json:"durationMs,omitempty"`
	Result         any             `json:"result,omitempty"`
	Meta           *metaInfo       `json:"meta,omitempty"`
}

// clone returns a deep copy whose slices share no backing array with s. The
// tracker emits/returns snapshots to consumers (onUpdate → registry → TUI,
// json.Marshal) that read them WITHOUT the tracker lock, while parallel agent
// goroutines keep mutating the live snapshot's Agents/Logs in place — so every
// snapshot that leaves the lock must be cloned or it is a data race.
func (s workflowSnapshot) clone() workflowSnapshot {
	out := s
	out.Phases = cloneStringSlice(s.Phases)
	out.Logs = cloneStringSlice(s.Logs)
	if s.PhaseSummaries != nil {
		out.PhaseSummaries = append([]phaseSummary(nil), s.PhaseSummaries...)
	}
	if s.Agents != nil {
		agents := make([]agentSnapshot, len(s.Agents))
		for i, a := range s.Agents {
			if a.RecentToolCalls != nil {
				a.RecentToolCalls = append([]workflowToolCallSnapshot(nil), a.RecentToolCalls...)
			}
			if a.Transcript != nil {
				a.Transcript = append([]workflowTranscriptEntry(nil), a.Transcript...)
			}
			agents[i] = a
		}
		out.Agents = agents
	}
	if s.Meta != nil {
		m := *s.Meta
		if s.Meta.Phases != nil {
			m.Phases = append([]phaseInfo(nil), s.Meta.Phases...)
		}
		out.Meta = &m
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
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

func (s *snapshotTracker) setScript(path, runDir string) {
	if path == "" && runDir == "" {
		return
	}
	s.mu.Lock()
	s.snapshot.ScriptPath = path
	s.snapshot.RunDir = runDir
	s.mu.Unlock()
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
		ID:        id,
		Label:     label,
		Phase:     phase,
		Prompt:    prompt,
		Status:    statusRunning,
		StartedAt: time.Now(),
	})
	s.recomputeLocked(0, nil)
	s.mu.Unlock()
	s.emit(false)
	return id
}

func (s *snapshotTracker) finishAgent(id int, status agentStatus, result any, errText string, estimatedTokens ...int) {
	s.mu.Lock()
	now := time.Now()
	for i := range s.snapshot.Agents {
		if s.snapshot.Agents[i].ID != id {
			continue
		}
		s.snapshot.Agents[i].Status = status
		s.snapshot.Agents[i].ResultPreview = preview(result, 120)
		s.snapshot.Agents[i].Error = errText
		s.snapshot.Agents[i].EndedAt = now
		if !s.snapshot.Agents[i].StartedAt.IsZero() {
			s.snapshot.Agents[i].DurationMs = now.Sub(s.snapshot.Agents[i].StartedAt).Milliseconds()
		}
		if len(estimatedTokens) > 0 && estimatedTokens[0] > 0 {
			s.snapshot.Agents[i].EstimatedTokens = estimatedTokens[0]
		}
		break
	}
	s.recomputeLocked(0, nil)
	s.mu.Unlock()
	s.emit(false)
}

func (s *snapshotTracker) cachedAgent(entry workflowAgentCacheEntry) {
	s.mu.Lock()
	id := len(s.snapshot.Agents) + 1
	startedAt := entry.StartedAt
	endedAt := entry.EndedAt
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	if endedAt.IsZero() {
		endedAt = startedAt
	}
	durationMs := entry.DurationMs
	if durationMs == 0 && !endedAt.Before(startedAt) {
		durationMs = endedAt.Sub(startedAt).Milliseconds()
	}
	s.snapshot.Agents = append(s.snapshot.Agents, agentSnapshot{
		ID:              id,
		Label:           entry.Label,
		Phase:           entry.Phase,
		Prompt:          entry.Prompt,
		Status:          statusDone,
		ResultPreview:   preview(entry.Value, 120),
		StartedAt:       startedAt,
		EndedAt:         endedAt,
		DurationMs:      durationMs,
		EstimatedTokens: entry.Spent,
		Cached:          true,
	})
	s.recomputeLocked(0, nil)
	s.mu.Unlock()
	s.emit(false)
}

func (s *snapshotTracker) complete(result any) workflowSnapshot {
	s.mu.Lock()
	duration := time.Since(s.started).Milliseconds()
	s.recomputeLocked(duration, result)
	out := s.snapshot.clone()
	s.mu.Unlock()
	s.emitSnapshot(out, true)
	return out
}

func (s *snapshotTracker) current() workflowSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recomputeLocked(time.Since(s.started).Milliseconds(), nil)
	return s.snapshot.clone()
}

func (s *snapshotTracker) updateAgentActivity(agentID int, activity workflowAgentActivity) {
	s.mu.Lock()
	for i := range s.snapshot.Agents {
		if s.snapshot.Agents[i].ID != agentID {
			continue
		}
		applyAgentActivity(&s.snapshot.Agents[i], activity)
		break
	}
	s.recomputeLocked(0, nil)
	s.mu.Unlock()
	s.emit(false)
}

func (s *snapshotTracker) skipRunning(reason string) {
	s.mu.Lock()
	now := time.Now()
	for i := range s.snapshot.Agents {
		if s.snapshot.Agents[i].Status == statusRunning || s.snapshot.Agents[i].Status == statusQueued {
			s.snapshot.Agents[i].Status = statusSkipped
			s.snapshot.Agents[i].Error = reason
			s.snapshot.Agents[i].EndedAt = now
			if !s.snapshot.Agents[i].StartedAt.IsZero() {
				s.snapshot.Agents[i].DurationMs = now.Sub(s.snapshot.Agents[i].StartedAt).Milliseconds()
			}
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
	var cost float64
	now := time.Now()
	for i := range s.snapshot.Agents {
		agent := &s.snapshot.Agents[i]
		if agent.Status == statusRunning && !agent.StartedAt.IsZero() {
			agent.DurationMs = now.Sub(agent.StartedAt).Milliseconds()
		}
	}
	for _, a := range s.snapshot.Agents {
		cost += a.Cost
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
	s.snapshot.Cost = cost
	s.snapshot.PhaseSummaries = computePhaseSummaries(s.snapshot.Phases, s.snapshot.Agents, now)
	if durationMs > 0 {
		s.snapshot.DurationMs = durationMs
	}
	if result != nil {
		s.snapshot.Result = result
	}
}

func computePhaseSummaries(phases []string, agents []agentSnapshot, now time.Time) []phaseSummary {
	type phaseAccumulator struct {
		summary phaseSummary
		started time.Time
		ended   time.Time
	}
	order := make([]string, 0, len(phases))
	byPhase := map[string]*phaseAccumulator{}
	ensure := func(title string) *phaseAccumulator {
		acc := byPhase[title]
		if acc != nil {
			return acc
		}
		acc = &phaseAccumulator{summary: phaseSummary{Title: title}}
		byPhase[title] = acc
		order = append(order, title)
		return acc
	}
	for _, phase := range phases {
		ensure(phase)
	}
	for _, agent := range agents {
		acc := ensure(agent.Phase)
		acc.summary.AgentCount++
		if agent.TurnTokens > 0 {
			acc.summary.EstimatedTokens += agent.TurnTokens
		} else {
			acc.summary.EstimatedTokens += agent.EstimatedTokens
		}
		acc.summary.Cost += agent.Cost
		switch agent.Status {
		case statusRunning:
			acc.summary.RunningCount++
		case statusDone:
			acc.summary.DoneCount++
		case statusError:
			acc.summary.ErrorCount++
		}
		if !agent.StartedAt.IsZero() && (acc.started.IsZero() || agent.StartedAt.Before(acc.started)) {
			acc.started = agent.StartedAt
		}
		ended := agent.EndedAt
		if agent.Status == statusRunning || ended.IsZero() {
			ended = now
		}
		if !ended.IsZero() && ended.After(acc.ended) {
			acc.ended = ended
		}
	}
	out := make([]phaseSummary, 0, len(order))
	for _, phase := range order {
		acc := byPhase[phase]
		if !acc.started.IsZero() && !acc.ended.Before(acc.started) {
			acc.summary.DurationMs = acc.ended.Sub(acc.started).Milliseconds()
		}
		out = append(out, acc.summary)
	}
	return out
}

func applyAgentActivity(agent *agentSnapshot, activity workflowAgentActivity) {
	if agent == nil {
		return
	}
	if activity.UsageTokens > 0 {
		if agent.TurnTokens < activity.UsageTokens {
			agent.TurnTokens = activity.UsageTokens
		}
	} else if activity.TurnTokens > 0 {
		agent.TurnTokens += activity.TurnTokens
	}
	if activity.UsageCost > 0 {
		if agent.Cost < activity.UsageCost {
			agent.Cost = activity.UsageCost
		}
	} else if activity.TurnCost > 0 {
		agent.Cost += activity.TurnCost
	}
	if activity.FailedToolCalls > 0 {
		agent.FailedToolCalls += activity.FailedToolCalls
	}
	if len(activity.RecentToolCalls) > 0 {
		agent.RecentToolCalls = append(agent.RecentToolCalls, activity.RecentToolCalls...)
		if len(agent.RecentToolCalls) > maxWorkflowRecentToolCalls {
			agent.RecentToolCalls = append([]workflowToolCallSnapshot(nil), agent.RecentToolCalls[len(agent.RecentToolCalls)-maxWorkflowRecentToolCalls:]...)
		}
	}
	if len(activity.Transcript) > 0 {
		agent.Transcript = append([]workflowTranscriptEntry(nil), activity.Transcript...)
	}
}

func (s *snapshotTracker) emit(completed bool) {
	s.mu.Lock()
	snapshot := s.snapshot.clone()
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
	if max <= 0 {
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
