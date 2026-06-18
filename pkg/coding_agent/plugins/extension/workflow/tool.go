package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		"Execute a deterministic Lua workflow that orchestrates forked subagents with agent(), parallel(), and pipeline().",
		"Use only when the user explicitly asks for a workflow, fan-out, or multi-agent orchestration.",
		"The script must call meta({name=..., description=...}) before phase/log/agent/parallel/pipeline and must call at least one agent.",
		"This tool only starts workflow runs; do not pass action, status, id, run_id, or agent_id. Inspect or control runs with slash commands such as /workflows show <run-id>, /workflows agent <run-id> <agent-id>, /workflows stop <run-id>, or the /workflows TUI panel.",
	}, " ")
}

func (t *workflowTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"script": map[string]any{
				"type":        "string",
				"description": "Raw Lua workflow script, with no Markdown fences. Exactly one of script, script_path, or name is required.",
			},
			"script_path": map[string]any{
				"type":        "string",
				"description": "Path to a Lua workflow script. Relative paths resolve from the current cwd.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Saved workflow name. Looks in project .coding_agent/.claude workflows, then user workflows.",
			},
			"args": map[string]any{
				"description": "Optional JSON value exposed to the Lua script as global args.",
			},
			"concurrency": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional max concurrent agent runs for this workflow.",
			},
			"budget": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional token budget exposed to Lua as budget.total and budget.remaining().",
			},
			"async": map[string]any{
				"type":        "boolean",
				"description": "Run the workflow in the background and return immediately with a run id. Use /workflows show <run-id>, /workflows agent <run-id> <agent-id>, /workflows stop <run-id>, or the /workflows TUI panel to inspect or control it. Do not call this tool with action/status/id fields.",
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
		text += fmt.Sprintf("\nUse /workflows show %s to inspect progress or /workflows stop %s to stop it.", runID, runID)
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
	script, sourcePath, err := loadWorkflowScript(args, e.api.Cwd(), e.api.AgentDir())
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
	if sourcePath != "" {
		args["script_path"] = sourcePath
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
	data, err := json.MarshalIndent(result.Result, "", "  ")
	if err != nil {
		data = []byte(fmt.Sprint(result.Result))
	}
	text := fmt.Sprintf("Workflow %s completed with %d agent(s).\n\nResult:\n%s",
		result.Meta.Name, result.Snapshot.AgentCount, string(data))
	if result.Snapshot.ScriptPath != "" {
		text += "\n\nScript: " + result.Snapshot.ScriptPath
	}
	return text
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
	if strings.ContainsAny(ref, `/\`) || filepath.Ext(ref) == ".lua" {
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
		name += ".lua"
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
		return "", fmt.Errorf("workflow %q not found", strings.TrimSuffix(name, ".lua"))
	}
	return "", fmt.Errorf("workflow %q not found in %s", strings.TrimSuffix(name, ".lua"), strings.Join(candidates, ", "))
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
	path := filepath.Join(runDir, "script.lua")
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
