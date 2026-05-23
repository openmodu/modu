package goal

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
)

// fakeAPI is a hand-rolled ExtensionAPI good enough to exercise the Extension's
// Init wiring without spinning up a real CodingSession. Captures every
// SendMessage call so tests can assert on the loop's behaviour.
type fakeAPI struct {
	mu       sync.Mutex
	tools    []agent.AgentTool
	commands map[string]extension.CommandHandler
	handlers map[string][]extension.EventHandler
	sent     []string
	notices  []string
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		commands: map[string]extension.CommandHandler{},
		handlers: map[string][]extension.EventHandler{},
	}
}

func (f *fakeAPI) RegisterTool(t agent.AgentTool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = append(f.tools, t)
}

func (f *fakeAPI) RegisterCommand(name, _ string, h extension.CommandHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[name] = h
}

func (f *fakeAPI) AddHook(extension.ToolHook) {}

func (f *fakeAPI) On(event string, h extension.EventHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[event] = append(f.handlers[event], h)
}

func (f *fakeAPI) SendMessage(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeAPI) SendFollowUpMessage(text string) error { return f.SendMessage(text) }
func (f *fakeAPI) SetActiveTools([]string)               {}
func (f *fakeAPI) SetModel(string, string) error         { return nil }
func (f *fakeAPI) GetCommands() []extension.Command      { return nil }
func (f *fakeAPI) SessionID() string                     { return "test-session" }
func (f *fakeAPI) SessionDir() string                    { return "" }
func (f *fakeAPI) AgentDir() string                      { return "" }
func (f *fakeAPI) Cwd() string                           { return "/tmp/project" }
func (f *fakeAPI) IsIdle() bool                          { return true }
func (f *fakeAPI) HasPendingMessages() bool              { return false }
func (f *fakeAPI) Notify(extensionName, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, extensionName+": "+text)
}

func (f *fakeAPI) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeAPI) lastSent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return ""
	}
	return f.sent[len(f.sent)-1]
}

func (f *fakeAPI) fireAgentEnd() {
	f.mu.Lock()
	hs := append([]extension.EventHandler(nil), f.handlers[string(agent.EventTypeAgentEnd)]...)
	f.mu.Unlock()
	for _, h := range hs {
		h(agent.AgentEvent{Type: agent.EventTypeAgentEnd})
	}
}

func (f *fakeAPI) runCommand(t *testing.T, name, args string) error {
	t.Helper()
	f.mu.Lock()
	h, ok := f.commands[name]
	f.mu.Unlock()
	if !ok {
		t.Fatalf("command %q not registered", name)
	}
	return h(args)
}

// initialized returns an Extension that's already been Init'd against a
// shared fakeAPI; both are returned so the test can drive commands + assert.
func initialized(t *testing.T) (*Extension, *fakeAPI) {
	t.Helper()
	ext := New(Options{})
	api := newFakeAPI()
	if err := ext.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return ext, api
}

func TestInitRegistersTheExpectedSurface(t *testing.T) {
	_, api := initialized(t)

	// Commands.
	for _, want := range []string{"goal", "goal-pause", "goal-resume", "goal-cancel", "goal-status"} {
		if _, ok := api.commands[want]; !ok {
			t.Errorf("command /%s not registered", want)
		}
	}
	// Tools.
	wantTools := map[string]bool{"create_goal": false, "update_goal": false, "get_goal": false}
	for _, tl := range api.tools {
		if _, ok := wantTools[tl.Name()]; ok {
			wantTools[tl.Name()] = true
		}
	}
	for name, found := range wantTools {
		if !found {
			t.Errorf("tool %q not registered", name)
		}
	}
	// agent_end handler.
	if len(api.handlers[string(agent.EventTypeAgentEnd)]) != 1 {
		t.Errorf("expected 1 agent_end handler, got %d", len(api.handlers[string(agent.EventTypeAgentEnd)]))
	}
}

func TestSlashGoalStartsLoopAndInjectsContinuation(t *testing.T) {
	_, api := initialized(t)

	if err := api.runCommand(t, "goal", "draft a release note"); err != nil {
		t.Fatalf("/goal: %v", err)
	}
	// /goal sends the initial continuation; subsequent agent_end events
	// keep the loop running.
	if api.sentCount() != 1 {
		t.Fatalf("expected 1 continuation after /goal, got %d", api.sentCount())
	}
	if !strings.Contains(api.lastSent(), "draft a release note") {
		t.Errorf("initial continuation missing objective body: %s", api.lastSent())
	}
	if !strings.Contains(api.lastSent(), "<untrusted_objective>") {
		t.Errorf("initial continuation missing untrusted envelope: %s", api.lastSent())
	}

	api.fireAgentEnd()
	if api.sentCount() != 2 {
		t.Fatalf("expected 2 continuations after agent_end, got %d", api.sentCount())
	}

	// Multiple agent_end without state change → one continuation each.
	api.fireAgentEnd()
	api.fireAgentEnd()
	if api.sentCount() != 4 {
		t.Fatalf("expected 4 continuations after 3 agent_ends, got %d", api.sentCount())
	}
}

func TestPauseStopsLoopResumeRestartsIt(t *testing.T) {
	_, api := initialized(t)
	api.runCommand(t, "goal", "scan the codebase for TODOs")
	api.fireAgentEnd() // active loop running

	before := api.sentCount()
	api.runCommand(t, "goal-pause", "")
	api.fireAgentEnd()
	api.fireAgentEnd()
	if api.sentCount() != before {
		t.Errorf("pause did not stop loop: sent=%d before=%d", api.sentCount(), before)
	}

	api.runCommand(t, "goal-resume", "")
	// Resume injects one continuation immediately.
	if api.sentCount() != before+1 {
		t.Errorf("resume should inject one immediate continuation; sent=%d want=%d", api.sentCount(), before+1)
	}
	api.fireAgentEnd()
	if api.sentCount() != before+2 {
		t.Errorf("loop should be active after resume; sent=%d want=%d", api.sentCount(), before+2)
	}
}

func TestCancelStopsLoopPermanently(t *testing.T) {
	_, api := initialized(t)
	api.runCommand(t, "goal", "noop")
	api.fireAgentEnd()

	before := api.sentCount()
	api.runCommand(t, "goal-cancel", "")
	api.fireAgentEnd()
	api.fireAgentEnd()
	if api.sentCount() != before {
		t.Errorf("cancel should halt loop, but sent=%d before=%d", api.sentCount(), before)
	}

	// A fresh /goal is allowed.
	if err := api.runCommand(t, "goal", "another"); err != nil {
		t.Errorf("/goal after cancel should succeed: %v", err)
	}
}

func TestUpdateGoalCompleteStopsLoop(t *testing.T) {
	ext, api := initialized(t)
	api.runCommand(t, "goal", "compile success")
	api.fireAgentEnd()
	before := api.sentCount()

	// Simulate the model calling the update_goal tool.
	for _, tl := range api.tools {
		if tl.Name() == "update_goal" {
			if _, err := tl.Execute(context.Background(), "tc-1", map[string]any{"status": "complete"}, nil); err != nil {
				t.Fatalf("update_goal: %v", err)
			}
		}
	}
	g, _ := ext.store.Current()
	if g.Status != StatusComplete {
		t.Fatalf("after update_goal store should be complete, got %q", g.Status)
	}

	api.fireAgentEnd()
	api.fireAgentEnd()
	if api.sentCount() != before {
		t.Errorf("loop should stop once complete; sent=%d before=%d", api.sentCount(), before)
	}
}

func TestGoalObjectiveReplacesExistingGoal(t *testing.T) {
	_, api := initialized(t)
	if err := api.runCommand(t, "goal", "first"); err != nil {
		t.Fatalf("first /goal: %v", err)
	}
	if err := api.runCommand(t, "goal", "second"); err != nil {
		t.Fatalf("second /goal should replace existing objective: %v", err)
	}
	if !strings.Contains(api.lastSent(), "second") {
		t.Errorf("replacement continuation missing new objective: %s", api.lastSent())
	}
}

func TestRuntimeStateExposesIndicator(t *testing.T) {
	ext, api := initialized(t)
	if err := api.runCommand(t, "goal", "show in status line"); err != nil {
		t.Fatalf("/goal: %v", err)
	}
	state, ok := ext.RuntimeState().(map[string]any)
	if !ok {
		t.Fatalf("RuntimeState type mismatch: %T", ext.RuntimeState())
	}
	if state["status"] != StatusActive {
		t.Fatalf("status = %v, want active", state["status"])
	}
	if got, _ := state["indicator"].(string); !strings.Contains(got, "Pursuing goal") {
		t.Fatalf("indicator missing pursuing text: %q", got)
	}

	if err := api.runCommand(t, "goal-pause", ""); err != nil {
		t.Fatalf("/goal-pause: %v", err)
	}
	state, _ = ext.RuntimeState().(map[string]any)
	if got, _ := state["indicator"].(string); got != "Goal paused (/goal resume)" {
		t.Fatalf("paused indicator mismatch: %q", got)
	}
}

func TestSlashGoalNotifiesHost(t *testing.T) {
	_, api := initialized(t)
	if err := api.runCommand(t, "goal", "notify the tui"); err != nil {
		t.Fatalf("/goal: %v", err)
	}
	if len(api.notices) == 0 {
		t.Fatal("expected host notification")
	}
	if got := api.notices[len(api.notices)-1]; !strings.Contains(got, "goal: Goal active") ||
		!strings.Contains(got, "notify the tui") {
		t.Fatalf("notification mismatch: %q", got)
	}
}
