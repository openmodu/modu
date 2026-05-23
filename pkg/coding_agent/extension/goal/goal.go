package goal

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/extension"
)

// Extension is the modu plug for /goal. Build one with New, then pass it via
// CodingSessionOptions.Extensions. It is self-contained: it holds the goal
// Store, registers two model-callable tools (update_goal, get_goal), five
// slash commands (/goal, /goal-pause, /goal-resume, /goal-cancel,
// /goal-status), and an agent_end listener that drives the continuation
// loop.
type Extension struct {
	store *Store
	api   extension.ExtensionAPI

	// out is where slash-command output goes back to the user. Optional;
	// when nil, command results are silent (the runtime is still expected
	// to surface the returned error).
	out func(string)

	// continuing guards against re-entrant continuation injection. agent_end
	// can fire close to a previous SendMessage's downstream agent_end if
	// the model immediately produces another short turn; one outstanding
	// continuation at a time is enough to keep the loop going.
	continuing atomic.Bool
}

// Options configures the Extension. The zero value is fine; Out is optional
// but recommended so /goal commands can echo confirmations to the TUI.
type Options struct {
	// Out is called once per slash command to deliver user-facing text.
	// modu_code TUI plugs its statusbar / scrollback printer in here.
	Out func(string)
}

// New constructs a Goal extension. Pass it into CodingSessionOptions.Extensions.
func New(opts Options) *Extension {
	return &Extension{
		store: NewStore(),
		out:   opts.Out,
	}
}

// Name implements extension.Extension.
func (e *Extension) Name() string { return "goal" }

// Init implements extension.Extension. Wires every part of the /goal
// protocol into the host CodingSession via the ExtensionAPI.
func (e *Extension) Init(api extension.ExtensionAPI) error {
	e.api = api

	// Slash commands. /goal starts a new goal; the rest control lifecycle.
	api.RegisterCommand("goal", "Start a long-horizon goal: /goal <objective>", e.cmdStart)
	api.RegisterCommand("goal-pause", "Pause the active goal (continuation loop halts until resume)", e.cmdPause)
	api.RegisterCommand("goal-resume", "Resume a paused goal and inject one continuation immediately", e.cmdResume)
	api.RegisterCommand("goal-cancel", "Drop the current goal regardless of status", e.cmdCancel)
	api.RegisterCommand("goal-status", "Print the current goal's status", e.cmdStatus)

	// Model-callable tools. update_goal is the only path that completes a
	// goal; get_goal is read-only for the model.
	api.RegisterTool(&updateGoalTool{store: e.store})
	api.RegisterTool(&getGoalTool{store: e.store})

	// The continuation driver: every time the agent finishes a turn, if a
	// goal is still active, inject the continuation prompt as a hidden
	// extension message. The host wires SendMessage → ag.Steer, which
	// triggers the next agent turn naturally.
	api.On(string(agent.EventTypeAgentEnd), e.onAgentEnd)

	return nil
}

// ─── slash command handlers ────────────────────────────────────────────────

func (e *Extension) cmdStart(args string) error {
	args = strings.TrimSpace(args)
	g, err := e.store.Start(args)
	if err != nil {
		return err
	}
	e.tell(fmt.Sprintf("▶ goal %s started: %s\n  Continuation loop is active; the agent will pursue this objective across turns. Use /goal-pause or /goal-cancel to stop.", g.ID[:8], g.Objective))

	// First push: kick off the loop right now even if the user's command
	// didn't itself trigger an agent turn. SendMessage → Steer drives a
	// fresh turn whose follow-up agent_end will keep the loop running.
	if err := e.api.SendMessage(BuildContinuationPrompt(g)); err != nil {
		return fmt.Errorf("inject initial continuation: %w", err)
	}
	return nil
}

func (e *Extension) cmdPause(string) error {
	g, err := e.store.Pause()
	if err != nil {
		return err
	}
	e.tell(fmt.Sprintf("⏸ goal %s paused; continuation loop halted. /goal-resume to continue.", g.ID[:8]))
	return nil
}

func (e *Extension) cmdResume(string) error {
	g, err := e.store.Resume()
	if err != nil {
		return err
	}
	e.tell(fmt.Sprintf("▶ goal %s resumed.", g.ID[:8]))
	// Immediately re-arm the loop with one continuation, matching pi-goal.
	if err := e.api.SendMessage(BuildContinuationPrompt(g)); err != nil {
		return fmt.Errorf("inject resume continuation: %w", err)
	}
	return nil
}

func (e *Extension) cmdCancel(string) error {
	g, err := e.store.Cancel()
	if err != nil {
		return err
	}
	e.tell(fmt.Sprintf("■ goal %s cancelled.", g.ID[:8]))
	return nil
}

func (e *Extension) cmdStatus(string) error {
	e.tell(e.store.Summary())
	return nil
}

// ─── continuation driver ───────────────────────────────────────────────────

// onAgentEnd is the loop heartbeat. Pi-goal queues a microtask after every
// agent_end; we go one step simpler — the modu extension runtime already
// calls handlers on the agent event goroutine sequentially, so dispatching
// SendMessage inline is safe. The atomic guard keeps a stray re-entry from
// stacking two continuations (which would double-prompt the next turn).
func (e *Extension) onAgentEnd(_ agent.AgentEvent) {
	g, ok := e.store.Current()
	if !ok || g.Status != StatusActive {
		return
	}
	if !e.continuing.CompareAndSwap(false, true) {
		return
	}
	defer e.continuing.Store(false)

	if err := e.api.SendMessage(BuildContinuationPrompt(g)); err != nil {
		e.tell(fmt.Sprintf("goal: continuation inject failed: %v (loop will halt)", err))
	}
}

// tell pushes a line to the user-facing output, if one is configured.
// Slash command callers also receive errors via the normal handler return
// path, so silent extensions still surface failures.
func (e *Extension) tell(msg string) {
	if e.out != nil {
		e.out(msg)
	}
}
