package coding_agent

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
)

type RuntimeStateSnapshot struct {
	UpdatedAt    int64             `json:"updatedAt"`
	SessionID    string            `json:"sessionId"`
	Cwd          string            `json:"cwd"`
	Model        map[string]string `json:"model"`
	Thinking     string            `json:"thinking"`
	Modes        map[string]any    `json:"modes"`
	Features     map[string]bool   `json:"features"`
	Permissions  map[string]any    `json:"permissions"`
	Git          map[string]any    `json:"git"`
	Counts       map[string]int    `json:"counts"`
	Paths        map[string]any    `json:"paths"`
	Todos        []TodoItem        `json:"todos"`
	Tasks        []BackgroundTask  `json:"tasks"`
	HarnessHints int               `json:"harnessHints"`
}

// cachedGitState holds the last-known git state so that writeRuntimeState
// can run without spawning git subprocesses on every call.
type cachedGitState struct {
	mu    sync.RWMutex
	state map[string]any
	cwd   string
}

func (s *CodingSession) refreshGitRuntimeState() {
	state := s.gitRuntimeState()
	s.gitCache.mu.Lock()
	s.gitCache.state = state
	s.gitCache.cwd = s.cwd
	s.gitCache.mu.Unlock()
}

func (s *CodingSession) cachedGitState() map[string]any {
	s.gitCache.mu.RLock()
	st := s.gitCache.state
	cwd := s.gitCache.cwd
	s.gitCache.mu.RUnlock()
	if st == nil || cwd != s.cwd {
		// Cache is empty or stale (cwd changed); refresh synchronously once.
		s.refreshGitRuntimeState()
		s.gitCache.mu.RLock()
		st = s.gitCache.state
		s.gitCache.mu.RUnlock()
	}
	return st
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
	}
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
