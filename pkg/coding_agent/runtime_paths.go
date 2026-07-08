package coding_agent

import (
	"path/filepath"

	"github.com/openmodu/modu/pkg/coding_agent/foundation/runtimepaths"
)

// Runtime paths derive the on-disk layout for a session under the agent dir.

type HarnessRuntimePaths struct {
	Root                 string `json:"root"`
	RuntimeDir           string `json:"runtimeDir"`
	RuntimeIndexFile     string `json:"runtimeIndexFile"`
	BackgroundTasksFile  string `json:"backgroundTasksFile"`
	AsyncSubagentRunsDir string `json:"asyncSubagentRunsDir"`
	SessionsDir          string `json:"sessionsDir"`
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
		"background_tasks_file":   p.BackgroundTasksFile,
		"async_subagent_runs_dir": p.AsyncSubagentRunsDir,
		"sessions_dir":            p.SessionsDir,
		"worktrees_dir":           p.WorktreesDir,
		"tool_results_dir":        p.ToolResultsDir,
		"global_memory_dir":       p.GlobalMemoryDir,
		"project_memory_dir":      p.ProjectMemoryDir,
	}
}

func (s *engine) RuntimePaths() HarnessRuntimePaths {
	projectKey := runtimepaths.ProjectKey(s.cwd)
	toolResultsDir := runtimepaths.ProjectToolResultsDir(s.agentDir, s.cwd)
	runtimeDir := filepath.Join(s.agentDir, "runtime", projectKey)
	asyncSubagentRunsDir := filepath.Join(runtimeDir, "async-subagent-runs")

	sessionsDir := filepath.Dir(s.messagesFilePath())
	if s.sessionManager != nil {
		sessionsDir = s.sessionManager.Dir()
	}
	return HarnessRuntimePaths{
		Root:                 s.agentDir,
		RuntimeDir:           runtimeDir,
		RuntimeIndexFile:     filepath.Join(runtimeDir, "index.json"),
		BackgroundTasksFile:  filepath.Join(runtimeDir, "background_tasks.json"),
		AsyncSubagentRunsDir: asyncSubagentRunsDir,
		SessionsDir:          sessionsDir,
		WorktreesDir:         filepath.Join(s.agentDir, "worktrees"),
		ToolResultsDir:       toolResultsDir,
		GlobalMemoryDir:      filepath.Join(s.agentDir, "memory"),
		ProjectMemoryDir:     filepath.Join(s.cwd, ".modu_code", "memory"),
	}
}
