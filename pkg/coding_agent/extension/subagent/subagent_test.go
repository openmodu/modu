package subagent

import (
	"context"
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
	registered []agent.AgentTool

	mu        sync.Mutex
	forkCalls []extension.ForkOptions
	forkFn    func(ctx context.Context, opts extension.ForkOptions) (string, error)
}

func (f *fakeAPI) RegisterTool(t agent.AgentTool) { f.registered = append(f.registered, t) }
func (f *fakeAPI) RegisterCommand(string, string, extension.CommandHandler) {}
func (f *fakeAPI) AddHook(extension.ToolHook)                               {}
func (f *fakeAPI) On(string, extension.EventHandler)                        {}
func (f *fakeAPI) SendMessage(string) error                                 { return nil }
func (f *fakeAPI) SendMessageWithOptions(string, extension.MessageOptions) error {
	return nil
}
func (f *fakeAPI) SendFollowUpMessage(string) error  { return nil }
func (f *fakeAPI) SetActiveTools([]string)           {}
func (f *fakeAPI) SetModel(string, string) error     { return nil }
func (f *fakeAPI) GetCommands() []extension.Command  { return nil }
func (f *fakeAPI) SessionID() string                 { return "test" }
func (f *fakeAPI) SessionDir() string                { return "" }
func (f *fakeAPI) AgentDir() string                  { return "" }
func (f *fakeAPI) Cwd() string                       { return "" }
func (f *fakeAPI) IsIdle() bool                      { return true }
func (f *fakeAPI) HasPendingMessages() bool          { return false }
func (f *fakeAPI) Notify(string, string)             {}
func (f *fakeAPI) Confirm(string, string, bool) bool { return false }
func (f *fakeAPI) Select(string, []string) string    { return "" }

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

func toolOf(t *testing.T, api *fakeAPI) agent.AgentTool {
	t.Helper()
	if len(api.registered) != 1 {
		t.Fatalf("want one registered tool, got %d", len(api.registered))
	}
	return api.registered[0]
}

func TestInitNoProfilesRegistersNothing(t *testing.T) {
	ext := New()
	ext.cfg.AgentsDir = filepath.Join(t.TempDir(), "missing")
	api := &fakeAPI{}
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if len(api.registered) != 0 {
		t.Errorf("expected no tool registration when agents dir is empty, got %d", len(api.registered))
	}
}

func TestInitDiscoversProfilesAndRegistersTool(t *testing.T) {
	_, api := newExtensionWithProfiles(t, map[string]string{
		"reviewer": frontmatterBody("reviewer", "review diffs"),
	})
	if len(api.registered) != 1 {
		t.Fatalf("want one tool registered, got %d", len(api.registered))
	}
	if got := api.registered[0].Name(); got != "subagent" {
		t.Errorf("tool name = %q, want %q", got, "subagent")
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
func textOf(res agent.AgentToolResult) string {
	var b strings.Builder
	for _, block := range res.Content {
		if tc, ok := block.(*types.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
