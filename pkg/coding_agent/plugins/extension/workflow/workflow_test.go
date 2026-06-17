package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

type fakeAPI struct {
	mu        sync.Mutex
	tools     []types.Tool
	cwd       string
	calls     []extension.ForkOptions
	responder func(context.Context, extension.ForkOptions) (string, error)
	active    int
	maxActive int
}

func (f *fakeAPI) RegisterTool(t types.Tool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = append(f.tools, t)
}
func (f *fakeAPI) RegisterCommand(string, string, extension.CommandHandler) {}
func (f *fakeAPI) AddHook(extension.ToolHook)                               {}
func (f *fakeAPI) On(string, extension.EventHandler)                        {}
func (f *fakeAPI) SendMessage(string) error                                 { return nil }
func (f *fakeAPI) SetActiveTools([]string)                                  {}
func (f *fakeAPI) SetModel(string, string) error                            { return nil }
func (f *fakeAPI) GetCommands() []extension.Command                         { return nil }
func (f *fakeAPI) SessionID() string                                        { return "session" }
func (f *fakeAPI) SessionDir() string                                       { return "" }
func (f *fakeAPI) AgentDir() string                                         { return "" }
func (f *fakeAPI) Cwd() string {
	if f.cwd != "" {
		return f.cwd
	}
	return "/repo"
}
func (f *fakeAPI) IsIdle() bool                                                  { return true }
func (f *fakeAPI) HasPendingMessages() bool                                      { return false }
func (f *fakeAPI) SendFollowUpMessage(string) error                              { return nil }
func (f *fakeAPI) SendMessageWithOptions(string, extension.MessageOptions) error { return nil }
func (f *fakeAPI) Notify(string, string)                                         {}
func (f *fakeAPI) Confirm(string, string, bool) bool                             { return true }
func (f *fakeAPI) Select(_ string, options []string) string {
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

func TestExtensionRegistersWorkflowTool(t *testing.T) {
	api := &fakeAPI{}
	ext := New()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(api.tools) != 1 || api.tools[0].Name() != "workflow" {
		t.Fatalf("registered tools = %#v", api.tools)
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
meta({ name = "demo", description = "dynamic phases" })
phase("Scan " .. args.area)
log("started")
local out = agent("inspect " .. args.area, { label = "scan" })
return { ok = true, out = out, cwd = process.cwd() }
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
	if _, err := newRunner(api, runOptions{}).run(context.Background(), `phase("x"); return {}`); err == nil || !strings.Contains(err.Error(), "meta") {
		t.Fatalf("expected missing meta error, got %v", err)
	}
	if _, err := newRunner(api, runOptions{}).run(context.Background(), `meta({name="x", description="y"}); return {}`); err == nil || !strings.Contains(err.Error(), "must call agent") {
		t.Fatalf("expected missing agent error, got %v", err)
	}
}

func TestRunWorkflowSandboxHidesUnsafeLibraries(t *testing.T) {
	api := &fakeAPI{}
	_, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name = "sandbox", description = "check globals" })
agent("x", { label = "x" })
return {
  os_missing = os == nil,
  io_missing = io == nil,
  package_missing = package == nil,
  require_missing = require == nil,
  load_missing = load == nil,
  random_missing = math.random == nil,
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestAgentMapsOptionsToForkSession(t *testing.T) {
	api := &fakeAPI{}
	_, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name = "map", description = "map opts" })
phase("Review")
return agent("inspect", {
  label = "repo scan",
  model = "model-a",
  cwd = "pkg/coding_agent",
  isolation = "worktree",
  tools = {"read", "grep"},
  disallowed_tools = {"bash"},
  permission_mode = "read-only",
  max_turns = 3,
  thinking = "low",
  skills = {"codebase"},
  memory_scope = "project",
})
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
meta({ name = "parallel", description = "fan out" })
phase("Fanout")
local out = parallel({
  { label = "first", prompt = "a" },
  { label = "bad", prompt = "fail" },
  { label = "third", prompt = "c" },
}, { concurrency = 2 })
return out
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

func TestPipelineStagesCanCallAgent(t *testing.T) {
	api := &fakeAPI{}
	api.responder = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		return "seen:" + opts.Name, nil
	}
	result, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name = "pipe", description = "pipeline" })
phase("Pipe")
return pipeline({"a", "b"}, {
  function(item, original, index)
    return agent("inspect " .. item, { label = "inspect " .. index })
  end,
  function(prev, original, index)
    return prev .. ":" .. original
  end,
})
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.([]any)
	if got[0] != "seen:inspect 1:a" || got[1] != "seen:inspect 2:b" {
		t.Fatalf("pipeline result = %#v", got)
	}
}

func TestPipelinePreservesOrderAndIsolatesStageFailures(t *testing.T) {
	api := &fakeAPI{}
	result, err := newRunner(api, runOptions{Concurrency: 2}).run(context.Background(), `
meta({ name = "pipe_fail", description = "pipeline failures" })
agent("anchor", { label = "anchor" })
return pipeline({"a", "bad", "c"}, {
  function(item, original, index)
    if item == "bad" then error("nope") end
    return item .. ":" .. index
  end,
})
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.([]any)
	if got[0] != "a:1" || got[1] != nil || got[2] != "c:3" {
		t.Fatalf("pipeline result = %#v", got)
	}
	if len(result.Snapshot.Logs) != 1 || !strings.Contains(result.Snapshot.Logs[0], "pipeline[2] failed") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestPipelineRejectsNestedPipeline(t *testing.T) {
	api := &fakeAPI{}
	result, err := newRunner(api, runOptions{}).run(context.Background(), `
meta({ name = "nested_pipe", description = "nested pipeline" })
agent("anchor", { label = "anchor" })
return pipeline({"a"}, {
  function(item)
    return pipeline({item}, { function(x) return x end })
  end,
})
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.Result.([]any)
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("nested pipeline should return null branch, got %#v", got)
	}
	if len(result.Snapshot.Logs) != 1 || !strings.Contains(result.Snapshot.Logs[0], "nested pipeline") {
		t.Fatalf("logs = %#v", result.Snapshot.Logs)
	}
}

func TestToolExecuteReturnsDetails(t *testing.T) {
	api := &fakeAPI{}
	ext := &Extension{cfg: DefaultConfig(), api: api}
	tool := newTool(ext)
	res, err := tool.Execute(context.Background(), "wf-1", map[string]any{"script": `
meta({ name = "tool", description = "tool result" })
return agent("x", { label = "x" })
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
