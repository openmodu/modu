package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

type workflowTool struct {
	ext *Extension
}

func newTool(ext *Extension) *workflowTool {
	return &workflowTool{ext: ext}
}

func (t *workflowTool) Name() string  { return "workflow" }
func (t *workflowTool) Label() string { return "Workflow" }
func (t *workflowTool) Description() string {
	return strings.Join([]string{
		"Execute a JavaScript workflow that orchestrates forked subagents with agent(), parallel(), and pipeline().",
		"Use only when the user explicitly asks for a workflow, fan-out, or multi-agent orchestration.",
		"",
		"The script is plain async JavaScript (no ES modules, no require, no Markdown fences). It MUST call meta({name, description}) first and MUST await at least one agent().",
		"",
		"API:",
		"- meta({name, description, whenToUse?, phases?}) — phases is [{title, detail?, model?}]. Call once, first.",
		"- await agent(prompt, opts?) — fork one subagent; returns its final text, or a validated object when opts.schema (a JSON Schema) is set. opts: {label, phase, model, tools:[], disallowedTools:[], permissionMode, isolation:'worktree', maxTurns, thinking, skills:[], memoryScope, schema}. Returns null if the agent is skipped (limit/budget) — guard with `if (x)` or `.filter(Boolean)`.",
		"- await parallel([() => agent(...), () => agent(...)]) — run thunks concurrently; results in input order; a throwing thunk becomes null. BARRIER: waits for all.",
		"- await pipeline(items, stage1, stage2, ...) — run each item through every stage independently, no barrier between stages. Each stage gets (prevResult, originalItem, index).",
		"- phase(title) groups following agents in the UI; log(message) emits a progress line; workflow(nameOrPath, args?) runs another workflow (one level deep).",
		"- Globals: args (the args input), cwd, process.cwd(), budget {total, spent(), remaining()}, JSON.",
		"",
		"Rules for reliable runs: always `await` agent/parallel/pipeline; end the script with `return <result>`; default to pipeline() and only use parallel() when you truly need all results at once; pass data between stages via the prompt string (use JSON.stringify for structured context); set opts.phase inside parallel/pipeline stages so agents are grouped correctly.",
		"",
		"This tool only starts workflow runs; do not pass action, status, id, run_id, or agent_id. Inspect or control runs with /workflows feed <run-id>, /workflows guide <run-id>, /workflows show <run-id>, /workflows map <run-id>, /workflows agent <run-id> <agent-id>, /workflows stop <run-id>, or the /workflows TUI panel.",
	}, "\n")
}

func (t *workflowTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"script": map[string]any{
				"type":        "string",
				"description": "Raw JavaScript workflow script, with no Markdown fences. Exactly one of script, script_path, or name is required.",
			},
			"script_path": map[string]any{
				"type":        "string",
				"description": "Path to a JavaScript workflow script (.js). Relative paths resolve from the current cwd.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Saved workflow name. Looks in project .coding_agent/.claude workflows, then user workflows.",
			},
			"args": map[string]any{
				"description": "Optional JSON value exposed to the script as the global args.",
			},
			"concurrency": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional max concurrent agent runs for this workflow.",
			},
			"budget": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional token budget exposed as budget.total and budget.remaining().",
			},
			"async": map[string]any{
				"type":        "boolean",
				"description": "Run the workflow in the background and return immediately with a run id. Use /workflows feed <run-id>, /workflows guide <run-id>, /workflows show <run-id>, /workflows map <run-id>, /workflows agent <run-id> <agent-id>, /workflows stop <run-id>, or the /workflows TUI panel to inspect or control it. Do not call this tool with action/status/id fields.",
			},
		},
		"additionalProperties": false,
	}
}

func (t *workflowTool) Execute(ctx context.Context, _ string, args map[string]any, onUpdate types.ToolUpdateCallback) (types.ToolResult, error) {
	exec, err := t.ext.prepareWorkflowExecution(args, onUpdate)
	if err != nil {
		return textResult(fmt.Sprintf("workflow: %v", err), true, nil), nil
	}
	async, err := decodeBool(args["async"], "async")
	if err != nil {
		return textResult(fmt.Sprintf("workflow: %v", err), true, nil), nil
	}
	if !t.ext.approveWorkflowRun(exec, "workflow tool") {
		return textResult("workflow: cancelled before start", true, nil), nil
	}
	if async {
		runID := t.ext.startBackgroundWorkflow(exec)
		text := fmt.Sprintf("Workflow started in background.\nRun: %s", runID)
		if exec.ScriptPath != "" {
			text += "\nScript: " + exec.ScriptPath
		}
		text += fmt.Sprintf("\nUse /workflows feed %s to watch progress, /workflows guide %s to understand the run views, /workflows show %s to inspect metadata, or /workflows stop %s to stop it.", runID, runID, runID, runID)
		return textResult(text, false, map[string]any{
			"runID":      runID,
			"scriptPath": exec.ScriptPath,
			"runDir":     exec.RunDir,
			"status":     string(workflowStatusRunning),
		}), nil
	}
	result, err := t.ext.runWorkflow(ctx, exec)
	if err != nil {
		return textResult(fmt.Sprintf("workflow: %v", err), true, result.Snapshot), nil
	}
	text := formatWorkflowCompletion(result)
	return textResult(text, false, result.Snapshot), nil
}

type workflowExecution struct {
	Script      string
	Args        any
	Concurrency int
	BudgetTotal int
	MaxAgents   int
	ScriptPath  string
	RunDir      string
	OnUpdate    types.ToolUpdateCallback
	AgentCache  *workflowAgentCache
	Resume      bool
}

func (e *Extension) prepareWorkflowExecution(args map[string]any, onUpdate types.ToolUpdateCallback) (workflowExecution, error) {
	// The resolved source path is intentionally discarded: it's captured in the
	// persisted run dir, and writing it back would mutate the caller's args map.
	script, _, err := loadWorkflowScript(args, e.api.Cwd(), e.api.AgentDir())
	if err != nil {
		return workflowExecution{}, err
	}
	concurrency, err := decodeConcurrency(args["concurrency"], e.cfg.Concurrency)
	if err != nil {
		return workflowExecution{}, err
	}
	budgetTotal, err := decodePositiveInt(args["budget"], "budget")
	if err != nil {
		return workflowExecution{}, err
	}
	scriptPath, runDir, err := persistWorkflowScript(e.api.SessionDir(), script)
	if err != nil {
		return workflowExecution{}, err
	}
	return workflowExecution{
		Script:      script,
		Args:        args["args"],
		Concurrency: concurrency,
		BudgetTotal: budgetTotal,
		MaxAgents:   e.cfg.MaxAgents,
		ScriptPath:  scriptPath,
		RunDir:      runDir,
		OnUpdate:    onUpdate,
	}, nil
}

func (e *Extension) runWorkflow(ctx context.Context, exec workflowExecution) (runResult, error) {
	result, err := newRunner(e.api, runOptions{
		Cwd:         e.api.Cwd(),
		AgentDir:    e.api.AgentDir(),
		Args:        exec.Args,
		Concurrency: exec.Concurrency,
		BudgetTotal: exec.BudgetTotal,
		MaxAgents:   exec.MaxAgents,
		ScriptPath:  exec.ScriptPath,
		RunDir:      exec.RunDir,
		OnUpdate:    exec.OnUpdate,
		Resume:      exec.Resume,
		Activities:  e.activities,
		Registry:    e.registry,
		State: &workflowRunState{
			cache: exec.AgentCache,
		},
	}).run(ctx, exec.Script)
	if result.Snapshot.Name != "" || result.Snapshot.AgentCount > 0 {
		if persistErr := persistWorkflowSnapshot(exec.RunDir, result.Snapshot); persistErr != nil && err == nil {
			return result, persistErr
		}
	}
	return result, err
}

func (e *Extension) startBackgroundWorkflow(exec workflowExecution) string {
	if e.registry == nil {
		e.registry = newWorkflowRegistry()
	}
	runID := workflowRunID(exec.RunDir)
	if exec.AgentCache == nil {
		exec.AgentCache = newWorkflowAgentCache()
	}
	ctx, cancel := context.WithCancel(context.Background())
	userUpdate := exec.OnUpdate
	exec.OnUpdate = func(partial types.ToolResult) {
		if snapshot, ok := partial.Details.(workflowSnapshot); ok {
			e.registry.update(runID, snapshot)
		}
		if userUpdate != nil {
			userUpdate(partial)
		}
	}
	e.registry.start(runID, exec.ScriptPath, exec.RunDir, cancel, exec)
	if err := persistWorkflowRunStatus(exec.RunDir, workflowStatusRunning, ""); err != nil {
		e.tell(fmt.Sprintf("Workflow %s status persistence failed: %v", runID, err))
	}
	go e.runBackgroundWorkflow(runID, ctx, exec)
	return runID
}

func (e *Extension) runBackgroundWorkflow(runID string, ctx context.Context, exec workflowExecution) {
	if exec.AgentCache == nil {
		exec.AgentCache = newWorkflowAgentCache()
	}
	result, err := e.runWorkflow(ctx, exec)
	status := workflowStatusCompleted
	errText := ""
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status = workflowStatusStopped
		} else {
			status = workflowStatusFailed
		}
		errText = err.Error()
	}
	status, errText = e.registry.finish(runID, status, result.Snapshot, errText)
	if statusErr := persistWorkflowRunStatus(exec.RunDir, status, errText); statusErr != nil {
		e.tell(fmt.Sprintf("Workflow %s status persistence failed: %v", runID, statusErr))
	}
	if status == workflowStatusStopped {
		if strings.TrimSpace(errText) == "" {
			errText = "stop requested"
		}
		e.tell(fmt.Sprintf("Workflow %s stopped: %s", runID, errText))
		return
	}
	if err != nil {
		e.tell(fmt.Sprintf("Workflow %s %s: %v", runID, status, err))
		return
	}
	e.tell(formatWorkflowCompletion(result))
}

func formatWorkflowCompletion(result runResult) string {
	body := strings.TrimSpace(renderWorkflowResult(result.Result))
	if body == "" {
		body = "(workflow returned no result)"
	}
	text := fmt.Sprintf("Workflow %s completed with %d agent(s).", result.Meta.Name, result.Snapshot.AgentCount)
	if flow := workflowExecutionFlowText(result.Snapshot); flow != "" {
		text += "\n\n## Execution flow\n\n" + flow
	}
	text += "\n\n## Final result\n\n" + body
	if result.Snapshot.ScriptPath != "" {
		text += "\n\nScript: " + result.Snapshot.ScriptPath
	}
	return text
}

func workflowExecutionFlowText(snapshot workflowSnapshot) string {
	if len(snapshot.Agents) == 0 {
		return ""
	}
	var b strings.Builder
	phases := snapshot.PhaseSummaries
	if len(phases) == 0 {
		phases = computePhaseSummaries(snapshot.Phases, snapshot.Agents, time.Now())
	}
	seenAgents := map[int]bool{}
	if len(phases) > 0 {
		for _, phase := range phases {
			title := strings.TrimSpace(phase.Title)
			if title == "" {
				title = "(no phase)"
			}
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "- %s: %d agent(s), %d done, %d running, %d errors", title, phase.AgentCount, phase.DoneCount, phase.RunningCount, phase.ErrorCount)
			if phase.DurationMs > 0 {
				fmt.Fprintf(&b, ", durationMs=%d", phase.DurationMs)
			}
			b.WriteByte('\n')
			for _, agent := range snapshot.Agents {
				if workflowAgentPhaseKey(agent.Phase) != workflowAgentPhaseKey(phase.Title) {
					continue
				}
				seenAgents[agent.ID] = true
				b.WriteString(workflowExecutionAgentLine(agent))
			}
		}
	}
	for _, agent := range snapshot.Agents {
		if seenAgents[agent.ID] {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("- (unphased)\n")
		}
		b.WriteString(workflowExecutionAgentLine(agent))
	}
	return strings.TrimRight(b.String(), "\n")
}

func workflowExecutionAgentLine(agent agentSnapshot) string {
	label := strings.TrimSpace(agent.Label)
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	line := fmt.Sprintf("  - #%d [%s] %s", agent.ID, agent.Status, label)
	if agent.DurationMs > 0 {
		line += fmt.Sprintf(" durationMs=%d", agent.DurationMs)
	}
	if agent.EstimatedTokens > 0 {
		line += fmt.Sprintf(" estimatedTokens=%d", agent.EstimatedTokens)
	}
	if agent.FailedToolCalls > 0 {
		line += fmt.Sprintf(" failedTools=%d", agent.FailedToolCalls)
	}
	if len(agent.RecentToolCalls) > 0 {
		line += fmt.Sprintf(" recentTools=%d", len(agent.RecentToolCalls))
	}
	if strings.TrimSpace(agent.Error) != "" {
		line += " error=" + preview(agent.Error, 120)
	} else if strings.TrimSpace(agent.ResultPreview) != "" {
		line += " result=" + preview(agent.ResultPreview, 120)
	}
	return line + "\n"
}

func workflowAgentPhaseKey(phase string) string {
	phase = strings.TrimSpace(phase)
	if phase == "" {
		return ""
	}
	return phase
}

// renderWorkflowResult turns a workflow's returned value into readable text
// instead of escaped JSON. Object keys become Markdown section headers, arrays
// become numbered items, and string leaves print verbatim (real newlines, not
// "\n" escapes). Scalars/unknown shapes fall back to compact JSON. This is what
// the user reads in the TUI, so it must not be a json.Marshal dump.
func renderWorkflowResult(v any) string {
	var b strings.Builder
	renderResultValue(&b, v, 0)
	return b.String()
}

func renderResultValue(b *strings.Builder, v any, depth int) {
	switch val := v.(type) {
	case nil:
		b.WriteString("(none)")
	case string:
		b.WriteString(strings.TrimSpace(val))
	case map[string]any:
		for i, k := range orderedResultKeys(val) {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(strings.Repeat("#", min(depth+2, 6)) + " " + k + "\n\n")
			renderResultValue(b, val[k], depth+1)
		}
	case []any:
		for i, item := range val {
			if i > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(b, "%d. ", i+1)
			renderResultValue(b, item, depth+1)
		}
	default:
		if data, err := json.Marshal(val); err == nil {
			b.Write(data)
		} else {
			fmt.Fprint(b, val)
		}
	}
}

// orderedResultKeys lists object keys with the most answer-like fields first
// (so a deep-research "report" shows before its "scope"/"findings" scaffolding),
// then the rest alphabetically for stable output.
func orderedResultKeys(m map[string]any) []string {
	preferred := []string{"report", "answer", "summary", "conclusion", "result", "output", "final"}
	seen := make(map[string]bool, len(m))
	var head []string
	for _, k := range preferred {
		if _, ok := m[k]; ok {
			head = append(head, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(head, rest...)
}

func loadWorkflowScript(args map[string]any, cwd, agentDir string) (string, string, error) {
	rawScript := strings.TrimSpace(stringArg(args, "script"))
	rawPath := strings.TrimSpace(stringArg(args, "script_path"))
	rawName := strings.TrimSpace(stringArg(args, "name"))
	count := 0
	for _, value := range []string{rawScript, rawPath, rawName} {
		if value != "" {
			count++
		}
	}
	if count != 1 {
		return "", "", fmt.Errorf("exactly one of script, script_path, or name is required")
	}
	if rawScript != "" {
		script := normalizeScript(rawScript)
		if script == "" {
			return "", "", fmt.Errorf("script is required")
		}
		return script, "", nil
	}
	var path string
	if rawPath != "" {
		path = resolveWorkflowScriptPath(cwd, rawPath)
	} else {
		resolved, err := resolveSavedWorkflowName(cwd, agentDir, rawName)
		if err != nil {
			return "", "", err
		}
		path = resolved
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read workflow script %s: %w", path, err)
	}
	script := normalizeScript(string(data))
	if script == "" {
		return "", "", fmt.Errorf("workflow script %s is empty", path)
	}
	return script, path, nil
}

func loadNestedWorkflowScript(ref, cwd, agentDir string) (string, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("workflow name or path is required")
	}
	args := map[string]any{}
	if strings.ContainsAny(ref, `/\`) || filepath.Ext(ref) == ".js" {
		args["script_path"] = ref
	} else {
		args["name"] = ref
	}
	return loadWorkflowScript(args, cwd, agentDir)
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func resolveWorkflowScriptPath(cwd, path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if strings.TrimSpace(cwd) == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

func resolveSavedWorkflowName(cwd, agentDir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("workflow name is required")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("workflow name must be a simple file name")
	}
	if filepath.Ext(name) == "" {
		name += ".js"
	}
	var candidates []string
	for _, dir := range projectWorkflowDirs(cwd) {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	for _, dir := range userWorkflowDirs(agentDir) {
		candidates = append(candidates, filepath.Join(dir, name))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("workflow %q not found", strings.TrimSuffix(name, ".js"))
	}
	return "", fmt.Errorf("workflow %q not found in %s", strings.TrimSuffix(name, ".js"), strings.Join(candidates, ", "))
}

func projectWorkflowDirs(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	start := filepath.Clean(cwd)
	root := findWorkflowProjectRoot(start)
	var dirs []string
	for dir := start; ; dir = filepath.Dir(dir) {
		dirs = append(dirs, filepath.Join(dir, ".claude", "workflows"))
		dirs = append(dirs, filepath.Join(dir, ".coding_agent", "workflows"))
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return dirs
}

func userWorkflowDirs(agentDir string) []string {
	agentDir = strings.TrimSpace(agentDir)
	if agentDir == "" {
		return nil
	}
	clean := filepath.Clean(agentDir)
	claudeDir := filepath.Join(filepath.Dir(clean), ".claude", "workflows")
	dirs := []string{claudeDir}
	agentWorkflowDir := filepath.Join(clean, "workflows")
	if agentWorkflowDir != claudeDir {
		dirs = append(dirs, agentWorkflowDir)
	}
	return dirs
}

func findWorkflowProjectRoot(cwd string) string {
	dir := filepath.Clean(cwd)
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(cwd)
		}
		dir = parent
	}
}

func projectWorkflowSaveDir(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", fmt.Errorf("project workflow save requires an active cwd")
	}
	start := filepath.Clean(cwd)
	root := findWorkflowProjectRoot(start)
	for dir := start; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, ".claude", "workflows")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return filepath.Join(root, ".claude", "workflows"), nil
}

func persistWorkflowScript(sessionDir, script string) (string, string, error) {
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return "", "", nil
	}
	runID := newWorkflowRunID()
	runDir := filepath.Join(sessionDir, "extensions", "workflow", "runs", sanitizeRunID(runID))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", "", fmt.Errorf("persist workflow script: %w", err)
	}
	path := filepath.Join(runDir, "script.js")
	if err := os.WriteFile(path, []byte(script+"\n"), 0o600); err != nil {
		return "", "", fmt.Errorf("persist workflow script: %w", err)
	}
	return path, runDir, nil
}

func persistWorkflowSnapshot(runDir string, snapshot workflowSnapshot) error {
	runDir = strings.TrimSpace(runDir)
	if runDir == "" {
		return nil
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("persist workflow snapshot: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "snapshot.json"), append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("persist workflow snapshot: %w", err)
	}
	return nil
}

var workflowRunIDUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func newWorkflowRunID() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z")
}

func workflowRunID(runDir string) string {
	runDir = strings.TrimSpace(runDir)
	if runDir == "" {
		return sanitizeRunID(newWorkflowRunID())
	}
	return sanitizeRunID(filepath.Base(runDir))
}

func sanitizeRunID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "run"
	}
	return workflowRunIDUnsafe.ReplaceAllString(value, "-")
}

func normalizeScript(script string) string {
	text := strings.TrimSpace(script)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 2 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			text = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}
	return text
}

func decodeConcurrency(raw any, fallback int) (int, error) {
	if fallback <= 0 {
		fallback = 4
	}
	if raw == nil {
		return fallback, nil
	}
	var n int
	switch v := raw.(type) {
	case int:
		n = v
	case int64:
		n = int(v)
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("concurrency must be an integer >= 1")
		}
		n = int(v)
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("concurrency must be an integer >= 1")
		}
		n = int(i)
	default:
		return 0, fmt.Errorf("concurrency must be an integer >= 1")
	}
	if n < 1 {
		return 0, fmt.Errorf("concurrency must be an integer >= 1")
	}
	if n > 16 {
		n = 16
	}
	return n, nil
}

func decodeBool(raw any, name string) (bool, error) {
	if raw == nil {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return value, nil
}

func decodePositiveInt(raw any, name string) (int, error) {
	if raw == nil {
		return 0, nil
	}
	var n int
	switch v := raw.(type) {
	case int:
		n = v
	case int64:
		n = int(v)
	case float64:
		if v != float64(int(v)) {
			return 0, fmt.Errorf("%s must be an integer >= 1", name)
		}
		n = int(v)
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer >= 1", name)
		}
		n = int(i)
	default:
		return 0, fmt.Errorf("%s must be an integer >= 1", name)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s must be an integer >= 1", name)
	}
	return n, nil
}

func textResult(text string, isErr bool, details any) types.ToolResult {
	return types.ToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		Details: details,
		IsError: isErr,
	}
}
