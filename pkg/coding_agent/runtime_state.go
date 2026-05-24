package coding_agent

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	sessiontrace "github.com/openmodu/modu/pkg/trace"
)

type RuntimeStateSnapshot struct {
	UpdatedAt    int64                `json:"updatedAt"`
	SessionID    string               `json:"sessionId"`
	Cwd          string               `json:"cwd"`
	Model        map[string]string    `json:"model"`
	Thinking     string               `json:"thinking"`
	Modes        map[string]any       `json:"modes"`
	Extensions   map[string]any       `json:"extensions"`
	Features     map[string]bool      `json:"features"`
	Permissions  map[string]any       `json:"permissions"`
	Git          map[string]any       `json:"git"`
	Counts       map[string]int       `json:"counts"`
	Paths        map[string]any       `json:"paths"`
	Todos        []TodoItem           `json:"todos"`
	Tasks        []BackgroundTask     `json:"tasks"`
	HarnessHints int                  `json:"harnessHints"`
	Trace        sessiontrace.Summary `json:"trace"`
}

// cachedGitState holds the last-known git state so that writeRuntimeState
// can run without spawning git subprocesses on every call.
type cachedGitState struct {
	mu            sync.RWMutex
	state         map[string]any
	cwd           string
	refreshing    bool
	refreshingCwd string
}

func (s *CodingSession) refreshGitRuntimeState() {
	cwd := s.cwd
	state := s.gitRuntimeStateForCwd(cwd)
	s.gitCache.mu.Lock()
	s.gitCache.state = state
	s.gitCache.cwd = cwd
	s.gitCache.refreshing = false
	s.gitCache.refreshingCwd = ""
	s.gitCache.mu.Unlock()
}

func (s *CodingSession) cachedGitState() map[string]any {
	cwd := s.cwd
	s.gitCache.mu.RLock()
	st := s.gitCache.state
	cachedCwd := s.gitCache.cwd
	s.gitCache.mu.RUnlock()
	if st != nil && cachedCwd == cwd {
		return st
	}
	return map[string]any{
		"available":  false,
		"refreshing": false,
	}
}

// RefreshRuntimeStateAsync refreshes expensive runtime state in the background.
// Callers use this after the UI is visible so startup is not blocked by git
// subprocesses on large repositories.
func (s *CodingSession) RefreshRuntimeStateAsync() {
	if s == nil {
		return
	}
	s.scheduleGitRuntimeStateRefresh(s.cwd)
}

func (s *CodingSession) scheduleGitRuntimeStateRefresh(cwd string) {
	s.gitCache.mu.Lock()
	if s.gitCache.refreshing && s.gitCache.refreshingCwd == cwd {
		s.gitCache.mu.Unlock()
		return
	}
	s.gitCache.refreshing = true
	s.gitCache.refreshingCwd = cwd
	s.gitCache.mu.Unlock()

	go func() {
		state := s.gitRuntimeStateForCwd(cwd)
		s.gitCache.mu.Lock()
		s.gitCache.state = state
		s.gitCache.cwd = cwd
		s.gitCache.refreshing = false
		s.gitCache.refreshingCwd = ""
		s.gitCache.mu.Unlock()
		s.writeRuntimeState()
	}()
}

func (s *CodingSession) RuntimeState() RuntimeStateSnapshot {
	model := map[string]string{}
	if s.model != nil {
		model["id"] = s.model.ID
		model["provider"] = s.model.ProviderID
	}
	hintCount := 0
	if s.harness != nil {
		s.harness.mu.RLock()
		hintCount = len(s.harness.pendingHints)
		s.harness.mu.RUnlock()
	}
	todos := s.GetTodos()
	tasks := s.GetBackgroundTasks()
	return RuntimeStateSnapshot{
		UpdatedAt: time.Now().UnixMilli(),
		SessionID: s.GetSessionID(),
		Cwd:       s.cwd,
		Model:     model,
		Thinking:  string(s.GetThinkingLevel()),
		Modes: map[string]any{
			"plan":      s.IsPlanMode(),
			"worktree":  s.ActiveWorktree(),
			"streaming": s.IsStreaming(),
		},
		Extensions: s.ExtensionRuntimeStates(),
		Features: map[string]bool{
			"memory_tool":         s.config.FeatureMemoryTool(),
			"todo_tool":           s.config.FeatureTodoTool(),
			"task_output_tool":    s.config.FeatureTaskOutputTool(),
			"plan_mode":           s.config.FeaturePlanMode(),
			"worktree_mode":       s.config.FeatureWorktreeMode(),
			"spawn_subagent_tool": s.config.FeatureSpawnSubagentTool(),
			"harness_actions":     s.config.FeatureHarnessActions() && s.config.HarnessEnableActions(),
		},
		Counts: map[string]int{
			"messages": len(s.GetMessages()),
			"todos":    len(todos),
			"tasks":    len(tasks),
			"tools":    len(s.GetActiveToolNames()),
		},
		Permissions: map[string]any{
			"allow_tools":         append([]string(nil), s.config.Permissions.AllowTools...),
			"deny_tools":          append([]string(nil), s.config.Permissions.DenyTools...),
			"allow_bash_prefixes": append([]string(nil), s.config.Permissions.AllowBashPrefixes...),
			"deny_bash_prefixes":  append([]string(nil), s.config.Permissions.DenyBashPrefixes...),
		},
		Git:          s.cachedGitState(),
		Paths:        s.RuntimePaths().ToMap(),
		Todos:        todos,
		Tasks:        tasks,
		HarnessHints: hintCount,
		Trace:        s.TraceSummary(),
	}
}

// ExtensionRuntimeStates returns lightweight state exposed by loaded extensions.
func (s *CodingSession) ExtensionRuntimeStates() map[string]any {
	if s == nil || s.extensions == nil {
		return map[string]any{}
	}
	states := s.extensions.RuntimeStates()
	if states == nil {
		return map[string]any{}
	}
	return states
}

func (s *CodingSession) RuntimeStateJSON() string {
	data, err := json.MarshalIndent(s.RuntimeState(), "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(data) + "\n"
}

func (s *CodingSession) writeRuntimeState() {
	paths := s.RuntimePaths()
	if err := os.MkdirAll(paths.RuntimeDir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(s.RuntimeState(), "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(paths.RuntimeStateFile, append(data, '\n'), 0o600)
}

func removeAgentToolByName(list []agent.AgentTool, name string) []agent.AgentTool {
	out := make([]agent.AgentTool, 0, len(list))
	for _, tool := range list {
		if tool.Name() == name {
			continue
		}
		out = append(out, tool)
	}
	return out
}
