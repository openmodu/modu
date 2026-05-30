package coding_agent

import (
	"os"
	"path/filepath"
	"strings"
)

// Runtime paths derive the on-disk layout for a session under the agent dir.

type HarnessRuntimePaths struct {
	Root                 string `json:"root"`
	RuntimeDir           string `json:"runtimeDir"`
	RuntimeIndexFile     string `json:"runtimeIndexFile"`
	RuntimeStateFile     string `json:"runtimeStateFile"`
	BackgroundTasksFile  string `json:"backgroundTasksFile"`
	AsyncSubagentRunsDir string `json:"asyncSubagentRunsDir"`
	SessionsDir          string `json:"sessionsDir"`
	PlansDir             string `json:"plansDir"`
	PlanFile             string `json:"planFile"`
	WorktreesDir         string `json:"worktreesDir"`
	ToolResultsDir       string `json:"toolResultsDir"`
	GlobalMemoryDir      string `json:"globalMemoryDir"`
	ProjectMemoryDir     string `json:"projectMemoryDir"`
}

func (p HarnessRuntimePaths) ToMap() map[string]any {
	return map[string]any{
		"root":                    p.Root,
		"runtime_dir":             p.RuntimeDir,
		"runtime_index_file":      p.RuntimeIndexFile,
		"runtime_state_file":      p.RuntimeStateFile,
		"background_tasks_file":   p.BackgroundTasksFile,
		"async_subagent_runs_dir": p.AsyncSubagentRunsDir,
		"sessions_dir":            p.SessionsDir,
		"plans_dir":               p.PlansDir,
		"plan_file":               p.PlanFile,
		"worktrees_dir":           p.WorktreesDir,
		"tool_results_dir":        p.ToolResultsDir,
		"global_memory_dir":       p.GlobalMemoryDir,
		"project_memory_dir":      p.ProjectMemoryDir,
	}
}

func (s *engine) RuntimePaths() HarnessRuntimePaths {
	projectKey := strings.ReplaceAll(strings.TrimPrefix(s.cwd, "/"), "/", "_")
	if projectKey == "" {
		projectKey = "root"
	}
	plansDir := filepath.Join(s.agentDir, "plans", projectKey)
	toolResultsDir := filepath.Join(s.agentDir, "tool-results", projectKey)
	runtimeDir := filepath.Join(s.agentDir, "runtime", projectKey)
	asyncSubagentRunsDir := filepath.Join(runtimeDir, "async-subagent-runs")
	_ = os.MkdirAll(plansDir, 0o755)
	_ = os.MkdirAll(toolResultsDir, 0o755)
	_ = os.MkdirAll(runtimeDir, 0o755)
	_ = os.MkdirAll(asyncSubagentRunsDir, 0o755)

	sessionsDir := filepath.Dir(s.messagesFilePath())
	if s.sessionManager != nil {
		sessionsDir = s.sessionManager.Dir()
	}
	return HarnessRuntimePaths{
		Root:                 s.agentDir,
		RuntimeDir:           runtimeDir,
		RuntimeIndexFile:     filepath.Join(runtimeDir, "index.json"),
		RuntimeStateFile:     filepath.Join(runtimeDir, "state.json"),
		BackgroundTasksFile:  filepath.Join(runtimeDir, "background_tasks.json"),
		AsyncSubagentRunsDir: asyncSubagentRunsDir,
		SessionsDir:          sessionsDir,
		PlansDir:             plansDir,
		PlanFile:             filepath.Join(plansDir, "latest.md"),
		WorktreesDir:         filepath.Join(s.agentDir, "worktrees"),
		ToolResultsDir:       toolResultsDir,
		GlobalMemoryDir:      filepath.Join(s.agentDir, "memory"),
		ProjectMemoryDir:     filepath.Join(s.cwd, ".modu_code", "memory"),
	}
}
