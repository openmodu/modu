package coding_agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	PreToolUse    func(call HarnessToolCall) error
	PostToolUse   func(call HarnessToolCall, result agent.AgentToolResult, err error)
	PreCompact    func(messageCount int) error
	PostCompact   func(result *compaction.Result, err error)
	SubagentStart func(run HarnessSubagentRun)
	SubagentStop  func(run HarnessSubagentRun, result string, err error)
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
	mu           sync.Mutex
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
	s.RegisterHarnessHook(HarnessHook{
		PreToolUse: func(call HarnessToolCall) error {
			if len(blocked) > 0 {
				if _, ok := blocked[call.ToolName]; ok {
					return fmt.Errorf("blocked by settings.json")
				}
			}
			return nil
		},
		PostToolUse: func(call HarnessToolCall, result agent.AgentToolResult, err error) {
			if logFiles.ToolUse == "" {
				return
			}
			s.appendHarnessLog(logFiles.ToolUse, map[string]any{
				"event": "post_tool_use",
				"tool":  call.ToolName,
				"args":  call.Args,
				"error": errString(err),
			})
		},
		PreCompact: func(messageCount int) error {
			if logFiles.Compact != "" {
				s.appendHarnessLog(logFiles.Compact, map[string]any{
					"event":         "pre_compact",
					"message_count": messageCount,
				})
			}
			return nil
		},
		PostCompact: func(result *compaction.Result, err error) {
			if logFiles.Compact == "" {
				return
			}
			entry := map[string]any{
				"event": "post_compact",
				"error": errString(err),
			}
			if result != nil {
				entry["original_count"] = result.OriginalCount
				entry["new_count"] = result.NewCount
			}
			s.appendHarnessLog(logFiles.Compact, entry)
		},
		SubagentStart: func(run HarnessSubagentRun) {
			if logFiles.Subagent == "" {
				return
			}
			s.appendHarnessLog(logFiles.Subagent, map[string]any{
				"event":      "subagent_start",
				"name":       run.Name,
				"task":       run.Task,
				"background": run.Background,
			})
		},
		SubagentStop: func(run HarnessSubagentRun, result string, err error) {
			if logFiles.Subagent == "" {
				return
			}
			s.appendHarnessLog(logFiles.Subagent, map[string]any{
				"event":      "subagent_stop",
				"name":       run.Name,
				"task":       run.Task,
				"background": run.Background,
				"result":     result,
				"error":      errString(err),
			})
		},
	})
}

func (s *CodingSession) GetPendingHarnessHints() []HarnessHint {
	if s.harness == nil {
		return nil
	}
	s.harness.mu.Lock()
	defer s.harness.mu.Unlock()
	out := make([]HarnessHint, len(s.harness.pendingHints))
	copy(out, s.harness.pendingHints)
	return out
}

func (s *CodingSession) RuntimePaths() HarnessRuntimePaths {
	projectKey := strings.ReplaceAll(strings.TrimPrefix(s.cwd, "/"), "/", "_")
	if projectKey == "" {
		projectKey = "root"
	}
	plansDir := filepath.Join(s.agentDir, "plans", projectKey)
	toolResultsDir := filepath.Join(s.agentDir, "tool-results", projectKey)
	_ = os.MkdirAll(plansDir, 0o755)
	_ = os.MkdirAll(toolResultsDir, 0o755)

	return HarnessRuntimePaths{
		Root:             s.agentDir,
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
	s.harness.mu.Lock()
	hooks := append([]HarnessHook{}, s.harness.hooks...)
	s.harness.mu.Unlock()
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
	s.harness.mu.Lock()
	hooks := append([]HarnessHook{}, s.harness.hooks...)
	s.harness.mu.Unlock()
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
	s.harness.mu.Lock()
	hooks := append([]HarnessHook{}, s.harness.hooks...)
	s.harness.mu.Unlock()
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
	s.harness.mu.Lock()
	hooks := append([]HarnessHook{}, s.harness.hooks...)
	s.harness.mu.Unlock()
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
	s.harness.mu.Lock()
	hooks := append([]HarnessHook{}, s.harness.hooks...)
	s.harness.mu.Unlock()
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
	s.harness.mu.Lock()
	hooks := append([]HarnessHook{}, s.harness.hooks...)
	s.harness.mu.Unlock()
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
	path := strings.TrimSpace(target)
	if path == "" {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.agentDir, path)
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

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
