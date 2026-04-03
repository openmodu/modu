package coding_agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/compaction"
	"github.com/openmodu/modu/pkg/coding_agent/tools"
	"github.com/openmodu/modu/pkg/types"
)

type HarnessToolCall struct {
	ToolName string
	Args     map[string]any
}

type HarnessHook struct {
	PreToolUse        func(call HarnessToolCall) error
	PostToolUse       func(call HarnessToolCall, result agent.AgentToolResult, err error)
	PreCompact        func(messageCount int) error
	PostCompact       func(result *compaction.Result, err error)
	SubagentStart     func(run HarnessSubagentRun)
	SubagentStop      func(run HarnessSubagentRun, result string, err error)
	UserPromptSubmit  func(text string) error
	SessionStart      func(source string)
	SessionEnd        func(reason string)
	PermissionRequest func(call HarnessToolCall)
	PermissionDenied  func(call HarnessToolCall, reason string)
	Stop              func()
	StopFailure       func(err error)
	CwdChanged        func(oldCwd, newCwd string)
	WorktreeCreate    func(path string)
	WorktreeRemove    func(path string)
}

type HarnessHint struct {
	Version    int    `json:"version"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	SourceTool string `json:"sourceTool"`
}

type HarnessSubagentRun struct {
	Name       string `json:"name"`
	Task       string `json:"task"`
	Background bool   `json:"background"`
}

type HarnessRuntimePaths struct {
	Root             string `json:"root"`
	RuntimeDir       string `json:"runtimeDir"`
	RuntimeIndexFile string `json:"runtimeIndexFile"`
	RuntimeStateFile string `json:"runtimeStateFile"`
	SessionsDir      string `json:"sessionsDir"`
	PlansDir         string `json:"plansDir"`
	PlanFile         string `json:"planFile"`
	WorktreesDir     string `json:"worktreesDir"`
	ToolResultsDir   string `json:"toolResultsDir"`
	GlobalMemoryDir  string `json:"globalMemoryDir"`
	ProjectMemoryDir string `json:"projectMemoryDir"`
}

func (p HarnessRuntimePaths) ToMap() map[string]any {
	return map[string]any{
		"root":               p.Root,
		"runtime_dir":        p.RuntimeDir,
		"runtime_index_file": p.RuntimeIndexFile,
		"runtime_state_file": p.RuntimeStateFile,
		"sessions_dir":       p.SessionsDir,
		"plans_dir":          p.PlansDir,
		"plan_file":          p.PlanFile,
		"worktrees_dir":      p.WorktreesDir,
		"tool_results_dir":   p.ToolResultsDir,
		"global_memory_dir":  p.GlobalMemoryDir,
		"project_memory_dir": p.ProjectMemoryDir,
	}
}

func (s *CodingSession) HarnessPathsMap() map[string]any {
	return s.RuntimePaths().ToMap()
}

type harnessState struct {
	mu           sync.RWMutex
	hooks        []HarnessHook
	pendingHints []HarnessHint
}

var claudeCodeHintRE = regexp.MustCompile(`(?m)^[ \t]*<claude-code-hint\s+([^>]*?)\s*/>[ \t]*$`)
var claudeCodeHintAttrRE = regexp.MustCompile(`(\w+)=(?:"([^"]*)"|([^\s/>]+))`)

func newHarnessState() *harnessState {
	return &harnessState{}
}

func (s *CodingSession) RegisterHarnessHook(hook HarnessHook) {
	if s.harness == nil {
		return
	}
	s.harness.mu.Lock()
	defer s.harness.mu.Unlock()
	s.harness.hooks = append(s.harness.hooks, hook)
}

func (s *CodingSession) runHarnessUserPromptSubmit(text string) error {
	if s.harness == nil {
		return nil
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.UserPromptSubmit != nil {
			if err := hook.UserPromptSubmit(text); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *CodingSession) runHarnessSessionStart(source string) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.SessionStart != nil {
			hook.SessionStart(source)
		}
	}
}

func (s *CodingSession) runHarnessSessionEnd(reason string) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.SessionEnd != nil {
			hook.SessionEnd(reason)
		}
	}
}

func (s *CodingSession) runHarnessPermissionRequest(call HarnessToolCall) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.PermissionRequest != nil {
			hook.PermissionRequest(call)
		}
	}
}

func (s *CodingSession) runHarnessPermissionDenied(call HarnessToolCall, reason string) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.PermissionDenied != nil {
			hook.PermissionDenied(call, reason)
		}
	}
}

func (s *CodingSession) runHarnessStop() {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.Stop != nil {
			hook.Stop()
		}
	}
}

func (s *CodingSession) runHarnessStopFailure(err error) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.StopFailure != nil {
			hook.StopFailure(err)
		}
	}
}

func (s *CodingSession) runHarnessCwdChanged(oldCwd, newCwd string) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.CwdChanged != nil {
			hook.CwdChanged(oldCwd, newCwd)
		}
	}
}

func (s *CodingSession) runHarnessWorktreeCreate(path string) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.WorktreeCreate != nil {
			hook.WorktreeCreate(path)
		}
	}
}

func (s *CodingSession) runHarnessWorktreeRemove(path string) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.WorktreeRemove != nil {
			hook.WorktreeRemove(path)
		}
	}
}

func (s *CodingSession) installConfigHarnessHooks() {
	if s.config == nil {
		return
	}
	blocked := make(map[string]struct{}, len(s.config.Harness.BlockTools))
	for _, name := range s.config.Harness.BlockTools {
		name = strings.TrimSpace(name)
		if name != "" {
			blocked[name] = struct{}{}
		}
	}
	if len(blocked) == 0 {
		blocked = nil
	}
	logFiles := s.config.Harness.LogFiles
	artifactFiles := s.config.Harness.ArtifactFiles
	bridgeDirs := s.config.Harness.BridgeDirs
	actions := s.config.Harness.Actions
	s.RegisterHarnessHook(HarnessHook{
		UserPromptSubmit: func(text string) error {
			entry := map[string]any{"event": "user_prompt_submit", "category": "session", "text": text}
			s.emitHarnessRecord(logFiles.Session, artifactFiles.Session, bridgeDirs.Session, entry)
			s.dispatchHarnessActions("session", actions.Session, entry)
			return nil
		},
		SessionStart: func(source string) {
			entry := map[string]any{"event": "session_start", "category": "session", "source": source}
			s.emitHarnessRecord(logFiles.Session, artifactFiles.Session, bridgeDirs.Session, entry)
			s.dispatchHarnessActions("session", actions.Session, entry)
		},
		SessionEnd: func(reason string) {
			entry := map[string]any{"event": "session_end", "category": "session", "reason": reason}
			s.emitHarnessRecord(logFiles.Session, artifactFiles.Session, bridgeDirs.Session, entry)
			s.dispatchHarnessActions("session", actions.Session, entry)
		},
		PreToolUse: func(call HarnessToolCall) error {
			if len(blocked) > 0 {
				if _, ok := blocked[call.ToolName]; ok {
					return fmt.Errorf("blocked by settings.json")
				}
			}
			return nil
		},
		PostToolUse: func(call HarnessToolCall, result agent.AgentToolResult, err error) {
			entry := map[string]any{
				"event":    "post_tool_use",
				"category": "tool_use",
				"tool":     call.ToolName,
				"args":     call.Args,
				"error":    errString(err),
			}
			s.emitHarnessRecord(logFiles.ToolUse, artifactFiles.ToolUse, bridgeDirs.ToolUse, entry)
			s.dispatchHarnessActions("tool_use", actions.ToolUse, entry)
		},
		PreCompact: func(messageCount int) error {
			entry := map[string]any{"event": "pre_compact", "category": "compact", "message_count": messageCount}
			s.emitHarnessRecord(logFiles.Compact, artifactFiles.Compact, bridgeDirs.Compact, entry)
			s.dispatchHarnessActions("compact", actions.Compact, entry)
			return nil
		},
		PostCompact: func(result *compaction.Result, err error) {
			entry := map[string]any{
				"event":    "post_compact",
				"category": "compact",
				"error":    errString(err),
			}
			if result != nil {
				entry["original_count"] = result.OriginalCount
				entry["new_count"] = result.NewCount
			}
			s.emitHarnessRecord(logFiles.Compact, artifactFiles.Compact, bridgeDirs.Compact, entry)
			s.dispatchHarnessActions("compact", actions.Compact, entry)
		},
		SubagentStart: func(run HarnessSubagentRun) {
			entry := map[string]any{"event": "subagent_start", "category": "subagent", "name": run.Name, "task": run.Task, "background": run.Background}
			s.emitHarnessRecord(logFiles.Subagent, artifactFiles.Subagent, bridgeDirs.Subagent, entry)
			s.dispatchHarnessActions("subagent", actions.Subagent, entry)
		},
		SubagentStop: func(run HarnessSubagentRun, result string, err error) {
			entry := map[string]any{"event": "subagent_stop", "category": "subagent", "name": run.Name, "task": run.Task, "background": run.Background, "result": result, "error": errString(err)}
			s.emitHarnessRecord(logFiles.Subagent, artifactFiles.Subagent, bridgeDirs.Subagent, entry)
			s.dispatchHarnessActions("subagent", actions.Subagent, entry)
		},
		PermissionRequest: func(call HarnessToolCall) {
			entry := map[string]any{"event": "permission_request", "category": "permission", "tool": call.ToolName, "args": call.Args}
			s.emitHarnessRecord(logFiles.Permission, artifactFiles.Permission, bridgeDirs.Permission, entry)
			s.dispatchHarnessActions("permission", actions.Permission, entry)
		},
		PermissionDenied: func(call HarnessToolCall, reason string) {
			entry := map[string]any{"event": "permission_denied", "category": "permission", "tool": call.ToolName, "args": call.Args, "reason": reason}
			s.emitHarnessRecord(logFiles.Permission, artifactFiles.Permission, bridgeDirs.Permission, entry)
			s.dispatchHarnessActions("permission", actions.Permission, entry)
		},
		Stop: func() {
			entry := map[string]any{"event": "stop", "category": "session"}
			s.emitHarnessRecord(logFiles.Session, artifactFiles.Session, bridgeDirs.Session, entry)
			s.dispatchHarnessActions("session", actions.Session, entry)
		},
		StopFailure: func(err error) {
			entry := map[string]any{"event": "stop_failure", "category": "session", "error": errString(err)}
			s.emitHarnessRecord(logFiles.Session, artifactFiles.Session, bridgeDirs.Session, entry)
			s.dispatchHarnessActions("session", actions.Session, entry)
		},
		CwdChanged: func(oldCwd, newCwd string) {
			entry := map[string]any{"event": "cwd_changed", "category": "session", "old_cwd": oldCwd, "new_cwd": newCwd}
			s.emitHarnessRecord(logFiles.Session, artifactFiles.Session, bridgeDirs.Session, entry)
			s.dispatchHarnessActions("session", actions.Session, entry)
		},
		WorktreeCreate: func(path string) {
			entry := map[string]any{"event": "worktree_create", "category": "session", "path": path}
			s.emitHarnessRecord(logFiles.Session, artifactFiles.Session, bridgeDirs.Session, entry)
			s.dispatchHarnessActions("session", actions.Session, entry)
		},
		WorktreeRemove: func(path string) {
			entry := map[string]any{"event": "worktree_remove", "category": "session", "path": path}
			s.emitHarnessRecord(logFiles.Session, artifactFiles.Session, bridgeDirs.Session, entry)
			s.dispatchHarnessActions("session", actions.Session, entry)
		},
	})
}

// GetPendingHarnessHints drains and returns all accumulated hints since the
// last call. Callers receive each hint exactly once.
func (s *CodingSession) GetPendingHarnessHints() []HarnessHint {
	if s.harness == nil {
		return nil
	}
	s.harness.mu.Lock()
	out := s.harness.pendingHints
	s.harness.pendingHints = nil
	s.harness.mu.Unlock()
	s.writeRuntimeState()
	return out
}

func (s *CodingSession) RuntimePaths() HarnessRuntimePaths {
	projectKey := strings.ReplaceAll(strings.TrimPrefix(s.cwd, "/"), "/", "_")
	if projectKey == "" {
		projectKey = "root"
	}
	plansDir := filepath.Join(s.agentDir, "plans", projectKey)
	toolResultsDir := filepath.Join(s.agentDir, "tool-results", projectKey)
	runtimeDir := filepath.Join(s.agentDir, "runtime", projectKey)
	_ = os.MkdirAll(plansDir, 0o755)
	_ = os.MkdirAll(toolResultsDir, 0o755)
	_ = os.MkdirAll(runtimeDir, 0o755)

	return HarnessRuntimePaths{
		Root:             s.agentDir,
		RuntimeDir:       runtimeDir,
		RuntimeIndexFile: filepath.Join(runtimeDir, "index.json"),
		RuntimeStateFile: filepath.Join(runtimeDir, "state.json"),
		SessionsDir:      filepath.Dir(s.messagesFilePath()),
		PlansDir:         plansDir,
		PlanFile:         filepath.Join(plansDir, "latest.md"),
		WorktreesDir:     filepath.Join(s.agentDir, "worktrees"),
		ToolResultsDir:   toolResultsDir,
		GlobalMemoryDir:  filepath.Join(s.agentDir, "memory"),
		ProjectMemoryDir: filepath.Join(s.cwd, ".modu_code", "memory"),
	}
}

func (s *CodingSession) installHarnessLayer() {
	pathsTool := tools.NewHarnessPathsTool(s)
	s.activeTools = replaceAgentTool(s.activeTools, pathsTool)
	s.activeTools = wrapHarnessTools(s.activeTools, s)
	s.agent.SetTools(s.activeTools)
}

func wrapHarnessTools(list []agent.AgentTool, session *CodingSession) []agent.AgentTool {
	out := make([]agent.AgentTool, len(list))
	for i, tool := range list {
		if _, ok := tool.(*HarnessWrappedTool); ok {
			out[i] = tool
			continue
		}
		out[i] = &HarnessWrappedTool{inner: tool, session: session}
	}
	return out
}

type HarnessWrappedTool struct {
	inner   agent.AgentTool
	session *CodingSession
}

func (w *HarnessWrappedTool) Name() string        { return w.inner.Name() }
func (w *HarnessWrappedTool) Label() string       { return w.inner.Label() }
func (w *HarnessWrappedTool) Description() string { return w.inner.Description() }
func (w *HarnessWrappedTool) Parameters() any     { return w.inner.Parameters() }

func (w *HarnessWrappedTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	call := HarnessToolCall{ToolName: w.inner.Name(), Args: args}
	if err := w.session.runHarnessPreToolHooks(call); err != nil {
		return agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: err.Error()}},
		}, nil
	}

	result, err := w.inner.Execute(ctx, toolCallID, args, onUpdate)
	result = w.session.stripHarnessHints(call, result)
	w.session.runHarnessPostToolHooks(call, result, err)
	return result, err
}

func (w *HarnessWrappedTool) Parallel() bool {
	if p, ok := w.inner.(agent.ParallelTool); ok {
		return p.Parallel()
	}
	return false
}

func (s *CodingSession) runHarnessPreToolHooks(call HarnessToolCall) error {
	if s.harness == nil {
		return nil
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.PreToolUse != nil {
			if err := hook.PreToolUse(call); err != nil {
				return fmt.Errorf("harness blocked %s: %w", call.ToolName, err)
			}
		}
	}
	return nil
}

func (s *CodingSession) runHarnessPostToolHooks(call HarnessToolCall, result agent.AgentToolResult, err error) {
	if s.harness == nil {
		return
	}
	s.writeToolResultArtifact(call, result, err)
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.PostToolUse != nil {
			hook.PostToolUse(call, result, err)
		}
	}
}

func (s *CodingSession) runHarnessPreCompact(messageCount int) error {
	if s.harness == nil {
		return nil
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.PreCompact != nil {
			if err := hook.PreCompact(messageCount); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *CodingSession) runHarnessPostCompact(result *compaction.Result, err error) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.PostCompact != nil {
			hook.PostCompact(result, err)
		}
	}
}

func (s *CodingSession) OnSubagentStart(name, task string, background bool) {
	s.onSubagentStart(HarnessSubagentRun{Name: name, Task: task, Background: background})
}

func (s *CodingSession) OnSubagentStop(name, task string, background bool, result string, err error) {
	s.onSubagentStop(HarnessSubagentRun{Name: name, Task: task, Background: background}, result, err)
}

func (s *CodingSession) onSubagentStart(run HarnessSubagentRun) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.SubagentStart != nil {
			hook.SubagentStart(run)
		}
	}
}

func (s *CodingSession) onSubagentStop(run HarnessSubagentRun, result string, err error) {
	if s.harness == nil {
		return
	}
	s.harness.mu.RLock()
	hooks := s.harness.hooks
	s.harness.mu.RUnlock()
	for _, hook := range hooks {
		if hook.SubagentStop != nil {
			hook.SubagentStop(run, result, err)
		}
	}
}

func (s *CodingSession) stripHarnessHints(call HarnessToolCall, result agent.AgentToolResult) agent.AgentToolResult {
	if s.harness == nil || !s.config.HarnessCaptureHints() {
		return result
	}
	for _, block := range result.Content {
		tc, ok := block.(*types.TextContent)
		if !ok || tc == nil || !strings.Contains(tc.Text, "<claude-code-hint") {
			continue
		}
		hints, stripped := extractHarnessHints(tc.Text, call.ToolName)
		if len(hints) == 0 {
			continue
		}
		tc.Text = stripped
		s.harness.mu.Lock()
		s.harness.pendingHints = append(s.harness.pendingHints, hints...)
		s.harness.mu.Unlock()
		s.writeRuntimeState()
	}
	return result
}

func extractHarnessHints(text, sourceTool string) ([]HarnessHint, string) {
	var hints []HarnessHint
	stripped := claudeCodeHintRE.ReplaceAllStringFunc(text, func(line string) string {
		attrs := parseHarnessHintAttrs(line)
		if attrs["v"] != "1" || attrs["type"] == "" || attrs["value"] == "" {
			return ""
		}
		hints = append(hints, HarnessHint{
			Version:    1,
			Type:       attrs["type"],
			Value:      attrs["value"],
			SourceTool: sourceTool,
		})
		return ""
	})
	if len(hints) > 0 {
		stripped = strings.TrimSpace(strings.ReplaceAll(stripped, "\n\n\n", "\n\n"))
	}
	return hints, stripped
}

func parseHarnessHintAttrs(tag string) map[string]string {
	attrs := make(map[string]string)
	for _, match := range claudeCodeHintAttrRE.FindAllStringSubmatch(tag, -1) {
		if len(match) >= 4 {
			attrs[match[1]] = firstNonEmpty(match[2], match[3])
		}
	}
	return attrs
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *CodingSession) writeToolResultArtifact(call HarnessToolCall, result agent.AgentToolResult, err error) {
	if s.config != nil && !s.config.HarnessPersistToolResults() {
		return
	}
	paths := s.RuntimePaths()
	if errMkdir := os.MkdirAll(paths.ToolResultsDir, 0o755); errMkdir != nil {
		return
	}
	name := fmt.Sprintf("%d-%s.txt", time.Now().UnixMilli(), sanitizeArtifactName(call.ToolName))
	path := filepath.Join(paths.ToolResultsDir, name)

	var text strings.Builder
	text.WriteString("tool: " + call.ToolName + "\n")
	if err != nil {
		text.WriteString("error: " + err.Error() + "\n")
	}
	for _, block := range result.Content {
		if tc, ok := block.(*types.TextContent); ok && tc != nil && tc.Text != "" {
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(tc.Text)
		}
	}
	_ = os.WriteFile(path, []byte(text.String()), 0o600)
}

func sanitizeArtifactName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "tool"
	}
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

func (s *CodingSession) appendHarnessLog(target string, entry map[string]any) {
	path := s.resolveHarnessTarget(target)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if entry == nil {
		entry = make(map[string]any)
	}
	entry["timestamp"] = time.Now().UnixMilli()
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

func (s *CodingSession) writeHarnessArtifact(target string, entry map[string]any) {
	path := s.resolveHarnessTarget(target)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if entry == nil {
		entry = make(map[string]any)
	}
	entry["timestamp"] = time.Now().UnixMilli()
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(data, '\n'), 0o600)
}

func (s *CodingSession) emitHarnessRecord(logTarget, artifactTarget, bridgeDir string, entry map[string]any) {
	s.updateRuntimeIndex(asString(entry["category"]), maps.Clone(entry))
	if strings.TrimSpace(logTarget) != "" {
		s.appendHarnessLog(logTarget, maps.Clone(entry))
	}
	if strings.TrimSpace(artifactTarget) != "" {
		s.writeHarnessArtifact(artifactTarget, maps.Clone(entry))
	}
	if strings.TrimSpace(bridgeDir) != "" {
		s.writeHarnessBridgeEvent(bridgeDir, maps.Clone(entry))
	}
}

func (s *CodingSession) updateRuntimeIndex(category string, entry map[string]any) {
	if s.config == nil {
		return
	}
	paths := s.RuntimePaths()
	index := map[string]any{
		"updated_at": time.Now().UnixMilli(),
		"paths":      paths.ToMap(),
		"outputs": map[string]any{
			"log_files": map[string]string{
				"tool_use":   s.resolveHarnessTarget(s.config.Harness.LogFiles.ToolUse),
				"compact":    s.resolveHarnessTarget(s.config.Harness.LogFiles.Compact),
				"subagent":   s.resolveHarnessTarget(s.config.Harness.LogFiles.Subagent),
				"session":    s.resolveHarnessTarget(s.config.Harness.LogFiles.Session),
				"permission": s.resolveHarnessTarget(s.config.Harness.LogFiles.Permission),
			},
			"artifact_files": map[string]string{
				"tool_use":   s.resolveHarnessTarget(s.config.Harness.ArtifactFiles.ToolUse),
				"compact":    s.resolveHarnessTarget(s.config.Harness.ArtifactFiles.Compact),
				"subagent":   s.resolveHarnessTarget(s.config.Harness.ArtifactFiles.Subagent),
				"session":    s.resolveHarnessTarget(s.config.Harness.ArtifactFiles.Session),
				"permission": s.resolveHarnessTarget(s.config.Harness.ArtifactFiles.Permission),
			},
			"bridge_dirs": map[string]string{
				"tool_use":   s.resolveHarnessTarget(s.config.Harness.BridgeDirs.ToolUse),
				"compact":    s.resolveHarnessTarget(s.config.Harness.BridgeDirs.Compact),
				"subagent":   s.resolveHarnessTarget(s.config.Harness.BridgeDirs.Subagent),
				"session":    s.resolveHarnessTarget(s.config.Harness.BridgeDirs.Session),
				"permission": s.resolveHarnessTarget(s.config.Harness.BridgeDirs.Permission),
			},
		},
		"last_events": map[string]any{},
	}
	if data, err := os.ReadFile(paths.RuntimeIndexFile); err == nil {
		var existing map[string]any
		if json.Unmarshal(data, &existing) == nil {
			if prev, ok := existing["last_events"].(map[string]any); ok {
				index["last_events"] = prev
			}
		}
	}
	if category == "" {
		category = "misc"
	}
	lastEvents := index["last_events"].(map[string]any)
	lastEvents[category] = entry
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(paths.RuntimeIndexFile, append(data, '\n'), 0o600)
}

func (s *CodingSession) resolveHarnessTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(s.agentDir, target)
}

func (s *CodingSession) writeHarnessBridgeEvent(targetDir string, entry map[string]any) {
	dir := s.resolveHarnessTarget(targetDir)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if entry == nil {
		entry = make(map[string]any)
	}
	ts := time.Now().UnixMilli()
	entry["timestamp"] = ts
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	name := fmt.Sprintf("%d-%s.json", ts, sanitizeArtifactName(firstNonEmpty(asString(entry["event"]), "event")))
	_ = os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0o600)
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (s *CodingSession) dispatchHarnessActions(category string, actions []HarnessAction, entry map[string]any) {
	if s.config == nil || !s.config.HarnessEnableActions() || !s.config.FeatureHarnessActions() {
		return
	}
	for _, action := range actions {
		if action.normalizedType() != "exec" || strings.TrimSpace(action.Command) == "" {
			continue
		}
		result := s.runHarnessAction(category, action, entry)
		if result.Status == "error" && strings.EqualFold(strings.TrimSpace(action.OnFailure), "stop") {
			return
		}
	}
}

func (s *CodingSession) runHarnessAction(category string, action HarnessAction, entry map[string]any) harnessActionRunResult {
	eventJSON, err := json.Marshal(entry)
	if err != nil {
		return harnessActionRunResult{}
	}
	cmdName := s.expandHarnessTemplate(action.Command, category, entry)
	if strings.TrimSpace(cmdName) == "" {
		return harnessActionRunResult{}
	}
	if err := validateHarnessActionCommand(s.config.Harness.ActionPolicy, cmdName); err != nil {
		result := harnessActionRunResult{
			Status:    "error",
			Error:     errString(err),
			Attempts:  0,
			Completed: time.Now().UnixMilli(),
		}
		s.writeHarnessActionStatus(category, action, entry, result)
		return result
	}
	args := make([]string, 0, len(action.Args))
	for _, arg := range action.Args {
		args = append(args, s.expandHarnessTemplate(arg, category, entry))
	}
	cmdDir := s.cwd
	if dir := strings.TrimSpace(action.Dir); dir != "" {
		dir = s.expandHarnessTemplate(dir, category, entry)
		cmdDir = s.resolveHarnessTarget(dir)
	}
	if filepath.IsAbs(cmdDir) {
		for _, denied := range s.config.Harness.ActionPolicy.DenyDirPrefixes {
			if denied = strings.TrimSpace(denied); denied != "" && strings.HasPrefix(cmdDir, denied) {
				result := harnessActionRunResult{
					Status:    "error",
					Error:     fmt.Sprintf("harness action dir denied by policy: %s", cmdDir),
					Attempts:  0,
					Completed: time.Now().UnixMilli(),
				}
				s.writeHarnessActionStatus(category, action, entry, result)
				return result
			}
		}
		if len(s.config.Harness.ActionPolicy.AllowDirPrefixes) > 0 {
			allowed := false
			for _, prefix := range s.config.Harness.ActionPolicy.AllowDirPrefixes {
				if prefix = strings.TrimSpace(prefix); prefix != "" && strings.HasPrefix(cmdDir, prefix) {
					allowed = true
					break
				}
			}
			if !allowed {
				result := harnessActionRunResult{
					Status:    "error",
					Error:     fmt.Sprintf("harness action dir not allowed by policy: %s", cmdDir),
					Attempts:  0,
					Completed: time.Now().UnixMilli(),
				}
				s.writeHarnessActionStatus(category, action, entry, result)
				return result
			}
		}
	}
	maxAttempts := 1
	if action.Retry.MaxAttempts > 1 {
		maxAttempts = action.Retry.MaxAttempts
	}
	delay := time.Duration(action.Retry.DelayMs) * time.Millisecond
	var result harnessActionRunResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result = s.runHarnessActionAttempt(category, action, entry, cmdName, args, cmdDir, string(eventJSON), attempt)
		if result.Status == "ok" || attempt == maxAttempts {
			break
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	s.writeHarnessActionStatus(category, action, entry, result)
	return result
}

func (s *CodingSession) expandHarnessTemplate(value, category string, entry map[string]any) string {
	replacements := map[string]string{
		"{{agent_dir}}":      s.agentDir,
		"{{cwd}}":            s.cwd,
		"{{runtime_dir}}":    s.RuntimePaths().RuntimeDir,
		"{{event_category}}": category,
		"{{event_type}}":     asString(entry["event"]),
		"{{tool}}":           asString(entry["tool"]),
		"{{subagent_name}}":  asString(entry["name"]),
		"{{subagent_task}}":  asString(entry["task"]),
	}
	for from, to := range replacements {
		value = strings.ReplaceAll(value, from, to)
	}
	return value
}

type harnessActionRunResult struct {
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
	Stdout    string `json:"stdout,omitempty"`
	Stderr    string `json:"stderr,omitempty"`
	Output    string `json:"output,omitempty"`
	TimedOut  bool   `json:"timed_out,omitempty"`
	Attempts  int    `json:"attempts,omitempty"`
	Completed int64  `json:"completed_at,omitempty"`
}

func (s *CodingSession) runHarnessActionAttempt(category string, action HarnessAction, entry map[string]any, cmdName string, args []string, cmdDir string, eventJSON string, attempt int) harnessActionRunResult {
	result := harnessActionRunResult{
		Status:    "ok",
		Attempts:  attempt,
		Completed: time.Now().UnixMilli(),
	}
	runCtx := context.Background()
	var cancel context.CancelFunc
	if action.TimeoutMs > 0 {
		runCtx, cancel = context.WithTimeout(context.Background(), time.Duration(action.TimeoutMs)*time.Millisecond)
	} else {
		runCtx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, cmdName, args...)
	cmd.Dir = cmdDir
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Env = append(os.Environ(),
		"HARNESS_EVENT_CATEGORY="+category,
		"HARNESS_EVENT_TYPE="+asString(entry["event"]),
		"HARNESS_EVENT_JSON="+eventJSON,
		"HARNESS_AGENT_DIR="+s.agentDir,
		"HARNESS_RUNTIME_ROOT="+s.RuntimePaths().RuntimeDir,
		"HARNESS_TOOL="+asString(entry["tool"]),
		"HARNESS_SUBAGENT_NAME="+asString(entry["name"]),
	)
	err := cmd.Run()
	result.Stdout = stdoutBuf.String()
	result.Stderr = stderrBuf.String()
	result.Output = result.Stdout + result.Stderr
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
	}
	result.Completed = time.Now().UnixMilli()
	return result
}

func (s *CodingSession) writeHarnessActionStatus(category string, action HarnessAction, entry map[string]any, result harnessActionRunResult) {
	dir := filepath.Join(s.RuntimePaths().RuntimeDir, "actions", sanitizeArtifactName(category))
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return
	}
	status := map[string]any{
		"updated_at":   time.Now().UnixMilli(),
		"category":     category,
		"event":        entry,
		"status":       result.Status,
		"stdout":       result.Stdout,
		"stderr":       result.Stderr,
		"output":       result.Output,
		"attempts":     result.Attempts,
		"completed_at": result.Completed,
		"action": map[string]any{
			"type":       action.Type,
			"command":    action.Command,
			"args":       action.Args,
			"dir":        action.Dir,
			"timeout_ms": action.TimeoutMs,
			"on_failure": action.OnFailure,
			"retry": map[string]any{
				"max_attempts": action.Retry.MaxAttempts,
				"delay_ms":     action.Retry.DelayMs,
			},
		},
	}
	if result.Error != "" {
		status["error"] = result.Error
	}
	if result.TimedOut {
		status["timed_out"] = true
	}
	data, marshalErr := json.MarshalIndent(status, "", "  ")
	if marshalErr != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "latest.json"), append(data, '\n'), 0o600)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
