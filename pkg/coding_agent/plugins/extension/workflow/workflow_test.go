package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

type fakeAPI struct {
	mu             sync.Mutex
	tools          []types.Tool
	commands       map[string]extension.CommandHandler
	handlers       map[string][]extension.EventHandler
	notifies       []string
	cwd            string
	sessionDir     string
	agentDir       string
	calls          []extension.ForkOptions
	responder      func(context.Context, extension.ForkOptions) (string, error)
	confirms       []workflowConfirmCall
	confirmFn      func(string, string, bool) bool
	selects        []workflowSelectCall
	selectFn       func(string, []string) string
	permissionMode string
	active         int
	maxActive      int
}

type workflowConfirmCall struct {
	Title      string
	Body       string
	DefaultYes bool
}

type workflowSelectCall struct {
	Title   string
	Options []string
}

func (f *fakeAPI) RegisterTool(t types.Tool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = append(f.tools, t)
}
func (f *fakeAPI) RegisterCommand(name string, _ string, h extension.CommandHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commands == nil {
		f.commands = map[string]extension.CommandHandler{}
	}
	f.commands[name] = h
}
func (f *fakeAPI) AddHook(extension.ToolHook) {}
func (f *fakeAPI) On(event string, h extension.EventHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handlers == nil {
		f.handlers = map[string][]extension.EventHandler{}
	}
	f.handlers[event] = append(f.handlers[event], h)
}
func (f *fakeAPI) SendMessage(string) error         { return nil }
func (f *fakeAPI) SetActiveTools([]string)          {}
func (f *fakeAPI) SetModel(string, string) error    { return nil }
func (f *fakeAPI) GetCommands() []extension.Command { return nil }
func (f *fakeAPI) SessionID() string                { return "session" }
func (f *fakeAPI) SessionDir() string               { return f.sessionDir }
func (f *fakeAPI) AgentDir() string                 { return f.agentDir }
func (f *fakeAPI) Cwd() string {
	if f.cwd != "" {
		return f.cwd
	}
	return "/repo"
}
func (f *fakeAPI) IsIdle() bool                                                  { return true }
func (f *fakeAPI) HasPendingMessages() bool                                      { return false }
func (f *fakeAPI) PermissionMode() string                                        { return f.permissionMode }
func (f *fakeAPI) SendFollowUpMessage(string) error                              { return nil }
func (f *fakeAPI) SendMessageWithOptions(string, extension.MessageOptions) error { return nil }
func (f *fakeAPI) Notify(_ string, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifies = append(f.notifies, text)
}
func (f *fakeAPI) Confirm(title, body string, defaultYes bool) bool {
	f.mu.Lock()
	f.confirms = append(f.confirms, workflowConfirmCall{Title: title, Body: body, DefaultYes: defaultYes})
	fn := f.confirmFn
	f.mu.Unlock()
	if fn != nil {
		return fn(title, body, defaultYes)
	}
	return true
}
func (f *fakeAPI) Select(title string, options []string) string {
	f.mu.Lock()
	f.selects = append(f.selects, workflowSelectCall{Title: title, Options: append([]string(nil), options...)})
	fn := f.selectFn
	f.mu.Unlock()
	if fn != nil {
		return fn(title, options)
	}
	if len(options) == 0 {
		return ""
	}
	return options[0]
}
func (f *fakeAPI) BackgroundTasks() []extension.TaskSnapshot { return nil }
func (f *fakeAPI) InterruptBackgroundTask(string, string) (extension.TaskSnapshot, bool) {
	return extension.TaskSnapshot{}, false
}
func (f *fakeAPI) ForkSession(ctx context.Context, opts extension.ForkOptions) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opts)
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if f.responder != nil {
		return f.responder(ctx, opts)
	}
	return "result:" + opts.Name, nil
}

func (f *fakeAPI) emit(event string, ev types.Event) {
	f.mu.Lock()
	handlers := append([]extension.EventHandler(nil), f.handlers[event]...)
	f.mu.Unlock()
	for _, h := range handlers {
		h(ev)
	}
}

func emitWorkflowChildUsage(api *fakeAPI, taskID string, input, output int) {
	api.emit("subagent_child_usage", types.Event{
		Type:   types.EventType("subagent_child_usage"),
		TaskID: taskID,
		Messages: []types.AgentMessage{
			&types.AssistantMessage{
				Role:  types.RoleAssistant,
				Usage: types.AgentUsage{Input: input, Output: output, TotalTokens: input + output},
			},
		},
	})
}

func (f *fakeAPI) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeAPI) call(i int) extension.ForkOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

func (f *fakeAPI) maxConcurrency() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}

func (f *fakeAPI) confirmCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.confirms)
}

func (f *fakeAPI) lastConfirm() workflowConfirmCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.confirms) == 0 {
		return workflowConfirmCall{}
	}
	return f.confirms[len(f.confirms)-1]
}

func (f *fakeAPI) selectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.selects)
}

func (f *fakeAPI) lastSelect() workflowSelectCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.selects) == 0 {
		return workflowSelectCall{}
	}
	return f.selects[len(f.selects)-1]
}

func (f *fakeAPI) command(name string) extension.CommandHandler {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commands[name]
}

func (f *fakeAPI) lastNotify() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.notifies) == 0 {
		return ""
	}
	return f.notifies[len(f.notifies)-1]
}

func (f *fakeAPI) waitNotifyContaining(t *testing.T, text string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, notify := range f.notifies {
			if strings.Contains(notify, text) {
				f.mu.Unlock()
				return notify
			}
		}
		f.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("notification containing %q not found; last notify = %q", text, f.lastNotify())
	return ""
}

func (f *fakeAPI) waitCallCount(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.callCount() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("call count = %d, want at least %d", f.callCount(), n)
}

func clearWorkflowDisableEnv(t *testing.T) {
	t.Helper()
	t.Setenv("MODU_CODE_DISABLE_WORKFLOWS", "")
	t.Setenv("CLAUDE_CODE_DISABLE_WORKFLOWS", "")
}

func TestWorkflowToolGuidesRunManagementThroughWorkflowsCommand(t *testing.T) {
	tool := newTool(New())
	description := tool.Description()
	for _, want := range []string{
		"only starts workflow runs",
		"do not pass action",
		"/workflows TUI cockpit first",
		"/workflows feed <run-id>",
		"/workflows guide <run-id>",
		"/workflows map <run-id>",
		"/workflows show <run-id>",
		"/workflows agent <run-id> <agent-id>",
		"/workflows stop <run-id>",
		"Result/Script rows",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("description missing %q:\n%s", want, description)
		}
	}
	params := tool.Parameters().(map[string]any)
	props := params["properties"].(map[string]any)
	async := props["async"].(map[string]any)
	asyncDescription := async["description"].(string)
	for _, want := range []string{
		"/workflows TUI cockpit first",
		"/workflows feed <run-id>",
		"/workflows guide <run-id>",
		"/workflows map <run-id>",
		"/workflows show <run-id>",
		"Do not call this tool with action/status/id fields",
	} {
		if !strings.Contains(asyncDescription, want) {
			t.Fatalf("async description missing %q:\n%s", want, asyncDescription)
		}
	}
}

func TestExtensionRegistersWorkflowTool(t *testing.T) {
	clearWorkflowDisableEnv(t)
	api := &fakeAPI{}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(api.tools) != 1 || api.tools[0].Name() != "workflow" {
		t.Fatalf("registered tools = %#v", api.tools)
	}
	if api.command("workflows") == nil {
		t.Fatal("expected workflows command")
	}
	if api.command("deep-research") == nil {
		t.Fatal("expected deep-research command")
	}
}

func TestDeepResearchCommandStartsBundledWorkflow(t *testing.T) {
	clearWorkflowDisableEnv(t)
	sessionDir := t.TempDir()
	api := &fakeAPI{sessionDir: sessionDir}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		return "result:" + opts.Name, nil
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cmd := api.command("deep-research")
	if cmd == nil {
		t.Fatal("missing deep-research command")
	}
	if err := cmd(""); err != nil {
		t.Fatalf("/deep-research empty: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Usage: /deep-research <question>") {
		t.Fatalf("empty notify = %q", got)
	}
	if err := cmd("What changed in Node permissions?"); err != nil {
		t.Fatalf("/deep-research: %v", err)
	}
	started := api.waitNotifyContaining(t, "Deep research workflow started in background")
	if !strings.Contains(started, "Run: ") || !strings.Contains(started, "Script: ") || !strings.Contains(started, "Open /workflows for the cockpit") {
		t.Fatalf("started notify = %q", started)
	}
	done := api.waitNotifyContaining(t, "Workflow deep_research completed")
	if !strings.Contains(done, "## Execution flow") || !strings.Contains(done, "ResultPreview:") || !strings.Contains(done, "result:report") || !strings.Contains(done, "Next:") || !strings.Contains(done, "/workflows guide ") {
		t.Fatalf("completion notify = %q", done)
	}
	if strings.Contains(done, "## Final result") || strings.Contains(done, "## report\n\nresult:report") {
		t.Fatalf("completion notify should not expand full result: %q", done)
	}
	if api.callCount() != 6 {
		t.Fatalf("deep research call count = %d, want 6", api.callCount())
	}
	selectCall := api.lastSelect()
	if !strings.Contains(selectCall.Title, "Allow workflow run?") || !strings.Contains(selectCall.Title, "Workflow: deep_research") || !strings.Contains(selectCall.Title, "- Scope") || !strings.Contains(selectCall.Title, "Script preview:") {
		t.Fatalf("approval select = %+v", selectCall)
	}
	if len(selectCall.Options) != 4 || selectCall.Options[0] != workflowApprovalRunOnce || selectCall.Options[1] != workflowApprovalAlways || selectCall.Options[2] != workflowApprovalViewRaw || selectCall.Options[3] != workflowApprovalCancel {
		t.Fatalf("approval options = %+v", selectCall.Options)
	}
}

func TestWorkflowApprovalDenialSkipsToolExecution(t *testing.T) {
	clearWorkflowDisableEnv(t)
	api := &fakeAPI{
		sessionDir: t.TempDir(),
		selectFn: func(title string, options []string) string {
			return workflowApprovalCancel
		},
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{
		"script": `
meta({ name: "approval_case", description: "approval check" })
phase("Review")
return agent("inspect", { label: "inspect" })
`,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*types.TextContent).Text, "cancelled before start") {
		t.Fatalf("expected cancelled error result, got %+v", res)
	}
	if api.callCount() != 0 {
		t.Fatalf("fork count = %d, want 0", api.callCount())
	}
	selectCall := api.lastSelect()
	if !strings.Contains(selectCall.Title, "Allow workflow run?") || !strings.Contains(selectCall.Title, "Workflow: approval_case") || !strings.Contains(selectCall.Title, "Description: approval check") || !strings.Contains(selectCall.Title, "- Review") || !strings.Contains(selectCall.Title, "return agent") {
		t.Fatalf("approval select = %+v", selectCall)
	}
}

func TestWorkflowApprovalAlwaysSkipsFuturePromptForSameProjectAndScript(t *testing.T) {
	clearWorkflowDisableEnv(t)
	script := `
meta({ name: "always_case", description: "remember approval" })
phase("Remember")
return agent("inspect", { label: "inspect" })
`
	api := &fakeAPI{
		cwd:        t.TempDir(),
		agentDir:   t.TempDir(),
		sessionDir: t.TempDir(),
		selectFn: func(title string, options []string) string {
			return workflowApprovalAlways
		},
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	for i := 0; i < 2; i++ {
		res, err := tool.Execute(context.Background(), fmt.Sprintf("wf-%d", i), map[string]any{"script": script}, nil)
		if err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
		if res.IsError {
			t.Fatalf("unexpected error result %d: %+v", i, res)
		}
	}
	if api.selectCount() != 1 {
		t.Fatalf("select count = %d, want 1", api.selectCount())
	}
	if api.callCount() != 2 {
		t.Fatalf("fork count = %d, want 2", api.callCount())
	}
	storePath := filepath.Join(api.agentDir, "workflow_approvals.json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("read approval store: %v", err)
	}
	if !strings.Contains(string(data), "always_case") || !strings.Contains(string(data), "scriptHash") {
		t.Fatalf("approval store = %s", string(data))
	}
}

func TestWorkflowApprovalBypassPermissionModeSkipsPrompt(t *testing.T) {
	clearWorkflowDisableEnv(t)
	api := &fakeAPI{
		permissionMode: "bypassPermissions",
		selectFn: func(title string, options []string) string {
			t.Fatal("bypassPermissions should not prompt for workflow approval")
			return workflowApprovalCancel
		},
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-bypass", map[string]any{
		"script": `
meta({ name: "bypass_case", description: "bypass approval" })
phase("Run")
return agent("inspect", { label: "inspect" })
`,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if api.selectCount() != 0 {
		t.Fatalf("select count = %d, want 0", api.selectCount())
	}
	if api.callCount() != 1 {
		t.Fatalf("fork count = %d, want 1", api.callCount())
	}
}

func TestWorkflowApprovalAutoPermissionModeRemembersRunOnce(t *testing.T) {
	clearWorkflowDisableEnv(t)
	script := `
meta({ name: "auto_case", description: "auto remember" })
phase("Auto")
return agent("inspect", { label: "inspect" })
`
	api := &fakeAPI{
		cwd:            t.TempDir(),
		agentDir:       t.TempDir(),
		permissionMode: "auto",
		selectFn: func(title string, options []string) string {
			return workflowApprovalRunOnce
		},
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	for i := 0; i < 2; i++ {
		res, err := tool.Execute(context.Background(), fmt.Sprintf("wf-auto-%d", i), map[string]any{"script": script}, nil)
		if err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
		if res.IsError {
			t.Fatalf("unexpected error result %d: %+v", i, res)
		}
	}
	if api.selectCount() != 1 {
		t.Fatalf("select count = %d, want 1", api.selectCount())
	}
	if api.callCount() != 2 {
		t.Fatalf("fork count = %d, want 2", api.callCount())
	}
	storePath := filepath.Join(api.agentDir, "workflow_approvals.json")
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("read approval store: %v", err)
	}
	if !strings.Contains(string(data), "auto_case") || !strings.Contains(string(data), "scriptHash") {
		t.Fatalf("approval store = %s", string(data))
	}
}

func TestWorkflowApprovalViewRawThenRunOnce(t *testing.T) {
	clearWorkflowDisableEnv(t)
	script := `
meta({ name: "raw_case", description: "raw script view" })
phase("Raw")
return agent("inspect raw", { label: "inspect raw" })
`
	var selections int
	api := &fakeAPI{
		sessionDir: t.TempDir(),
		selectFn: func(title string, options []string) string {
			selections++
			if selections == 1 {
				return workflowApprovalViewRaw
			}
			return workflowApprovalRunOnce
		},
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-raw", map[string]any{"script": script}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if api.selectCount() != 2 {
		t.Fatalf("select count = %d, want 2", api.selectCount())
	}
	if api.callCount() != 1 {
		t.Fatalf("fork count = %d, want 1", api.callCount())
	}
	raw := api.waitNotifyContaining(t, "Workflow raw script: raw_case")
	if !strings.Contains(raw, "```js") || !strings.Contains(raw, "inspect raw") {
		t.Fatalf("raw notify = %q", raw)
	}
}

func TestSavedWorkflowApprovalDenialSkipsBackgroundRun(t *testing.T) {
	clearWorkflowDisableEnv(t)
	cwd := t.TempDir()
	sessionDir := t.TempDir()
	projectDir := filepath.Join(cwd, ".coding_agent", "workflows")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "review.js"), []byte(`
meta({ name: "saved_review", description: "saved approval" })
phase("Saved")
return agent("review", { label: "review" })
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{
		cwd:        cwd,
		sessionDir: sessionDir,
		selectFn: func(title string, options []string) string {
			return workflowApprovalCancel
		},
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cmd := api.command("review")
	if cmd == nil {
		t.Fatal("missing saved workflow command")
	}
	if err := cmd(""); err != nil {
		t.Fatalf("/review: %v", err)
	}
	if api.callCount() != 0 {
		t.Fatalf("fork count = %d, want 0", api.callCount())
	}
	if got := api.lastNotify(); !strings.Contains(got, "Workflow review cancelled before start") {
		t.Fatalf("notify = %q", got)
	}
	selectCall := api.lastSelect()
	if !strings.Contains(selectCall.Title, "Workflow: saved_review") || !strings.Contains(selectCall.Title, "Source: /review") || !strings.Contains(selectCall.Title, "- Saved") {
		t.Fatalf("approval select = %+v", selectCall)
	}
}

func TestExtensionApplyConfigKnownKeys(t *testing.T) {
	ext := New()
	err := ext.ApplyConfig(map[string]any{
		"disabled":    true,
		"concurrency": 8,
		"max_agents":  42,
	})
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if !ext.cfg.Disabled {
		t.Fatal("expected Disabled=true")
	}
	if ext.cfg.Concurrency != 8 {
		t.Fatalf("Concurrency = %d", ext.cfg.Concurrency)
	}
	if ext.cfg.MaxAgents != 42 {
		t.Fatalf("MaxAgents = %d", ext.cfg.MaxAgents)
	}
}

func TestExtensionApplyConfigUnknownKeyErrors(t *testing.T) {
	ext := New()
	err := ext.ApplyConfig(map[string]any{"unknown": "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}

func TestExtensionApplyConfigTypeMismatchErrors(t *testing.T) {
	ext := New()
	err := ext.ApplyConfig(map[string]any{"disabled": "yes"})
	if err == nil || !strings.Contains(err.Error(), "disabled must be bool") {
		t.Fatalf("expected bool type error, got %v", err)
	}
	err = ext.ApplyConfig(map[string]any{"max_agents": 0})
	if err == nil || !strings.Contains(err.Error(), "max_agents must be positive int") {
		t.Fatalf("expected positive-int error, got %v", err)
	}
}

func TestExtensionDisabledByConfigRegistersNothing(t *testing.T) {
	clearWorkflowDisableEnv(t)
	api := &fakeAPI{}
	ext := New()
	if err := ext.ApplyConfig(map[string]any{"disabled": true}); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(api.tools) != 0 {
		t.Fatalf("registered tools = %#v", api.tools)
	}
	if api.command("workflows") != nil {
		t.Fatal("did not expect workflows command")
	}
}

func TestExtensionDisabledByEnvRegistersNothing(t *testing.T) {
	clearWorkflowDisableEnv(t)
	t.Setenv("MODU_CODE_DISABLE_WORKFLOWS", "1")
	api := &fakeAPI{}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(api.tools) != 0 {
		t.Fatalf("registered tools = %#v", api.tools)
	}
	if api.command("workflows") != nil {
		t.Fatal("did not expect workflows command")
	}
}

func TestRunWorkflowRecordsMetaPhaseLogAndResult(t *testing.T) {
	api := &fakeAPI{}
	var updates []types.ToolResult
	r := newRunner(api, runOptions{
		Cwd:  "/repo",
		Args: map[string]any{"area": "pkg"},
		OnUpdate: func(partial types.ToolResult) {
			updates = append(updates, partial)
		},
	})
	result, err := r.run(context.Background(), `
meta({ name: "demo", description: "dynamic phases" });
phase("Scan " + args.area);
log("started");
const out = await agent("inspect " + args.area, { label: "scan" });
return { ok: true, out: out, cwd: process.cwd() };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Meta.Name != "demo" || result.Snapshot.Name != "demo" {
		t.Fatalf("bad meta/snapshot: %+v", result)
	}
	if len(result.Snapshot.Phases) != 1 || result.Snapshot.Phases[0] != "Scan pkg" {
		t.Fatalf("phases = %#v", result.Snapshot.Phases)
	}
	if len(result.Snapshot.Logs) != 1 || result.Snapshot.Logs[0] != "started" {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
	if len(updates) == 0 {
		t.Fatal("expected tool updates")
	}
	if got := result.Result.(map[string]any)["out"]; got != "result:scan" {
		t.Fatalf("result out = %#v", got)
	}
}

func TestRunWorkflowRejectsMissingMetaAndMissingAgent(t *testing.T) {
	api := &fakeAPI{}
	if _, err := newRunner(api, runOptions{}).run(context.Background(), `phase("x"); return {};`); err == nil || !strings.Contains(err.Error(), "meta") {
		t.Fatalf("expected missing meta error, got %v", err)
	}
	if _, err := newRunner(api, runOptions{}).run(context.Background(), `meta({name:"x", description:"y"}); return {};`); err == nil || !strings.Contains(err.Error(), "must call agent") {
		t.Fatalf("expected missing agent error, got %v", err)
	}
}

func TestRunWorkflowSandboxHidesUnsafeLibraries(t *testing.T) {
	api := &fakeAPI{}
	result, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "sandbox", description: "check globals" });
await agent("x", { label: "x" });
let requireBlocked = false;
try { require("fs"); } catch (e) { requireBlocked = true; }
return {
  process_exit_missing: (typeof process.exit) === "undefined",
  no_global_fs: (typeof globalThis.fs) === "undefined",
  require_blocked: requireBlocked,
};
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["process_exit_missing"] != true || got["no_global_fs"] != true || got["require_blocked"] != true {
		t.Fatalf("sandbox leak: %#v", got)
	}
}

func TestAgentMapsOptionsToForkSession(t *testing.T) {
	api := &fakeAPI{}
	_, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "map", description: "map opts" });
phase("Review");
return await agent("inspect", {
  label: "repo scan",
  model: "model-a",
  cwd: "pkg/coding_agent",
  isolation: "worktree",
  tools: ["read", "grep"],
  disallowedTools: ["bash"],
  permissionMode: "read-only",
  maxTurns: 3,
  thinking: "low",
  skills: ["codebase"],
  memoryScope: "project",
});
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if api.callCount() != 1 {
		t.Fatalf("call count = %d", api.callCount())
	}
	call := api.call(0)
	if call.Name != "repo scan" || call.Model != "model-a" || call.Cwd != "pkg/coding_agent" || call.Isolation != "worktree" {
		t.Fatalf("bad mapped call: %+v", call)
	}
	if call.PermissionMode != "read-only" || call.MaxTurns != 3 || call.ThinkingLevel != "low" || call.MemoryScope != "project" {
		t.Fatalf("bad policy call: %+v", call)
	}
	if strings.Contains(call.Task, "Workflow phase: Review") == false || strings.Contains(call.Task, "Task label: repo scan") == false {
		t.Fatalf("task missing workflow context:\n%s", call.Task)
	}
	if got := strings.Join(call.AllowedTools, ","); got != "read,grep" {
		t.Fatalf("AllowedTools = %q", got)
	}
	if got := strings.Join(call.DisallowedTools, ","); got != "bash" {
		t.Fatalf("DisallowedTools = %q", got)
	}
	if got := strings.Join(call.Skills, ","); got != "codebase" {
		t.Fatalf("Skills = %q", got)
	}
}

func TestAgentSchemaReturnsValidatedTableAndAddsPromptContract(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		return "final:\n{\"ok\":true,\"count\":2}", nil
	}
	result, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "schema", description: "structured output" });
const out = await agent("return status", {
  label: "structured",
  phase: "Structured",
  schema: {
    type: "object",
    required: ["ok", "count"],
    properties: {
      ok: { type: "boolean" },
      count: { type: "integer" },
    },
  },
});
return { ok: out.ok, count: out.count };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["ok"] != true || got["count"] != int64(2) {
		t.Fatalf("structured result = %#v", got)
	}
	if api.callCount() != 1 {
		t.Fatalf("call count = %d", api.callCount())
	}
	task := api.call(0).Task
	if !strings.Contains(task, "Final output contract") || !strings.Contains(task, `"required"`) {
		t.Fatalf("task missing schema contract:\n%s", task)
	}
	if result.Snapshot.Agents[0].ResultPreview != `{"count":2,"ok":true}` {
		t.Fatalf("preview = %q", result.Snapshot.Agents[0].ResultPreview)
	}
	agent := result.Snapshot.Agents[0]
	if agent.StartedAt.IsZero() || agent.EndedAt.IsZero() {
		t.Fatalf("agent timing missing: %+v", agent)
	}
	if agent.EndedAt.Before(agent.StartedAt) || agent.DurationMs < 0 {
		t.Fatalf("agent timing invalid: %+v", agent)
	}
	if agent.EstimatedTokens <= 0 {
		t.Fatalf("agent estimated tokens missing: %+v", agent)
	}
	if len(result.Snapshot.PhaseSummaries) != 1 {
		t.Fatalf("phase summaries = %+v", result.Snapshot.PhaseSummaries)
	}
	phase := result.Snapshot.PhaseSummaries[0]
	if phase.Title != "Structured" || phase.AgentCount != 1 || phase.DoneCount != 1 || phase.EstimatedTokens != agent.EstimatedTokens {
		t.Fatalf("phase summary = %+v, agent = %+v", phase, agent)
	}
}

func TestParallelSchemaFailureReturnsJSONNull(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		if strings.HasPrefix(opts.Name, "bad") {
			return `{"ok":"not bool"}`, nil
		}
		return `{"ok":true}`, nil
	}
	result, err := newRunner(api, runOptions{Concurrency: 2}).run(context.Background(), `
meta({ name: "schema_parallel", description: "structured output failures" });
const schema = {
  type: "object",
  required: ["ok"],
  properties: {
    ok: { type: "boolean" },
  },
};
const out = await parallel([
  () => agent("good", { label: "good", schema: schema }),
  () => agent("bad", { label: "bad", schema: schema }),
]);
return { good: out[0].ok, bad_is_null: out[1] === null };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["good"] != true || got["bad_is_null"] != true {
		t.Fatalf("result = %#v", got)
	}
	// retries bumped to 2: the bad agent errors on the initial attempt plus two retries.
	if result.Snapshot.ErrorCount != 3 {
		t.Fatalf("error count = %d", result.Snapshot.ErrorCount)
	}
	if len(result.Snapshot.Logs) != 3 || !strings.Contains(result.Snapshot.Logs[0], "structured output retry") || !strings.Contains(result.Snapshot.Logs[len(result.Snapshot.Logs)-1], "failed after 2 retries") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestAgentSchemaRetriesOnceBeforeReturningTable(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name == "structured" {
			return `{"ok":"not bool"}`, nil
		}
		if opts.Name == "structured retry 1" {
			if !strings.Contains(opts.Task, "Previous response:") || !strings.Contains(opts.Task, "Validation error:") {
				t.Fatalf("retry task missing corrective context:\n%s", opts.Task)
			}
			return `{"ok":true}`, nil
		}
		return "", nil
	}
	result, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "schema_retry", description: "retry structured output" });
const out = await agent("return status", {
  label: "structured",
  schema: {
    type: "object",
    required: ["ok"],
    properties: {
      ok: { type: "boolean" },
    },
  },
});
return { ok: out.ok };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["ok"] != true {
		t.Fatalf("result = %#v", got)
	}
	if api.callCount() != 2 {
		t.Fatalf("call count = %d", api.callCount())
	}
	if result.Snapshot.AgentCount != 2 || result.Snapshot.DoneCount != 1 || result.Snapshot.ErrorCount != 1 {
		t.Fatalf("snapshot = %+v", result.Snapshot)
	}
	if len(result.Snapshot.Logs) != 1 || !strings.Contains(result.Snapshot.Logs[0], "structured output retry 1") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestAgentRejectsInvalidSchemaDefinition(t *testing.T) {
	api := &fakeAPI{}
	_, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "bad_schema", description: "bad schema definition" });
return await agent("x", { label: "bad", schema: { type: "weird" } });
`)
	if err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("expected schema definition error, got %v", err)
	}
}

func TestAgentRejectsInvalidMemoryScope(t *testing.T) {
	api := &fakeAPI{}
	_, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "memory", description: "validate memory scope" });
return await agent("inspect", { label: "bad", memoryScope: "team" });
`)
	if err == nil || !strings.Contains(err.Error(), "memoryScope") {
		t.Fatalf("expected memoryScope error, got %v", err)
	}
}

func TestAgentRejectsNonPositiveMaxTurns(t *testing.T) {
	api := &fakeAPI{}
	_, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "turns", description: "validate max turns" });
return await agent("inspect", { label: "bad", maxTurns: 0 });
`)
	if err == nil || !strings.Contains(err.Error(), "maxTurns") {
		t.Fatalf("expected maxTurns error, got %v", err)
	}
}

func TestBudgetRemainingTracksSpendAndUnsetBudget(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		return "12345678", nil
	}
	result, err := newRunner(api, runOptions{BudgetTotal: 10}).run(context.Background(), `
meta({ name: "budget", description: "track budget" });
const before = budget.remaining();
await agent("spend", { label: "spend" });
return { total: budget.total, before: before, spent: budget.spent(), after: budget.remaining() };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["total"] != int64(10) || got["before"] != int64(10) || got["spent"] != int64(2) || got["after"] != int64(8) {
		t.Fatalf("budget result = %#v", got)
	}

	result, err = newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "budget_unset", description: "unset budget" });
await agent("spend", { label: "spend" });
return { unset: budget.total === null && budget.remaining() === null };
`)
	if err != nil {
		t.Fatalf("run unset: %v", err)
	}
	got = result.Result.(map[string]any)
	if got["unset"] != true {
		t.Fatalf("unset budget result = %#v", got)
	}
}

func TestBudgetUsesChildUsageWhenAvailable(t *testing.T) {
	api := &fakeAPI{}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		emitWorkflowChildUsage(api, opts.BubbleTaskID, 7, 5)
		return "x", nil
	}
	result, err := newRunner(api, runOptions{
		BudgetTotal: 20,
		RunDir:      filepath.Join(t.TempDir(), "usage-budget"),
		Activities:  ext.activities,
		Registry:    ext.registry,
	}).run(context.Background(), `
meta({ name: "usage_budget", description: "track real usage" });
const before = budget.remaining();
await agent("spend", { label: "spend" });
return { before: before, spent: budget.spent(), after: budget.remaining() };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["before"] != int64(20) || got["spent"] != int64(12) || got["after"] != int64(8) {
		t.Fatalf("budget result = %#v", got)
	}
	if len(result.Snapshot.Agents) != 1 || result.Snapshot.Agents[0].EstimatedTokens != 12 || result.Snapshot.Agents[0].TurnTokens != 12 {
		t.Fatalf("agent accounting = %+v", result.Snapshot.Agents)
	}
}

func TestBudgetStopsFurtherAgents(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		return "12345678", nil
	}
	result, err := newRunner(api, runOptions{BudgetTotal: 1}).run(context.Background(), `
meta({ name: "budget_stop", description: "stop after budget" });
const first = await agent("first", { label: "first" });
const second = await agent("second", { label: "second" });
return { first: first, second: second, spent: budget.spent(), remaining: budget.remaining() };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if api.callCount() != 1 {
		t.Fatalf("call count = %d", api.callCount())
	}
	got := result.Result.(map[string]any)
	if got["first"] != "12345678" || got["second"] != nil || got["spent"] != int64(1) || got["remaining"] != int64(0) {
		t.Fatalf("budget stop result = %#v", got)
	}
	if len(result.Snapshot.Logs) != 1 || !strings.Contains(result.Snapshot.Logs[0], "budget exhausted") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestBudgetStopsFurtherAgentsUsingChildUsage(t *testing.T) {
	api := &fakeAPI{}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		emitWorkflowChildUsage(api, opts.BubbleTaskID, 7, 5)
		return "x", nil
	}
	result, err := newRunner(api, runOptions{
		BudgetTotal: 10,
		RunDir:      filepath.Join(t.TempDir(), "usage-budget-stop"),
		Activities:  ext.activities,
		Registry:    ext.registry,
	}).run(context.Background(), `
meta({ name: "usage_budget_stop", description: "stop after real usage" });
const first = await agent("first", { label: "first" });
const second = await agent("second", { label: "second" });
return { first: first, second: second, spent: budget.spent(), remaining: budget.remaining() };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if api.callCount() != 1 {
		t.Fatalf("call count = %d", api.callCount())
	}
	got := result.Result.(map[string]any)
	if got["first"] != "x" || got["second"] != nil || got["spent"] != int64(10) || got["remaining"] != int64(0) {
		t.Fatalf("budget stop result = %#v", got)
	}
	if len(result.Snapshot.Agents) != 1 || result.Snapshot.Agents[0].EstimatedTokens != 12 || result.Snapshot.Agents[0].TurnTokens != 12 {
		t.Fatalf("agent accounting = %+v", result.Snapshot.Agents)
	}
	if len(result.Snapshot.Logs) != 1 || !strings.Contains(result.Snapshot.Logs[0], "budget exhausted") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestParallelBudgetReservationsLimitConcurrentForks(t *testing.T) {
	api := &fakeAPI{}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	started := make(chan struct{}, 8)
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		started <- struct{}{}
		emitWorkflowChildUsage(api, opts.BubbleTaskID, 7, 5)
		time.Sleep(20 * time.Millisecond)
		return "ok:" + opts.Name, nil
	}
	result, err := newRunner(api, runOptions{
		Concurrency: 8,
		BudgetTotal: 2,
		RunDir:      filepath.Join(t.TempDir(), "parallel-budget-reserve"),
		Activities:  ext.activities,
		Registry:    ext.registry,
	}).run(context.Background(), `
meta({ name: "parallel_budget_reserve", description: "reserve budget before concurrent forks" });
const out = await parallel([
  () => agent("one", { label: "one" }),
  () => agent("two", { label: "two" }),
  () => agent("three", { label: "three" }),
  () => agent("four", { label: "four" }),
  () => agent("five", { label: "five" }),
  () => agent("six", { label: "six" }),
]);
return { out: out, spent: budget.spent(), remaining: budget.remaining() };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if api.callCount() != 2 {
		t.Fatalf("call count = %d, want 2", api.callCount())
	}
	if api.maxConcurrency() > 2 {
		t.Fatalf("max concurrency = %d, want <= 2", api.maxConcurrency())
	}
	got := result.Result.(map[string]any)
	if got["spent"] != int64(2) || got["remaining"] != int64(0) {
		t.Fatalf("budget result = %#v", got)
	}
	out := got["out"].([]any)
	done := 0
	for _, item := range out {
		if item != nil {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("done results = %d, out=%#v", done, out)
	}
	if len(result.Snapshot.Agents) != 2 {
		t.Fatalf("snapshot agents = %d", len(result.Snapshot.Agents))
	}
	for _, agent := range result.Snapshot.Agents {
		if agent.EstimatedTokens != 12 || agent.TurnTokens != 12 {
			t.Fatalf("agent accounting = %+v", result.Snapshot.Agents)
		}
	}
	if len(started) != 2 {
		t.Fatalf("started signals = %d, want 2", len(started))
	}
}

func TestAgentLimitStopsFurtherAgents(t *testing.T) {
	api := &fakeAPI{}
	result, err := newRunner(api, runOptions{MaxAgents: 2}).run(context.Background(), `
meta({ name: "agent_limit", description: "stop runaway scripts" });
const one = await agent("one", { label: "one" });
const two = await agent("two", { label: "two" });
const three = await agent("three", { label: "three" });
return { one: one, two: two, three: three };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if api.callCount() != 2 {
		t.Fatalf("call count = %d", api.callCount())
	}
	got := result.Result.(map[string]any)
	if got["one"] != "result:one" || got["two"] != "result:two" || got["three"] != nil {
		t.Fatalf("agent limit result = %#v", got)
	}
	if len(result.Snapshot.Logs) != 1 || !strings.Contains(result.Snapshot.Logs[0], "agent limit exceeded") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestParallelLimitsConcurrencyPreservesOrderAndReturnsNullForFailures(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		time.Sleep(20 * time.Millisecond)
		if strings.Contains(opts.Task, "fail") {
			return "", errors.New("boom")
		}
		return "done:" + opts.Name, nil
	}
	result, err := newRunner(api, runOptions{Concurrency: 2}).run(context.Background(), `
meta({ name: "parallel", description: "fan out" });
phase("Fanout");
const out = await parallel([
  () => agent("a", { label: "first" }),
  () => agent("fail", { label: "bad" }),
  () => agent("c", { label: "third" }),
]);
return out;
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if api.maxConcurrency() > 2 {
		t.Fatalf("max concurrency = %d", api.maxConcurrency())
	}
	got, ok := result.Result.([]any)
	if !ok || len(got) != 3 {
		t.Fatalf("result = %#v", result.Result)
	}
	if got[0] != "done:first" || got[1] != nil || got[2] != "done:third" {
		t.Fatalf("ordered results = %#v", got)
	}
	if result.Snapshot.ErrorCount != 1 {
		t.Fatalf("error count = %d", result.Snapshot.ErrorCount)
	}
}

func TestParallelFailureComparesEqualToJSONNull(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name == "bad" {
			return "", errors.New("boom")
		}
		return "ok", nil
	}
	result, err := newRunner(api, runOptions{Concurrency: 2}).run(context.Background(), `
meta({ name: "json_null", description: "stable null" });
const out = await parallel([
  () => agent("ok", { label: "good" }),
  () => agent("fail", { label: "bad" }),
]);
return { ok: out[0] !== null && out[1] === null, bad: out[1] };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["ok"] != true || got["bad"] != nil {
		t.Fatalf("result = %#v", got)
	}
}

func TestPipelineStagesCanCallAgent(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		return "seen:" + opts.Name, nil
	}
	result, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "pipe", description: "pipeline" });
phase("Pipe");
return await pipeline(["a", "b"],
  (item, original, index) => agent("inspect " + item, { label: "inspect " + index }),
  (prev, original, index) => prev + ":" + original,
);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.([]any)
	// pipeline index is 0-based (JS semantics).
	if got[0] != "seen:inspect 0:a" || got[1] != "seen:inspect 1:b" {
		t.Fatalf("pipeline result = %#v", got)
	}
}

func TestPipelinePreservesOrderAndIsolatesStageFailures(t *testing.T) {
	api := &fakeAPI{}
	result, err := newRunner(api, runOptions{Concurrency: 2}).run(context.Background(), `
meta({ name: "pipe_fail", description: "pipeline failures" });
await agent("anchor", { label: "anchor" });
return await pipeline(["a", "bad", "c"],
  (item, original, index) => {
    if (item === "bad") throw new Error("nope");
    return item + ":" + index;
  },
);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.([]any)
	// pipeline index is 0-based (JS semantics).
	if got[0] != "a:0" || got[1] != nil || got[2] != "c:2" {
		t.Fatalf("pipeline result = %#v", got)
	}
	if len(result.Snapshot.Logs) != 1 || !strings.Contains(result.Snapshot.Logs[0], "pipeline[1] failed") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestPipelineSupportsNesting(t *testing.T) {
	// Under the JS engine pipeline() is plain async control flow, so nesting it
	// works (the single-LState restriction of the Lua engine is gone).
	api := &fakeAPI{}
	result, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name: "nested_pipe", description: "nested pipeline" });
await agent("anchor", { label: "anchor" });
return await pipeline(["a"],
  (item) => pipeline([item], (x) => x + "!"),
);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.([]any)
	if len(got) != 1 {
		t.Fatalf("nested pipeline result = %#v", got)
	}
	inner, ok := got[0].([]any)
	if !ok || len(inner) != 1 || inner[0] != "a!" {
		t.Fatalf("nested pipeline inner = %#v", got[0])
	}
}

func TestNestedWorkflowLoadsSavedNameAndPassesArgs(t *testing.T) {
	cwd := t.TempDir()
	workflowDir := filepath.Join(cwd, ".coding_agent", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "child.js"), []byte(`
meta({ name: "child", description: "child workflow" });
const seen = await agent("child " + args.suffix, { label: "child-agent" });
return { seen: seen, suffix: args.suffix };
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{cwd: cwd}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name != "child-agent" || !strings.Contains(opts.Task, "child OK") {
			t.Fatalf("bad nested call: %+v", opts)
		}
		return "CHILD_OK", nil
	}
	result, err := newRunner(api, runOptions{Cwd: cwd}).run(context.Background(), `
meta({ name: "parent", description: "parent workflow" });
return await workflow("child", { suffix: "OK" });
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["seen"] != "CHILD_OK" || got["suffix"] != "OK" {
		t.Fatalf("nested result = %#v", got)
	}
	if api.callCount() != 1 {
		t.Fatalf("call count = %d", api.callCount())
	}
	if len(result.Snapshot.Logs) != 1 || !strings.Contains(result.Snapshot.Logs[0], "nested workflow child completed") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestNestedWorkflowLoadsClaudeProjectWorkflowName(t *testing.T) {
	cwd := t.TempDir()
	workflowDir := filepath.Join(cwd, ".claude", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "child.js"), []byte(`
meta({ name: "claude_child", description: "claude child workflow" });
const seen = await agent("claude child", { label: "claude-child" });
return { seen: seen };
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{cwd: cwd}
	result, err := newRunner(api, runOptions{Cwd: cwd}).run(context.Background(), `
meta({ name: "parent", description: "parent workflow" });
return await workflow("child");
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.(map[string]any)
	if got["seen"] != "result:claude-child" {
		t.Fatalf("nested result = %#v", got)
	}
}

func TestNestedWorkflowSharesBudget(t *testing.T) {
	cwd := t.TempDir()
	workflowDir := filepath.Join(cwd, ".coding_agent", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "child.js"), []byte(`
meta({ name: "child_budget", description: "child spends budget" });
return { first: await agent("first", { label: "first" }) };
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{cwd: cwd}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		return "12345678", nil
	}
	result, err := newRunner(api, runOptions{Cwd: cwd, BudgetTotal: 1}).run(context.Background(), `
meta({ name: "parent_budget", description: "shared budget" });
const child = await workflow("child");
const second = await agent("second", { label: "second" });
return { first: child.first, second: second, spent: budget.spent(), remaining: budget.remaining() };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if api.callCount() != 1 {
		t.Fatalf("call count = %d", api.callCount())
	}
	got := result.Result.(map[string]any)
	if got["first"] != "12345678" || got["second"] != nil || got["spent"] != int64(1) || got["remaining"] != int64(0) {
		t.Fatalf("nested budget result = %#v", got)
	}
}

func TestNestedWorkflowSharesAgentLimit(t *testing.T) {
	cwd := t.TempDir()
	workflowDir := filepath.Join(cwd, ".coding_agent", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "child.js"), []byte(`
meta({ name: "child_limit", description: "child uses one agent slot" });
return { first: await agent("first", { label: "first" }) };
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{cwd: cwd}
	result, err := newRunner(api, runOptions{Cwd: cwd, MaxAgents: 1}).run(context.Background(), `
meta({ name: "parent_limit", description: "shared agent limit" });
const child = await workflow("child");
const second = await agent("second", { label: "second" });
return { first: child.first, second: second };
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if api.callCount() != 1 {
		t.Fatalf("call count = %d", api.callCount())
	}
	got := result.Result.(map[string]any)
	if got["first"] != "result:first" || got["second"] != nil {
		t.Fatalf("nested agent limit result = %#v", got)
	}
}

func TestNestedWorkflowRejectsSecondLevelNesting(t *testing.T) {
	cwd := t.TempDir()
	workflowDir := filepath.Join(cwd, ".coding_agent", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "child.js"), []byte(`
meta({ name: "child_nested", description: "child tries nested workflow" });
return await workflow("grandchild");
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{cwd: cwd}
	_, err := newRunner(api, runOptions{Cwd: cwd}).run(context.Background(), `
meta({ name: "parent_nested", description: "parent workflow" });
return await workflow("child");
`)
	if err == nil || !strings.Contains(err.Error(), "limited to one level") {
		t.Fatalf("expected nested limit error, got %v", err)
	}
}

func TestPreviewHandlesNonPositiveMax(t *testing.T) {
	if got := preview("abcdef", 0); got != "" {
		t.Fatalf("preview max 0 = %q", got)
	}
	if got := preview("abcdef", -1); got != "" {
		t.Fatalf("preview max -1 = %q", got)
	}
	if got := preview("abcdef", 1); got != "a" {
		t.Fatalf("preview max 1 = %q", got)
	}
}

func TestToolExecuteReturnsDetails(t *testing.T) {
	api := &fakeAPI{}
	ext := &Extension{cfg: DefaultConfig(), api: api}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{"script": `
meta({ name: "tool", description: "tool result" });
return await agent("x", { label: "x" });
`}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if res.Details == nil || !strings.Contains(res.Content[0].(*types.TextContent).Text, "Workflow tool completed") {
		t.Fatalf("bad tool result: %+v", res)
	}
}

func TestToolExecutePersistsScriptWhenSessionDirIsAvailable(t *testing.T) {
	sessionDir := t.TempDir()
	api := &fakeAPI{sessionDir: sessionDir}
	ext := &Extension{cfg: DefaultConfig(), api: api}
	tool := newTool(ext)
	script := `
meta({ name: "persist", description: "persist script" });
return await agent("x", { label: "x" });
`
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{"script": script}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	snapshot, ok := res.Details.(workflowSnapshot)
	if !ok {
		t.Fatalf("details = %T, want workflowSnapshot", res.Details)
	}
	if snapshot.ScriptPath == "" || snapshot.RunDir == "" {
		t.Fatalf("expected script path and run dir in snapshot: %+v", snapshot)
	}
	if !strings.Contains(res.Content[0].(*types.TextContent).Text, "Script: "+snapshot.ScriptPath) {
		t.Fatalf("tool text missing script path:\n%s", res.Content[0].(*types.TextContent).Text)
	}
	if filepath.Dir(snapshot.ScriptPath) != snapshot.RunDir {
		t.Fatalf("script path %q not under run dir %q", snapshot.ScriptPath, snapshot.RunDir)
	}
	if !strings.HasPrefix(snapshot.ScriptPath, filepath.Join(sessionDir, "extensions", "workflow", "runs")) {
		t.Fatalf("script path outside workflow runs dir: %q", snapshot.ScriptPath)
	}
	data, err := os.ReadFile(snapshot.ScriptPath)
	if err != nil {
		t.Fatalf("read persisted script: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != strings.TrimSpace(script) {
		t.Fatalf("persisted script = %q, want %q", got, strings.TrimSpace(script))
	}
	snapshotData, err := os.ReadFile(filepath.Join(snapshot.RunDir, "snapshot.json"))
	if err != nil {
		t.Fatalf("read persisted snapshot: %v", err)
	}
	var persisted workflowSnapshot
	if err := json.Unmarshal(snapshotData, &persisted); err != nil {
		t.Fatalf("decode persisted snapshot: %v", err)
	}
	if persisted.Name != "persist" || persisted.AgentCount != 1 || persisted.Result == nil {
		t.Fatalf("persisted snapshot = %+v", persisted)
	}
}

func TestToolExecuteAsyncStartGuidanceUsesCockpitFirst(t *testing.T) {
	api := &fakeAPI{sessionDir: t.TempDir()}
	ext := &Extension{cfg: DefaultConfig(), api: api}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-async", map[string]any{
		"script": `meta({ name: "async_guidance" }); return "ok";`,
		"async":  true,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	text := res.Content[0].(*types.TextContent).Text
	for _, want := range []string{
		"Workflow started in background.",
		"Run: ",
		"Open /workflows for the cockpit",
		"/workflows feed ",
		"/workflows guide ",
		"/workflows show ",
		"/workflows stop ",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("async guidance missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Use /workflows feed") {
		t.Fatalf("async guidance should be cockpit-first:\n%s", text)
	}
}

func TestToolExecuteLoadsScriptPath(t *testing.T) {
	cwd := t.TempDir()
	sessionDir := t.TempDir()
	scriptPath := filepath.Join(cwd, "saved.js")
	script := `
meta({ name: "from_path", description: "load from path" });
return await agent("x", { label: "from path" });
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	api := &fakeAPI{cwd: cwd, sessionDir: sessionDir}
	ext := &Extension{cfg: DefaultConfig(), api: api}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{"script_path": "saved.js"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if api.callCount() != 1 || api.call(0).Name != "from path" {
		t.Fatalf("calls = %#v", api.calls)
	}
	snapshot := res.Details.(workflowSnapshot)
	if snapshot.ScriptPath == "" || snapshot.ScriptPath == scriptPath {
		t.Fatalf("expected new persisted run script, got %q", snapshot.ScriptPath)
	}
	data, err := os.ReadFile(snapshot.ScriptPath)
	if err != nil {
		t.Fatalf("read persisted script: %v", err)
	}
	if strings.TrimSpace(string(data)) != strings.TrimSpace(script) {
		t.Fatalf("persisted script = %q", strings.TrimSpace(string(data)))
	}
}

func TestToolExecuteLoadsSavedWorkflowNameWithProjectPrecedence(t *testing.T) {
	repo := t.TempDir()
	cwd := filepath.Join(repo, "services", "api")
	agentDir := t.TempDir()
	rootProjectDir := filepath.Join(repo, ".coding_agent", "workflows")
	nearestProjectDir := filepath.Join(repo, "services", ".coding_agent", "workflows")
	userDir := filepath.Join(agentDir, "workflows")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nearestProjectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "review.js"), []byte(`
meta({ name: "user_review", description: "user workflow" });
return await agent("x", { label: "user" });
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootProjectDir, "review.js"), []byte(`
meta({ name: "root_review", description: "root workflow" });
return await agent("x", { label: "root" });
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nearestProjectDir, "review.js"), []byte(`
meta({ name: "nearest_review", description: "nearest workflow" });
return await agent("x", { label: "nearest" });
`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := &fakeAPI{cwd: cwd, agentDir: agentDir}
	ext := &Extension{cfg: DefaultConfig(), api: api}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{"name": "review"}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if api.callCount() != 1 || api.call(0).Name != "nearest" {
		t.Fatalf("expected nearest project workflow call, got %#v", api.calls)
	}
	if snapshot := res.Details.(workflowSnapshot); snapshot.Name != "nearest_review" {
		t.Fatalf("snapshot name = %q", snapshot.Name)
	}
}

func TestToolExecuteRequiresExactlyOneScriptSource(t *testing.T) {
	api := &fakeAPI{}
	ext := &Extension{cfg: DefaultConfig(), api: api}
	tool := newTool(ext)
	for _, args := range []map[string]any{
		{},
		{"script": "meta({name=\"x\", description=\"y\"}); return agent(\"x\")", "script_path": "x.js"},
		{"script_path": "x.js", "name": "x"},
	} {
		res, err := tool.Execute(context.Background(), "wf-1", args, nil)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !res.IsError || !strings.Contains(res.Content[0].(*types.TextContent).Text, "exactly one") {
			t.Fatalf("expected exactly-one error for %#v, got %+v", args, res)
		}
	}
}

func TestWorkflowsSaveUsesClaudeWorkflowDirs(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "script.js")
	script := `meta({ name: "save_dir", description: "save dir" })
return agent("x", { label: "x" })`
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	run := workflowRunSummary{ID: "run-1", ScriptPath: scriptPath}

	repo := t.TempDir()
	cwd := filepath.Join(repo, "services", "api")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyWorkflowDir := filepath.Join(repo, "services", ".coding_agent", "workflows")
	if err := os.MkdirAll(legacyWorkflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ext := &Extension{cfg: DefaultConfig(), api: &fakeAPI{cwd: cwd}}
	path, err := ext.saveWorkflowRunScript(run, "project_saved", "project")
	if err != nil {
		t.Fatalf("save project: %v", err)
	}
	if want := filepath.Join(repo, ".claude", "workflows", "project_saved.js"); path != want {
		t.Fatalf("saved path = %q, want %q", path, want)
	}
	nearestClaudeWorkflowDir := filepath.Join(repo, "services", ".claude", "workflows")
	if err := os.MkdirAll(nearestClaudeWorkflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ext = &Extension{cfg: DefaultConfig(), api: &fakeAPI{cwd: cwd}}
	path, err = ext.saveWorkflowRunScript(run, "nearest", "project")
	if err != nil {
		t.Fatalf("save nearest project: %v", err)
	}
	if want := filepath.Join(nearestClaudeWorkflowDir, "nearest.js"); path != want {
		t.Fatalf("nearest saved path = %q, want %q", path, want)
	}
	agentDir := filepath.Join(t.TempDir(), ".coding_agent")
	ext = &Extension{cfg: DefaultConfig(), api: &fakeAPI{cwd: cwd, agentDir: agentDir}}
	path, err = ext.saveWorkflowRunScript(run, "personal", "user")
	if err != nil {
		t.Fatalf("save user: %v", err)
	}
	if want := filepath.Join(filepath.Dir(agentDir), ".claude", "workflows", "personal.js"); path != want {
		t.Fatalf("user saved path = %q, want %q", path, want)
	}
}

func TestDiscoverSavedWorkflowCommandsUsesNearestProjectPrecedence(t *testing.T) {
	repo := t.TempDir()
	cwd := filepath.Join(repo, "apps", "web")
	agentDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{
		filepath.Join(cwd),
		filepath.Join(repo, ".coding_agent", "workflows"),
		filepath.Join(repo, "apps", ".claude", "workflows"),
		filepath.Join(repo, "apps", ".coding_agent", "workflows"),
		filepath.Join(agentDir, "workflows"),
		filepath.Join(filepath.Dir(agentDir), ".claude", "workflows"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		filepath.Join(repo, ".coding_agent", "workflows", "review.js"):             `meta({ name: "root", description: "root" })`,
		filepath.Join(repo, "apps", ".claude", "workflows", "review.js"):           `meta({ name: "claude_nearest", description: "claude nearest" })`,
		filepath.Join(repo, "apps", ".claude", "workflows", "claude.js"):           `meta({ name: "claude", description: "claude" })`,
		filepath.Join(repo, "apps", ".coding_agent", "workflows", "review.js"):     `meta({ name: "nearest", description: "nearest" })`,
		filepath.Join(agentDir, "workflows", "review.js"):                          `meta({ name: "user", description: "user" })`,
		filepath.Join(agentDir, "workflows", "personal.js"):                        `meta({ name: "personal", description: "personal" })`,
		filepath.Join(filepath.Dir(agentDir), ".claude", "workflows", "global.js"): `meta({ name: "global", description: "global" })`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	commands, err := discoverSavedWorkflowCommands(cwd, agentDir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	byName := map[string]string{}
	for _, cmd := range commands {
		byName[cmd.Name] = cmd.Path
	}
	if got, want := byName["review"], filepath.Join(repo, "apps", ".claude", "workflows", "review.js"); got != want {
		t.Fatalf("review path = %q, want %q", got, want)
	}
	if got, want := byName["claude"], filepath.Join(repo, "apps", ".claude", "workflows", "claude.js"); got != want {
		t.Fatalf("claude path = %q, want %q", got, want)
	}
	if got, want := byName["personal"], filepath.Join(agentDir, "workflows", "personal.js"); got != want {
		t.Fatalf("personal path = %q, want %q", got, want)
	}
	if got, want := byName["global"], filepath.Join(filepath.Dir(agentDir), ".claude", "workflows", "global.js"); got != want {
		t.Fatalf("global path = %q, want %q", got, want)
	}
}

func TestWorkflowsCommandListsAndShowsPersistedRuns(t *testing.T) {
	clearWorkflowDisableEnv(t)
	sessionDir := t.TempDir()
	cwd := t.TempDir()
	agentDir := t.TempDir()
	runDir := filepath.Join(sessionDir, "extensions", "workflow", "runs", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	longScriptTail := `return agent("x", { label: "x" })`
	script := `meta({ name: "listed", description: "listed run" })
` + strings.Repeat("// keep full script output\n", 180) + longScriptTail
	if err := os.WriteFile(filepath.Join(runDir, "script.js"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	snapshot := workflowSnapshot{
		Name:         "listed",
		ScriptPath:   filepath.Join(runDir, "script.js"),
		RunDir:       runDir,
		Phases:       []string{},
		CurrentPhase: "Review",
		PhaseSummaries: []phaseSummary{{
			Title:           "Review",
			AgentCount:      4,
			DoneCount:       1,
			RunningCount:    1,
			ErrorCount:      1,
			EstimatedTokens: 2,
			DurationMs:      7,
		}},
		Logs: []string{"scope complete", "writing review"},
		Agents: []agentSnapshot{{
			ID:              1,
			Label:           "x",
			Phase:           "Review",
			Prompt:          "x",
			Status:          statusDone,
			ResultPreview:   "ok",
			StartedAt:       now,
			EndedAt:         now.Add(7 * time.Millisecond),
			DurationMs:      7,
			EstimatedTokens: 2,
		}, {
			ID:     2,
			Label:  "verify",
			Phase:  "Review",
			Prompt: "verify",
			Status: statusRunning,
			RecentToolCalls: []workflowToolCallSnapshot{{
				ToolName: "web_search",
			}},
		}, {
			ID:     3,
			Label:  "risk",
			Phase:  "Review",
			Prompt: "risk",
			Status: statusError,
			Error:  "source unavailable",
		}, {
			ID:     4,
			Label:  "draft",
			Phase:  "Review",
			Prompt: "draft",
			Status: statusQueued,
		}},
		AgentCount:   4,
		RunningCount: 1,
		DoneCount:    1,
		ErrorCount:   1,
		Result:       map[string]any{"ok": true, "long": strings.Repeat("r", 700) + "FULL_RESULT_END"},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "snapshot.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{sessionDir: sessionDir, cwd: cwd, agentDir: agentDir}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cmd := api.command("workflows")
	if cmd == nil {
		t.Fatal("missing workflows command")
	}
	if err := cmd(""); err != nil {
		t.Fatalf("/workflows: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Workflow runs:") || !strings.Contains(got, "run-1") || !strings.Contains(got, "listed (4 agent(s), 1 error(s))") || !strings.Contains(got, "Open /workflows for the cockpit") || !strings.Contains(got, "/workflows feed <run-id|latest>") || !strings.Contains(got, "/workflows guide <run-id|latest>") || !strings.Contains(got, "/workflows map <run-id|latest>") {
		t.Fatalf("list notify = %q", got)
	}
	if err := cmd("show latest"); err != nil {
		t.Fatalf("/workflows show: %v", err)
	}
	got := api.lastNotify()
	if !strings.Contains(got, "Workflow run run-1") || !strings.Contains(got, "Workflow: listed") || !strings.Contains(got, "Review: 4 agent(s)") || !strings.Contains(got, "estimatedTokens=2") || !strings.Contains(got, "durationMs=7") || !strings.Contains(got, "ResultPreview:") || !strings.Contains(got, "Artifacts:") || !strings.Contains(got, "Snapshot:") || !strings.Contains(got, "Script:") || !strings.Contains(got, "Next:") || !strings.Contains(got, "/workflows guide run-1") || !strings.Contains(got, "TUI /workflows -> Result or Script rows") {
		t.Fatalf("show notify = %q", got)
	}
	if strings.Contains(got, "FULL_RESULT_END") || strings.Contains(got, "```js") || strings.Contains(got, longScriptTail) {
		t.Fatalf("show notify should not include full result or script: %q", got)
	}
	if err := cmd("feed latest"); err != nil {
		t.Fatalf("/workflows feed: %v", err)
	}
	got = api.lastNotify()
	if !strings.Contains(got, "Workflow feed run-1") || !strings.Contains(got, "Workflow: listed") || !strings.Contains(got, "Progress: 1/4 done, 1 running, 1 errors") || !strings.Contains(got, "Current phase: Review") || !strings.Contains(got, "Updates:") || !strings.Contains(got, "- writing review") || !strings.Contains(got, "Lanes:") || !strings.Contains(got, "- Review: done #1 x | run #2 verify 1 tools | err #3 risk | wait #4 draft") || !strings.Contains(got, "Legend: run active | done complete | err attention | wait queued") || !strings.Contains(got, "Active:") || !strings.Contains(got, "Agent 2 [running] verify @Review tools=1 failed=0") || !strings.Contains(got, "Attention:") || !strings.Contains(got, "Agent 3 [error] risk @Review: source unavailable") || !strings.Contains(got, "Timeline:") || !strings.Contains(got, "- Review: 1/4 done, 1 running, 1 errors") {
		t.Fatalf("feed notify = %q", got)
	}
	if strings.Contains(got, "FULL_RESULT_END") || strings.Contains(got, "```js") || strings.Contains(got, longScriptTail) {
		t.Fatalf("feed notify should not include full result or script: %q", got)
	}
	if err := cmd("guide latest"); err != nil {
		t.Fatalf("/workflows guide: %v", err)
	}
	got = api.lastNotify()
	if !strings.Contains(got, "Workflow guide run-1") || !strings.Contains(got, "Workflow: listed") || !strings.Contains(got, "Views:") || !strings.Contains(got, "Feed: live cards") || !strings.Contains(got, "Map: full phase and agent tree") || !strings.Contains(got, "Result/Script: final workflow artifact views") || !strings.Contains(got, "Route:") || !strings.Contains(got, "/workflows -> running run -> Feed") || !strings.Contains(got, "Current phase: Review") || !strings.Contains(got, "Attention: Agent 3 [error] risk @Review: source unavailable") || !strings.Contains(got, "Active: Agent 2 [running] verify @Review tools=1 failed=0") || !strings.Contains(got, "Commands:") || !strings.Contains(got, "TUI Result and Script rows") || !strings.Contains(got, "/workflows transcript run-1 <agent-id>") {
		t.Fatalf("guide notify = %q", got)
	}
	if strings.Contains(got, "FULL_RESULT_END") || strings.Contains(got, "```js") || strings.Contains(got, longScriptTail) {
		t.Fatalf("guide notify should not include full result or script: %q", got)
	}
	if err := cmd("map latest"); err != nil {
		t.Fatalf("/workflows map: %v", err)
	}
	got = api.lastNotify()
	if !strings.Contains(got, "Workflow map run-1") || !strings.Contains(got, "Workflow: listed") || !strings.Contains(got, "- Review: 1/4 done, 1 running, 1 errors, estimatedTokens=2, durationMs=7") || !strings.Contains(got, "Agent 1 [done] x estimatedTokens=2 durationMs=7") || !strings.Contains(got, "ResultPreview: ok") {
		t.Fatalf("map notify = %q", got)
	}
	if strings.Contains(got, "FULL_RESULT_END") || strings.Contains(got, "```js") || strings.Contains(got, longScriptTail) {
		t.Fatalf("map notify should not include full result or script: %q", got)
	}
	if err := cmd("agent latest 1"); err != nil {
		t.Fatalf("/workflows agent: %v", err)
	}
	got = api.lastNotify()
	if !strings.Contains(got, "Workflow agent 1 in run run-1") || !strings.Contains(got, "Workflow: listed") || !strings.Contains(got, "Agent status: done") || !strings.Contains(got, "Phase: Review") || !strings.Contains(got, "ResultPreview: ok") || !strings.Contains(got, "Prompt:") || !strings.Contains(got, "```text\nx\n```") {
		t.Fatalf("agent notify = %q", got)
	}
	if err := cmd("agent latest nope"); err != nil {
		t.Fatalf("/workflows agent invalid: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "workflow agent id must be a positive integer") {
		t.Fatalf("invalid agent notify = %q", got)
	}
	if err := cmd("save latest reusable"); err != nil {
		t.Fatalf("/workflows save: %v", err)
	}
	projectSaved := filepath.Join(cwd, ".claude", "workflows", "reusable.js")
	data, err = os.ReadFile(projectSaved)
	if err != nil {
		t.Fatalf("read project saved workflow: %v", err)
	}
	if !strings.Contains(string(data), `name: "listed"`) {
		t.Fatalf("project saved workflow = %q", string(data))
	}
	if got := api.lastNotify(); !strings.Contains(got, "saved as /reusable (also /workflow:reusable)") || !strings.Contains(got, projectSaved) {
		t.Fatalf("save notify = %q", got)
	}
	if err := cmd("save latest personal user"); err != nil {
		t.Fatalf("/workflows save user: %v", err)
	}
	userSaved := filepath.Join(filepath.Dir(agentDir), ".claude", "workflows", "personal.js")
	if _, err := os.Stat(userSaved); err != nil {
		t.Fatalf("stat user saved workflow: %v", err)
	}
	if err := cmd("save latest bad/name"); err != nil {
		t.Fatalf("/workflows save invalid: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Workflow save failed") || !strings.Contains(got, "workflow name") {
		t.Fatalf("invalid save notify = %q", got)
	}
	callsBeforeRestart := api.callCount()
	if err := cmd("restart run-1"); err != nil {
		t.Fatalf("/workflows restart: %v", err)
	}
	started := api.waitNotifyContaining(t, "restarted in background")
	if !strings.Contains(started, "Workflow run run-1 restarted") || !strings.Contains(started, "New run: ") || !strings.Contains(started, "Open /workflows for the cockpit") {
		t.Fatalf("restart notify = %q", started)
	}
	api.waitNotifyContaining(t, "Workflow listed completed")
	if api.callCount() != callsBeforeRestart+1 {
		t.Fatalf("call count after restart = %d, want %d", api.callCount(), callsBeforeRestart+1)
	}
	runs, _, err := ext.workflowRuns()
	if err != nil {
		t.Fatalf("workflowRuns after restart: %v", err)
	}
	if len(runs) < 2 {
		t.Fatalf("runs after restart = %+v", runs)
	}
	foundRestart := false
	for _, run := range runs {
		if run.ID == "run-1" {
			continue
		}
		if run.Snapshot != nil && run.Snapshot.Name == "listed" {
			foundRestart = true
			break
		}
	}
	if !foundRestart {
		t.Fatalf("restarted run not found in %+v", runs)
	}
}

func TestFormatWorkflowCompletionShowsFlowBeforeFinalResult(t *testing.T) {
	now := time.Now()
	text := formatWorkflowCompletion(runResult{
		Meta: metaInfo{Name: "market_watch", Description: "market watch"},
		Result: map[string]any{
			"report": "final answer",
		},
		Snapshot: workflowSnapshot{
			Name: "market_watch",
			PhaseSummaries: []phaseSummary{{
				Title:      "Research",
				AgentCount: 2,
				DoneCount:  2,
				DurationMs: 12,
			}, {
				Title:      "Synthesis",
				AgentCount: 1,
				DoneCount:  1,
				DurationMs: 5,
			}},
			Agents: []agentSnapshot{{
				ID:              1,
				Label:           "market breadth",
				Phase:           "Research",
				Status:          statusDone,
				ResultPreview:   "breadth result",
				StartedAt:       now,
				EndedAt:         now.Add(10 * time.Millisecond),
				DurationMs:      10,
				EstimatedTokens: 42,
			}, {
				ID:            2,
				Label:         "sector rotation",
				Phase:         "Research",
				Status:        statusDone,
				ResultPreview: "sector result",
			}, {
				ID:            3,
				Label:         "report",
				Phase:         "Synthesis",
				Status:        statusDone,
				ResultPreview: "report result",
			}},
			AgentCount: 3,
			DoneCount:  3,
		},
	})

	for _, want := range []string{
		"Workflow market_watch completed with 3 agent(s).",
		"## Execution flow",
		"- Research: 2 agent(s), 2 done, 0 running, 0 errors",
		"#1 [done] market breadth durationMs=10 estimatedTokens=42 result=breadth result",
		"#2 [done] sector rotation result=sector result",
		"- Synthesis: 1 agent(s), 1 done, 0 running, 0 errors",
		"#3 [done] report result=report result",
		"## Final result",
		"## report",
		"final answer",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatted completion missing %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "## Execution flow") > strings.Index(text, "## Final result") {
		t.Fatalf("execution flow should appear before final result:\n%s", text)
	}
}

func TestFormatWorkflowCompletionNotifyStaysSummaryFirst(t *testing.T) {
	text := formatWorkflowCompletionNotify("run-123", runResult{
		Meta: metaInfo{Name: "market_watch"},
		Result: map[string]any{
			"report": strings.Repeat("final answer ", 100) + "FULL_RESULT_END",
		},
		Snapshot: workflowSnapshot{
			Name: "market_watch",
			PhaseSummaries: []phaseSummary{{
				Title:      "Research",
				AgentCount: 1,
				DoneCount:  1,
				DurationMs: 12,
			}},
			Agents: []agentSnapshot{{
				ID:            1,
				Label:         "market breadth",
				Phase:         "Research",
				Status:        statusDone,
				ResultPreview: "breadth result",
			}},
			AgentCount: 1,
			DoneCount:  1,
			ScriptPath: "/tmp/workflow/script.js",
		},
	})

	for _, want := range []string{
		"Workflow market_watch completed with 1 agent(s).",
		"## Execution flow",
		"- Research: 1 agent(s), 1 done, 0 running, 0 errors",
		"#1 [done] market breadth result=breadth result",
		"ResultPreview:",
		"Script: /tmp/workflow/script.js",
		"Next:",
		"/workflows guide run-123",
		"/workflows feed run-123",
		"/workflows show run-123",
		"TUI /workflows -> Result or Script rows",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("notify completion missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "## Final result") || strings.Contains(text, "FULL_RESULT_END") {
		t.Fatalf("notify completion should not include full final result:\n%s", text)
	}
}

func TestWorkflowChildEventsAppearInAgentDetail(t *testing.T) {
	clearWorkflowDisableEnv(t)
	sessionDir := t.TempDir()
	api := &fakeAPI{sessionDir: sessionDir}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		if opts.BubbleTaskID == "" {
			return "", fmt.Errorf("missing bubble task id")
		}
		api.emit("subagent_child_event", types.Event{
			Type:     types.EventType("subagent_child_event"),
			TaskID:   opts.BubbleTaskID,
			Reason:   string(types.EventTypeToolExecutionEnd),
			ToolName: "read",
			Args:     map[string]any{"path": "go.mod"},
			Result:   "module github.com/openmodu/modu",
		})
		api.emit("subagent_child_event", types.Event{
			Type:     types.EventType("subagent_child_event"),
			TaskID:   opts.BubbleTaskID,
			Reason:   string(types.EventTypeToolExecutionEnd),
			ToolName: "bash",
			Args:     map[string]any{"command": "exit 1"},
			Result:   map[string]any{"stderr": "boom"},
			IsError:  true,
		})
		usage := types.AgentUsage{Input: 30, Output: 12, TotalTokens: 42}
		usage.Cost.Total = 0.001234
		api.emit("subagent_child_event", types.Event{
			Type:   types.EventType("subagent_child_event"),
			TaskID: opts.BubbleTaskID,
			Reason: string(types.EventTypeTurnEnd),
			Message: &types.AssistantMessage{
				Usage: usage,
			},
		})
		api.emit("subagent_child_usage", types.Event{
			Type:   types.EventType("subagent_child_usage"),
			TaskID: opts.BubbleTaskID,
			Messages: []types.AgentMessage{
				types.UserMessage{Role: types.RoleUser, Content: "inspect events"},
				&types.AssistantMessage{
					Role: types.RoleAssistant,
					Content: []types.ContentBlock{
						&types.TextContent{Type: "text", Text: "I will read go.mod."},
						&types.ToolCallContent{Type: "toolCall", ID: "read-1", Name: "read", Arguments: map[string]any{"path": "go.mod"}},
					},
					Usage: usage,
				},
				types.ToolResultMessage{
					Role:       types.RoleToolResult,
					ToolCallID: "read-1",
					ToolName:   "read",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "module github.com/openmodu/modu"}},
				},
			},
		})
		return "EVENT_DETAIL_OK", nil
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{
		"script": `
meta({ name: "events", description: "events" })
return await agent("inspect events", { label: "event-agent", phase: "Inspect" });
`,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	snapshot := res.Details.(workflowSnapshot)
	agent := snapshot.Agents[0]
	if agent.TurnTokens != 42 || agent.Cost != 0.001234 || snapshot.Cost != 0.001234 || agent.FailedToolCalls != 1 || len(agent.RecentToolCalls) != 2 || !strings.Contains(agent.RecentToolCalls[0].ArgsPreview, "go.mod") || !strings.Contains(agent.RecentToolCalls[1].ResultPreview, "boom") {
		t.Fatalf("agent activity = %+v", agent)
	}
	if len(agent.Transcript) != 3 || agent.Transcript[1].Role != types.RoleAssistant || len(agent.Transcript[1].ToolCalls) != 1 || agent.Transcript[1].ToolCalls[0].Name != "read" {
		t.Fatalf("agent transcript = %+v", agent.Transcript)
	}
	cmd := api.command("workflows")
	if cmd == nil {
		t.Fatal("missing workflows command")
	}
	if err := cmd("agent latest 1"); err != nil {
		t.Fatalf("/workflows agent: %v", err)
	}
	got := api.lastNotify()
	if !strings.Contains(got, "TurnTokens: 42") || !strings.Contains(got, "Cost: 0.001234") || !strings.Contains(got, "FailedToolCalls: 1") || !strings.Contains(got, "RecentToolCalls:") || !strings.Contains(got, "- read [ok]") || !strings.Contains(got, "Args: {\"path\":\"go.mod\"}") || !strings.Contains(got, "Result: module github.com/openmodu/modu") || !strings.Contains(got, "- bash [error]") || !strings.Contains(got, "Args: {\"command\":\"exit 1\"}") || !strings.Contains(got, "Result: {\"stderr\":\"boom\"}") {
		t.Fatalf("agent detail notify = %q", got)
	}
	if err := cmd("transcript latest 1"); err != nil {
		t.Fatalf("/workflows transcript: %v", err)
	}
	got = api.lastNotify()
	for _, want := range []string{
		"Workflow agent 1 transcript",
		"## 1. USER",
		"inspect events",
		"## 2. ASSISTANT",
		"I will read go.mod.",
		"ToolCall: read (read-1)",
		"Args: {\"path\":\"go.mod\"}",
		"Usage: input=30 output=12 total=42",
		"## 3. TOOLRESULT read",
		"module github.com/openmodu/modu",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript notify missing %q:\n%s", want, got)
		}
	}
}

func TestWorkflowRuntimeStateTracksRunningRuns(t *testing.T) {
	clearWorkflowDisableEnv(t)
	ext := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ext.registry.start("run-1", "/tmp/workflow/script.js", "/tmp/workflow", cancel, workflowExecution{})
	ext.registry.update("run-1", workflowSnapshot{
		Name:         "review",
		AgentCount:   3,
		DoneCount:    1,
		RunningCount: 2,
		ErrorCount:   0,
		CurrentPhase: "Review",
		Logs: []string{
			"old setup log",
			"phase scoped",
			"fanout started",
			"first source done",
			"second source done",
			"cross-check started",
			"risk retry",
			"risk retry " + strings.Repeat("detail ", 80),
			"writing summary",
		},
		PhaseSummaries: []phaseSummary{{
			Title:           "Review",
			AgentCount:      3,
			DoneCount:       1,
			RunningCount:    2,
			EstimatedTokens: 120,
			Cost:            0.0123,
			DurationMs:      700,
		}},
		Agents: []agentSnapshot{
			{
				ID:              1,
				Label:           "inventory",
				Phase:           "Review",
				Prompt:          "Inspect repository inventory",
				Status:          statusDone,
				EstimatedTokens: 20,
				TurnTokens:      12,
				Cost:            0.0023,
				DurationMs:      300,
				ResultPreview:   "ok",
			},
			{
				ID:              2,
				Label:           "risk",
				Phase:           "Review",
				Prompt:          strings.Repeat("risk ", 80),
				Status:          statusRunning,
				EstimatedTokens: 40,
				Cost:            0.0100,
				FailedToolCalls: 1,
				RecentToolCalls: []workflowToolCallSnapshot{{
					ToolName:      "read",
					ArgsPreview:   `{"path":"go.mod"}`,
					ResultPreview: "module github.com/openmodu/modu",
				}},
			},
		},
	})
	_ = ctx

	state, ok := ext.RuntimeState().(map[string]any)
	if !ok {
		t.Fatalf("RuntimeState type = %T", ext.RuntimeState())
	}
	if got, _ := state["runningCount"].(int); got != 1 {
		t.Fatalf("runningCount = %d, state = %+v", got, state)
	}
	if got, _ := state["indicator"].(string); !strings.Contains(got, "workflow review 1/3 running: Review") {
		t.Fatalf("indicator = %q", got)
	}
	runs, ok := state["runs"].([]map[string]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs = %#v", state["runs"])
	}
	if runs[0]["id"] != "run-1" || runs[0]["status"] != "running" || runs[0]["name"] != "review" || runs[0]["currentPhase"] != "Review" {
		t.Fatalf("run state = %+v", runs[0])
	}
	phases, ok := runs[0]["phases"].([]map[string]any)
	if !ok || len(phases) != 1 {
		t.Fatalf("phases = %#v", runs[0]["phases"])
	}
	if phases[0]["title"] != "Review" || phases[0]["agentCount"] != 3 || phases[0]["estimatedTokens"] != 120 || phases[0]["cost"] != 0.0123 || phases[0]["durationMs"] != int64(700) {
		t.Fatalf("phase state = %+v", phases[0])
	}
	logs, ok := runs[0]["logs"].([]string)
	if !ok || len(logs) != workflowRuntimeLogLimit {
		t.Fatalf("logs = %#v", runs[0]["logs"])
	}
	if logs[0] != "phase scoped" || logs[len(logs)-1] != "writing summary" || !strings.HasSuffix(logs[6], "...") {
		t.Fatalf("logs were not capped/truncated as expected: %#v", logs)
	}
	agents, ok := runs[0]["agents"].([]map[string]any)
	if !ok || len(agents) != 2 {
		t.Fatalf("agents = %#v", runs[0]["agents"])
	}
	if agents[0]["id"] != 1 || agents[0]["label"] != "inventory" || agents[0]["status"] != "done" || agents[0]["turnTokens"] != 12 || agents[0]["cost"] != 0.0023 || agents[0]["promptPreview"] != "Inspect repository inventory" || agents[0]["prompt"] != "Inspect repository inventory" {
		t.Fatalf("agent state = %+v", agents[0])
	}
	if prompt, _ := agents[1]["promptPreview"].(string); !strings.HasPrefix(prompt, "risk risk") || !strings.HasSuffix(prompt, "...") {
		t.Fatalf("prompt preview = %q", prompt)
	}
	if prompt, _ := agents[1]["prompt"].(string); !strings.Contains(prompt, "risk risk risk") || strings.Contains(prompt, "\n...") {
		t.Fatalf("prompt = %q", prompt)
	}
	if agents[1]["failedToolCalls"] != 1 || agents[1]["recentToolCalls"] != 1 {
		t.Fatalf("agent state = %+v", agents[1])
	}
	toolCalls, ok := agents[1]["recentToolCallPreviews"].([]map[string]any)
	if !ok || len(toolCalls) != 1 || toolCalls[0]["toolName"] != "read" || !strings.Contains(toolCalls[0]["resultPreview"].(string), "openmodu") {
		t.Fatalf("tool call previews = %#v", agents[1]["recentToolCallPreviews"])
	}
}

func TestWorkflowRuntimePromptPreservesLinesAndCaps(t *testing.T) {
	if got := workflowRuntimePrompt(" line one\nline two "); got != "line one\nline two" {
		t.Fatalf("prompt = %q", got)
	}
	long := strings.Repeat("x", workflowRuntimePromptLimit+20)
	got := workflowRuntimePrompt(long)
	if len(got) != workflowRuntimePromptLimit || !strings.HasSuffix(got, "\n...") {
		t.Fatalf("capped prompt len=%d suffix=%q", len(got), got[len(got)-4:])
	}
}

func TestWorkflowRuntimeStateIncludesPersistedRuns(t *testing.T) {
	clearWorkflowDisableEnv(t)
	sessionDir := t.TempDir()
	runDir := filepath.Join(sessionDir, "extensions", "workflow", "runs", "run-persisted")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(runDir, "script.js")
	if err := os.WriteFile(scriptPath, []byte(`meta({ name: "persisted" })`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot := workflowSnapshot{
		Name:       "persisted",
		RunDir:     runDir,
		Phases:     []string{"Collect"},
		AgentCount: 1,
		DoneCount:  1,
		PhaseSummaries: []phaseSummary{{
			Title:      "Collect",
			AgentCount: 1,
			DoneCount:  1,
		}},
		Agents: []agentSnapshot{{
			ID:            1,
			Label:         "market data",
			Phase:         "Collect",
			Status:        statusDone,
			ResultPreview: "ok",
		}},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "snapshot.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	ext := New()
	if err := ext.Init(&fakeAPI{sessionDir: sessionDir}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	state, ok := ext.RuntimeState().(map[string]any)
	if !ok {
		t.Fatalf("RuntimeState type = %T", ext.RuntimeState())
	}
	if got, _ := state["completedCount"].(int); got != 1 {
		t.Fatalf("completedCount = %d, state = %+v", got, state)
	}
	runs, ok := state["runs"].([]map[string]any)
	if !ok || len(runs) != 1 {
		t.Fatalf("runs = %#v", state["runs"])
	}
	if runs[0]["id"] != "run-persisted" || runs[0]["status"] != "completed" || runs[0]["name"] != "persisted" || runs[0]["scriptPath"] != scriptPath {
		t.Fatalf("persisted run state = %+v", runs[0])
	}
	agents, ok := runs[0]["agents"].([]map[string]any)
	if !ok || len(agents) != 1 || agents[0]["label"] != "market data" {
		t.Fatalf("persisted agents = %#v", runs[0]["agents"])
	}
}

func TestToolExecuteAsyncRegistersRunningWorkflowAndStopCancelsIt(t *testing.T) {
	clearWorkflowDisableEnv(t)
	sessionDir := t.TempDir()
	api := &fakeAPI{sessionDir: sessionDir}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{
		"async": true,
		"script": `
meta({ name: "async_stop", description: "async stop" })
return agent("wait", { label: "wait" })
`,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	details := res.Details.(map[string]any)
	runID, _ := details["runID"].(string)
	if runID == "" {
		t.Fatalf("missing run id: %+v", res.Details)
	}
	api.waitCallCount(t, 1)
	cmd := api.command("workflows")
	if cmd == nil {
		t.Fatal("missing workflows command")
	}
	if err := cmd(""); err != nil {
		t.Fatalf("/workflows: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, runID) || !strings.Contains(got, "running") || !strings.Contains(got, "async_stop") {
		t.Fatalf("list notify = %q", got)
	}
	if err := cmd("stop " + runID); err != nil {
		t.Fatalf("/workflows stop: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Stop requested") || !strings.Contains(got, runID) {
		t.Fatalf("stop notify = %q", got)
	}
	api.waitNotifyContaining(t, "stopped")
	if err := cmd("show " + runID); err != nil {
		t.Fatalf("/workflows show: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Status: stopped") || !strings.Contains(got, "Workflow: async_stop") {
		t.Fatalf("show stopped notify = %q", got)
	}
	statusPath := filepath.Join(sessionDir, "extensions", "workflow", "runs", runID, "status.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var status workflowRunStatusFile
	if err := json.Unmarshal(statusData, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Status != workflowStatusStopped || status.ID != runID || status.Error == "" {
		t.Fatalf("status = %+v", status)
	}
	reloadedAPI := &fakeAPI{sessionDir: sessionDir}
	reloaded := New()
	if err := reloaded.Init(reloadedAPI); err != nil {
		t.Fatalf("reload Init: %v", err)
	}
	reloadedCmd := reloadedAPI.command("workflows")
	if reloadedCmd == nil {
		t.Fatal("missing reloaded workflows command")
	}
	if err := reloadedCmd("show " + runID); err != nil {
		t.Fatalf("reloaded /workflows show: %v", err)
	}
	if got := reloadedAPI.lastNotify(); !strings.Contains(got, "Status: stopped") || !strings.Contains(got, "Error: stop requested") {
		t.Fatalf("reloaded show notify = %q", got)
	}
}

func TestWorkflowsResumeUsesCompletedAgentCache(t *testing.T) {
	clearWorkflowDisableEnv(t)
	sessionDir := t.TempDir()
	api := &fakeAPI{sessionDir: sessionDir}
	secondStarted := make(chan struct{}, 1)
	var mu sync.Mutex
	secondAttempts := 0
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		switch opts.Name {
		case "first":
			return "FIRST_OK", nil
		case "second":
			mu.Lock()
			secondAttempts++
			attempt := secondAttempts
			mu.Unlock()
			if attempt == 1 {
				secondStarted <- struct{}{}
				<-ctx.Done()
				return "", ctx.Err()
			}
			return "SECOND_OK", nil
		default:
			return "", fmt.Errorf("unexpected agent %q", opts.Name)
		}
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{
		"async": true,
		"script": `
meta({ name: "resume_cache", description: "resume cache" })
const first = await agent("first prompt", { label: "first" });
const second = await agent("second prompt", { label: "second" });
return { first: first, second: second };
`,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	runID := res.Details.(map[string]any)["runID"].(string)
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second agent did not start")
	}
	cmd := api.command("workflows")
	if cmd == nil {
		t.Fatal("missing workflows command")
	}
	if err := cmd("stop " + runID); err != nil {
		t.Fatalf("/workflows stop: %v", err)
	}
	api.waitNotifyContaining(t, "stopped")
	if api.callCount() != 2 {
		t.Fatalf("call count after stop = %d, want 2", api.callCount())
	}
	if err := cmd("resume " + runID); err != nil {
		t.Fatalf("/workflows resume: %v", err)
	}
	if got := api.waitNotifyContaining(t, "resumed in background"); !strings.Contains(got, runID) {
		t.Fatalf("resume notify = %q", got)
	}
	done := api.waitNotifyContaining(t, "Workflow resume_cache completed")
	if !strings.Contains(done, "FIRST_OK") || !strings.Contains(done, "SECOND_OK") {
		t.Fatalf("completion notify = %q", done)
	}
	if api.callCount() != 3 {
		t.Fatalf("call count after resume = %d, want 3", api.callCount())
	}
	firstCalls := 0
	secondCalls := 0
	for i := 0; i < api.callCount(); i++ {
		switch api.call(i).Name {
		case "first":
			firstCalls++
		case "second":
			secondCalls++
		}
	}
	if firstCalls != 1 || secondCalls != 2 {
		t.Fatalf("first calls = %d, second calls = %d", firstCalls, secondCalls)
	}
	if err := cmd("show " + runID); err != nil {
		t.Fatalf("/workflows show: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Status: completed") || !strings.Contains(got, "[done cached] first") {
		t.Fatalf("show notify = %q", got)
	}
}

func TestWorkflowsPauseCanResumeStoppedRun(t *testing.T) {
	clearWorkflowDisableEnv(t)
	sessionDir := t.TempDir()
	api := &fakeAPI{sessionDir: sessionDir}
	secondStarted := make(chan struct{}, 1)
	var mu sync.Mutex
	secondAttempts := 0
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		switch opts.Name {
		case "first":
			return "FIRST_OK", nil
		case "second":
			mu.Lock()
			secondAttempts++
			attempt := secondAttempts
			mu.Unlock()
			if attempt == 1 {
				secondStarted <- struct{}{}
				<-ctx.Done()
				return "", ctx.Err()
			}
			return "SECOND_OK", nil
		default:
			return "", fmt.Errorf("unexpected agent %q", opts.Name)
		}
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-pause", map[string]any{
		"async": true,
		"script": `
meta({ name: "pause_resume", description: "pause and resume" })
const first = await agent("first prompt", { label: "first" });
const second = await agent("second prompt", { label: "second" });
return { first: first, second: second };
`,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	runID := res.Details.(map[string]any)["runID"].(string)
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second agent did not start")
	}
	cmd := api.command("workflows")
	if cmd == nil {
		t.Fatal("missing workflows command")
	}
	if err := cmd("pause " + runID); err != nil {
		t.Fatalf("/workflows pause: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Pause requested") || !strings.Contains(got, "/workflows resume "+runID) {
		t.Fatalf("pause notify = %q", got)
	}
	statusPath := filepath.Join(sessionDir, "extensions", "workflow", "runs", runID, "status.json")
	statusData, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var status workflowRunStatusFile
	if err := json.Unmarshal(statusData, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Status != workflowStatusStopped || status.Error != "pause requested" {
		t.Fatalf("status = %+v", status)
	}
	if err := cmd("resume " + runID); err != nil {
		t.Fatalf("/workflows resume: %v", err)
	}
	api.waitNotifyContaining(t, "resumed in background")
	done := api.waitNotifyContaining(t, "Workflow pause_resume completed")
	if !strings.Contains(done, "FIRST_OK") || !strings.Contains(done, "SECOND_OK") {
		t.Fatalf("completion notify = %q", done)
	}
	if api.callCount() != 3 {
		t.Fatalf("call count after pause/resume = %d, want 3", api.callCount())
	}
}

func TestWorkflowsAgentStopSkipsOneRunningAgent(t *testing.T) {
	clearWorkflowDisableEnv(t)
	api := &fakeAPI{sessionDir: t.TempDir()}
	firstStarted := make(chan struct{}, 1)
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		switch opts.Name {
		case "first":
			firstStarted <- struct{}{}
			<-ctx.Done()
			return "", ctx.Err()
		case "second":
			return "SECOND_OK", nil
		default:
			return "", fmt.Errorf("unexpected agent %q", opts.Name)
		}
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-agent-stop", map[string]any{
		"async": true,
		"script": `
meta({ name: "agent_stop", description: "stop one agent" })
const first = await agent("first prompt", { label: "first" });
const second = await agent("second prompt", { label: "second" });
return { firstStopped: first === null, second: second };
`,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	runID := res.Details.(map[string]any)["runID"].(string)
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first agent did not start")
	}
	cmd := api.command("workflows")
	if cmd == nil {
		t.Fatal("missing workflows command")
	}
	if err := cmd("agent-stop " + runID + " 1"); err != nil {
		t.Fatalf("/workflows agent-stop: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Stop requested for workflow agent 1") {
		t.Fatalf("agent-stop notify = %q", got)
	}
	done := api.waitNotifyContaining(t, "Workflow agent_stop completed")
	if !strings.Contains(done, "firstStopped") || !strings.Contains(done, "SECOND_OK") {
		t.Fatalf("completion notify = %q", done)
	}
	if api.callCount() != 2 {
		t.Fatalf("call count = %d, want 2", api.callCount())
	}
	if err := cmd("agent " + runID + " 1"); err != nil {
		t.Fatalf("/workflows agent: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Agent status: skipped") || !strings.Contains(got, "stopped by user") {
		t.Fatalf("agent detail = %q", got)
	}
}

func TestWorkflowsAgentRestartRerunsOneRunningAgent(t *testing.T) {
	clearWorkflowDisableEnv(t)
	api := &fakeAPI{sessionDir: t.TempDir()}
	firstStarted := make(chan struct{}, 1)
	var mu sync.Mutex
	firstAttempts := 0
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name != "first" {
			return "", fmt.Errorf("unexpected agent %q", opts.Name)
		}
		mu.Lock()
		firstAttempts++
		attempt := firstAttempts
		mu.Unlock()
		if attempt == 1 {
			firstStarted <- struct{}{}
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "FIRST_RESTARTED", nil
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-agent-restart", map[string]any{
		"async": true,
		"script": `
meta({ name: "agent_restart", description: "restart one agent" })
const first = await agent("first prompt", { label: "first" });
return { first: first };
`,
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	runID := res.Details.(map[string]any)["runID"].(string)
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first agent did not start")
	}
	cmd := api.command("workflows")
	if cmd == nil {
		t.Fatal("missing workflows command")
	}
	if err := cmd("agent-restart " + runID + " 1"); err != nil {
		t.Fatalf("/workflows agent-restart: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Restart requested for workflow agent 1") {
		t.Fatalf("agent-restart notify = %q", got)
	}
	done := api.waitNotifyContaining(t, "Workflow agent_restart completed")
	if !strings.Contains(done, "FIRST_RESTARTED") {
		t.Fatalf("completion notify = %q", done)
	}
	if api.callCount() != 2 {
		t.Fatalf("call count = %d, want 2", api.callCount())
	}
	if err := cmd("agent " + runID + " 1"); err != nil {
		t.Fatalf("/workflows agent: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Agent status: skipped") || !strings.Contains(got, "restart requested") {
		t.Fatalf("agent detail = %q", got)
	}
	if err := cmd("agent " + runID + " 2"); err != nil {
		t.Fatalf("/workflows agent: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "Agent status: done") || !strings.Contains(got, "FIRST_RESTARTED") {
		t.Fatalf("restarted agent detail = %q", got)
	}
}

func TestSavedWorkflowCommandRegistersProjectPrecedenceAndRuns(t *testing.T) {
	clearWorkflowDisableEnv(t)
	cwd := t.TempDir()
	agentDir := t.TempDir()
	sessionDir := t.TempDir()
	projectDir := filepath.Join(cwd, ".coding_agent", "workflows")
	userDir := filepath.Join(agentDir, "workflows")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "review.js"), []byte(`
meta({ name: "user_cmd", description: "user command" })
return agent("user " + args.suffix, { label: "user" })
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "review.js"), []byte(`
meta({ name: "project_cmd", description: "project command" })
return agent("project " + args.suffix, { label: "project" })
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "bad name.js"), []byte(`meta({ name: "bad", description: "bad" })`), 0o600); err != nil {
		t.Fatal(err)
	}

	api := &fakeAPI{cwd: cwd, agentDir: agentDir, sessionDir: sessionDir}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name != "project" || !strings.Contains(opts.Task, "project OK") {
			return "", fmt.Errorf("bad saved workflow command call: %+v", opts)
		}
		return "COMMAND_OK", nil
	}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if api.command("bad name") != nil || api.command("workflow:bad name") != nil {
		t.Fatal("invalid saved workflow command should not register")
	}
	if api.command("review") == nil {
		t.Fatal("missing Claude-style saved workflow command")
	}
	if api.command("workflow:review") == nil {
		t.Fatal("missing compatibility saved workflow command")
	}
	cmd := api.command("review")
	if cmd == nil {
		t.Fatal("missing saved workflow command")
	}
	if err := cmd(`{"suffix":"OK"}`); err != nil {
		t.Fatalf("/review: %v", err)
	}
	started := api.waitNotifyContaining(t, "started in background")
	if !strings.Contains(started, "Workflow review started in background") || !strings.Contains(started, "Run: ") || !strings.Contains(started, "Open /workflows for the cockpit") {
		t.Fatalf("started notify = %q", started)
	}
	api.waitNotifyContaining(t, "Workflow project_cmd completed")
	if api.callCount() != 1 {
		t.Fatalf("call count = %d", api.callCount())
	}
	got := api.lastNotify()
	if !strings.Contains(got, "Workflow project_cmd completed") || !strings.Contains(got, "COMMAND_OK") || !strings.Contains(got, "Script: ") {
		t.Fatalf("notify = %q", got)
	}
	runs, _, err := ext.workflowRuns()
	if err != nil {
		t.Fatalf("workflowRuns: %v", err)
	}
	if len(runs) != 1 || runs[0].Snapshot == nil || runs[0].Snapshot.Name != "project_cmd" {
		t.Fatalf("runs = %+v", runs)
	}
}

func TestSavedWorkflowCommandKeepsReservedDirectCommandFree(t *testing.T) {
	clearWorkflowDisableEnv(t)
	cwd := t.TempDir()
	projectDir := filepath.Join(cwd, ".coding_agent", "workflows")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "workflows.js"), []byte(`
meta({ name: "reserved", description: "reserved command" })
return agent("reserved", { label: "reserved" })
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{cwd: cwd}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cmd := api.command("workflows")
	if cmd == nil {
		t.Fatal("missing /workflows management command")
	}
	if err := cmd("list"); err != nil {
		t.Fatalf("/workflows list: %v", err)
	}
	if got := api.lastNotify(); !strings.Contains(got, "No workflow runs found") && !strings.Contains(got, "Workflow runs:") {
		t.Fatalf("/workflows should remain the manager command, got %q", got)
	}
	if api.command("workflow:workflows") == nil {
		t.Fatal("missing compatibility command for reserved saved workflow name")
	}
}

func TestSavedWorkflowCommandRejectsInvalidJSONArgs(t *testing.T) {
	clearWorkflowDisableEnv(t)
	cwd := t.TempDir()
	projectDir := filepath.Join(cwd, ".coding_agent", "workflows")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "review.js"), []byte(`
meta({ name: "review", description: "review command" })
return agent("review", { label: "review" })
`), 0o600); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{cwd: cwd}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cmd := api.command("review")
	if cmd == nil {
		t.Fatal("missing saved workflow command")
	}
	if err := cmd(`{"bad"`); err != nil {
		t.Fatalf("/review: %v", err)
	}
	if api.callCount() != 0 {
		t.Fatalf("call count = %d", api.callCount())
	}
	if got := api.lastNotify(); !strings.Contains(got, "args must be valid JSON") {
		t.Fatalf("notify = %q", got)
	}
}

func TestToolExecuteRejectsInvalidBudget(t *testing.T) {
	api := &fakeAPI{}
	ext := &Extension{cfg: DefaultConfig(), api: api}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{
		"script": "meta({ name = \"x\", description = \"y\" }); return agent(\"x\")",
		"budget": float64(1.5),
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*types.TextContent).Text, "budget") {
		t.Fatalf("expected budget error result: %+v", res)
	}
}
