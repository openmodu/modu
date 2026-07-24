package goal

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

// fakeAPI is a hand-rolled ExtensionAPI good enough to exercise the Extension's
// Init wiring without spinning up a real CodingSession. Captures every
// SendMessage call so tests can assert on the loop's behaviour.
type fakeAPI struct {
	mu       sync.Mutex
	tools    []types.Tool
	commands map[string]extension.CommandHandler
	handlers map[string][]extension.EventHandler
	sent     []string
	sentOpts []extension.MessageOptions
	notices  []string
	confirms []string
	confirm  *bool
	selects  []string
	selectQ  []string
	dir      string
	agentDir string
	// fork, when set, backs ForkSession so verifier tests can script the
	// child's reply. forkOpts records every call for assertions.
	fork     func(context.Context, extension.ForkOptions) (string, error)
	forkOpts []extension.ForkOptions
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{
		commands: map[string]extension.CommandHandler{},
		handlers: map[string][]extension.EventHandler{},
	}
}

func (f *fakeAPI) RegisterTool(t types.Tool) {
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
	return f.SendMessageWithOptions(text, extension.MessageOptions{})
}

func (f *fakeAPI) SendMessageWithOptions(text string, opts extension.MessageOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	f.sentOpts = append(f.sentOpts, opts)
	return nil
}

func (f *fakeAPI) SendFollowUpMessage(text string) error {
	return f.SendMessageWithOptions(text, extension.MessageOptions{DeliverAs: "followUp"})
}
func (f *fakeAPI) SetActiveTools([]string)          {}
func (f *fakeAPI) SetModel(string, string) error    { return nil }
func (f *fakeAPI) GetCommands() []extension.Command { return nil }
func (f *fakeAPI) SessionID() string                { return "test-session" }
func (f *fakeAPI) SessionDir() string               { return f.dir }
func (f *fakeAPI) AgentDir() string                 { return f.agentDir }
func (f *fakeAPI) Cwd() string                      { return "/tmp/project" }
func (f *fakeAPI) IsIdle() bool                     { return true }
func (f *fakeAPI) HasPendingMessages() bool         { return false }
func (f *fakeAPI) PermissionMode() string           { return "" }
func (f *fakeAPI) BackgroundTasks() []extension.TaskSnapshot {
	return nil
}
func (f *fakeAPI) InterruptBackgroundTask(string, string) (extension.TaskSnapshot, bool) {
	return extension.TaskSnapshot{}, false
}
func (f *fakeAPI) AddPending(int) {}
func (f *fakeAPI) DonePending()   {}
func (f *fakeAPI) ForkSession(ctx context.Context, opts extension.ForkOptions) (string, error) {
	f.mu.Lock()
	f.forkOpts = append(f.forkOpts, opts)
	fork := f.fork
	f.mu.Unlock()
	if fork != nil {
		return fork(ctx, opts)
	}
	return "", nil
}
func (f *fakeAPI) Notify(extensionName, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, extensionName+": "+text)
}
func (f *fakeAPI) Confirm(title, body string, defaultYes bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirms = append(f.confirms, title+"\n"+body)
	if f.confirm != nil {
		return *f.confirm
	}
	return defaultYes
}
func (f *fakeAPI) Select(title string, options []string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.selects = append(f.selects, title)
	if len(f.selectQ) > 0 {
		out := f.selectQ[0]
		f.selectQ = f.selectQ[1:]
		return out
	}
	if len(options) == 0 {
		return ""
	}
	return options[0]
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

func (f *fakeAPI) lastSentOptions() extension.MessageOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sentOpts) == 0 {
		return extension.MessageOptions{}
	}
	return f.sentOpts[len(f.sentOpts)-1]
}

func (f *fakeAPI) fireAgentEnd() {
	f.fire(string(types.EventTypeAgentEnd))
}

func (f *fakeAPI) fire(event string) {
	f.fireEvent(types.Event{Type: types.EventType(event)})
}

func (f *fakeAPI) fireEvent(event types.Event) {
	f.mu.Lock()
	hs := append([]extension.EventHandler(nil), f.handlers[string(event.Type)]...)
	f.mu.Unlock()
	for _, h := range hs {
		h(event)
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
	api.dir = t.TempDir()
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
	if len(api.handlers[string(types.EventTypeAgentEnd)]) != 1 {
		t.Errorf("expected 1 agent_end handler, got %d", len(api.handlers[string(types.EventTypeAgentEnd)]))
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
	if got := api.lastSentOptions(); got.CustomType != goalContinuationType || got.DeliverAs != "followUp" || got.Display {
		t.Fatalf("continuation message options mismatch: %+v", got)
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

func TestSessionShutdownFlushesActiveTurnAccounting(t *testing.T) {
	ext, api := initialized(t)
	if err := api.runCommand(t, "goal", "account shutdown time"); err != nil {
		t.Fatalf("/goal: %v", err)
	}
	active, _ := ext.store.Current()
	ext.mu.Lock()
	ext.agentTurnInProgress = true
	ext.agentGoalID = active.ID
	ext.agentMeasuredFrom = time.Now().Add(-2 * time.Second)
	ext.mu.Unlock()

	api.fire(extensionShutdown)
	got, _ := ext.store.Current()
	if got.TimeUsedSeconds < 1 {
		t.Fatalf("shutdown should flush active elapsed time, got %ds", got.TimeUsedSeconds)
	}
	ext.mu.Lock()
	cleared := ext.agentGoalID == "" && ext.completedThisTurnGoalID == "" && ext.agentMeasuredFrom.IsZero()
	ext.mu.Unlock()
	if !cleared {
		t.Fatal("shutdown should clear in-memory goal accounting")
	}
}

func TestSubagentChildUsageCountsTowardGoalBudget(t *testing.T) {
	ext, api := initialized(t)
	if err := api.runCommand(t, "goal", "build feature"); err != nil {
		t.Fatalf("/goal: %v", err)
	}
	// agent_start begins per-turn accounting against the active goal.
	api.fire(string(types.EventTypeAgentStart))
	before, _ := ext.store.Current()

	// Host reports a subagent child's transcript with token usage.
	api.fireEvent(types.Event{
		Type: types.EventType(extensionSubagentChildUsage),
		Messages: []types.AgentMessage{
			&types.AssistantMessage{Usage: types.AgentUsage{Input: 300, Output: 200, TotalTokens: 500}},
		},
	})
	// The turn ends with no main-agent usage of its own.
	api.fireAgentEnd()

	after, _ := ext.store.Current()
	if got := after.TokensUsed - before.TokensUsed; got != 500 {
		t.Fatalf("expected subagent usage (500 tokens) folded into goal budget, got delta %d", got)
	}
}

func TestSubagentChildUsageIgnoredWithoutActiveGoal(t *testing.T) {
	ext, api := initialized(t)
	// No goal set: a child-usage event must not panic or fabricate a goal.
	api.fireEvent(types.Event{
		Type: types.EventType(extensionSubagentChildUsage),
		Messages: []types.AgentMessage{
			&types.AssistantMessage{Usage: types.AgentUsage{Input: 10, Output: 10}},
		},
	})
	api.fireAgentEnd()
	if _, ok := ext.store.Current(); ok {
		t.Fatal("no goal should exist after a child-usage event with no active goal")
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

func TestGoalClearWithoutGoalMatchesPiFeedback(t *testing.T) {
	_, api := initialized(t)
	if err := api.runCommand(t, "goal", "clear"); err != nil {
		t.Fatalf("/goal clear: %v", err)
	}
	if len(api.notices) == 0 {
		t.Fatal("expected clear notification")
	}
	got := api.notices[len(api.notices)-1]
	if !strings.Contains(got, "No goal to clear\nThis thread does not currently have a goal.") {
		t.Fatalf("clear without goal notification mismatch: %q", got)
	}
}

func TestGoalStatusSubcommandIsObjective(t *testing.T) {
	ext, api := initialized(t)
	if err := api.runCommand(t, "goal", "status"); err != nil {
		t.Fatalf("/goal status: %v", err)
	}
	g, ok := ext.store.Current()
	if !ok {
		t.Fatal("/goal status should create a goal with objective status")
	}
	if g.Objective != "status" {
		t.Fatalf("objective = %q, want status", g.Objective)
	}
}

func TestUpdateGoalCompleteStopsLoop(t *testing.T) {
	ext, api := initialized(t)
	api.runCommand(t, "goal", "compile success")
	api.fireAgentEnd()
	before := api.sentCount()
	active, _ := ext.store.Current()
	ext.mu.Lock()
	ext.agentTurnInProgress = true
	ext.agentGoalID = active.ID
	ext.agentMeasuredFrom = time.Now().Add(-2 * time.Second)
	ext.mu.Unlock()

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
	if g, _ := ext.store.Current(); g.TimeUsedSeconds < 1 {
		t.Fatalf("completion accounting should include the active turn, got %ds", g.TimeUsedSeconds)
	}
	foundCompleteNotice := false
	for _, notice := range api.notices {
		// formatGoalActionFeedback emits FormatGoalForUser, whose header leads
		// with the status icon+label and puts the objective on its own line —
		// match each independently rather than wedging a fixed separator.
		if strings.Contains(notice, "✓ complete") && strings.Contains(notice, "compile success") {
			foundCompleteNotice = true
			break
		}
	}
	if !foundCompleteNotice {
		t.Fatalf("expected visible completion notice, got %#v", api.notices)
	}
}

func TestCreateGoalToolStartsAccountingDuringAgentTurn(t *testing.T) {
	ext, api := initialized(t)
	ext.mu.Lock()
	ext.agentTurnInProgress = true
	ext.mu.Unlock()

	for _, tl := range api.tools {
		if tl.Name() == "create_goal" {
			if _, err := tl.Execute(context.Background(), "tc-create", map[string]any{"objective": "created in turn"}, nil); err != nil {
				t.Fatalf("create_goal: %v", err)
			}
		}
	}
	g, _ := ext.store.Current()
	ext.mu.Lock()
	accountingStarted := ext.agentGoalID == g.ID && !ext.agentMeasuredFrom.IsZero()
	ext.mu.Unlock()
	if !accountingStarted {
		t.Fatal("create_goal should begin accounting for goals created inside an agent turn")
	}
}

func TestStaleAccountingClearsInMemoryState(t *testing.T) {
	ext, _ := initialized(t)
	first, err := ext.store.Start("first")
	if err != nil {
		t.Fatalf("Start first: %v", err)
	}
	if _, err := ext.store.ReplaceObjective("second", nil); err != nil {
		t.Fatalf("ReplaceObjective: %v", err)
	}
	ext.mu.Lock()
	ext.agentGoalID = first.ID
	ext.agentMeasuredFrom = time.Now().Add(-time.Second)
	ext.mu.Unlock()

	if _, ok := ext.accountCurrentAgentTurn(types.AgentUsage{Input: 1, Output: 1}, false); !ok {
		t.Fatal("expected current replacement goal returned")
	}
	ext.mu.Lock()
	cleared := ext.agentGoalID == "" && ext.agentMeasuredFrom.IsZero()
	ext.mu.Unlock()
	if !cleared {
		t.Fatal("stale accounting should clear in-memory accounting state")
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

func TestGoalSecondObjectiveAddsAndFocuses(t *testing.T) {
	ext, api := initialized(t)
	if err := api.runCommand(t, "goal", "first"); err != nil {
		t.Fatalf("first /goal: %v", err)
	}
	if err := api.runCommand(t, "goal", "second"); err != nil {
		t.Fatalf("second /goal: %v", err)
	}

	// Both goals persist; the new objective is focused and active, the old one
	// is parked (paused) so only one goal is active at a time.
	goals, focused, err := ext.store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 2 {
		t.Fatalf("want 2 goals, got %d: %#v", len(goals), goals)
	}
	current, _ := ext.store.Current()
	if current.Objective != "second" || current.ID != focused {
		t.Fatalf("focused goal should be 'second', got %q (focused=%s)", current.Objective, focused)
	}
	if current.Status != StatusActive {
		t.Fatalf("focused goal should be active, got %q", current.Status)
	}
	activeCount := 0
	for _, g := range goals {
		if g.Status == StatusActive {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("exactly one goal should be active, got %d", activeCount)
	}
}

func TestUIReadyCanResumePausedGoal(t *testing.T) {
	_, api := initialized(t)
	if err := api.runCommand(t, "goal", "scan todo"); err != nil {
		t.Fatalf("/goal: %v", err)
	}
	if err := api.runCommand(t, "goal-pause", ""); err != nil {
		t.Fatalf("/goal-pause: %v", err)
	}
	api.selectQ = []string{resumeGoalChoice}
	before := api.sentCount()
	api.fireEvent(types.Event{Type: types.EventType(extensionSessionStart), Reason: "resume"})
	api.fire(extensionUIReady)
	if api.sentCount() != before+1 {
		t.Fatalf("ui_ready resume should queue one continuation: got %d want %d", api.sentCount(), before+1)
	}
	if len(api.selects) == 0 || !strings.Contains(api.selects[len(api.selects)-1], "Resume paused goal?") {
		t.Fatalf("ui_ready should ask for resume selection, got %#v", api.selects)
	}
}

func TestUIReadyDoesNotPromptPausedGoalOnStartup(t *testing.T) {
	ext, api := initialized(t)
	if _, err := ext.store.Start("paused startup"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := ext.store.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	api.fireEvent(types.Event{Type: types.EventType(extensionSessionStart), Reason: "startup"})
	api.fire(extensionUIReady)
	if len(api.selects) != 0 {
		t.Fatalf("startup should not prompt to resume paused goal, got %#v", api.selects)
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
	if got, _ := state["indicator"].(string); !strings.HasPrefix(got, "● goal ") {
		t.Fatalf("indicator missing pursuing text: %q", got)
	}

	if err := api.runCommand(t, "goal-pause", ""); err != nil {
		t.Fatalf("/goal-pause: %v", err)
	}
	state, _ = ext.RuntimeState().(map[string]any)
	if got, _ := state["indicator"].(string); got != "⏸ goal paused" {
		t.Fatalf("paused indicator mismatch: %q", got)
	}
}

func TestRuntimeStateAlwaysCarriesWatchingFlag(t *testing.T) {
	ext, _ := initialized(t)
	// Even without a goal the key exists so host renderers can branch on
	// it without a presence check.
	empty, _ := ext.RuntimeState().(map[string]any)
	if _, ok := empty["watching"]; !ok {
		t.Fatal("watching key missing from empty runtime state")
	}
	if v, _ := empty["watching"].(bool); v {
		t.Fatalf("watching should default to false, got %v", v)
	}
}

func TestGoalWatchTogglesRuntimeStateFlag(t *testing.T) {
	ext, api := initialized(t)
	if err := api.runCommand(t, "goal-watch", ""); err != nil {
		t.Fatalf("/goal-watch toggle on: %v", err)
	}
	if state, _ := ext.RuntimeState().(map[string]any); !state["watching"].(bool) {
		t.Fatal("watching should be true after first /goal-watch")
	}

	if err := api.runCommand(t, "goal-watch", ""); err != nil {
		t.Fatalf("/goal-watch toggle off: %v", err)
	}
	if state, _ := ext.RuntimeState().(map[string]any); state["watching"].(bool) {
		t.Fatal("watching should flip back to false on second /goal-watch")
	}

	// Explicit on / off override toggle semantics.
	for _, arg := range []string{"on", "true", "1", "show"} {
		if err := api.runCommand(t, "goal-watch", "off"); err != nil {
			t.Fatalf("reset to off: %v", err)
		}
		if err := api.runCommand(t, "goal-watch", arg); err != nil {
			t.Fatalf("explicit %q: %v", arg, err)
		}
		if state, _ := ext.RuntimeState().(map[string]any); !state["watching"].(bool) {
			t.Fatalf("%q should set watching=true", arg)
		}
	}
	for _, arg := range []string{"off", "false", "0", "hide"} {
		if err := api.runCommand(t, "goal-watch", "on"); err != nil {
			t.Fatalf("reset to on: %v", err)
		}
		if err := api.runCommand(t, "goal-watch", arg); err != nil {
			t.Fatalf("explicit %q: %v", arg, err)
		}
		if state, _ := ext.RuntimeState().(map[string]any); state["watching"].(bool) {
			t.Fatalf("%q should set watching=false", arg)
		}
	}
}

func TestGoalWatchRejectsBadArgs(t *testing.T) {
	_, api := initialized(t)
	err := api.runCommand(t, "goal-watch", "maybe")
	if err == nil || !strings.Contains(err.Error(), "expected on|off") {
		t.Fatalf("bad arg should error with hint, got %v", err)
	}
}

func TestGoalIndicatorTextMatchesPiGoalFooter(t *testing.T) {
	budget := 50_000
	active := Goal{Status: StatusActive, TokenBudget: &budget, TokensUsed: 63_876}
	if got, want := goalIndicatorText(active), "● goal 63.9K/50K"; got != want {
		t.Fatalf("active budget indicator = %q, want %q", got, want)
	}
	limited := Goal{Status: StatusBudgetLimited, TokenBudget: &budget, TokensUsed: 63_876}
	if got, want := goalIndicatorText(limited), "⚠ goal limited 63.9K/50K"; got != want {
		t.Fatalf("budget-limited indicator = %q, want %q", got, want)
	}
	abandoned := Goal{Status: StatusBudgetLimited}
	if got, want := goalIndicatorText(abandoned), "⚠ goal limited"; got != want {
		t.Fatalf("budget-limited without budget indicator = %q, want %q", got, want)
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
	if got := api.notices[len(api.notices)-1]; !strings.Contains(got, "● active") ||
		!strings.Contains(got, "notify the tui") {
		t.Fatalf("notification mismatch: %q", got)
	}
}
