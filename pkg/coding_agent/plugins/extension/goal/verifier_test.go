package goal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

func TestApplyConfigVerifier(t *testing.T) {
	e := New(Options{})
	if err := e.ApplyConfig(map[string]any{"verifier": map[string]any{
		"enabled":     true,
		"model":       " openai/gpt-4o ",
		"max_rejects": 2,
		"max_turns":   5,
	}}); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	cfg := e.verifierSnapshot()
	if !cfg.Enabled || cfg.Model != "openai/gpt-4o" || cfg.MaxRejects != 2 || cfg.MaxTurns != 5 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestApplyConfigVerifierDefaultsAndErrors(t *testing.T) {
	e := New(Options{})
	// No verifier key is a no-op.
	if err := e.ApplyConfig(map[string]any{}); err != nil {
		t.Fatalf("empty config: %v", err)
	}
	if e.verifierEnabled() {
		t.Fatal("verifier should default to disabled")
	}
	// Defaults fill in when only enabled is set.
	if err := e.ApplyConfig(map[string]any{"verifier": map[string]any{"enabled": true}}); err != nil {
		t.Fatalf("minimal config: %v", err)
	}
	cfg := e.verifierSnapshot()
	if cfg.maxRejects() != defaultVerifierMaxRejects || cfg.maxTurns() != defaultVerifierMaxTurns {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	// Bad shapes error loudly.
	for _, bad := range []map[string]any{
		{"verifier": "yes"},
		{"verifier": map[string]any{"enabled": "true"}},
		{"verifier": map[string]any{"model": 5}},
		{"verifier": map[string]any{"max_rejects": 0}},
		{"verifier": map[string]any{"max_rejects": -1}},
		{"verifier": map[string]any{"max_turns": "many"}},
		{"verifier": map[string]any{"typo_key": true}},
	} {
		if err := e.ApplyConfig(bad); err == nil {
			t.Errorf("ApplyConfig accepted invalid config %v", bad)
		}
	}
}

func TestParseVerifierVerdict(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		reasons int
		ok      bool
	}{
		{"plain pass", `{"verdict":"PASS","reasons":[]}`, "PASS", 0, true},
		{"reject with reasons", `{"verdict":"REJECT","reasons":["tests fail","lint dirty"]}`, "REJECT", 2, true},
		{"embedded in prose", "I checked everything.\n\n{\"verdict\":\"PASS\",\"reasons\":[]}\n", "PASS", 0, true},
		{"code fence", "```json\n{\"verdict\":\"REJECT\",\"reasons\":[\"missing doc\"]}\n```", "REJECT", 1, true},
		{"lowercase verdict", `{"verdict":"pass","reasons":[]}`, "PASS", 0, true},
		{"last valid wins", `{"verdict":"REJECT","reasons":["draft"]} later corrected: {"verdict":"PASS","reasons":[]}`, "PASS", 0, true},
		{"no json", "looks good to me", "", 0, false},
		{"wrong verdict value", `{"verdict":"MAYBE","reasons":[]}`, "", 0, false},
		{"unrelated json", `{"status":"ok"}`, "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseVerifierVerdict(c.in)
			if ok != c.ok {
				t.Fatalf("ok=%v, want %v", ok, c.ok)
			}
			if !c.ok {
				return
			}
			if got.Verdict != c.want || len(got.Reasons) != c.reasons {
				t.Fatalf("got %+v, want verdict=%s reasons=%d", got, c.want, c.reasons)
			}
		})
	}
}

// newVerifierExtension wires an Extension with the fakeAPI, enables the
// verifier, and starts an active goal. Returns the extension, api, and the
// registered update_goal tool.
func newVerifierExtension(t *testing.T, cfg map[string]any) (*Extension, *fakeAPI, types.Tool) {
	t.Helper()
	e := New(Options{})
	if err := e.ApplyConfig(map[string]any{"verifier": cfg}); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	api := newFakeAPI()
	api.dir = t.TempDir()
	if err := e.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := e.store.Start("objective: make tests pass"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var updateTool types.Tool
	for _, tool := range api.tools {
		if tool.Name() == "update_goal" {
			updateTool = tool
		}
	}
	if updateTool == nil {
		t.Fatal("update_goal tool not registered")
	}
	return e, api, updateTool
}

func TestUpdateGoalVerifierRejectKeepsGoalActive(t *testing.T) {
	e, api, tool := newVerifierExtension(t, map[string]any{"enabled": true})
	api.fork = func(context.Context, extension.ForkOptions) (string, error) {
		return `{"verdict":"REJECT","reasons":["tests in pkg/foo fail","objective requires a doc update"]}`, nil
	}

	text, res := callToolResult(t, tool, map[string]any{"status": "complete"})
	if !res.IsError {
		t.Fatalf("expected error result, got: %s", text)
	}
	if !strings.Contains(text, "tests in pkg/foo fail") || !strings.Contains(text, "reject 1/3") {
		t.Fatalf("rejection message missing detail: %s", text)
	}
	g, ok := e.store.Current()
	if !ok || g.Status != StatusActive {
		t.Fatalf("goal should stay active, got %+v", g)
	}
	if g.VerifierRejects != 1 {
		t.Fatalf("VerifierRejects=%d, want 1", g.VerifierRejects)
	}

	// The verifier child must be a fresh, tool-restricted agent.
	if len(api.forkOpts) != 1 {
		t.Fatalf("fork calls=%d, want 1", len(api.forkOpts))
	}
	opts := api.forkOpts[0]
	if opts.Context != "fresh" || opts.Name != "goal-verifier" {
		t.Fatalf("unexpected fork opts: %+v", opts)
	}
	for _, banned := range []string{"write", "edit"} {
		for _, tool := range opts.AllowedTools {
			if tool == banned {
				t.Fatalf("verifier must not get %q tool: %v", banned, opts.AllowedTools)
			}
		}
	}
	if !strings.Contains(opts.Task, "objective: make tests pass") {
		t.Fatalf("verifier task missing objective: %s", opts.Task)
	}
}

func TestUpdateGoalVerifierPassCompletes(t *testing.T) {
	e, api, tool := newVerifierExtension(t, map[string]any{"enabled": true})
	api.fork = func(context.Context, extension.ForkOptions) (string, error) {
		return "All checks hold.\n{\"verdict\":\"PASS\",\"reasons\":[]}", nil
	}
	text, res := callToolResult(t, tool, map[string]any{"status": "complete"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", text)
	}
	g, ok := e.store.Current()
	if !ok || g.Status != StatusComplete {
		t.Fatalf("goal should be complete, got %+v", g)
	}
}

func TestUpdateGoalVerifierPausesAfterMaxRejects(t *testing.T) {
	e, api, tool := newVerifierExtension(t, map[string]any{"enabled": true, "max_rejects": 2})
	api.fork = func(context.Context, extension.ForkOptions) (string, error) {
		return `{"verdict":"REJECT","reasons":["still broken"]}`, nil
	}

	text, _ := callToolResult(t, tool, map[string]any{"status": "complete"})
	if strings.Contains(text, "PAUSED") {
		t.Fatalf("first reject must not pause: %s", text)
	}
	text, res := callToolResult(t, tool, map[string]any{"status": "complete"})
	if !res.IsError || !strings.Contains(text, "PAUSED") {
		t.Fatalf("second reject should pause, got: %s", text)
	}
	g, ok := e.store.Current()
	if !ok || g.Status != StatusPaused {
		t.Fatalf("goal should be paused, got %+v", g)
	}

	// Human resume grants a fresh round of verification attempts.
	if _, err := e.store.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	g, _ = e.store.Current()
	if g.VerifierRejects != 0 {
		t.Fatalf("VerifierRejects should reset on resume, got %d", g.VerifierRejects)
	}
}

func TestUpdateGoalVerifierFailsOpenOnForkError(t *testing.T) {
	e, api, tool := newVerifierExtension(t, map[string]any{"enabled": true})
	api.fork = func(context.Context, extension.ForkOptions) (string, error) {
		return "", errors.New("fork support not wired")
	}
	text, res := callToolResult(t, tool, map[string]any{"status": "complete"})
	if res.IsError {
		t.Fatalf("verifier infra failure must fail open: %s", text)
	}
	g, ok := e.store.Current()
	if !ok || g.Status != StatusComplete {
		t.Fatalf("goal should be complete, got %+v", g)
	}
}

func TestUpdateGoalVerifierUnparseableVerdictRejects(t *testing.T) {
	e, api, tool := newVerifierExtension(t, map[string]any{"enabled": true})
	api.fork = func(context.Context, extension.ForkOptions) (string, error) {
		return "everything looks great, ship it!", nil
	}
	text, res := callToolResult(t, tool, map[string]any{"status": "complete"})
	if !res.IsError || !strings.Contains(text, "parseable verdict") {
		t.Fatalf("unparseable verdict should reject: %s", text)
	}
	g, _ := e.store.Current()
	if g.Status != StatusActive || g.VerifierRejects != 1 {
		t.Fatalf("goal should stay active with 1 reject, got %+v", g)
	}
}

func TestUpdateGoalVerifierDisabledSkipsFork(t *testing.T) {
	e := New(Options{})
	api := newFakeAPI()
	api.dir = t.TempDir()
	if err := e.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := e.store.Start("obj"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var tool types.Tool
	for _, tl := range api.tools {
		if tl.Name() == "update_goal" {
			tool = tl
		}
	}
	_, res := callToolResult(t, tool, map[string]any{"status": "complete"})
	if res.IsError {
		t.Fatal("disabled verifier must not block completion")
	}
	if len(api.forkOpts) != 0 {
		t.Fatalf("no fork expected when disabled, got %d", len(api.forkOpts))
	}
}
