package goal

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
	"github.com/openmodu/modu/pkg/types"
)

const (
	goalUsage             = "Usage: /goal <objective>"
	extensionSessionStart = "session_start"
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

func (e *Extension) Name() string { return "goal" }

// RuntimeState exposes goal state for RuntimeState JSON and host UIs.
func (e *Extension) RuntimeState() any {
	g, ok := e.store.Current()
	if !ok {
		return map[string]any{
			"active": false,
		}
	}
	state := map[string]any{
		"active":          g.Status == StatusActive,
		"id":              g.ID,
		"threadId":        g.ThreadID,
		"objective":       g.Objective,
		"status":          g.Status,
		"tokensUsed":      g.TokensUsed,
		"timeUsedSeconds": g.TimeUsedSeconds,
		"createdAt":       g.CreatedAt,
		"updatedAt":       g.UpdatedAt,
		"indicator":       goalIndicatorText(g),
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
				return StoreRef{}
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

	api.RegisterTool(&createGoalTool{store: e.store})
	api.RegisterTool(&updateGoalTool{store: e.store, onComplete: e.markGoalCompletedThisTurn})
	api.RegisterTool(&getGoalTool{store: e.store})

	api.On(string(agent.EventTypeAgentStart), e.onAgentStart)
	api.On(string(agent.EventTypeAgentEnd), e.onAgentEnd)
	api.On(extensionSessionStart, e.onSessionStart)
	return nil
}

func (e *Extension) cmdGoal(args string) error {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return e.cmdStatus("")
	}
	switch strings.ToLower(trimmed) {
	case "pause":
		return e.cmdPause("")
	case "resume":
		return e.cmdResume("")
	case "clear", "cancel":
		return e.cmdClear("")
	case "status":
		return e.cmdStatus("")
	default:
		return e.cmdSetObjective(trimmed)
	}
}

func (e *Extension) cmdSetObjective(objective string) error {
	if g, ok := e.store.Current(); ok && g.Status == StatusActive {
		e.accountCurrentAgentTurn(types.AgentUsage{}, false)
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
	e.tell(fmt.Sprintf("Goal %s\n%s", goalStatusLabel(g.Status), e.store.Summary()))
	return e.queueGoalContinuation(g)
}

func (e *Extension) cmdPause(string) error {
	e.accountCurrentAgentTurn(types.AgentUsage{}, false)
	g, err := e.store.Pause()
	if err != nil {
		return err
	}
	e.stopAgentGoalAccounting(g.ID)
	e.tell(fmt.Sprintf("Goal paused\n%s", e.store.Summary()))
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
	e.tell(fmt.Sprintf("Goal %s\n%s", goalStatusLabel(g.Status), e.store.Summary()))
	return e.queueGoalContinuation(g)
}

func (e *Extension) cmdClear(string) error {
	e.accountCurrentAgentTurn(types.AgentUsage{}, false)
	g, err := e.store.Cancel()
	if err != nil {
		return err
	}
	e.stopAgentGoalAccounting(g.ID)
	e.tell("Goal cleared")
	return nil
}

func (e *Extension) cmdStatus(string) error {
	g, ok := e.store.Current()
	if !ok {
		e.tell(goalUsage + "\nNo goal is currently set.")
		return nil
	}
	e.tell(e.store.Summary())
	if g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
	}
	return nil
}

func (e *Extension) onAgentStart(_ agent.AgentEvent) {
	e.mu.Lock()
	e.agentTurnInProgress = true
	e.completedThisTurnGoalID = ""
	e.mu.Unlock()

	if g, ok := e.store.Current(); ok && g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
		return
	}
	e.clearAgentGoalAccounting()
}

func (e *Extension) onSessionStart(_ agent.AgentEvent) {
	if g, ok := e.store.Current(); ok && g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
		if e.shouldQueueContinuation() {
			if err := e.queueHiddenGoalPrompt(BuildContinuationPrompt(g)); err != nil {
				e.tell(fmt.Sprintf("goal: startup continuation inject failed: %v", err))
			}
		}
		return
	}
	e.clearAgentGoalAccounting()
}

func (e *Extension) onAgentEnd(event agent.AgentEvent) {
	includeComplete := e.completedGoalThisTurn() != ""
	g, ok := e.accountCurrentAgentTurn(collectUsage(event.Messages), includeComplete)

	e.mu.Lock()
	e.agentTurnInProgress = false
	e.completedThisTurnGoalID = ""
	e.mu.Unlock()

	if !ok {
		e.clearAgentGoalAccounting()
		return
	}
	if g.Status == StatusActive {
		e.beginAgentGoalAccounting(g)
		if e.shouldQueueContinuation() {
			if err := e.queueHiddenGoalPrompt(BuildContinuationPrompt(g)); err != nil {
				e.tell(fmt.Sprintf("goal: continuation inject failed: %v (loop will halt)", err))
			}
		}
		return
	}
	e.clearAgentGoalAccounting()
	if g.Status == StatusBudgetLimited && e.shouldQueueContinuation() {
		if err := e.queueHiddenGoalPrompt(BuildBudgetLimitedPrompt(g)); err != nil {
			e.tell(fmt.Sprintf("goal: budget-limit prompt inject failed: %v", err))
		}
	}
}

func (e *Extension) queueGoalContinuation(g Goal) error {
	if g.Status != StatusActive || !e.shouldQueueContinuation() {
		return nil
	}
	return e.queueHiddenGoalPrompt(BuildContinuationPrompt(g))
}

func (e *Extension) queueHiddenGoalPrompt(content string) error {
	if e.api == nil {
		return nil
	}
	return e.api.SendFollowUpMessage(content)
}

func (e *Extension) shouldQueueContinuation() bool {
	if e.api == nil {
		return true
	}
	return e.api.IsIdle() && !e.api.HasPendingMessages()
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

func (e *Extension) accountCurrentAgentTurn(usage types.AgentUsage, includeComplete bool) (Goal, bool) {
	e.mu.Lock()
	goalID := e.agentGoalID
	measuredFrom := e.agentMeasuredFrom
	e.mu.Unlock()
	if goalID == "" {
		g, ok := e.store.Current()
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
	}
	return g, ok
}

func collectUsage(messages []agent.AgentMessage) types.AgentUsage {
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
			return fmt.Sprintf("Pursuing goal (%d/%d tokens)", g.TokensUsed, *g.TokenBudget)
		}
		return fmt.Sprintf("Pursuing goal (%s)", formatElapsed(g.TimeUsedSeconds))
	case StatusPaused:
		return "Goal paused (/goal resume)"
	case StatusBudgetLimited:
		if g.TokenBudget != nil {
			return fmt.Sprintf("Goal unmet (%d/%d tokens)", g.TokensUsed, *g.TokenBudget)
		}
		return "Goal unmet"
	case StatusComplete:
		if g.TokenBudget != nil {
			return fmt.Sprintf("Goal achieved (%d tokens)", g.TokensUsed)
		}
		return fmt.Sprintf("Goal achieved (%s)", formatElapsed(g.TimeUsedSeconds))
	default:
		return string(g.Status)
	}
}

func cwdStoreKey(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "unknown"
	}
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(cwd)
}

func (e *Extension) tell(msg string) {
	if e.out != nil {
		e.out(msg)
	}
	if e.api != nil {
		e.api.Notify(e.Name(), msg)
	}
}
