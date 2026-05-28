package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
	"github.com/openmodu/modu/pkg/types"
)

// fakeAPI implements extension.ExtensionAPI with a configurable ForkSession
// hook so tests can exercise single / parallel / chain dispatch without
// spinning up real child agents.
//
// Most ExtensionAPI methods return zero values — the subagent extension
// only touches RegisterTool and ForkSession in normal flow.
type fakeAPI struct {
	registered []agent.Tool
	commands   map[string]extension.CommandHandler
	notices    []string

	mu          sync.Mutex
	forkCalls   []extension.ForkOptions
	forkFn      func(ctx context.Context, opts extension.ForkOptions) (string, error)
	tasks       []extension.TaskSnapshot
	interruptFn func(id, reason string) (extension.TaskSnapshot, bool)
	confirmFn   func(title, body string, defaultYes bool) bool
	agentDir    string
	cwd         string
}

func (f *fakeAPI) RegisterTool(t agent.Tool) { f.registered = append(f.registered, t) }
func (f *fakeAPI) RegisterCommand(name string, _ string, h extension.CommandHandler) {
	if f.commands == nil {
		f.commands = map[string]extension.CommandHandler{}
	}
	f.commands[name] = h
}
func (f *fakeAPI) AddHook(extension.ToolHook)        {}
func (f *fakeAPI) On(string, extension.EventHandler) {}
func (f *fakeAPI) SendMessage(string) error          { return nil }
func (f *fakeAPI) SendMessageWithOptions(string, extension.MessageOptions) error {
	return nil
}
func (f *fakeAPI) SendFollowUpMessage(string) error { return nil }
func (f *fakeAPI) SetActiveTools([]string)          {}
func (f *fakeAPI) SetModel(string, string) error    { return nil }
func (f *fakeAPI) GetCommands() []extension.Command { return nil }
func (f *fakeAPI) SessionID() string                { return "test" }
func (f *fakeAPI) SessionDir() string               { return "" }
func (f *fakeAPI) AgentDir() string {
	if f.agentDir != "" {
		return f.agentDir
	}
	return "/tmp/agent"
}
func (f *fakeAPI) Cwd() string {
	if f.cwd != "" {
		return f.cwd
	}
	return "/tmp/project"
}
func (f *fakeAPI) IsIdle() bool             { return true }
func (f *fakeAPI) HasPendingMessages() bool { return false }
func (f *fakeAPI) Notify(_ string, text string) {
	f.mu.Lock()
	f.notices = append(f.notices, text)
	f.mu.Unlock()
}

// noticesSnapshot returns a copy of the recorded notices under the mutex.
// Tests must use this instead of reading f.notices directly when a
// background goroutine (e.g. the control timer) may call Notify in
// parallel.
func (f *fakeAPI) noticesSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.notices))
	copy(out, f.notices)
	return out
}

// confirmFn is an optional hook the test can install to drive the
// host's confirmation gate (used by the clarify flow). When nil,
// Confirm returns false to match the historical default.
func (f *fakeAPI) Confirm(title, body string, defaultYes bool) bool {
	if f.confirmFn != nil {
		return f.confirmFn(title, body, defaultYes)
	}
	return false
}
func (f *fakeAPI) Select(string, []string) string { return "" }
func (f *fakeAPI) BackgroundTasks() []extension.TaskSnapshot {
	return append([]extension.TaskSnapshot(nil), f.tasks...)
}
func (f *fakeAPI) InterruptBackgroundTask(id, reason string) (extension.TaskSnapshot, bool) {
	if f.interruptFn != nil {
		return f.interruptFn(id, reason)
	}
	return extension.TaskSnapshot{}, false
}

func (f *fakeAPI) ForkSession(ctx context.Context, opts extension.ForkOptions) (string, error) {
	f.mu.Lock()
	f.forkCalls = append(f.forkCalls, opts)
	fn := f.forkFn
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, opts)
	}
	return "[forked] " + opts.Task, nil
}

// forkOptionsSnapshot returns a copy of the recorded ForkOptions under the
// mutex. Tests that observe a background goroutine's writes must use this
// instead of reading f.forkCalls directly.
func (f *fakeAPI) forkOptionsSnapshot() []extension.ForkOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]extension.ForkOptions, len(f.forkCalls))
	copy(out, f.forkCalls)
	return out
}

// writeProfile creates an agent profile .md file in dir and returns its path.
// Frontmatter uses the existing utils.ParseFrontmatter format (key: value
// per line, no `---` markers required) since that's what csubagent.Loader
// parses.
func writeProfile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

// frontmatterBody assembles a minimal profile body the existing loader can
// parse. Keys are simple; tests don't exercise every field.
func frontmatterBody(name, desc string) string {
	return "---\nname: " + name + "\ndescription: " + desc + "\n---\nSystem prompt for " + name + ".\n"
}

func newExtensionWithProfiles(t *testing.T, profiles map[string]string) (*Extension, *fakeAPI) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range profiles {
		writeProfile(t, dir, name, body)
	}
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return ext, api
}

func toolOf(t *testing.T, api *fakeAPI) agent.Tool {
	t.Helper()
	return registeredTool(t, api, "subagent")
}

func registeredTool(t *testing.T, api *fakeAPI, name string) agent.Tool {
	t.Helper()
	for _, tool := range api.registered {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("registered tool %q not found; got %v", name, registeredToolNames(api))
	return nil
}

func registeredToolNames(api *fakeAPI) []string {
	names := make([]string, 0, len(api.registered))
	for _, tool := range api.registered {
		names = append(names, tool.Name())
	}
	return names
}

func runCommand(t *testing.T, api *fakeAPI, name, args string) {
	t.Helper()
	h := api.commands[name]
	if h == nil {
		t.Fatalf("command %q not registered; got %v", name, api.commands)
	}
	if err := h(args); err != nil {
		t.Fatalf("command %s: %v", name, err)
	}
}

func TestInitNoProfilesRegistersManagementToolOnly(t *testing.T) {
	ext := New()
	ext.cfg.AgentsDir = filepath.Join(t.TempDir(), "missing")
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Always-on tools: the subagent management/exec tool plus the intercom
	// send tool. The spawn_subagent compatibility alias only appears when
	// at least one profile is discovered.
	got := registeredToolNames(api)
	want := map[string]bool{"subagent": false, "subagent_intercom_send": false}
	for _, name := range got {
		if _, ok := want[name]; ok {
			want[name] = true
		} else {
			t.Errorf("unexpected tool %q registered with empty agents dir, got %v", name, got)
		}
	}
	for name, present := range want {
		if !present {
			t.Errorf("expected %q to be registered, got %v", name, got)
		}
	}
}

func TestInitDiscoversProfilesAndRegistersTool(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	for _, name := range []string{"subagent", "spawn_subagent"} {
		if got := registeredTool(t, api, name).Name(); got != name {
			t.Errorf("tool name = %q, want %q", got, name)
		}
	}
}

func TestSingleModeDispatchesForkSession(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	tool := toolOf(t, api)

	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if !strings.HasPrefix(opts.SystemPrompt, "System prompt for reviewer") {
			t.Errorf("forkSession got wrong prompt: %q", opts.SystemPrompt)
		}
		if opts.Task != "look at PR 42" {
			t.Errorf("forkSession got wrong task: %q", opts.Task)
		}
		return "looks good", nil
	}

	res, err := tool.Execute(context.Background(), "id1", map[string]any{
		"mode":  "single",
		"agent": "reviewer",
		"task":  "look at PR 42",
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if len(ext.loader.List()) != 1 {
		t.Errorf("loader did not pick up profile: %v", ext.loader.List())
	}
	if !strings.Contains(textOf(res), "looks good") {
		t.Errorf("result missing fork output: %s", textOf(res))
	}
}

func TestSingleModeAsyncArgumentOverridesProfileBackground(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"foreground": frontmatterBody("foreground", "normally foreground"),
		"background": `---
name: background
description: normally background
background: true
---
System prompt for background.
`,
	})
	tool := toolOf(t, api)

	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name == "foreground" && !opts.Background {
			t.Errorf("async=true did not force background: %+v", opts)
		}
		if opts.Name == "background" && opts.Background {
			t.Errorf("async=false did not force foreground: %+v", opts)
		}
		return "ok", nil
	}

	res, err := tool.Execute(context.Background(), "async-1", map[string]any{
		"agent": "foreground",
		"task":  "go",
		"async": true,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("async=true call failed: err=%v res=%+v", err, res)
	}
	res, err = tool.Execute(context.Background(), "async-2", map[string]any{
		"agent": "background",
		"task":  "go",
		"async": false,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("async=false call failed: err=%v res=%+v", err, res)
	}
}

func TestMaxDepthAllowsTopLevelAndBlocksNested(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "do work"),
	})
	tool := toolOf(t, api)

	api.forkFn = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		if got := subagentDepth(ctx); got != 1 {
			t.Errorf("fork context depth=%d, want 1", got)
		}
		return "ok:" + opts.Name, nil
	}
	res, err := tool.Execute(context.Background(), "depth-1", map[string]any{
		"agent": "worker",
		"task":  "top-level",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("top-level subagent should be allowed: err=%v res=%+v", err, res)
	}

	res, err = tool.Execute(withSubagentDepth(context.Background(), ext.cfg.MaxDepth), "depth-2", map[string]any{
		"agent": "worker",
		"task":  "nested",
	}, nil)
	if err != nil {
		t.Fatalf("nested tool execution should return tool error, not Go error: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "max_depth=1 reached") {
		t.Fatalf("expected max_depth tool error, got:\n%s", textOf(res))
	}
}

func TestMaxDepthZeroDisablesSubagentExecution(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "do work"),
	})
	ext.cfg.MaxDepth = 0
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "depth-0", map[string]any{
		"agent": "worker",
		"task":  "top-level",
	}, nil)
	if err != nil {
		t.Fatalf("tool execution should return tool error, not Go error: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "max_depth=0 reached") {
		t.Fatalf("expected max_depth=0 tool error, got:\n%s", textOf(res))
	}
}

func TestSingleModeContextModelAndSkillOverrides(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "worker", `---
name: worker
description: do work
model: profile-model
skills: profile-skill,other-profile-skill
default_context: fork
---
System prompt for worker.
`)
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	var captured []extension.ForkOptions
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		captured = append(captured, opts)
		return "ok", nil
	}

	res, err := tool.Execute(context.Background(), "override-1", map[string]any{
		"agent": "worker",
		"task":  "use defaults",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("default override call failed: err=%v res=%+v", err, res)
	}
	if captured[0].Model != "profile-model" || captured[0].Context != "fork" {
		t.Fatalf("profile defaults not applied: %+v", captured[0])
	}
	if strings.Join(captured[0].Skills, ",") != "profile-skill,other-profile-skill" {
		t.Fatalf("profile skills not applied: %v", captured[0].Skills)
	}

	res, err = tool.Execute(context.Background(), "override-2", map[string]any{
		"agent":   "worker",
		"task":    "override",
		"context": "fresh",
		"model":   "call-model",
		"skill":   []any{"call-skill", "extra-skill"},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("call override failed: err=%v res=%+v", err, res)
	}
	if captured[1].Model != "call-model" || captured[1].Context != "" {
		t.Fatalf("call model/context override not applied: %+v", captured[1])
	}
	if strings.Join(captured[1].Skills, ",") != "call-skill,extra-skill" {
		t.Fatalf("call skill override not applied: %v", captured[1].Skills)
	}

	res, err = tool.Execute(context.Background(), "override-3", map[string]any{
		"agent": "worker",
		"task":  "disable skills",
		"skill": false,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("skill=false call failed: err=%v res=%+v", err, res)
	}
	if len(captured[2].Skills) != 0 {
		t.Fatalf("skill=false should disable profile skills, got %v", captured[2].Skills)
	}
}

func TestSingleModeOutputFileOnlySavesResult(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "large output\nline two", nil
	}
	out := filepath.Join(t.TempDir(), "review.md")
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "output-1", map[string]any{
		"agent":      "reviewer",
		"task":       "write report",
		"output":     out,
		"outputMode": "file-only",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("output save failed: err=%v res=%+v", err, res)
	}
	if got := textOf(res); !strings.Contains(got, "Output saved to: "+out) || strings.Contains(got, "large output") {
		t.Fatalf("file-only result should be compact reference, got:\n%s", got)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(data) != "large output\nline two" {
		t.Fatalf("saved output mismatch: %q", string(data))
	}
}

func TestParallelModePerTaskOutputSavesResult(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r1": frontmatterBody("r1", "first"),
		"r2": frontmatterBody("r2", "second"),
	})
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		return "done:" + opts.Name, nil
	}
	dir := t.TempDir()
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "output-2", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "r1", "task": "A", "output": filepath.Join(dir, "r1.md"), "outputMode": "file-only"},
			map[string]any{"agent": "r2", "task": "B"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("parallel output save failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	if !strings.Contains(got, "Output saved to: "+filepath.Join(dir, "r1.md")) || !strings.Contains(got, "done:r2") {
		t.Fatalf("parallel output mismatch:\n%s", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "r1.md"))
	if err != nil {
		t.Fatalf("read per-task output: %v", err)
	}
	if string(data) != "done:r1" {
		t.Fatalf("per-task output mismatch: %q", string(data))
	}
}

func TestSingleModeReadsAndProgressInjectTaskInstructions(t *testing.T) {
	dir := t.TempDir()
	agentDir := t.TempDir()
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout": `---
name: scout
description: scan
reads: docs/overview.md,/absolute/ref.md
progress: true
---
System prompt for scout.
`,
	})
	api.cwd = dir
	api.agentDir = agentDir
	tool := toolOf(t, api)

	var captured []extension.ForkOptions
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		captured = append(captured, opts)
		return "ok", nil
	}

	res, err := tool.Execute(context.Background(), "behavior-1", map[string]any{
		"agent": "scout",
		"task":  "inspect auth",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("reads/progress call failed: err=%v res=%+v", err, res)
	}
	if len(captured) != 1 {
		t.Fatalf("expected one fork call, got %d", len(captured))
	}
	progressPath := filepath.Join(agentDir, "tool-results", projectKey(dir), "subagents", "progress.md")
	for _, want := range []string{
		"[Read from: " + filepath.Join(dir, "docs/overview.md") + ", /absolute/ref.md]",
		"inspect auth",
		"Create and maintain progress at: " + progressPath,
	} {
		if !strings.Contains(captured[0].Task, want) {
			t.Fatalf("task missing %q:\n%s", want, captured[0].Task)
		}
	}
	if data, err := os.ReadFile(progressPath); err != nil {
		t.Fatalf("expected initial progress file: %v", err)
	} else if !strings.Contains(string(data), "## Status\nIn Progress") {
		t.Fatalf("unexpected progress file content:\n%s", string(data))
	}

	res, err = tool.Execute(context.Background(), "behavior-2", map[string]any{
		"agent":    "scout",
		"task":     "plain",
		"reads":    false,
		"progress": false,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("reads/progress override call failed: err=%v res=%+v", err, res)
	}
	if got := captured[1].Task; got != "plain" {
		t.Fatalf("reads/progress false should suppress profile defaults, got:\n%s", got)
	}
}

func TestListActionShowsDiscoveredProfiles(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
		"scout":    frontmatterBody("scout", "scan code"),
	})
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "list-1", map[string]any{"action": "list"}, nil)
	if err != nil || res.IsError {
		t.Fatalf("list failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	for _, want := range []string{"Available subagents (2):", "- reviewer: review diffs", "- scout: scan code"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q:\n%s", want, got)
		}
	}
}

func TestStatusActionListsAndResolvesBackgroundTasks(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	api.tasks = []extension.TaskSnapshot{
		{ID: "task-1", Kind: "subagent", Status: "running", Summary: "reviewer: check"},
		{ID: "task-2", Kind: "bash", Status: "running", Summary: "server"},
		{ID: "task-3", Kind: "subagent", Status: "completed", Summary: "scout: scan", Output: "done"},
		{ID: "task-4", Kind: "subagent", Status: "running", Summary: "reviewer: follow-up", ParentID: "task-1"},
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "status-1", map[string]any{"action": "status"}, nil)
	if err != nil || res.IsError {
		t.Fatalf("status failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	if !strings.Contains(got, "task-1 [running] reviewer: check") || strings.Contains(got, "server") {
		t.Fatalf("status list should include only subagent tasks, got:\n%s", got)
	}
	if !strings.Contains(got, "\n  - task-4 [running] reviewer: follow-up") {
		t.Fatalf("status list should render child tasks under parents, got:\n%s", got)
	}

	res, err = tool.Execute(context.Background(), "status-2", map[string]any{"action": "status", "id": "task-3"}, nil)
	if err != nil || res.IsError {
		t.Fatalf("status by id failed: err=%v res=%+v", err, res)
	}
	got = textOf(res)
	if !strings.Contains(got, "Task task-3") || !strings.Contains(got, "output:\ndone") {
		t.Fatalf("status by id missing task output:\n%s", got)
	}
}

func TestResumeActionRestartsCompletedTaskInBackground(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	api.tasks = []extension.TaskSnapshot{
		{
			ID:      "task-1",
			Kind:    "subagent",
			Status:  "completed",
			Summary: "reviewer: old task",
			Agent:   "reviewer",
			Task:    "old task",
			Output:  "old output",
		},
	}
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if !opts.Background {
			t.Errorf("resume should start a background fork: %+v", opts)
		}
		if opts.ParentTaskID != "task-1" {
			t.Errorf("ParentTaskID=%q, want task-1", opts.ParentTaskID)
		}
		for _, want := range []string{"old task", "old output", "check the edge case"} {
			if !strings.Contains(opts.Task, want) {
				t.Errorf("resume prompt missing %q:\n%s", want, opts.Task)
			}
		}
		return "Started extension-fork in background. Use task_output with task_id=task-2 to inspect the result.", nil
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "resume-1", map[string]any{
		"action":  "resume",
		"id":      "task-1",
		"message": "check the edge case",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("resume failed: err=%v res=%+v", err, res)
	}
	if got := textOf(res); !strings.Contains(got, "task_id=task-2") {
		t.Fatalf("resume output missing new task id:\n%s", got)
	}
}

func TestInterruptActionCancelsLiveTask(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	api.tasks = []extension.TaskSnapshot{
		{ID: "task-1", Kind: "subagent", Status: "running", Summary: "reviewer: old task", Agent: "reviewer", Task: "old task"},
	}
	api.interruptFn = func(id, reason string) (extension.TaskSnapshot, bool) {
		if id != "task-1" {
			t.Errorf("interrupt id=%q, want task-1", id)
		}
		if reason != "stop now" {
			t.Errorf("interrupt reason=%q, want stop now", reason)
		}
		return extension.TaskSnapshot{ID: "task-1", Kind: "subagent", Status: "interrupted", Summary: "reviewer: old task", Agent: "reviewer", Task: "old task", Error: reason}, true
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "interrupt-1", map[string]any{
		"action":  "interrupt",
		"id":      "task",
		"message": "stop now",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("interrupt failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	if !strings.Contains(got, "status: interrupted") || !strings.Contains(got, "stop now") {
		t.Fatalf("interrupt output missing status/reason:\n%s", got)
	}
}

func TestDoctorActionReportsMissingProfiles(t *testing.T) {
	ext := New()
	ext.cfg.AgentsDir = filepath.Join(t.TempDir(), "missing")
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := registeredTool(t, api, "subagent")

	res, err := tool.Execute(context.Background(), "doctor-1", map[string]any{"action": "doctor"}, nil)
	if err != nil || res.IsError {
		t.Fatalf("doctor failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	if !strings.Contains(got, "status: warning") || !strings.Contains(got, "no subagent profiles discovered") {
		t.Fatalf("doctor should warn about missing profiles, got:\n%s", got)
	}
}

// TestDoctorActionEnrichedFields covers the new doctor lines: profile source
// breakdown, runtime dir + existence status, background task count, and the
// force_top_level_async flag.
func TestDoctorActionEnrichedFields(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "alpha", frontmatterBody("alpha", "a"))
	writeProfile(t, dir, "beta", frontmatterBody("beta", "b"))

	agentDir := t.TempDir()
	cwd := t.TempDir()
	ext := New()
	ext.cfg.AgentsDir = dir
	ext.cfg.ForceTopLevelAsync = true
	api := &fakeAPI{agentDir: agentDir, cwd: cwd}
	// Use completed/non-running statuses so the stale-run reconciler stays
	// quiet — the reconciler-warning path is covered separately.
	api.tasks = []extension.TaskSnapshot{
		{ID: "task-1", Kind: "subagent", Status: "completed", Summary: "alpha: x"},
		{ID: "task-2", Kind: "bash", Status: "running", Summary: "irrelevant"},
	}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "doctor-rich", map[string]any{"action": "doctor"}, nil)
	if err != nil || res.IsError {
		t.Fatalf("doctor failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	wantSubstrings := []string{
		"status: ok",
		"profiles discovered: 2",
		"profile sources: extra 2",
		"subagents runtime dir: ",
		"(missing, created on first use)",
		"background subagent tasks: 1",
		"force_top_level_async: true",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("doctor output missing %q:\n%s", want, got)
		}
	}
}

func TestSlashRunCommandDispatchesAndNotifies(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name != "reviewer" {
			t.Errorf("forkSession got wrong name: %q", opts.Name)
		}
		if opts.Task != "look at PR 42" {
			t.Errorf("forkSession got wrong task: %q", opts.Task)
		}
		return "looks good", nil
	}

	runCommand(t, api, "run", `reviewer "look at PR 42"`)
	if len(api.notices) != 1 || !strings.Contains(api.notices[0], "looks good") {
		t.Fatalf("expected command notification, got %#v", api.notices)
	}
}

func TestSlashParallelAndChainCommandsDispatch(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout":   frontmatterBody("scout", "scan code"),
		"planner": frontmatterBody("planner", "make plan"),
	})
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		return "done:" + opts.Name + ":" + opts.Task, nil
	}

	runCommand(t, api, "parallel", "scout scan repo -> planner make plan")
	if len(api.notices) != 1 || !strings.Contains(api.notices[0], "[0] scout") || !strings.Contains(api.notices[0], "[1] planner") {
		t.Fatalf("expected parallel notification, got %#v", api.notices)
	}

	api.notices = nil
	runCommand(t, api, "chain", "scout scan repo -> planner use {previous}")
	if len(api.notices) != 1 || !strings.Contains(api.notices[0], "done:planner:use done:scout:scan repo") {
		t.Fatalf("expected chain notification, got %#v", api.notices)
	}
}

func TestSlashDoctorCommandNotifiesDiagnostics(t *testing.T) {
	ext := New()
	ext.cfg.AgentsDir = filepath.Join(t.TempDir(), "missing")
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}

	runCommand(t, api, "subagents-doctor", "")
	if len(api.notices) != 1 || !strings.Contains(api.notices[0], "status: warning") {
		t.Fatalf("expected doctor warning notification, got %#v", api.notices)
	}
}

func TestLegacySpawnSubagentToolDispatchesViaExtension(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	tool := registeredTool(t, api, "spawn_subagent")

	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name != "reviewer" {
			t.Errorf("forkSession got wrong name: %q", opts.Name)
		}
		if opts.Task != "look at PR 42" {
			t.Errorf("forkSession got wrong task: %q", opts.Task)
		}
		return "looks good", nil
	}

	res, err := tool.Execute(context.Background(), "id1", map[string]any{
		"name": "reviewer",
		"task": "look at PR 42",
	}, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res)
	}
	if !strings.Contains(textOf(res), "looks good") {
		t.Errorf("result missing fork output: %s", textOf(res))
	}
	details, _ := res.Details.(map[string]string)
	if details["subagent"] != "reviewer" {
		t.Errorf("legacy result details = %#v", res.Details)
	}
}

func TestSingleModeOmittingModeStillRuns(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "reviewer"),
	})
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "id", map[string]any{
		"agent": "r",
		"task":  "do thing",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("default single mode failed: err=%v res=%+v", err, res)
	}
}

func TestSingleModeMissingAgentErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "x"),
	})
	tool := toolOf(t, api)

	res, _ := tool.Execute(context.Background(), "id", map[string]any{
		"mode": "single",
		"task": "x",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "requires \"agent\"") {
		t.Errorf("expected missing-agent error, got %s", textOf(res))
	}
}

func TestSingleModeUnknownAgentErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "x"),
	})
	tool := toolOf(t, api)

	res, _ := tool.Execute(context.Background(), "id", map[string]any{
		"mode":  "single",
		"agent": "ghost",
		"task":  "x",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), `agent "ghost" not found`) {
		t.Errorf("expected not-found error, got %s", textOf(res))
	}
}

func TestUnknownModeErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "x"),
	})
	tool := toolOf(t, api)

	res, _ := tool.Execute(context.Background(), "id", map[string]any{
		"mode": "fanout",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "unknown mode") {
		t.Errorf("expected unknown-mode error, got %s", textOf(res))
	}
}

func TestParallelModeFansOutConcurrently(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r1": frontmatterBody("r1", "first"),
		"r2": frontmatterBody("r2", "second"),
	})
	tool := toolOf(t, api)

	var concurrent atomic.Int32
	var peak atomic.Int32
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		current := concurrent.Add(1)
		// Update peak with simple CAS loop — atomic.Max isn't on Int32.
		for {
			p := peak.Load()
			if current <= p || peak.CompareAndSwap(p, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		concurrent.Add(-1)
		return "done:" + opts.Task, nil
	}

	res, err := tool.Execute(context.Background(), "id", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "r1", "task": "A"},
			map[string]any{"agent": "r2", "task": "B"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("parallel failed: err=%v res=%+v", err, res)
	}
	if peak.Load() < 2 {
		t.Errorf("expected peak concurrency >= 2, got %d", peak.Load())
	}
	got := textOf(res)
	for _, want := range []string{"[0] r1", "done:A", "[1] r2", "done:B"} {
		if !strings.Contains(got, want) {
			t.Errorf("parallel output missing %q\n%s", want, got)
		}
	}
}

func TestTasksAliasExpandsCountAndLimitsConcurrency(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "does work"),
	})
	tool := toolOf(t, api)

	var concurrent atomic.Int32
	var peak atomic.Int32
	var total atomic.Int32
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		total.Add(1)
		current := concurrent.Add(1)
		for {
			p := peak.Load()
			if current <= p || peak.CompareAndSwap(p, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		concurrent.Add(-1)
		return "done:" + opts.Task, nil
	}

	res, err := tool.Execute(context.Background(), "tasks", map[string]any{
		"tasks": []any{
			map[string]any{"agent": "worker", "task": "A", "count": 3},
		},
		"concurrency": 2,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("tasks alias failed: err=%v res=%+v", err, res)
	}
	if total.Load() != 3 {
		t.Fatalf("count should expand to 3 fork calls, got %d", total.Load())
	}
	if peak.Load() > 2 {
		t.Fatalf("concurrency limit should cap peak at 2, got %d", peak.Load())
	}
	got := textOf(res)
	for _, want := range []string{"[0] worker", "[1] worker", "[2] worker"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tasks output missing %q:\n%s", want, got)
		}
	}
}

func TestParallelModeReadsAndProgressApplyPerTask(t *testing.T) {
	dir := t.TempDir()
	chainDir := filepath.Join(dir, "chain")
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r1": frontmatterBody("r1", "first"),
		"r2": frontmatterBody("r2", "second"),
	})
	api.cwd = dir
	tool := toolOf(t, api)

	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		return "done:" + opts.Name, nil
	}
	res, err := tool.Execute(context.Background(), "behavior-parallel", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "r1", "task": "A", "reads": []any{"handoff.md"}, "progress": true, "chainDir": chainDir},
			map[string]any{"agent": "r2", "task": "B", "progress": true, "chainDir": chainDir},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("parallel reads/progress failed: err=%v res=%+v", err, res)
	}

	byName := map[string]string{}
	for _, call := range api.forkCalls {
		byName[call.Name] = call.Task
	}
	if !strings.Contains(byName["r1"], "[Read from: "+filepath.Join(chainDir, "handoff.md")+"]") {
		t.Fatalf("r1 task missing read instruction:\n%s", byName["r1"])
	}
	if !strings.Contains(byName["r1"], "Create and maintain progress at: "+filepath.Join(chainDir, "progress.md")) {
		t.Fatalf("r1 task should create progress:\n%s", byName["r1"])
	}
	if !strings.Contains(byName["r2"], "Update progress at: "+filepath.Join(chainDir, "progress.md")) {
		t.Fatalf("r2 task should update progress:\n%s", byName["r2"])
	}
}

func TestParallelModeOnePairFailsOthersStillReport(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"good": frontmatterBody("good", "ok"),
		"bad":  frontmatterBody("bad", "explodes"),
	})
	tool := toolOf(t, api)

	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if strings.HasPrefix(opts.SystemPrompt, "System prompt for bad") {
			return "", errors.New("boom")
		}
		return "fine", nil
	}

	res, _ := tool.Execute(context.Background(), "id", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "good", "task": "x"},
			map[string]any{"agent": "bad", "task": "y"},
		},
	}, nil)
	if res.IsError {
		t.Fatalf("parallel-with-partial-failure should not surface as error result: %+v", res)
	}
	got := textOf(res)
	if !strings.Contains(got, "fine") || !strings.Contains(got, "ERROR: boom") {
		t.Errorf("parallel partial failure output wrong:\n%s", got)
	}
}

func TestParallelModeEmptyArrayErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "x"),
	})
	tool := toolOf(t, api)

	res, _ := tool.Execute(context.Background(), "id", map[string]any{
		"mode":     "parallel",
		"parallel": []any{},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "non-empty") {
		t.Errorf("expected non-empty error, got %s", textOf(res))
	}
}

func TestChainModeSubstitutesPrevious(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout":   frontmatterBody("scout", "recon"),
		"planner": frontmatterBody("planner", "plan"),
	})
	tool := toolOf(t, api)

	step := 0
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		step++
		if step == 1 {
			if opts.Task != "find the file" {
				t.Errorf("step 1 task = %q", opts.Task)
			}
			return "found foo.go", nil
		}
		// step 2 — {previous} must have been substituted.
		if opts.Task != "plan around: found foo.go" {
			t.Errorf("step 2 task did not substitute {previous}: %q", opts.Task)
		}
		return "plan: edit foo.go", nil
	}

	res, _ := tool.Execute(context.Background(), "id", map[string]any{
		"mode": "chain",
		"chain": []any{
			map[string]any{"agent": "scout", "task": "find the file"},
			map[string]any{"agent": "planner", "task": "plan around: {previous}"},
		},
	}, nil)
	if res.IsError {
		t.Fatalf("chain failed: %+v", res)
	}
	if !strings.Contains(textOf(res), "plan: edit foo.go") {
		t.Errorf("chain result should be final step's output, got %s", textOf(res))
	}
}

func TestChainModeRunsParallelGroupAndFeedsAggregateToNextStep(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout":      frontmatterBody("scout", "recon"),
		"reviewer-a": frontmatterBody("reviewer-a", "review a"),
		"reviewer-b": frontmatterBody("reviewer-b", "review b"),
		"planner":    frontmatterBody("planner", "plan"),
	})
	tool := toolOf(t, api)

	var concurrent atomic.Int32
	var peak atomic.Int32
	var plannerTask string
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name == "reviewer-a" || opts.Name == "reviewer-b" {
			current := concurrent.Add(1)
			for {
				p := peak.Load()
				if current <= p || peak.CompareAndSwap(p, current) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			concurrent.Add(-1)
		}
		switch opts.Name {
		case "scout":
			return "scout output", nil
		case "reviewer-a":
			if opts.Task != "review scout output" {
				t.Errorf("reviewer-a task = %q", opts.Task)
			}
			return "A says ok", nil
		case "reviewer-b":
			if opts.Task != "review scout output" {
				t.Errorf("reviewer-b task = %q", opts.Task)
			}
			return "B says fix tests", nil
		case "planner":
			plannerTask = opts.Task
			return "final plan", nil
		default:
			return "unexpected", nil
		}
	}

	res, err := tool.Execute(context.Background(), "chain-parallel", map[string]any{
		"chain": []any{
			map[string]any{"agent": "scout", "task": "scan"},
			map[string]any{"parallel": []any{
				map[string]any{"agent": "reviewer-a", "task": "review {previous}"},
				map[string]any{"agent": "reviewer-b", "task": "review {previous}"},
			}},
			map[string]any{"agent": "planner", "task": "combine {previous}"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain parallel group failed: err=%v res=%+v", err, res)
	}
	if peak.Load() < 2 {
		t.Fatalf("parallel group should run concurrently, peak=%d", peak.Load())
	}
	for _, want := range []string{"[0] reviewer-a", "A says ok", "[1] reviewer-b", "B says fix tests"} {
		if !strings.Contains(plannerTask, want) {
			t.Fatalf("planner did not receive aggregate %q:\n%s", want, plannerTask)
		}
	}
	if !strings.Contains(textOf(res), "final plan") {
		t.Fatalf("chain should return final sequential output, got:\n%s", textOf(res))
	}
}

func TestChainModeReadsAndProgressUseSharedChainDir(t *testing.T) {
	dir := t.TempDir()
	chainDir := filepath.Join(dir, "shared")
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout":   frontmatterBody("scout", "recon"),
		"planner": frontmatterBody("planner", "plan"),
	})
	api.cwd = dir
	tool := toolOf(t, api)

	var calls []extension.ForkOptions
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		calls = append(calls, opts)
		return "done:" + opts.Name, nil
	}
	res, err := tool.Execute(context.Background(), "behavior-chain", map[string]any{
		"mode":     "chain",
		"chainDir": chainDir,
		"chain": []any{
			map[string]any{"agent": "scout", "task": "scan", "reads": []any{"context.md"}, "progress": true},
			map[string]any{"agent": "planner", "task": "plan from {previous}", "progress": true},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain reads/progress failed: err=%v res=%+v", err, res)
	}
	if len(calls) != 2 {
		t.Fatalf("expected two chain calls, got %d", len(calls))
	}
	for _, want := range []string{
		"[Read from: " + filepath.Join(chainDir, "context.md") + "]",
		"Create and maintain progress at: " + filepath.Join(chainDir, "progress.md"),
	} {
		if !strings.Contains(calls[0].Task, want) {
			t.Fatalf("first chain task missing %q:\n%s", want, calls[0].Task)
		}
	}
	if !strings.Contains(calls[1].Task, "Update progress at: "+filepath.Join(chainDir, "progress.md")) {
		t.Fatalf("second chain task should update progress:\n%s", calls[1].Task)
	}
}

func TestChainModeStopsOnFirstFailure(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"a": frontmatterBody("a", "x"),
		"b": frontmatterBody("b", "x"),
	})
	tool := toolOf(t, api)

	step := 0
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		step++
		if step == 1 {
			return "", errors.New("boom")
		}
		t.Fatalf("step 2 should not run after step 1 failed")
		return "", nil
	}

	res, _ := tool.Execute(context.Background(), "id", map[string]any{
		"mode": "chain",
		"chain": []any{
			map[string]any{"agent": "a", "task": "x"},
			map[string]any{"agent": "b", "task": "y"},
		},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "chain step 0 (a): boom") {
		t.Errorf("expected chain-step error, got %s", textOf(res))
	}
}

// TestPerCallCwdForwardsAcrossModes covers the per-call `cwd` field added to
// single, parallel, chain step, and chain-parallel-group items. The extension
// forwards `cwd` verbatim (after TrimSpace) into ForkOptions.Cwd; downstream
// ForkSession is responsible for resolving it against the parent session cwd.
func TestPerCallCwdForwardsAcrossModes(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout":   frontmatterBody("scout", "recon"),
		"planner": frontmatterBody("planner", "plan"),
	})
	tool := toolOf(t, api)

	var (
		mu       sync.Mutex
		captured []extension.ForkOptions
	)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		captured = append(captured, opts)
		mu.Unlock()
		return "ok:" + opts.Name, nil
	}

	res, err := tool.Execute(context.Background(), "cwd-single", map[string]any{
		"agent": "scout",
		"task":  "x",
		"cwd":   "  workdir/single  ",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("single cwd failed: err=%v res=%+v", err, res)
	}

	res, err = tool.Execute(context.Background(), "cwd-parallel", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "scout", "task": "a", "cwd": "workdir/p1"},
			map[string]any{"agent": "planner", "task": "b", "cwd": "workdir/p2"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("parallel cwd failed: err=%v res=%+v", err, res)
	}

	res, err = tool.Execute(context.Background(), "cwd-chain", map[string]any{
		"chain": []any{
			map[string]any{"agent": "scout", "task": "scan", "cwd": "workdir/chain-step"},
			map[string]any{"parallel": []any{
				map[string]any{"agent": "planner", "task": "plan {previous}", "cwd": "workdir/chain-group"},
			}},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain cwd failed: err=%v res=%+v", err, res)
	}

	if len(captured) != 5 {
		t.Fatalf("expected 5 fork calls, got %d", len(captured))
	}
	if captured[0].Cwd != "workdir/single" {
		t.Errorf("single cwd should trim whitespace and forward, got %q", captured[0].Cwd)
	}
	// Parallel goroutines run in undefined order; key by agent name.
	parallelByName := map[string]string{
		captured[1].Name: captured[1].Cwd,
		captured[2].Name: captured[2].Cwd,
	}
	if parallelByName["scout"] != "workdir/p1" || parallelByName["planner"] != "workdir/p2" {
		t.Errorf("parallel cwd not forwarded per item: %#v", parallelByName)
	}
	if captured[3].Cwd != "workdir/chain-step" {
		t.Errorf("chain sequential cwd not forwarded, got %q", captured[3].Cwd)
	}
	if captured[4].Cwd != "workdir/chain-group" {
		t.Errorf("chain parallel-group item cwd not forwarded, got %q", captured[4].Cwd)
	}
}

func TestChainStepRejectsCountField(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "x"),
	})
	tool := toolOf(t, api)

	res, _ := tool.Execute(context.Background(), "chain-count", map[string]any{
		"chain": []any{
			map[string]any{"agent": "worker", "task": "x", "count": 2},
		},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "count is only supported inside parallel groups") {
		t.Errorf("expected count rejection in chain step, got: %s", textOf(res))
	}
}

func TestChainParallelGroupRespectsPerGroupConcurrency(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "x"),
	})
	tool := toolOf(t, api)

	var concurrent atomic.Int32
	var peak atomic.Int32
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		current := concurrent.Add(1)
		for {
			p := peak.Load()
			if current <= p || peak.CompareAndSwap(p, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		concurrent.Add(-1)
		return "done", nil
	}

	res, err := tool.Execute(context.Background(), "chain-group-conc", map[string]any{
		"chain": []any{
			map[string]any{
				"parallel": []any{
					map[string]any{"agent": "worker", "task": "A"},
					map[string]any{"agent": "worker", "task": "B"},
					map[string]any{"agent": "worker", "task": "C"},
				},
				"concurrency": 1,
			},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain parallel-group concurrency failed: err=%v res=%+v", err, res)
	}
	if peak.Load() != 1 {
		t.Fatalf("per-group concurrency=1 should serialize forks, peak=%d", peak.Load())
	}
}

// TestChainParallelGroupInheritsTopLevelConcurrency verifies that a chain
// parallel group that omits its own concurrency picks up the top-level
// concurrency from the surrounding call.
func TestChainParallelGroupInheritsTopLevelConcurrency(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "x"),
	})
	tool := toolOf(t, api)

	var concurrent atomic.Int32
	var peak atomic.Int32
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		current := concurrent.Add(1)
		for {
			p := peak.Load()
			if current <= p || peak.CompareAndSwap(p, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		concurrent.Add(-1)
		return "done", nil
	}

	res, err := tool.Execute(context.Background(), "chain-top-conc", map[string]any{
		"concurrency": 1,
		"chain": []any{
			map[string]any{"parallel": []any{
				map[string]any{"agent": "worker", "task": "A"},
				map[string]any{"agent": "worker", "task": "B"},
				map[string]any{"agent": "worker", "task": "C"},
			}},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain top-level concurrency failed: err=%v res=%+v", err, res)
	}
	if peak.Load() != 1 {
		t.Fatalf("top-level concurrency=1 should serialize chain group, peak=%d", peak.Load())
	}
}

func TestParallelModeCountExpandsItems(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "x"),
	})
	tool := toolOf(t, api)

	var total atomic.Int32
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		total.Add(1)
		return "done", nil
	}

	res, err := tool.Execute(context.Background(), "parallel-count", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "worker", "task": "X", "count": 3},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("parallel count failed: err=%v res=%+v", err, res)
	}
	if total.Load() != 3 {
		t.Fatalf("count=3 should fan out 3 fork calls, got %d", total.Load())
	}
	got := textOf(res)
	for _, want := range []string{"[0] worker", "[1] worker", "[2] worker"} {
		if !strings.Contains(got, want) {
			t.Fatalf("parallel count output missing %q:\n%s", want, got)
		}
	}
}

func TestConcurrencyInvalidValueErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "x"),
	})
	tool := toolOf(t, api)

	res, _ := tool.Execute(context.Background(), "conc-bad", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "worker", "task": "x"},
		},
		"concurrency": 0,
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "concurrency must be an integer >= 1") {
		t.Errorf("expected concurrency error, got: %s", textOf(res))
	}
}

func TestTasksEmptyArrayErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "x"),
	})
	tool := toolOf(t, api)

	res, _ := tool.Execute(context.Background(), "tasks-empty", map[string]any{
		"tasks": []any{},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "non-empty") {
		t.Errorf("expected tasks empty error, got: %s", textOf(res))
	}
}

// TestIntercomAutoAttachInjectsBatchTaskIDIntoBatchChildPrompts covers the
// H auto-attach: every child of a batch async dispatch gets an
// "# Intercom" section in its system prompt naming the batch task id and
// pointing at the subagent_intercom_send tool. Default IntercomMode is
// "always" so callers don't have to opt in.
func TestIntercomAutoAttachInjectsBatchTaskIDIntoBatchChildPrompts(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout": `---
name: scout
description: recon
---
You are scout. Stay focused.
`,
	})
	tool := toolOf(t, api)

	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		return "ok:" + opts.Name, nil
	}

	res, err := tool.Execute(context.Background(), "intercom-attach", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "scout", "task": "t"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("batch async failed: err=%v res=%+v", err, res)
	}
	taskID := extractTaskID(textOf(res))
	if taskID == "" {
		t.Fatalf("expected batch task id in reply: %s", textOf(res))
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(api.forkOptionsSnapshot()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := api.forkOptionsSnapshot()
	if len(calls) == 0 {
		t.Fatalf("expected fork to be called")
	}
	prompt := calls[0].SystemPrompt
	for _, want := range []string{
		"You are scout. Stay focused.", // original prompt preserved
		"# Intercom",
		taskID,
		"subagent_intercom_send",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("auto-attach prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestIntercomAutoAttachSkippedForSyncCalls(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout": frontmatterBody("scout", "x"),
	})
	tool := toolOf(t, api)
	var captured extension.ForkOptions
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		captured = opts
		return "ok", nil
	}
	res, err := tool.Execute(context.Background(), "intercom-sync", map[string]any{
		"agent": "scout",
		"task":  "t",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("sync call failed: err=%v res=%+v", err, res)
	}
	if strings.Contains(captured.SystemPrompt, "# Intercom") {
		t.Errorf("sync call must not auto-attach an Intercom section:\n%s", captured.SystemPrompt)
	}
}

func TestIntercomModeOffSuppressesAutoAttach(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout": frontmatterBody("scout", "x"),
	})
	// The extension instance returned from newExtensionWithProfiles uses
	// DefaultConfig() with IntercomMode="always"; we flip it off here to
	// exercise the toggle.
	for _, regTool := range api.registered {
		if t, ok := regTool.(*subagentTool); ok {
			t.ext.cfg.IntercomMode = "off"
			break
		}
	}
	tool := toolOf(t, api)
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "ok", nil
	}
	res, err := tool.Execute(context.Background(), "mode-off", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "scout", "task": "t"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("mode-off batch failed: err=%v res=%+v", err, res)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(api.forkOptionsSnapshot()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := api.forkOptionsSnapshot()
	if len(calls) == 0 {
		t.Fatalf("expected one fork call, got none")
	}
	if strings.Contains(calls[0].SystemPrompt, "# Intercom") {
		t.Errorf("IntercomMode=off must skip auto-attach:\n%s", calls[0].SystemPrompt)
	}
}

func TestIntercomModeForkOnlyRequiresForkContext(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout": frontmatterBody("scout", "x"),
	})
	for _, regTool := range api.registered {
		if t, ok := regTool.(*subagentTool); ok {
			t.ext.cfg.IntercomMode = "fork-only"
			break
		}
	}
	tool := toolOf(t, api)
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "ok", nil
	}

	// fresh context (default) → no auto-attach
	_, _ = tool.Execute(context.Background(), "fork-only-fresh", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "scout", "task": "t"},
		},
	}, nil)

	// fork context → auto-attach
	_, _ = tool.Execute(context.Background(), "fork-only-fork", map[string]any{
		"mode":    "parallel",
		"async":   true,
		"context": "fork",
		"parallel": []any{
			map[string]any{"agent": "scout", "task": "t"},
		},
	}, nil)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(api.forkOptionsSnapshot()) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := api.forkOptionsSnapshot()
	if len(calls) < 2 {
		t.Fatalf("expected 2 fork calls, got %d", len(calls))
	}
	if strings.Contains(calls[0].SystemPrompt, "# Intercom") {
		t.Errorf("fork-only mode + fresh context should skip auto-attach:\n%s", calls[0].SystemPrompt)
	}
	if !strings.Contains(calls[1].SystemPrompt, "# Intercom") {
		t.Errorf("fork-only mode + fork context should auto-attach:\n%s", calls[1].SystemPrompt)
	}
}

func TestConfigIntercomModeUnknownRejected(t *testing.T) {
	ext := New()
	err := ext.ApplyConfig(map[string]any{"intercom_mode": "weird"})
	if err == nil || !strings.Contains(err.Error(), "intercom_mode must be one of") {
		t.Errorf("expected validation error, got: %v", err)
	}
}

// TestIntercomSendAndReadRoundtrip covers the minimum-viable H intercom
// bridge: the `subagent_intercom_send` tool appends one JSONL message
// per call, and `subagent action=intercom id=...` plays them back in
// order with the recorded `from` label.
func TestIntercomSendAndReadRoundtrip(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	ext := New()
	ext.cfg.AgentsDir = t.TempDir()
	api := &fakeAPI{agentDir: agentDir, cwd: cwd}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}

	sendTool := registeredTool(t, api, "subagent_intercom_send")
	for _, msg := range []map[string]any{
		{"taskId": "subagent-batch-1", "message": "starting", "from": "scout"},
		{"taskId": "subagent-batch-1", "message": "halfway", "from": "scout"},
	} {
		res, err := sendTool.Execute(context.Background(), "send", msg, nil)
		if err != nil || res.IsError {
			t.Fatalf("send %v failed: err=%v res=%+v", msg, err, res)
		}
	}

	// File ended up under the agreed tool-results dir.
	path := filepath.Join(agentDir, "tool-results", projectKey(cwd), "subagents", "intercom", "subagent-batch-1.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("intercom file missing at %s: %v", path, err)
	}
	if !strings.Contains(string(data), `"text":"starting"`) || !strings.Contains(string(data), `"text":"halfway"`) {
		t.Fatalf("intercom file missing messages:\n%s", string(data))
	}

	// action=intercom returns both messages in order.
	subagentTool := toolOf(t, api)
	res, err := subagentTool.Execute(context.Background(), "read", map[string]any{
		"action": "intercom",
		"id":     "subagent-batch-1",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("intercom read failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	for _, want := range []string{
		"Intercom messages for task subagent-batch-1",
		"[scout @",
		"starting",
		"halfway",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("intercom read missing %q:\n%s", want, got)
		}
	}
}

func TestIntercomSendValidatesArgs(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{})
	sendTool := registeredTool(t, api, "subagent_intercom_send")

	res, _ := sendTool.Execute(context.Background(), "bad-id", map[string]any{
		"message": "x",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), `"taskId" is required`) {
		t.Errorf("expected taskId-required error, got %s", textOf(res))
	}

	res, _ = sendTool.Execute(context.Background(), "bad-msg", map[string]any{
		"taskId": "task-1",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), `"message" is required`) {
		t.Errorf("expected message-required error, got %s", textOf(res))
	}
}

func TestIntercomActionMissingInboxReturnsEmpty(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{})
	tool := toolOf(t, api)
	res, err := tool.Execute(context.Background(), "no-inbox", map[string]any{
		"action": "intercom",
		"id":     "ghost-task",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("intercom read with no file should succeed, got err=%v res=%+v", err, res)
	}
	if !strings.Contains(textOf(res), "No intercom messages for task ghost-task") {
		t.Errorf("expected empty-inbox message, got %s", textOf(res))
	}
}

// TestClarifyConfirmedProceedsWithDispatch covers the I.clarify gate's
// happy path: when the host returns true from api.Confirm, the dispatch
// goes ahead and the underlying fork is invoked.
func TestClarifyConfirmedProceedsWithDispatch(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "x"),
	})
	tool := toolOf(t, api)

	var confirmCalled int
	api.confirmFn = func(_, _ string, _ bool) bool {
		confirmCalled++
		return true
	}
	forkCalled := false
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		forkCalled = true
		return "dispatched", nil
	}
	res, err := tool.Execute(context.Background(), "clarify-yes", map[string]any{
		"agent":   "r",
		"task":    "do thing",
		"clarify": true,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("clarify-confirmed call failed: err=%v res=%+v", err, res)
	}
	if confirmCalled != 1 {
		t.Errorf("expected exactly one Confirm() call, got %d", confirmCalled)
	}
	if !forkCalled {
		t.Errorf("expected fork to run after confirmation")
	}
	if !strings.Contains(textOf(res), "dispatched") {
		t.Errorf("expected dispatched output, got:\n%s", textOf(res))
	}
}

// TestClarifyDeniedAbortsBeforeDispatch covers the denial path: when the
// host returns false from api.Confirm, no fork happens and the result
// echoes the preview so the orchestrator can see what was rejected.
func TestClarifyDeniedAbortsBeforeDispatch(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"a": frontmatterBody("a", "x"),
		"b": frontmatterBody("b", "y"),
	})
	tool := toolOf(t, api)
	api.confirmFn = func(_, _ string, _ bool) bool { return false }
	forkCalled := false
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		forkCalled = true
		return "should-not-run", nil
	}
	res, err := tool.Execute(context.Background(), "clarify-no", map[string]any{
		"clarify": true,
		"chain": []any{
			map[string]any{"agent": "a", "task": "scan files"},
			map[string]any{"agent": "b", "task": "plan from {previous}"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("clarify-denied call should succeed at the tool level: err=%v res=%+v", err, res)
	}
	if forkCalled {
		t.Errorf("denied clarify must not dispatch the fork")
	}
	got := textOf(res)
	for _, want := range []string{
		"Dispatch aborted via clarify gate",
		"chain (2 steps)",
		"scan files",
		"plan from {previous}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("clarify abort text missing %q:\n%s", want, got)
		}
	}
}

func TestClarifyOmittedSkipsConfirmation(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "x"),
	})
	tool := toolOf(t, api)
	var confirmCalled int
	api.confirmFn = func(_, _ string, _ bool) bool {
		confirmCalled++
		return false
	}
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "ok", nil
	}
	res, err := tool.Execute(context.Background(), "clarify-off", map[string]any{
		"agent": "r",
		"task":  "t",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("default-clarify call failed: err=%v res=%+v", err, res)
	}
	if confirmCalled != 0 {
		t.Errorf("Confirm should not run when clarify is omitted, got %d calls", confirmCalled)
	}
}

// TestControlNeedsAttentionTimerFires covers the second timer added on top
// of the G skeleton: needsAttentionAfterMs also fires through the
// notification channels when its threshold passes before the batch
// completes.
func TestControlNeedsAttentionTimerFires(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"slow": frontmatterBody("slow", "x"),
	})
	tool := toolOf(t, api)

	release := make(chan struct{})
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		<-release
		return "done", nil
	}

	res, err := tool.Execute(context.Background(), "ctrl-attn", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "slow", "task": "t"},
		},
		"control": map[string]any{
			"needsAttentionAfterMs": 30,
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("needs-attention batch failed: err=%v res=%+v", err, res)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	var snap []string
	for time.Now().Before(deadline) {
		snap = api.noticesSnapshot()
		if len(snap) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(snap) == 0 || !strings.Contains(snap[0], "appears stuck past 30ms") {
		t.Fatalf("expected needs-attention notice text, got %#v", snap)
	}
	close(release)
}

// TestControlNotifyOnFiltersEvents verifies that listing only one event
// in notifyOn suppresses the others. Here we set both thresholds but
// allowlist only "needs_attention".
func TestControlNotifyOnFiltersEvents(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"slow": frontmatterBody("slow", "x"),
	})
	tool := toolOf(t, api)
	release := make(chan struct{})
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		<-release
		return "done", nil
	}

	res, err := tool.Execute(context.Background(), "ctrl-filter", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "slow", "task": "t"},
		},
		"control": map[string]any{
			"activeNoticeAfterMs":   20,
			"needsAttentionAfterMs": 40,
			"notifyOn":              []any{"needs_attention"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("filter call failed: err=%v res=%+v", err, res)
	}

	// Both timers will fire under the hood; only needs_attention's text
	// should hit the notify channel.
	time.Sleep(120 * time.Millisecond)
	snap := api.noticesSnapshot()
	if len(snap) == 0 {
		t.Fatalf("expected at least one needs_attention notice, got nothing")
	}
	for _, n := range snap {
		if strings.Contains(n, "still running past") {
			t.Errorf("active_long_running should have been suppressed by notifyOn, got %q", n)
		}
	}
	close(release)
}

// TestControlIntercomChannelRoutesToInbox covers the new
// notifyChannels=["intercom"] route: the notice still lands somewhere,
// but it lands in the batch task's intercom JSONL inbox rather than
// (only) on api.Notify.
func TestControlIntercomChannelRoutesToInbox(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	dir := t.TempDir()
	writeProfile(t, dir, "slow", frontmatterBody("slow", "x"))
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{agentDir: agentDir, cwd: cwd}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)
	release := make(chan struct{})
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		<-release
		return "done", nil
	}

	res, err := tool.Execute(context.Background(), "ctrl-intercom", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "slow", "task": "t"},
		},
		"control": map[string]any{
			"activeNoticeAfterMs": 30,
			"notifyChannels":      []any{"intercom"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("intercom-channel call failed: err=%v res=%+v", err, res)
	}
	taskID := extractTaskID(textOf(res))
	if taskID == "" {
		t.Fatalf("expected batch task id in reply: %s", textOf(res))
	}

	inboxPath := filepath.Join(agentDir, "tool-results", projectKey(cwd), "subagents", "intercom", taskID+".jsonl")
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(inboxPath); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	data, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatalf("expected intercom inbox to receive the notice: %v", err)
	}
	if !strings.Contains(string(data), "still running past 30ms") {
		t.Errorf("intercom inbox missing notice text:\n%s", string(data))
	}
	if !strings.Contains(string(data), "control:active_long_running") {
		t.Errorf("intercom inbox missing control 'from' label:\n%s", string(data))
	}
	// notifyChannels was intercom-only, so api.Notify should NOT have fired.
	if snap := api.noticesSnapshot(); len(snap) != 0 {
		t.Errorf("intercom-only channel should not call api.Notify, got %#v", snap)
	}
	close(release)
}

// TestControlActiveNoticeFiresOnLongRunningBatchAsync covers the G control
// skeleton: when batch async runs past `activeNoticeAfterMs`, the
// extension emits an api.Notify with the batch task id. Once the run
// completes the timer is reclaimed so no spurious notice fires.
func TestControlActiveNoticeFiresOnLongRunningBatchAsync(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"slow": frontmatterBody("slow", "x"),
	})
	tool := toolOf(t, api)

	childRelease := make(chan struct{})
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		<-childRelease
		return "done", nil
	}

	res, err := tool.Execute(context.Background(), "ctrl-fire", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "slow", "task": "t"},
		},
		"control": map[string]any{
			"activeNoticeAfterMs": 30,
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("batch async with control failed: err=%v res=%+v", err, res)
	}

	// Wait long enough for the timer to fire while the child is blocked.
	deadline := time.Now().Add(500 * time.Millisecond)
	var snap []string
	for time.Now().Before(deadline) {
		snap = api.noticesSnapshot()
		if len(snap) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(snap) == 0 {
		t.Fatalf("expected api.Notify to fire after activeNoticeAfterMs, got no notices")
	}
	if !strings.Contains(snap[0], "still running past 30ms") {
		t.Errorf("notice text missing threshold: %q", snap[0])
	}
	if !strings.Contains(snap[0], "subagent-batch-") {
		t.Errorf("notice text should reference the batch task id: %q", snap[0])
	}

	close(childRelease)
}

// TestControlActiveNoticeSuppressedOnFastBatchAsync verifies the timer's
// stop channel works: when the batch finishes before the threshold, no
// notification fires.
func TestControlActiveNoticeSuppressedOnFastBatchAsync(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{
		"fast": frontmatterBody("fast", "x"),
	})
	tool := toolOf(t, api)
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "done", nil
	}

	res, err := tool.Execute(context.Background(), "ctrl-quiet", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "fast", "task": "t"},
		},
		"control": map[string]any{
			"activeNoticeAfterMs": 200,
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("fast batch failed: err=%v res=%+v", err, res)
	}

	// Wait for the batch task to complete in-memory.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		snaps := ext.batchTasks.snapshots()
		if len(snaps) == 1 && snaps[0].Status == "completed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Sleep past the original threshold to give a buggy timer time to fire.
	time.Sleep(250 * time.Millisecond)
	if snap := api.noticesSnapshot(); len(snap) != 0 {
		t.Fatalf("expected zero notifications when batch finishes before threshold, got %#v", snap)
	}
}

func TestControlOmittedDoesNotStartTimer(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"w": frontmatterBody("w", "x"),
	})
	tool := toolOf(t, api)
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "done", nil
	}
	res, err := tool.Execute(context.Background(), "ctrl-off", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "w", "task": "t"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("default-control batch failed: err=%v res=%+v", err, res)
	}
	time.Sleep(50 * time.Millisecond)
	if snap := api.noticesSnapshot(); len(snap) != 0 {
		t.Errorf("control omitted should yield no notices, got %#v", snap)
	}
}

func TestControlInvalidValueErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"w": frontmatterBody("w", "x"),
	})
	tool := toolOf(t, api)
	res, _ := tool.Execute(context.Background(), "ctrl-bad", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "w", "task": "t"},
		},
		"control": map[string]any{
			"activeNoticeAfterMs": 0,
		},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "activeNoticeAfterMs must be a positive integer") {
		t.Errorf("expected control validation error, got: %s", textOf(res))
	}
}

// TestSessionDirForwardsToForkOptionsForAllModes covers K.sessionDir at the
// extension boundary: top-level `sessionDir` is propagated verbatim to each
// child's ForkOptions for single, parallel, and chain modes so the host's
// task manager can place per-run files under a caller-controlled parent.
func TestSessionDirForwardsToForkOptionsForAllModes(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"a": frontmatterBody("a", "x"),
		"b": frontmatterBody("b", "y"),
	})
	tool := toolOf(t, api)

	var (
		mu       sync.Mutex
		captured []extension.ForkOptions
	)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		captured = append(captured, opts)
		mu.Unlock()
		return "ok:" + opts.Name, nil
	}

	// Single mode forwards sessionDir.
	res, err := tool.Execute(context.Background(), "sd-single", map[string]any{
		"agent":      "a",
		"task":       "t",
		"sessionDir": "shared/sessions",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("single sessionDir failed: err=%v res=%+v", err, res)
	}

	// Parallel mode forwards sessionDir to every child.
	res, err = tool.Execute(context.Background(), "sd-par", map[string]any{
		"mode":       "parallel",
		"sessionDir": "shared/sessions",
		"parallel": []any{
			map[string]any{"agent": "a", "task": "p1"},
			map[string]any{"agent": "b", "task": "p2"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("parallel sessionDir failed: err=%v res=%+v", err, res)
	}

	// Chain (sequential + parallel group) forwards sessionDir to every child.
	res, err = tool.Execute(context.Background(), "sd-chain", map[string]any{
		"sessionDir": "shared/sessions",
		"chain": []any{
			map[string]any{"agent": "a", "task": "scan"},
			map[string]any{"parallel": []any{
				map[string]any{"agent": "b", "task": "review {previous}"},
			}},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain sessionDir failed: err=%v res=%+v", err, res)
	}

	if len(captured) != 5 {
		t.Fatalf("expected 5 fork calls, got %d", len(captured))
	}
	for i, opts := range captured {
		if opts.SessionDir != "shared/sessions" {
			t.Errorf("captured[%d].SessionDir=%q, want %q", i, opts.SessionDir, "shared/sessions")
		}
	}
}

func TestSessionDirOmittedLeavesForkOptionsEmpty(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"a": frontmatterBody("a", "x"),
	})
	tool := toolOf(t, api)
	var captured extension.ForkOptions
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		captured = opts
		return "ok", nil
	}
	res, err := tool.Execute(context.Background(), "sd-missing", map[string]any{
		"agent": "a",
		"task":  "t",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("default sessionDir call failed: err=%v res=%+v", err, res)
	}
	if captured.SessionDir != "" {
		t.Errorf("expected empty SessionDir when arg omitted, got %q", captured.SessionDir)
	}
}

// TestArtifactsWriteInputOutputMetadataPerRun covers the K.artifacts parity
// item: with artifacts:true, a sync run writes input/output/metadata JSON
// under the project's tool-results subagents/artifacts/<runID>/ tree, and
// the tool result advertises the directory in a `[artifacts: ...]` tail.
func TestArtifactsWriteInputOutputMetadataPerRun(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	dir := t.TempDir()
	writeProfile(t, dir, "scout", frontmatterBody("scout", "recon"))
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{agentDir: agentDir, cwd: cwd}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "scout reply text", nil
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "art-on", map[string]any{
		"agent":     "scout",
		"task":      "scan",
		"artifacts": true,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("artifacts call failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	if !strings.Contains(got, "[artifacts: ") {
		t.Fatalf("expected '[artifacts: <path>]' tail in result, got:\n%s", got)
	}

	// Locate the artifact dir from the tool result tail.
	tail := got[strings.Index(got, "[artifacts: ")+len("[artifacts: "):]
	dirPath := strings.TrimSuffix(strings.SplitN(tail, "]", 2)[0], "]")
	for _, f := range []string{"input.json", "output.json", "metadata.json"} {
		if _, statErr := os.Stat(filepath.Join(dirPath, f)); statErr != nil {
			t.Errorf("expected %s under %s: %v", f, dirPath, statErr)
		}
	}
	inputData, _ := os.ReadFile(filepath.Join(dirPath, "input.json"))
	if !strings.Contains(string(inputData), `"mode": "single"`) || !strings.Contains(string(inputData), `"scan"`) {
		t.Errorf("input.json missing mode/task info:\n%s", string(inputData))
	}
	outputData, _ := os.ReadFile(filepath.Join(dirPath, "output.json"))
	if !strings.Contains(string(outputData), "scout reply text") {
		t.Errorf("output.json should contain final text:\n%s", string(outputData))
	}
	metaData, _ := os.ReadFile(filepath.Join(dirPath, "metadata.json"))
	for _, want := range []string{`"mode": "single"`, `"status": "completed"`, `"durationMs":`} {
		if !strings.Contains(string(metaData), want) {
			t.Errorf("metadata.json missing %q:\n%s", want, string(metaData))
		}
	}
}

func TestArtifactsOmittedWritesNoFiles(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	dir := t.TempDir()
	writeProfile(t, dir, "scout", frontmatterBody("scout", "recon"))
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{agentDir: agentDir, cwd: cwd}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "ok", nil
	}
	tool := toolOf(t, api)
	res, err := tool.Execute(context.Background(), "art-off", map[string]any{
		"agent": "scout",
		"task":  "scan",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("call failed: err=%v res=%+v", err, res)
	}
	if strings.Contains(textOf(res), "[artifacts:") {
		t.Errorf("artifacts omitted should not advertise a path, got:\n%s", textOf(res))
	}
	// Artifacts root should not exist at all.
	artRoot := filepath.Join(agentDir, "tool-results", projectKey(cwd), "subagents", "artifacts")
	if _, statErr := os.Stat(artRoot); !os.IsNotExist(statErr) {
		t.Errorf("artifacts dir should not exist when artifacts is omitted, got err=%v", statErr)
	}
}

func TestArtifactsRecordsFailureFromDispatch(t *testing.T) {
	agentDir := t.TempDir()
	cwd := t.TempDir()
	dir := t.TempDir()
	writeProfile(t, dir, "scout", frontmatterBody("scout", "recon"))
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{agentDir: agentDir, cwd: cwd}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "", errors.New("boom")
	}
	tool := toolOf(t, api)
	res, _ := tool.Execute(context.Background(), "art-fail", map[string]any{
		"agent":     "scout",
		"task":      "scan",
		"artifacts": true,
	}, nil)
	if !res.IsError {
		t.Fatalf("expected fork error to surface, got:\n%s", textOf(res))
	}
	// Walk the artifact root to find the per-run dir and confirm it
	// recorded the failure.
	artRoot := filepath.Join(agentDir, "tool-results", projectKey(cwd), "subagents", "artifacts")
	entries, err := os.ReadDir(artRoot)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected at least one artifact dir, err=%v entries=%v", err, entries)
	}
	metaPath := filepath.Join(artRoot, entries[0].Name(), "metadata.json")
	metaData, _ := os.ReadFile(metaPath)
	if !strings.Contains(string(metaData), `"status": "failed"`) {
		t.Errorf("metadata.json should record failure status:\n%s", string(metaData))
	}
	outputData, _ := os.ReadFile(filepath.Join(artRoot, entries[0].Name(), "output.json"))
	if !strings.Contains(string(outputData), "boom") {
		t.Errorf("output.json should preserve the error message:\n%s", string(outputData))
	}
}

// TestIncludeProgressAppendsProgressBodyToResult covers the K.includeProgress
// parity item: when the caller sets includeProgress:true and a progress.md
// exists for the call, the body is appended after a "## Progress" marker.
// Without includeProgress the body must not leak into the result.
func TestIncludeProgressAppendsProgressBodyToResult(t *testing.T) {
	dir := t.TempDir()
	chainDir := filepath.Join(dir, "shared")
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout": frontmatterBody("scout", "recon"),
	})
	api.cwd = dir
	tool := toolOf(t, api)

	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		// Mimic the child finishing and updating progress.md with new
		// content. The extension wrote the initial template before the
		// fork; we overwrite it here to simulate what a real child would
		// have done.
		path := filepath.Join(chainDir, "progress.md")
		if err := os.WriteFile(path, []byte("# Progress\n\n## Status\nDone\n\n## Notes\nfound foo.go\n"), 0o644); err != nil {
			t.Fatalf("simulate child progress write: %v", err)
		}
		return "scout reply", nil
	}

	// With includeProgress=true: result contains the progress body.
	res, err := tool.Execute(context.Background(), "ip-on", map[string]any{
		"agent":           "scout",
		"task":            "scan",
		"chainDir":        chainDir,
		"progress":        true,
		"includeProgress": true,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("includeProgress=true call failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	for _, want := range []string{
		"scout reply",
		"\n---\n\n## Progress\n\n",
		"## Status\nDone",
		"found foo.go",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("includeProgress=true result missing %q:\n%s", want, got)
		}
	}

	// With includeProgress omitted: result must NOT contain the progress body.
	res, err = tool.Execute(context.Background(), "ip-off", map[string]any{
		"agent":    "scout",
		"task":     "scan",
		"chainDir": chainDir,
		"progress": true,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("includeProgress=false call failed: err=%v res=%+v", err, res)
	}
	got = textOf(res)
	if strings.Contains(got, "## Progress") || strings.Contains(got, "found foo.go") {
		t.Errorf("omitted includeProgress should not append progress body, got:\n%s", got)
	}
}

// TestIncludeProgressNoFileLeavesResultUntouched covers the no-progress-file
// edge case: includeProgress is true but progress.md doesn't exist (e.g.
// progress was not requested). The result should pass through unchanged.
func TestIncludeProgressNoFileLeavesResultUntouched(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout": frontmatterBody("scout", "recon"),
	})
	tool := toolOf(t, api)
	api.cwd = t.TempDir() // fresh cwd → no progress.md anywhere yet
	api.forkFn = func(_ context.Context, _ extension.ForkOptions) (string, error) {
		return "plain reply", nil
	}

	res, err := tool.Execute(context.Background(), "ip-missing", map[string]any{
		"agent":           "scout",
		"task":            "scan",
		"includeProgress": true,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("includeProgress with no file failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	if strings.Contains(got, "## Progress") {
		t.Fatalf("no progress file should mean no append, got:\n%s", got)
	}
	if !strings.Contains(got, "plain reply") {
		t.Fatalf("expected raw reply preserved, got:\n%s", got)
	}
}

// TestBatchAsyncParallelReturnsImmediatelyAndCompletesInBackground covers
// the E parity item for parallel mode: `mode:parallel + async:true` reserves
// a synthetic batch task id, returns immediately, and finishes its children
// in a goroutine. Status output should then show the batch as completed
// with the aggregated child output.
func TestBatchAsyncParallelReturnsImmediatelyAndCompletesInBackground(t *testing.T) {
	ext, api := newExtensionWithProfiles(t, map[string]string{
		"a": frontmatterBody("a", "x"),
		"b": frontmatterBody("b", "y"),
	})
	tool := toolOf(t, api)

	release := make(chan struct{})
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		<-release
		return "done:" + opts.Name, nil
	}

	res, err := tool.Execute(context.Background(), "batch-par", map[string]any{
		"mode":  "parallel",
		"async": true,
		"parallel": []any{
			map[string]any{"agent": "a", "task": "ta"},
			map[string]any{"agent": "b", "task": "tb"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("batch parallel dispatch failed: err=%v res=%+v", err, res)
	}
	text := textOf(res)
	if !strings.Contains(text, "task_id=subagent-batch-") {
		t.Fatalf("expected synthetic batch task_id in reply, got:\n%s", text)
	}
	id := extractTaskID(text)
	if id == "" {
		t.Fatalf("could not extract task_id from %q", text)
	}

	// Status while children are still blocked: batch is running.
	statusRes, _ := tool.Execute(context.Background(), "batch-status-running", map[string]any{
		"action": "status",
		"id":     id,
	}, nil)
	if statusRes.IsError {
		t.Fatalf("status while running errored: %s", textOf(statusRes))
	}
	if !strings.Contains(textOf(statusRes), "status: running") {
		t.Fatalf("expected batch to be running before children finish, got:\n%s", textOf(statusRes))
	}

	// Release the children and poll the extension's batch registry until the
	// goroutine has recorded completion.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snaps := ext.batchTasks.snapshots()
		if len(snaps) == 1 && snaps[0].Status == "completed" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Status after completion includes the aggregated output.
	statusRes, _ = tool.Execute(context.Background(), "batch-status-done", map[string]any{
		"action": "status",
		"id":     id,
	}, nil)
	if statusRes.IsError {
		t.Fatalf("status after completion errored: %s", textOf(statusRes))
	}
	out := textOf(statusRes)
	if !strings.Contains(out, "status: completed") {
		t.Fatalf("expected batch to be completed, got:\n%s", out)
	}
	for _, want := range []string{"done:a", "done:b"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing aggregated child output %q:\n%s", want, out)
		}
	}
}

// TestBatchAsyncForceTopLevelAsyncTriggersForOmittedAsync covers the
// force_top_level_async config path: a top-level chain call that omits
// async should still go batch when the extension config opts in.
func TestBatchAsyncForceTopLevelAsyncTriggersForOmittedAsync(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "worker", frontmatterBody("worker", "x"))
	ext := New()
	ext.cfg.AgentsDir = dir
	ext.cfg.ForceTopLevelAsync = true
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		return "ok:" + opts.Name, nil
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "fta-batch", map[string]any{
		"chain": []any{
			map[string]any{"agent": "worker", "task": "scan"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("force-top-level-async chain failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(textOf(res), "task_id=subagent-batch-") {
		t.Fatalf("force_top_level_async should turn the chain into a batch async, got:\n%s", textOf(res))
	}
}

// TestBatchAsyncExplicitAsyncFalseOverridesForce ensures an explicit
// async:false beats the force_top_level_async config — the caller must
// always be able to opt out per call.
func TestBatchAsyncExplicitAsyncFalseOverridesForce(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "worker", frontmatterBody("worker", "x"))
	ext := New()
	ext.cfg.AgentsDir = dir
	ext.cfg.ForceTopLevelAsync = true
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		return "ok:" + opts.Name, nil
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "fta-explicit-off", map[string]any{
		"async": false,
		"chain": []any{
			map[string]any{"agent": "worker", "task": "scan"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("explicit async:false chain failed: err=%v res=%+v", err, res)
	}
	if strings.Contains(textOf(res), "task_id=subagent-batch-") {
		t.Fatalf("async:false should keep the chain synchronous, got:\n%s", textOf(res))
	}
	if !strings.Contains(textOf(res), "ok:worker") {
		t.Fatalf("expected synchronous chain output, got:\n%s", textOf(res))
	}
}

// TestStaleRunReconcilerMarksRunningSubagentsStale verifies that at Init,
// any subagent task the host recovered as `running` (from a previous
// session's status.json) is treated as stale. The reconciler:
//   - rewrites the on-disk status.json with status="stale"
//   - records the task id so status / doctor display it as stale in the
//     current session even though the host's in-memory snapshot still says
//     "running".
//
// Tasks of other kinds (bash etc.) and non-running statuses are untouched.
func TestStaleRunReconcilerMarksRunningSubagentsStale(t *testing.T) {
	runRoot := t.TempDir()
	staleRunDir := filepath.Join(runRoot, "task-1")
	if err := os.MkdirAll(staleRunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	staleStatusFile := filepath.Join(staleRunDir, "status.json")
	// Seed the file with the host's recovered "running" status — what
	// loadRunStatusesLocked would have written on the previous run.
	preExisting := `{"id":"task-1","kind":"subagent","status":"running","summary":"alpha: x","agent":"alpha","task":"do thing"}`
	if err := os.WriteFile(staleStatusFile, []byte(preExisting), 0o644); err != nil {
		t.Fatal(err)
	}

	api := &fakeAPI{}
	api.tasks = []extension.TaskSnapshot{
		// Stale subagent: still claims running after restart.
		{ID: "task-1", Kind: "subagent", Status: "running", Summary: "alpha: x", Agent: "alpha", Task: "do thing", StatusFile: staleStatusFile, RunDir: staleRunDir},
		// Completed task — reconciler must leave it alone.
		{ID: "task-2", Kind: "subagent", Status: "completed", Summary: "beta done", Agent: "beta"},
		// Different kind — reconciler ignores it.
		{ID: "task-3", Kind: "bash", Status: "running", Summary: "server"},
	}
	ext := New()
	ext.cfg.AgentsDir = t.TempDir()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// In-memory marker captured at Init time.
	if !ext.staleTaskIDs["task-1"] {
		t.Fatalf("expected task-1 in staleTaskIDs, got %#v", ext.staleTaskIDs)
	}
	if ext.staleTaskIDs["task-2"] || ext.staleTaskIDs["task-3"] {
		t.Fatalf("non-running / non-subagent tasks must not be reconciled: %#v", ext.staleTaskIDs)
	}

	// status output overlays "stale" on the running subagent.
	tool := toolOf(t, api)
	res, err := tool.Execute(context.Background(), "status-stale", map[string]any{"action": "status"}, nil)
	if err != nil || res.IsError {
		t.Fatalf("status failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	if !strings.Contains(got, "task-1 [stale]") {
		t.Errorf("status should show task-1 as stale:\n%s", got)
	}
	if !strings.Contains(got, "task-2 [completed]") {
		t.Errorf("status should preserve task-2 completed status:\n%s", got)
	}

	// On-disk status.json was rewritten with status=stale and a reason.
	data, err := os.ReadFile(staleStatusFile)
	if err != nil {
		t.Fatalf("read rewritten status.json: %v", err)
	}
	var disk map[string]any
	if err := json.Unmarshal(data, &disk); err != nil {
		t.Fatalf("status.json should remain valid JSON after reconcile: %v", err)
	}
	if disk["status"] != "stale" {
		t.Errorf("on-disk status should be 'stale', got %v", disk["status"])
	}
	if disk["error"] == nil || !strings.Contains(disk["error"].(string), "abandoned") {
		t.Errorf("on-disk error should mention abandoned, got %v", disk["error"])
	}
	// Preserve identity fields the original file already had.
	for k, want := range map[string]string{"id": "task-1", "agent": "alpha", "task": "do thing"} {
		if got, ok := disk[k].(string); !ok || got != want {
			t.Errorf("on-disk %q should be %q, got %v", k, want, disk[k])
		}
	}

	// doctor downgrades to warning and reports the reconciled count.
	res, err = tool.Execute(context.Background(), "doctor-stale", map[string]any{"action": "doctor"}, nil)
	if err != nil || res.IsError {
		t.Fatalf("doctor failed: err=%v res=%+v", err, res)
	}
	got = textOf(res)
	for _, want := range []string{"status: warning", "stale tasks reconciled at init: 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("doctor missing %q:\n%s", want, got)
		}
	}
}

// TestPerCallThinkingOverridesProfileForSingleParallelChain covers the L
// parity item: a per-call `thinking` overrides the profile's ThinkingLevel
// for single mode, parallel items, and sequential chain steps. Empty
// override should still fall back to the profile.
func TestPerCallThinkingOverridesProfileForSingleParallelChain(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "worker", `---
name: worker
description: profile thinking=low
thinking: low
---
System prompt.
`)
	writeProfile(t, dir, "other", `---
name: other
description: no thinking
---
System prompt.
`)
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	var (
		mu       sync.Mutex
		captured []extension.ForkOptions
	)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		captured = append(captured, opts)
		mu.Unlock()
		return "ok:" + opts.Name, nil
	}

	// 1. Single mode: explicit thinking=high overrides profile thinking=low.
	res, err := tool.Execute(context.Background(), "thinking-single", map[string]any{
		"agent":    "worker",
		"task":     "x",
		"thinking": "high",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("single override failed: err=%v res=%+v", err, res)
	}

	// 2. Single mode: omitted → inherits profile (low).
	res, err = tool.Execute(context.Background(), "thinking-default", map[string]any{
		"agent": "worker",
		"task":  "x",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("default thinking failed: err=%v res=%+v", err, res)
	}

	// 3. Parallel: per-item thinking applies per item.
	res, err = tool.Execute(context.Background(), "thinking-parallel", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "worker", "task": "a", "thinking": "minimal"},
			map[string]any{"agent": "other", "task": "b"}, // no thinking → empty
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("parallel override failed: err=%v res=%+v", err, res)
	}

	// 4. Chain: per-step thinking applies per step.
	res, err = tool.Execute(context.Background(), "thinking-chain", map[string]any{
		"chain": []any{
			map[string]any{"agent": "worker", "task": "scan", "thinking": "xhigh"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain override failed: err=%v res=%+v", err, res)
	}

	if len(captured) != 5 {
		t.Fatalf("expected 5 fork calls, got %d", len(captured))
	}
	if captured[0].ThinkingLevel != "high" {
		t.Errorf("single override thinking=high, got %q", captured[0].ThinkingLevel)
	}
	if captured[1].ThinkingLevel != "low" {
		t.Errorf("single default should inherit profile low, got %q", captured[1].ThinkingLevel)
	}
	parallelByName := map[string]string{
		captured[2].Name: captured[2].ThinkingLevel,
		captured[3].Name: captured[3].ThinkingLevel,
	}
	if parallelByName["worker"] != "minimal" {
		t.Errorf("parallel worker thinking=minimal, got %q", parallelByName["worker"])
	}
	if parallelByName["other"] != "" {
		t.Errorf("parallel other should inherit empty profile thinking, got %q", parallelByName["other"])
	}
	if captured[4].ThinkingLevel != "xhigh" {
		t.Errorf("chain override thinking=xhigh, got %q", captured[4].ThinkingLevel)
	}
}

// newExtensionWithUserAndProjectProfiles wires the extension against the
// host's standard discovery layout so test profiles get Source="user" or
// "project" the same way Discover normally assigns them.
func newExtensionWithUserAndProjectProfiles(t *testing.T, userProfiles, projectProfiles map[string]string) (*Extension, *fakeAPI) {
	t.Helper()
	agentDir := t.TempDir()
	cwd := t.TempDir()
	userDir := filepath.Join(agentDir, "agents")
	projectDir := filepath.Join(cwd, ".coding_agent", "agents")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range userProfiles {
		if err := os.WriteFile(filepath.Join(userDir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range projectProfiles {
		if err := os.WriteFile(filepath.Join(projectDir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ext := New()
	api := &fakeAPI{agentDir: agentDir, cwd: cwd}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return ext, api
}

// TestListActionAgentScopeFiltersBySource exercises agentScope: user|project
// |both on action=list, verifying source filtering against profiles loaded
// from the host's standard user/project discovery paths.
func TestListActionAgentScopeFiltersBySource(t *testing.T) {
	_, api := newExtensionWithUserAndProjectProfiles(t,
		map[string]string{
			"user-only": frontmatterBody("user-only", "user profile"),
			"shared":    frontmatterBody("shared", "user version"),
		},
		map[string]string{
			"project-only": frontmatterBody("project-only", "project profile"),
		},
	)
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "scope-user", map[string]any{
		"action":     "list",
		"agentScope": "user",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("list user-scope failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	if !strings.Contains(got, "[scope: user]") {
		t.Errorf("user-scope output should mention scope header:\n%s", got)
	}
	if !strings.Contains(got, "user-only") || !strings.Contains(got, "shared") {
		t.Errorf("user-scope list missing expected entries:\n%s", got)
	}
	if strings.Contains(got, "project-only") {
		t.Errorf("user-scope list should not include project profile:\n%s", got)
	}

	res, err = tool.Execute(context.Background(), "scope-project", map[string]any{
		"action":     "list",
		"agentScope": "project",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("list project-scope failed: err=%v res=%+v", err, res)
	}
	got = textOf(res)
	if !strings.Contains(got, "project-only") {
		t.Errorf("project-scope list missing project-only:\n%s", got)
	}
	if strings.Contains(got, "user-only") {
		t.Errorf("project-scope list should not include user profiles:\n%s", got)
	}

	res, err = tool.Execute(context.Background(), "scope-both", map[string]any{
		"action":     "list",
		"agentScope": "both",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("list both-scope failed: err=%v res=%+v", err, res)
	}
	got = textOf(res)
	for _, want := range []string{"user-only", "project-only"} {
		if !strings.Contains(got, want) {
			t.Errorf("both-scope list missing %q:\n%s", want, got)
		}
	}
}

func TestListActionAgentScopeOmittedIsBoth(t *testing.T) {
	_, api := newExtensionWithUserAndProjectProfiles(t,
		map[string]string{"u": frontmatterBody("u", "user")},
		map[string]string{"p": frontmatterBody("p", "project")},
	)
	tool := toolOf(t, api)
	res, err := tool.Execute(context.Background(), "scope-default", map[string]any{"action": "list"}, nil)
	if err != nil || res.IsError {
		t.Fatalf("list default failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	for _, want := range []string{"u: user", "p: project"} {
		if !strings.Contains(got, want) {
			t.Errorf("default list missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[scope:") {
		t.Errorf("default list should not include scope header:\n%s", got)
	}
}

func TestListActionAgentScopeUnknownErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "x"),
	})
	tool := toolOf(t, api)
	res, _ := tool.Execute(context.Background(), "scope-bad", map[string]any{
		"action":     "list",
		"agentScope": "team",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "agentScope must be") {
		t.Errorf("expected agentScope validation error, got: %s", textOf(res))
	}
}

func TestGetActionAgentScopeRespectsSource(t *testing.T) {
	_, api := newExtensionWithUserAndProjectProfiles(t,
		map[string]string{},
		map[string]string{"only-project": frontmatterBody("only-project", "p")},
	)
	tool := toolOf(t, api)

	// In scope, find it.
	res, err := tool.Execute(context.Background(), "get-scope-ok", map[string]any{
		"action":     "get",
		"agent":      "only-project",
		"agentScope": "project",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("get project-scope failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(textOf(res), "Agent only-project") {
		t.Errorf("expected agent detail, got:\n%s", textOf(res))
	}

	// Out of scope, refuse with a scope-aware error.
	res, _ = tool.Execute(context.Background(), "get-scope-miss", map[string]any{
		"action":     "get",
		"agent":      "only-project",
		"agentScope": "user",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), `not found in scope "user"`) {
		t.Errorf("expected scope-mismatch error, got: %s", textOf(res))
	}
}

// TestTopLevelParallelWorktreeForcesIsolation verifies that
// `mode:parallel + worktree:true` at the top level promotes every child's
// ForkOptions.Isolation to "worktree", regardless of the profile's own
// isolation setting.
func TestTopLevelParallelWorktreeForcesIsolation(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"none": frontmatterBody("none", "no isolation"),
		"set": `---
name: set
description: profile already sets isolation
isolation: process
---
System prompt.
`,
	})
	tool := toolOf(t, api)

	var (
		mu        sync.Mutex
		isolation = map[string]string{}
	)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		isolation[opts.Name] = opts.Isolation
		mu.Unlock()
		return "ok", nil
	}
	res, err := tool.Execute(context.Background(), "wt-top", map[string]any{
		"mode":     "parallel",
		"worktree": true,
		"parallel": []any{
			map[string]any{"agent": "none", "task": "a"},
			map[string]any{"agent": "set", "task": "b"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("top-level worktree call failed: err=%v res=%+v", err, res)
	}
	if isolation["none"] != "worktree" || isolation["set"] != "worktree" {
		t.Fatalf("expected worktree to force isolation for every child, got %#v", isolation)
	}
}

// TestChainParallelGroupWorktreeForcesIsolation covers the same override on
// a chain[].parallel group — only that group's items get worktree; the
// surrounding sequential steps still inherit the profile's isolation.
func TestChainParallelGroupWorktreeForcesIsolation(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"none":   frontmatterBody("none", "no isolation"),
		"second": frontmatterBody("second", "second"),
	})
	tool := toolOf(t, api)

	var (
		mu        sync.Mutex
		recorded  []string
		isoByName = map[string]string{}
	)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		recorded = append(recorded, opts.Name)
		isoByName[opts.Name] = opts.Isolation
		mu.Unlock()
		return "ok:" + opts.Name, nil
	}
	res, err := tool.Execute(context.Background(), "wt-chain", map[string]any{
		"chain": []any{
			// Sequential step — no worktree override.
			map[string]any{"agent": "none", "task": "scan"},
			// Parallel group with worktree:true — every item forced.
			map[string]any{"parallel": []any{
				map[string]any{"agent": "none", "task": "a"},
				map[string]any{"agent": "second", "task": "b"},
			}, "worktree": true},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain worktree call failed: err=%v res=%+v", err, res)
	}
	if len(recorded) != 3 {
		t.Fatalf("expected 3 fork calls, got %d (%v)", len(recorded), recorded)
	}
	// Sequential step's isolation stays empty (no profile isolation, no
	// override). Both parallel-group items get worktree forced.
	if isoByName["none"] != "" {
		// "none" appeared in both the sequential step AND the parallel group;
		// when keying by name the later parallel write wins. So this assertion
		// captures the worktree forcing for the group.
		if isoByName["none"] != "worktree" {
			t.Fatalf("expected worktree for parallel-group 'none' item, got %q", isoByName["none"])
		}
	}
	if isoByName["second"] != "worktree" {
		t.Fatalf("expected worktree for parallel-group 'second' item, got %q", isoByName["second"])
	}
}

// TestTopLevelParallelWithoutWorktreePreservesProfileIsolation makes sure the
// override only fires when `worktree:true` is requested explicitly — without
// it, the profile's isolation is used verbatim.
func TestTopLevelParallelWithoutWorktreePreservesProfileIsolation(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"isolated": `---
name: isolated
description: process-isolated
isolation: process
---
System prompt.
`,
		"plain": frontmatterBody("plain", "no isolation"),
	})
	tool := toolOf(t, api)

	var (
		mu        sync.Mutex
		isolation = map[string]string{}
	)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		isolation[opts.Name] = opts.Isolation
		mu.Unlock()
		return "ok", nil
	}
	res, err := tool.Execute(context.Background(), "wt-off", map[string]any{
		"mode": "parallel",
		"parallel": []any{
			map[string]any{"agent": "isolated", "task": "a"},
			map[string]any{"agent": "plain", "task": "b"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("default isolation call failed: err=%v res=%+v", err, res)
	}
	if isolation["isolated"] != "process" {
		t.Errorf("expected profile isolation=process, got %q", isolation["isolated"])
	}
	if isolation["plain"] != "" {
		t.Errorf("expected empty isolation for plain profile, got %q", isolation["plain"])
	}
}

// TestForceTopLevelAsyncDefaultsSingleToBackground covers the
// force_top_level_async config: when set, a single-mode call that omits the
// `async` argument runs as a background fork, even when the profile leaves
// background=false. An explicit async:false must still win so callers can
// opt out per call.
func TestForceTopLevelAsyncDefaultsSingleToBackground(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "worker", frontmatterBody("worker", "x"))
	ext := New()
	ext.cfg.AgentsDir = dir
	ext.cfg.ForceTopLevelAsync = true
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	var captured []extension.ForkOptions
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		captured = append(captured, opts)
		return "ok", nil
	}

	res, err := tool.Execute(context.Background(), "fta-default", map[string]any{
		"agent": "worker",
		"task":  "x",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("force_top_level_async default call failed: err=%v res=%+v", err, res)
	}

	res, err = tool.Execute(context.Background(), "fta-explicit-off", map[string]any{
		"agent": "worker",
		"task":  "x",
		"async": false,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("explicit async:false call failed: err=%v res=%+v", err, res)
	}

	if len(captured) != 2 {
		t.Fatalf("expected 2 fork calls, got %d", len(captured))
	}
	if !captured[0].Background {
		t.Errorf("omitted async with force_top_level_async should default to background, got %+v", captured[0])
	}
	if captured[1].Background {
		t.Errorf("explicit async:false should override force_top_level_async, got %+v", captured[1])
	}
}

func TestForceTopLevelAsyncOffPreservesProfileBackground(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "worker", frontmatterBody("worker", "x"))
	ext := New()
	ext.cfg.AgentsDir = dir
	// Default: ForceTopLevelAsync is false.
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	var captured []extension.ForkOptions
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		captured = append(captured, opts)
		return "ok", nil
	}
	res, err := tool.Execute(context.Background(), "fta-off", map[string]any{
		"agent": "worker",
		"task":  "x",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("default off call failed: err=%v res=%+v", err, res)
	}
	if len(captured) != 1 || captured[0].Background {
		t.Errorf("without force_top_level_async, omitted async should not background, got %+v", captured)
	}
}

// TestChainParallelGroupFailFastCancelsSiblingsAndAbortsChain verifies that
// `chain[i].parallel + failFast: true` (a) cancels in-flight sibling forks
// via ctx, (b) surfaces the original error to the caller, and (c) aborts
// the surrounding chain before any later step runs.
func TestChainParallelGroupFailFastCancelsSiblingsAndAbortsChain(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"fast-fail":     frontmatterBody("fast-fail", "errors immediately"),
		"slow-finish":   frontmatterBody("slow-finish", "waits for ctx"),
		"never-reached": frontmatterBody("never-reached", "should not run"),
	})
	tool := toolOf(t, api)

	var siblingCancelled atomic.Bool
	api.forkFn = func(ctx context.Context, opts extension.ForkOptions) (string, error) {
		switch opts.Name {
		case "fast-fail":
			return "", errors.New("boom")
		case "slow-finish":
			select {
			case <-time.After(2 * time.Second):
				return "should not arrive", nil
			case <-ctx.Done():
				siblingCancelled.Store(true)
				return "", ctx.Err()
			}
		case "never-reached":
			t.Errorf("post-failFast chain step should not run")
			return "should-not-run", nil
		}
		return "", nil
	}

	res, _ := tool.Execute(context.Background(), "failfast", map[string]any{
		"chain": []any{
			map[string]any{"parallel": []any{
				map[string]any{"agent": "fast-fail", "task": "fail"},
				map[string]any{"agent": "slow-finish", "task": "wait"},
			}, "failFast": true},
			map[string]any{"agent": "never-reached", "task": "x"},
		},
	}, nil)
	if !res.IsError {
		t.Fatalf("expected failFast to abort the chain, got non-error result:\n%s", textOf(res))
	}
	got := textOf(res)
	if !strings.Contains(got, "fail-fast") || !strings.Contains(got, "boom") {
		t.Fatalf("expected fail-fast error containing 'boom', got:\n%s", got)
	}
	if !siblingCancelled.Load() {
		t.Fatal("expected slow sibling to observe ctx cancellation")
	}
}

// TestChainParallelGroupWithoutFailFastReportsErrorsAndContinues confirms the
// existing partial-success behavior is preserved when failFast is omitted:
// the group's text aggregates per-child errors and the chain continues to
// the next step.
func TestChainParallelGroupWithoutFailFastReportsErrorsAndContinues(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"bad":      frontmatterBody("bad", "fails"),
		"good":     frontmatterBody("good", "ok"),
		"follower": frontmatterBody("follower", "next"),
	})
	tool := toolOf(t, api)

	followerRan := false
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		switch opts.Name {
		case "bad":
			return "", errors.New("boom")
		case "good":
			return "ok", nil
		case "follower":
			followerRan = true
			return "done", nil
		}
		return "", nil
	}

	res, err := tool.Execute(context.Background(), "nofailfast", map[string]any{
		"chain": []any{
			map[string]any{"parallel": []any{
				map[string]any{"agent": "bad", "task": "x"},
				map[string]any{"agent": "good", "task": "y"},
			}},
			map[string]any{"agent": "follower", "task": "after"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain without failFast should not abort: err=%v res=%+v", err, res)
	}
	if !followerRan {
		t.Fatal("follower step should run when failFast is not set")
	}
}

// TestChainSubstitutesTaskAndChainDirVariables covers the pi-style template
// vars {task} (chain's first sequential step's raw task) and {chain_dir}
// (resolved shared chain directory). It exercises both a sequential later
// step and a parallel group: both should see the original chain task and the
// chain dir, while {previous} continues to carry the prior step's reply.
func TestChainSubstitutesTaskAndChainDirVariables(t *testing.T) {
	dir := t.TempDir()
	chainDir := filepath.Join(dir, "shared")
	_, api := newExtensionWithProfiles(t, map[string]string{
		"scout":   frontmatterBody("scout", "recon"),
		"planner": frontmatterBody("planner", "plan"),
		"runner":  frontmatterBody("runner", "run"),
	})
	api.cwd = dir
	tool := toolOf(t, api)

	var (
		mu    sync.Mutex
		tasks []string
		names []string
	)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		tasks = append(tasks, opts.Task)
		names = append(names, opts.Name)
		mu.Unlock()
		return "ok:" + opts.Name, nil
	}

	res, err := tool.Execute(context.Background(), "vars-chain", map[string]any{
		"chainDir": chainDir,
		"chain": []any{
			map[string]any{"agent": "scout", "task": "scan PR 42"},
			map[string]any{"agent": "planner", "task": "anchor={task}; latest={previous}; dir={chain_dir}"},
			map[string]any{"parallel": []any{
				map[string]any{"agent": "runner", "task": "anchor={task}; dir={chain_dir}"},
			}},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("chain vars failed: err=%v res=%+v", err, res)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 fork calls, got %d", len(tasks))
	}
	// Step 0: scout's raw task is the anchor; it doesn't substitute itself.
	if tasks[0] != "scan PR 42" {
		t.Errorf("step 0 task should be raw, got %q", tasks[0])
	}
	// Step 1: planner sees {task} = scout's raw task, {previous} = scout's
	// output, {chain_dir} = resolved chainDir.
	want1 := "anchor=scan PR 42; latest=ok:scout; dir=" + chainDir
	if tasks[1] != want1 {
		t.Errorf("step 1 task substitution wrong:\n got: %q\nwant: %q", tasks[1], want1)
	}
	// Step 2: parallel group item also sees the same anchor / chain dir.
	want2 := "anchor=scan PR 42; dir=" + chainDir
	if tasks[2] != want2 {
		t.Errorf("parallel-group task substitution wrong:\n got: %q\nwant: %q", tasks[2], want2)
	}
}

// TestParallelSubstitutesChainDirFromTopLevelArg covers top-level
// parallel/tasks substitution of {chain_dir} when the caller sets chainDir
// at the top level. {task} stays empty (no chain context).
func TestParallelSubstitutesChainDirFromTopLevelArg(t *testing.T) {
	dir := t.TempDir()
	chainDir := filepath.Join(dir, "p-shared")
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "x"),
	})
	api.cwd = dir
	tool := toolOf(t, api)

	var (
		mu       sync.Mutex
		captured []string
	)
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		mu.Lock()
		captured = append(captured, opts.Task)
		mu.Unlock()
		return "done", nil
	}
	res, err := tool.Execute(context.Background(), "vars-par", map[string]any{
		"mode":     "parallel",
		"chainDir": chainDir,
		"parallel": []any{
			map[string]any{"agent": "worker", "task": "anchor=[{task}]; dir={chain_dir}"},
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("parallel vars failed: err=%v res=%+v", err, res)
	}
	want := "anchor=[]; dir=" + chainDir
	if len(captured) != 1 || captured[0] != want {
		t.Fatalf("parallel substitution wrong:\n got: %q\nwant: %q", captured, want)
	}
}

// TestSingleSubstitutesChainDirVariable covers {chain_dir} in single mode
// when the caller passes chainDir explicitly. {task} stays empty in single
// mode — there is no surrounding chain anchor.
func TestSingleSubstitutesChainDirVariable(t *testing.T) {
	dir := t.TempDir()
	chainDir := filepath.Join(dir, "single-shared")
	_, api := newExtensionWithProfiles(t, map[string]string{
		"worker": frontmatterBody("worker", "x"),
	})
	api.cwd = dir
	tool := toolOf(t, api)

	var captured string
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		captured = opts.Task
		return "ok", nil
	}
	res, err := tool.Execute(context.Background(), "vars-single", map[string]any{
		"agent":    "worker",
		"task":     "dir={chain_dir}",
		"chainDir": chainDir,
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("single vars failed: err=%v res=%+v", err, res)
	}
	if captured != "dir="+chainDir {
		t.Fatalf("single substitution wrong:\n got: %q\nwant: %q", captured, "dir="+chainDir)
	}
}

// TestGetActionReturnsFullProfile verifies action=get returns frontmatter
// fields plus the system prompt body for one named profile.
func TestGetActionReturnsFullProfile(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": `---
name: reviewer
description: review diffs
model: claude-sonnet-4-6
tools: read,grep
skills: code-review
---
You are the reviewer. Always quote line numbers.
`,
	})
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "get-1", map[string]any{
		"action": "get",
		"agent":  "reviewer",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("get failed: err=%v res=%+v", err, res)
	}
	got := textOf(res)
	for _, want := range []string{
		"Agent reviewer",
		"description: review diffs",
		"model: claude-sonnet-4-6",
		"tools: read,grep",
		"skills: code-review",
		"You are the reviewer. Always quote line numbers.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("get output missing %q:\n%s", want, got)
		}
	}
}

func TestGetActionMissingAgentErrors(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"r": frontmatterBody("r", "x"),
	})
	tool := toolOf(t, api)
	res, _ := tool.Execute(context.Background(), "get-bad", map[string]any{
		"action": "get",
		"agent":  "ghost",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), `agent "ghost" not found`) {
		t.Errorf("expected not-found error, got: %s", textOf(res))
	}
}

// TestCreateActionWritesProfileAndReloads exercises create through the tool
// API: it writes the .md file under the configured agents_dir, re-runs
// discovery, and the new profile becomes dispatchable in the same session.
func TestCreateActionWritesProfileAndReloads(t *testing.T) {
	dir := t.TempDir()
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "create-1", map[string]any{
		"action": "create",
		"config": map[string]any{
			"name":         "scout",
			"description":  "scan code",
			"tools":        []any{"read", "grep"},
			"model":        "claude-haiku-4-5",
			"systemPrompt": "Be brief. Quote file paths.",
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("create failed: err=%v res=%+v", err, res)
	}
	path := filepath.Join(dir, "scout.md")
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("expected new profile file, got read error: %v", readErr)
	}
	content := string(data)
	for _, want := range []string{
		"name: scout",
		"description: scan code",
		"tools: read,grep",
		"model: claude-haiku-4-5",
		"Be brief. Quote file paths.",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("profile file missing %q:\n%s", want, content)
		}
	}

	// Loader was reloaded — dispatch via single mode should now work.
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Name != "scout" {
			t.Errorf("forked the wrong profile: %q", opts.Name)
		}
		return "ok", nil
	}
	res, err = tool.Execute(context.Background(), "create-2", map[string]any{
		"agent": "scout",
		"task":  "scan",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("dispatch after create failed: err=%v res=%+v", err, res)
	}
}

func TestCreateActionRejectsExistingName(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "reviewer", frontmatterBody("reviewer", "review"))
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	res, _ := tool.Execute(context.Background(), "create-dup", map[string]any{
		"action": "create",
		"config": map[string]any{
			"name":        "reviewer",
			"description": "duplicate",
		},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "already exists") {
		t.Errorf("expected duplicate-name error, got: %s", textOf(res))
	}
}

func TestCreateActionRejectsMissingConfigName(t *testing.T) {
	dir := t.TempDir()
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)
	res, _ := tool.Execute(context.Background(), "create-noname", map[string]any{
		"action": "create",
		"config": map[string]any{"description": "nameless"},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "config.name is required") {
		t.Errorf("expected missing-name error, got: %s", textOf(res))
	}
}

// TestUpdateActionMergesFrontmatterAndBody verifies update keeps existing
// frontmatter values, overwrites supplied ones, replaces the body when
// systemPrompt is set, and reloads the loader.
func TestUpdateActionMergesFrontmatterAndBody(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "reviewer", `---
name: reviewer
description: review diffs
model: claude-haiku-4-5
tools: read
---
You are the reviewer.
`)
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "update-1", map[string]any{
		"action": "update",
		"agent":  "reviewer",
		"config": map[string]any{
			"model":        "claude-sonnet-4-6",
			"tools":        []any{"read", "grep"},
			"systemPrompt": "You are the reviewer. Quote line numbers.",
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("update failed: err=%v res=%+v", err, res)
	}
	data, readErr := os.ReadFile(filepath.Join(dir, "reviewer.md"))
	if readErr != nil {
		t.Fatalf("read updated profile: %v", readErr)
	}
	content := string(data)
	for _, want := range []string{
		"name: reviewer",
		"description: review diffs",
		"model: claude-sonnet-4-6",
		"tools: read,grep",
		"You are the reviewer. Quote line numbers.",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("updated profile missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "claude-haiku-4-5") {
		t.Errorf("update should overwrite the model line:\n%s", content)
	}
}

func TestUpdateActionMissingAgentErrors(t *testing.T) {
	dir := t.TempDir()
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)
	res, _ := tool.Execute(context.Background(), "update-bad", map[string]any{
		"action": "update",
		"agent":  "ghost",
		"config": map[string]any{"description": "x"},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), `agent "ghost" not found`) {
		t.Errorf("expected not-found, got: %s", textOf(res))
	}
}

func TestUpdateActionRejectsRename(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "reviewer", frontmatterBody("reviewer", "x"))
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)
	res, _ := tool.Execute(context.Background(), "update-rename", map[string]any{
		"action": "update",
		"agent":  "reviewer",
		"config": map[string]any{"name": "reviewer-new"},
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), "renaming via update is not supported") {
		t.Errorf("expected rename rejection, got: %s", textOf(res))
	}
}

func TestDeleteActionRemovesProfileAndReloads(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "reviewer", frontmatterBody("reviewer", "x"))
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "delete-1", map[string]any{
		"action": "delete",
		"agent":  "reviewer",
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("delete failed: err=%v res=%+v", err, res)
	}
	if _, err := os.Stat(filepath.Join(dir, "reviewer.md")); !os.IsNotExist(err) {
		t.Fatalf("expected profile file removed, stat err=%v", err)
	}
	// After delete the loader is empty, so dispatching the deleted name fails.
	res, _ = tool.Execute(context.Background(), "delete-2", map[string]any{
		"agent": "reviewer",
		"task":  "x",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), `agent "reviewer" not found`) {
		t.Errorf("expected not-found after delete, got: %s", textOf(res))
	}
}

func TestDeleteActionMissingAgentErrors(t *testing.T) {
	dir := t.TempDir()
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)
	res, _ := tool.Execute(context.Background(), "delete-bad", map[string]any{
		"action": "delete",
		"agent":  "ghost",
	}, nil)
	if !res.IsError || !strings.Contains(textOf(res), `agent "ghost" not found`) {
		t.Errorf("expected not-found, got: %s", textOf(res))
	}
}

// TestCreateActionSanitizesName covers the kebab-case sanitizer so
// "Heavy Reviewer!" lands at heavy-reviewer.md without quoting issues in the
// frontmatter or filename.
func TestCreateActionSanitizesName(t *testing.T) {
	dir := t.TempDir()
	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	res, err := tool.Execute(context.Background(), "create-sanitize", map[string]any{
		"action": "create",
		"config": map[string]any{
			"name":        "Heavy Reviewer!",
			"description": "heavy reviewer",
		},
	}, nil)
	if err != nil || res.IsError {
		t.Fatalf("create failed: err=%v res=%+v", err, res)
	}
	if _, err := os.Stat(filepath.Join(dir, "heavy-reviewer.md")); err != nil {
		t.Fatalf("expected sanitized filename heavy-reviewer.md, stat err=%v", err)
	}
}

func TestApplyConfigKnownKeys(t *testing.T) {
	ext := New()
	err := ext.ApplyConfig(map[string]any{
		"agents_dir":      "/tmp/agents",
		"default_model":   "claude-sonnet-4-6",
		"max_depth":       3,
		"timeout_seconds": 120,
	})
	if err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if ext.cfg.AgentsDir != "/tmp/agents" {
		t.Errorf("AgentsDir=%q", ext.cfg.AgentsDir)
	}
	if ext.cfg.DefaultModel != "claude-sonnet-4-6" {
		t.Errorf("DefaultModel=%q", ext.cfg.DefaultModel)
	}
	if ext.cfg.MaxDepth != 3 {
		t.Errorf("MaxDepth=%d", ext.cfg.MaxDepth)
	}
	if ext.cfg.TimeoutSeconds != 120 {
		t.Errorf("TimeoutSeconds=%d", ext.cfg.TimeoutSeconds)
	}
}

func TestApplyConfigUnknownKeyErrors(t *testing.T) {
	ext := New()
	err := ext.ApplyConfig(map[string]any{"unknown": "x"})
	if err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("expected unknown-key error, got: %v", err)
	}
}

func TestApplyConfigTypeMismatchErrors(t *testing.T) {
	ext := New()
	err := ext.ApplyConfig(map[string]any{"max_depth": "three"})
	if err == nil || !strings.Contains(err.Error(), "max_depth must be int") {
		t.Errorf("expected type-mismatch error, got: %v", err)
	}
}

// TestProfilePassesBackgroundIsolationSkillsMemoryThrough verifies that
// profile-declared Background / Isolation / Skills / MemoryScope land in
// ForkOptions verbatim — the host (not the model) decides what to do with
// them, but the extension must transmit them faithfully.
func TestProfilePassesBackgroundIsolationSkillsMemoryThrough(t *testing.T) {
	dir := t.TempDir()
	// utils.ParseFrontmatter supports simple `key: value` lines; CSVs for
	// list fields (tools/skills) are parsed by the loader's applyFrontmatter.
	body := `---
name: heavy
description: heavy-duty profile
background: true
isolation: worktree
skills: codebase-tools,bash-tools
memory: project
---
You are a heavy worker.
`
	writeProfile(t, dir, "heavy", body)

	ext := New()
	ext.cfg.AgentsDir = dir
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	var captured extension.ForkOptions
	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		captured = opts
		return "ok", nil
	}

	res, _ := tool.Execute(context.Background(), "id", map[string]any{
		"agent": "heavy",
		"task":  "go",
	}, nil)
	if res.IsError {
		t.Fatalf("unexpected error: %s", textOf(res))
	}
	if !captured.Background {
		t.Errorf("Background flag did not propagate: %+v", captured)
	}
	if captured.Isolation != "worktree" {
		t.Errorf("Isolation=%q, want %q", captured.Isolation, "worktree")
	}
	if len(captured.Skills) != 2 || captured.Skills[0] != "codebase-tools" || captured.Skills[1] != "bash-tools" {
		t.Errorf("Skills did not propagate: %v", captured.Skills)
	}
	if captured.MemoryScope != "project" {
		t.Errorf("MemoryScope=%q, want %q", captured.MemoryScope, "project")
	}
}

func TestDefaultModelAppliedWhenProfileLeavesItEmpty(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "r", frontmatterBody("r", "x"))

	ext := New()
	ext.cfg.AgentsDir = dir
	ext.cfg.DefaultModel = "claude-haiku-4-5"
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	tool := toolOf(t, api)

	api.forkFn = func(_ context.Context, opts extension.ForkOptions) (string, error) {
		if opts.Model != "claude-haiku-4-5" {
			t.Errorf("expected DefaultModel to apply, got %q", opts.Model)
		}
		return "ok", nil
	}
	_, _ = tool.Execute(context.Background(), "id", map[string]any{
		"agent": "r",
		"task":  "x",
	}, nil)
}

// textOf concatenates every TextContent block in a tool result. Used to
// keep assertions concise.
func textOf(res agent.ToolResult) string {
	var b strings.Builder
	for _, block := range res.Content {
		if tc, ok := block.(*types.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
