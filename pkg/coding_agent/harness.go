package coding_agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

type HarnessToolCall struct {
	ToolName string
	Args     map[string]any
}

type HarnessSubagentRun struct {
	Name       string `json:"name"`
	Task       string `json:"task"`
	Background bool   `json:"background"`
}

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

func (s *engine) installHarnessLayer() {
	s.activeTools = wrapHarnessTools(s.activeTools, s)
	s.agent.SetTools(s.activeTools)
}

func wrapHarnessTools(list []agent.Tool, session *engine) []agent.Tool {
	out := make([]agent.Tool, len(list))
	for i, tool := range list {
		if _, ok := tool.(*HarnessWrappedTool); ok {
			out[i] = tool
			continue
		}
		out[i] = &HarnessWrappedTool{inner: tool, session: session}
	}
	return out
}

// HarnessWrappedTool wraps every active tool with the two host-side gates that
// must run before any tool executes: plan-mode mutation blocking and the
// settings-driven blockTools deny list.
type HarnessWrappedTool struct {
	inner   agent.Tool
	session *engine
}

func (w *HarnessWrappedTool) Name() string        { return w.inner.Name() }
func (w *HarnessWrappedTool) Label() string       { return w.inner.Label() }
func (w *HarnessWrappedTool) Description() string { return w.inner.Description() }
func (w *HarnessWrappedTool) Parameters() any     { return w.inner.Parameters() }
func (w *HarnessWrappedTool) WithCwd(cwd string) agent.Tool {
	if rebindable, ok := w.inner.(interface{ WithCwd(string) agent.Tool }); ok {
		return &HarnessWrappedTool{inner: rebindable.WithCwd(cwd), session: w.session}
	}
	return w
}

func (w *HarnessWrappedTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.ToolUpdateCallback) (agent.ToolResult, error) {
	name := w.inner.Name()
	if w.session != nil && w.session.planModeBlocksTool(name) {
		return agent.ToolResult{
			Content: []types.ContentBlock{&types.TextContent{
				Type: "text",
				Text: planModeBlockMessage(name),
			}},
			Details: map[string]any{"isError": true, "blockedBy": "plan_mode"},
		}, nil
	}
	if w.session != nil && w.session.harnessToolBlocked(name) {
		return agent.ToolResult{
			Content: []types.ContentBlock{&types.TextContent{
				Type: "text",
				Text: fmt.Sprintf("harness blocked %s: blocked by settings.json", name),
			}},
			Details: map[string]any{"isError": true},
		}, nil
	}
	return w.inner.Execute(ctx, toolCallID, args, onUpdate)
}

func (w *HarnessWrappedTool) Parallel() bool {
	if p, ok := w.inner.(agent.ParallelTool); ok {
		return p.Parallel()
	}
	return false
}

func (s *engine) harnessToolBlocked(name string) bool {
	if s == nil || s.config == nil {
		return false
	}
	for _, blocked := range s.config.Harness.BlockTools {
		if strings.TrimSpace(blocked) == name {
			return true
		}
	}
	return false
}

func (s *engine) planModeBlocksTool(toolName string) bool {
	if s == nil || !s.IsPlanMode() {
		return false
	}
	switch toolName {
	case "write", "edit", "bash":
		return true
	default:
		return false
	}
}

func planModeBlockMessage(toolName string) string {
	return fmt.Sprintf("%s is blocked while plan mode is active; exit plan mode before making changes", toolName)
}

func (s *engine) runHarnessPermissionRequest(call HarnessToolCall) {
	s.emitSessionEvent(SessionEvent{
		Type:     SessionEventPermissionReq,
		ToolName: call.ToolName,
	})
}

func (s *engine) runHarnessPermissionDenied(call HarnessToolCall, reason string) {
	s.emitSessionEvent(SessionEvent{
		Type:     SessionEventPermissionDeny,
		ToolName: call.ToolName,
		Reason:   reason,
	})
}

func (s *engine) runHarnessCwdChanged(oldCwd, newCwd string) {
	s.emitSessionEvent(SessionEvent{
		Type:   SessionEventCwdChanged,
		OldCwd: oldCwd,
		NewCwd: newCwd,
	})
}

func (s *engine) runHarnessWorktreeCreate(path string) {
	s.emitSessionEvent(SessionEvent{
		Type: SessionEventWorktreeCreate,
		Path: path,
	})
}

func (s *engine) runHarnessWorktreeRemove(path string) {
	s.emitSessionEvent(SessionEvent{
		Type: SessionEventWorktreeRemove,
		Path: path,
	})
}

func (s *engine) OnSubagentStart(name, task string, background bool) {
	s.onSubagentStart(HarnessSubagentRun{Name: name, Task: task, Background: background})
}

func (s *engine) OnSubagentStop(name, task string, background bool, result string, err error) {
	s.onSubagentStop(HarnessSubagentRun{Name: name, Task: task, Background: background}, result, err)
}

func (s *engine) onSubagentStart(run HarnessSubagentRun) {
	s.emitSessionEvent(SessionEvent{
		Type:               SessionEventSubagentStart,
		SubagentName:       run.Name,
		SubagentTask:       run.Task,
		SubagentBackground: run.Background,
	})
}

func (s *engine) onSubagentStop(run HarnessSubagentRun, result string, err error) {
	evt := SessionEvent{
		Type:               SessionEventSubagentStop,
		SubagentName:       run.Name,
		SubagentTask:       run.Task,
		SubagentBackground: run.Background,
	}
	if result != "" {
		preview := result
		if len(preview) > 240 {
			preview = preview[:237] + "..."
		}
		evt.SubagentResult = preview
	}
	if err != nil {
		evt.ErrorMessage = err.Error()
	}
	s.emitSessionEvent(evt)
}
