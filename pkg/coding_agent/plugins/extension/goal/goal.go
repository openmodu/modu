package goal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

const (
	goalUsage                   = "Usage: /goal <objective>"
	extensionSessionStart       = "session_start"
	extensionUIReady            = "ui_ready"
	extensionShutdown           = "session_shutdown"
	extensionSubagentChildUsage = "subagent_child_usage"
	goalContinuationType        = "pi-goal-continuation"
	goalBudgetLimitType         = "pi-goal-budget-limit"
	replaceGoalChoice           = "Replace current goal"
	cancelReplaceChoice         = "Cancel"
	resumeGoalChoice            = "Resume goal"
	leaveGoalPausedChoice       = "Leave paused"
)

// Extension wires persistent /goal support into a CodingSession.
type Extension struct {
	store *Store
	api   extension.ExtensionAPI

	out func(string)

	mu                      sync.Mutex
	agentGoalID             string
	agentMeasuredFrom       time.Time
	agentTurnInProgress     bool
	completedThisTurnGoalID string
	lastSessionStartReason  string
	// pendingChildUsage accumulates token usage reported by subagent
	// (ForkSession) children during the current agent turn. It is folded
	// into the turn's own usage at agent_end so a goal's budget reflects
	// what its subagents spent, not just the main agent. Reset when taken.
	pendingChildUsage types.AgentUsage
	// watching toggles whether the host UI should render the goal
	// indicator (e.g. statusbar line). Off by default; controlled via
	// /goal-watch [on|off]. Not persisted — every session starts hidden so
	// the statusbar stays clean until the user opts in.
	watching bool
	// verifier configures the maker-checker completion gate; zero value =
	// disabled (update_goal completes without independent verification).
	verifier verifierConfig
}

// Options configures the Extension. The zero value is usable.
type Options struct {
	Out func(string)
}

// New constructs a Goal extension. Pass it into CodingSessionOptions.Extensions.
func New(opts Options) *Extension {
	return &Extension{
		store: NewStore(),
		out:   opts.Out,
	}
}

// init registers goal as a builtin extension. Hosts that want the goal
// extension only need to anonymous-import this package; LoadEnabled then
// picks it up.
//
// The factory uses Options{} (Out=nil) — the modu_code TUI cannot safely
// receive Out writes (writing to stderr corrupts the inline-mode widget
// layout). All goal feedback already reaches the user via api.Notify.
func init() {
	extension.Register("goal", func() extension.Extension {
		return New(Options{})
	})
}

func (e *Extension) Name() string { return "goal" }

// StartAutomationGoal creates the session goal for a host-driven automation
// before the first agent turn. It intentionally does not queue the initial
// hidden continuation because the host is about to send the task prompt; the
// normal agent_end hook will drive follow-up turns until completion.
func (e *Extension) StartAutomationGoal(objective string) (Goal, error) {
	g, err := e.store.Start(objective)
	if err != nil {
		return Goal{}, err
	}
	if g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
	}
	return g, nil
}

// AutomationGoalStatus exposes the current goal status to headless hosts that
// need to wait until hidden continuations have completed or a cap stops them.
func (e *Extension) AutomationGoalStatus() (Status, bool, error) {
	g, ok, err := e.store.CurrentErr()
	if err != nil || !ok {
		return "", ok, err
	}
	return g.Status, true, nil
}

// AutomationVerifierEnabled reports whether update_goal(status=complete) is
// currently gated by the maker-checker verifier for host-driven automations.
func (e *Extension) AutomationVerifierEnabled() bool {
	return e.verifierEnabled()
}

// RuntimeState exposes goal state for RuntimeState JSON and host UIs.
//
// `watching` is the host-UI opt-in: callers that render a statusbar /
// footer (e.g. modu_code TUI) should suppress the goal line unless this
// flag is true. The flag is always present so consumers can rely on the
// key existing even when no goal is set.
func (e *Extension) RuntimeState() any {
	watching := e.isWatching()
	g, ok, err := e.store.CurrentErr()
	if err != nil {
		return map[string]any{
			"active":   false,
			"watching": watching,
			"error":    err.Error(),
		}
	}
	if !ok {
		return map[string]any{
			"active":   false,
			"watching": watching,
		}
	}
	state := map[string]any{
		"active":           g.Status == StatusActive,
		"watching":         watching,
		"id":               g.ID,
		"threadId":         g.ThreadID,
		"objective":        g.Objective,
		"status":           g.Status,
		"tokensUsed":       g.TokensUsed,
		"inputTokens":      g.InputTokens,
		"outputTokens":     g.OutputTokens,
		"cacheReadTokens":  g.CacheReadTokens,
		"cacheWriteTokens": g.CacheWriteTokens,
		"timeUsedSeconds":  g.TimeUsedSeconds,
		"createdAt":        g.CreatedAt,
		"updatedAt":        g.UpdatedAt,
		"indicator":        goalIndicatorText(g),
	}
	if g.TokenBudget != nil {
		state["tokenBudget"] = *g.TokenBudget
		remaining := *g.TokenBudget - g.TokensUsed
		if remaining < 0 {
			remaining = 0
		}
		state["remainingTokens"] = remaining
	}
	if g.CompletedAt != nil {
		state["completedAt"] = *g.CompletedAt
	}
	return state
}

func (e *Extension) Init(api extension.ExtensionAPI) error {
	e.api = api
	e.store.SetRefProvider(func() StoreRef {
		sessionDir := api.SessionDir()
		if sessionDir == "" {
			agentDir := api.AgentDir()
			if agentDir == "" {
				if home, err := os.UserHomeDir(); err == nil && home != "" {
					agentDir = filepath.Join(home, ".pi", "agent")
				} else {
					return StoreRef{}
				}
			}
			sessionDir = filepath.Join(agentDir, "extensions", "pi-goal", "no-session", cwdStoreKey(api.Cwd()))
			return StoreRef{BaseDir: sessionDir, ThreadID: api.SessionID()}
		}
		return StoreRef{
			BaseDir:  filepath.Join(sessionDir, "extensions", "pi-goal"),
			ThreadID: api.SessionID(),
		}
	})

	api.RegisterCommand("goal", "Set, inspect, pause, resume, or clear the persistent goal", e.cmdGoal)
	// Keep the MVP commands as aliases so existing users are not broken.
	api.RegisterCommand("goal-pause", "Pause the active goal", e.cmdPause)
	api.RegisterCommand("goal-resume", "Resume a paused goal and inject one continuation immediately", e.cmdResume)
	api.RegisterCommand("goal-cancel", "Clear the current goal", e.cmdClear)
	api.RegisterCommand("goal-status", "Print the current goal's status", e.cmdStatus)
	api.RegisterCommand("goal-watch", "Toggle goal indicator in the statusbar: /goal-watch [on|off]", e.cmdWatch)

	api.RegisterTool(&createGoalTool{store: e.store, onCreate: e.beginAgentGoalAccounting})
	api.RegisterTool(&updateGoalTool{
		store:          e.store,
		verify:         e.verifyCompletion,
		beforeComplete: func() { e.accountCurrentAgentTurn(types.AgentUsage{}, false) },
		onComplete:     e.markGoalCompletedThisTurn,
	})
	api.RegisterTool(&getGoalTool{store: e.store})

	api.On(string(types.EventTypeAgentStart), e.onAgentStart)
	api.On(string(types.EventTypeAgentEnd), e.onAgentEnd)
	api.On(extensionSubagentChildUsage, e.onSubagentChildUsage)
	api.On(extensionSessionStart, e.onSessionStart)
	api.On(extensionUIReady, e.onUIReady)
	api.On(extensionShutdown, e.onSessionShutdown)
	return nil
}

func (e *Extension) cmdGoal(args string) error {
	command := parseGoalCommand(args)
	switch command.Kind {
	case parsedGoalShow:
		return e.cmdStatus("")
	case parsedGoalSetStatus:
		if command.Status == StatusPaused {
			return e.cmdPause("")
		}
		return e.cmdResume("")
	case parsedGoalClear:
		return e.cmdClear("")
	case parsedGoalSetObjective:
		return e.cmdSetObjective(command.Objective)
	}
	return nil
}

func (e *Extension) cmdSetObjective(objective string) error {
	if current, ok, err := e.store.CurrentErr(); err != nil {
		return err
	} else if ok {
		if e.api.Select(fmt.Sprintf("Replace goal?\nNew objective: %s", objective), []string{replaceGoalChoice, cancelReplaceChoice}) != replaceGoalChoice {
			e.tell("Goal unchanged")
			return nil
		}
		if current.Status == StatusActive {
			e.accountCurrentAgentTurn(types.AgentUsage{}, false)
		}
	}
	g, err := e.store.ReplaceObjective(objective, nil)
	if err != nil {
		return err
	}
	if g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
	} else {
		e.stopAgentGoalAccounting(g.ID)
	}
	e.tell(formatGoalActionFeedback(g))
	return e.queueGoalContinuation(g)
}

func (e *Extension) cmdPause(string) error {
	e.accountCurrentAgentTurn(types.AgentUsage{}, false)
	g, err := e.store.Pause()
	if err != nil {
		return err
	}
	e.stopAgentGoalAccounting(g.ID)
	e.tell(formatGoalActionFeedback(g))
	return nil
}

func (e *Extension) cmdResume(string) error {
	g, err := e.store.Resume()
	if err != nil {
		return err
	}
	if g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
	}
	e.tell(formatGoalActionFeedback(g))
	return e.queueGoalContinuation(g)
}

func (e *Extension) cmdClear(string) error {
	e.accountCurrentAgentTurn(types.AgentUsage{}, false)
	g, ok, err := e.store.CurrentErr()
	if err != nil {
		return err
	}
	if !ok {
		e.clearAgentGoalAccounting()
		e.tell("No goal to clear\nThis thread does not currently have a goal.")
		return nil
	}
	if _, err := e.store.Cancel(); err != nil {
		return err
	}
	e.stopAgentGoalAccounting(g.ID)
	e.tell("Goal cleared")
	return nil
}

func (e *Extension) cmdStatus(string) error {
	g, ok, err := e.store.CurrentErr()
	if err != nil {
		return err
	}
	if !ok {
		e.tell(goalUsage + "\nNo goal is currently set.")
		return nil
	}
	e.tell(FormatGoalForUser(&g))
	if g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
	}
	return nil
}

// cmdWatch toggles whether the host UI surfaces the goal indicator.
// Bare /goal-watch flips state; explicit on / off / true / false / 1 / 0
// set it. Any other argument is rejected rather than silently treated as
// toggle, so a typo doesn't surprise the user.
func (e *Extension) cmdWatch(args string) error {
	arg := strings.ToLower(strings.TrimSpace(args))
	var next bool
	switch arg {
	case "":
		next = !e.isWatching()
	case "on", "true", "1", "show":
		next = true
	case "off", "false", "0", "hide":
		next = false
	default:
		return fmt.Errorf("goal-watch: expected on|off, got %q", args)
	}
	e.setWatching(next)
	if next {
		e.tell("Goal indicator on (statusbar will show pursuit / pause / budget / done state)")
	} else {
		e.tell("Goal indicator off")
	}
	return nil
}

func (e *Extension) isWatching() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.watching
}

func (e *Extension) setWatching(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.watching = v
}

func (e *Extension) onAgentStart(_ types.Event) {
	e.mu.Lock()
	e.agentTurnInProgress = true
	e.completedThisTurnGoalID = ""
	e.mu.Unlock()

	g, ok, err := e.store.CurrentErr()
	if err != nil {
		e.tell(fmt.Sprintf("goal: read failed: %v", err))
		e.clearAgentGoalAccounting()
		return
	}
	if ok && g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
		return
	}
	e.clearAgentGoalAccounting()
}

func (e *Extension) onSessionStart(event types.Event) {
	e.mu.Lock()
	e.lastSessionStartReason = event.Reason
	e.mu.Unlock()

	g, ok, err := e.store.CurrentErr()
	if err != nil {
		e.tell(fmt.Sprintf("goal: read failed: %v", err))
		e.clearAgentGoalAccounting()
		return
	}
	if ok && g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
		if ShouldQueueContinuationWhenIdle(&g, e.apiIsIdle(), e.apiHasPendingMessages()) {
			if err := e.queueHiddenGoalPrompt(goalContinuationType, BuildContinuationPrompt(g)); err != nil {
				e.tell(fmt.Sprintf("goal: startup continuation inject failed: %v", err))
			}
		}
		return
	}
	e.clearAgentGoalAccounting()
}

func (e *Extension) onUIReady(_ types.Event) {
	g, ok, err := e.store.CurrentErr()
	if err != nil {
		e.tell(fmt.Sprintf("goal: read failed: %v", err))
		return
	}
	if !ok || g.Status != StatusPaused {
		return
	}
	if e.sessionStartReason() != "resume" || !e.apiIsIdle() || e.apiHasPendingMessages() {
		return
	}
	if e.api.Select(fmt.Sprintf("Resume paused goal?\nGoal: %s", g.Objective), []string{resumeGoalChoice, leaveGoalPausedChoice}) != resumeGoalChoice {
		return
	}
	if err := e.cmdResume(""); err != nil {
		e.tell(fmt.Sprintf("goal: resume failed: %v", err))
	}
}

func (e *Extension) onSessionShutdown(_ types.Event) {
	if e.hasAgentGoalAccounting() {
		e.accountCurrentAgentTurn(types.AgentUsage{}, false)
	}
	e.clearAgentGoalAccounting()
}

// onSubagentChildUsage folds a subagent child's token usage into the
// current turn's pending accounting. The host emits this event (carrying
// the child's transcript) when a ForkSession child finishes; without it the
// goal budget would ignore everything subagents spent.
func (e *Extension) onSubagentChildUsage(event types.Event) {
	usage := collectUsage(event.Messages)
	if usage == (types.AgentUsage{}) {
		return
	}
	e.mu.Lock()
	e.pendingChildUsage = addUsage(e.pendingChildUsage, usage)
	e.mu.Unlock()
}

// takePendingChildUsage returns and clears the accumulated subagent usage.
func (e *Extension) takePendingChildUsage() types.AgentUsage {
	e.mu.Lock()
	defer e.mu.Unlock()
	u := e.pendingChildUsage
	e.pendingChildUsage = types.AgentUsage{}
	return u
}

func (e *Extension) onAgentEnd(event types.Event) {
	includeComplete := e.completedGoalThisTurn() != ""
	usage := addUsage(collectUsage(event.Messages), e.takePendingChildUsage())
	g, ok := e.accountCurrentAgentTurn(usage, includeComplete)

	e.mu.Lock()
	e.agentTurnInProgress = false
	e.completedThisTurnGoalID = ""
	e.mu.Unlock()

	if !ok {
		e.clearAgentGoalAccounting()
		return
	}
	if includeComplete && g.Status == StatusComplete {
		e.clearAgentGoalAccounting()
		e.tell(formatGoalActionFeedback(g))
		return
	}
	if g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
		// agent_end fires inside the agent loop just before IsStreaming
		// flips off, so we must not gate on IsIdle here; the pure
		// after-agent-end policy only cares about pending user messages.
		if ShouldQueueContinuationAfterAgentEnd(&g, e.apiHasPendingMessages()) {
			if err := e.queueHiddenGoalPrompt(goalContinuationType, BuildContinuationPrompt(g)); err != nil {
				e.tell(fmt.Sprintf("goal: continuation inject failed: %v (loop will halt)", err))
			}
		}
		return
	}
	e.clearAgentGoalAccounting()
	if g.Status == StatusBudgetLimited && !e.apiHasPendingMessages() {
		if err := e.queueHiddenGoalPrompt(goalBudgetLimitType, BuildBudgetLimitedPrompt(g)); err != nil {
			e.tell(fmt.Sprintf("goal: budget-limit prompt inject failed: %v", err))
		}
	}
}

// queueGoalContinuation is the slash-command entry point (used by /goal,
// /goal-resume, /goal <new objective>). It uses the WhenIdle policy because
// these handlers can fire while the agent has a pending message queue or is
// otherwise busy and we must not double-prompt.
func (e *Extension) queueGoalContinuation(g Goal) error {
	if !ShouldQueueContinuationWhenIdle(&g, e.apiIsIdle(), e.apiHasPendingMessages()) {
		return nil
	}
	return e.queueHiddenGoalPrompt(goalContinuationType, BuildContinuationPrompt(g))
}

func (e *Extension) queueHiddenGoalPrompt(customType, content string) error {
	if e.api == nil {
		return nil
	}
	return e.api.SendMessageWithOptions(content, extension.MessageOptions{
		CustomType: customType,
		Display:    false,
		DeliverAs:  "followUp",
	})
}

// apiIsIdle and apiHasPendingMessages thin-wrap the API so the nil-api
// case (unit tests using NewStore directly) gracefully degrades to "idle
// and no pending work" - the same default a freshly started, never-driven
// agent would report.
func (e *Extension) apiIsIdle() bool {
	if e.api == nil {
		return true
	}
	return e.api.IsIdle()
}

func (e *Extension) apiHasPendingMessages() bool {
	if e.api == nil {
		return false
	}
	return e.api.HasPendingMessages()
}

func (e *Extension) beginAgentGoalAccounting(g Goal) {
	if g.Status != StatusActive {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.agentGoalID == g.ID {
		return
	}
	e.agentGoalID = g.ID
	e.agentMeasuredFrom = time.Now()
}

func (e *Extension) markGoalCompletedThisTurn(g Goal) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.agentTurnInProgress {
		return
	}
	e.completedThisTurnGoalID = g.ID
	e.agentGoalID = g.ID
	e.agentMeasuredFrom = time.Now()
}

func (e *Extension) stopAgentGoalAccounting(goalID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.agentGoalID == goalID {
		e.agentGoalID = ""
		e.agentMeasuredFrom = time.Time{}
	}
	if e.completedThisTurnGoalID == goalID {
		e.completedThisTurnGoalID = ""
	}
}

func (e *Extension) clearAgentGoalAccounting() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agentGoalID = ""
	e.agentMeasuredFrom = time.Time{}
	e.completedThisTurnGoalID = ""
}

func (e *Extension) completedGoalThisTurn() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.completedThisTurnGoalID
}

func (e *Extension) hasAgentGoalAccounting() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.agentGoalID != ""
}

func (e *Extension) sessionStartReason() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastSessionStartReason
}

func (e *Extension) accountCurrentAgentTurn(usage types.AgentUsage, includeComplete bool) (Goal, bool) {
	e.mu.Lock()
	goalID := e.agentGoalID
	measuredFrom := e.agentMeasuredFrom
	e.mu.Unlock()
	if goalID == "" {
		g, ok, err := e.store.CurrentErr()
		if err != nil {
			e.tell(fmt.Sprintf("goal: usage accounting failed: %v", err))
			return Goal{}, false
		}
		return g, ok
	}
	elapsed := int64(0)
	if !measuredFrom.IsZero() {
		elapsed = int64(time.Since(measuredFrom).Round(time.Second).Seconds())
	}
	g, ok, err := e.store.AccountUsage(usage, elapsed, includeComplete, goalID)
	if err != nil {
		e.tell(fmt.Sprintf("goal: usage accounting failed: %v", err))
		return Goal{}, false
	}
	if ok && g.ID == goalID {
		e.mu.Lock()
		e.agentMeasuredFrom = time.Now()
		e.mu.Unlock()
	} else {
		e.clearAgentGoalAccounting()
	}
	return g, ok
}

// addUsage returns the field-wise sum of two usage records, including the
// cost breakdown. Used to merge subagent child usage into a turn's usage.
func addUsage(a, b types.AgentUsage) types.AgentUsage {
	a.Input += b.Input
	a.Output += b.Output
	a.CacheRead += b.CacheRead
	a.CacheWrite += b.CacheWrite
	a.TotalTokens += b.TotalTokens
	a.Cost.Input += b.Cost.Input
	a.Cost.Output += b.Cost.Output
	a.Cost.CacheRead += b.Cost.CacheRead
	a.Cost.CacheWrite += b.Cost.CacheWrite
	a.Cost.Total += b.Cost.Total
	return a
}

func collectUsage(messages []types.AgentMessage) types.AgentUsage {
	var out types.AgentUsage
	for _, msg := range messages {
		switch m := msg.(type) {
		case types.AssistantMessage:
			out.Input += m.Usage.Input
			out.Output += m.Usage.Output
			out.CacheRead += m.Usage.CacheRead
			out.CacheWrite += m.Usage.CacheWrite
			out.TotalTokens += m.Usage.TotalTokens
			out.Cost.Input += m.Usage.Cost.Input
			out.Cost.Output += m.Usage.Cost.Output
			out.Cost.CacheRead += m.Usage.Cost.CacheRead
			out.Cost.CacheWrite += m.Usage.Cost.CacheWrite
			out.Cost.Total += m.Usage.Cost.Total
		case *types.AssistantMessage:
			if m != nil {
				out.Input += m.Usage.Input
				out.Output += m.Usage.Output
				out.CacheRead += m.Usage.CacheRead
				out.CacheWrite += m.Usage.CacheWrite
				out.TotalTokens += m.Usage.TotalTokens
				out.Cost.Input += m.Usage.Cost.Input
				out.Cost.Output += m.Usage.Cost.Output
				out.Cost.CacheRead += m.Usage.Cost.CacheRead
				out.Cost.CacheWrite += m.Usage.Cost.CacheWrite
				out.Cost.Total += m.Usage.Cost.Total
			}
		}
	}
	return out
}

func goalStatusLabel(status Status) string {
	switch status {
	case StatusActive:
		return "active"
	case StatusPaused:
		return "paused"
	case StatusBudgetLimited:
		return "limited by budget"
	case StatusComplete:
		return "complete"
	default:
		return string(status)
	}
}

func goalIndicatorText(g Goal) string {
	switch g.Status {
	case StatusActive:
		if g.TokenBudget != nil {
			return fmt.Sprintf("goal %s/%s", formatTokensCompact(g.TokensUsed), formatTokensCompact(*g.TokenBudget))
		}
		return "goal " + formatElapsed(g.TimeUsedSeconds)
	case StatusPaused:
		return "goal paused"
	case StatusBudgetLimited:
		if g.TokenBudget != nil {
			return fmt.Sprintf("goal limited %s/%s", formatTokensCompact(g.TokensUsed), formatTokensCompact(*g.TokenBudget))
		}
		return "goal limited"
	case StatusComplete:
		if g.TokenBudget != nil {
			return fmt.Sprintf("goal done %s", formatTokensCompact(g.TokensUsed))
		}
		return "goal done " + formatElapsed(g.TimeUsedSeconds)
	default:
		return string(g.Status)
	}
}

// formatGoalActionFeedback assembles the slash-command echo / host
// notification body. Mirrors pi-goal's "Goal <label>\n<formatGoalForTool>"
// pattern so callers see the full state (Status / Time used / Tokens used /
// Completed at) — not just an objective preview.
func formatGoalActionFeedback(g Goal) string {
	return fmt.Sprintf("Goal %s\n%s", goalStatusLabel(g.Status), FormatGoalForUser(&g))
}

func (e *Extension) tell(msg string) {
	if e.out != nil {
		e.out(msg)
	}
	if e.api != nil {
		e.api.Notify(e.Name(), msg)
	}
}
