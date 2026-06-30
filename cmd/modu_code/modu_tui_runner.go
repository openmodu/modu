package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/slash"
	"github.com/openmodu/modu/pkg/types"
)

const moduTUITerminalStatusTTL = 10 * time.Second
const moduTUIContextCompactDivider = "------------- context compact ------------------"

func runModuTUI(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions) (err error) {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		_ = n
	}

	initial := messagesFromSessionTranscript(session)
	if notice := strings.TrimSpace(opts.StartupNotice); notice != "" {
		initial = append(initial, modutui.Message{
			Role:         modutui.RoleAssistant,
			Text:         notice,
			Preformatted: true,
		})
	}
	var program *tea.Program
	var programMu sync.RWMutex
	defer func() {
		if r := recover(); r != nil {
			programMu.RLock()
			p := program
			programMu.RUnlock()
			if p != nil {
				func() {
					defer func() { _ = recover() }()
					p.Kill()
				}()
			}
			restoreModuTUITerminal()
			fmt.Fprintf(os.Stderr, "modu_code TUI panic: %v\n%s\n", r, debug.Stack())
			err = fmt.Errorf("TUI panic: %v", r)
		}
	}()
	var promptMu sync.Mutex
	var currentCancel context.CancelFunc
	var currentPromptID int
	var nextPromptID int
	var continueQueuedAfterCancel bool
	var foregroundMu sync.Mutex
	var foregroundRuns int
	var workflowPanelMu sync.Mutex
	var workflowPanelRef moduTUIWorkflowPanelRef
	var workflowPanelActive bool
	var workflowPanelFingerprint string
	workflowFingerprint := func() string {
		return moduTUIWorkflowRuntimeFingerprint(session)
	}
	rememberWorkflowPanel := func(panel modutui.Panel) {
		ref, ok := moduTUIWorkflowPanelRefFromPanel(panel)
		workflowPanelMu.Lock()
		defer workflowPanelMu.Unlock()
		if !ok {
			workflowPanelActive = false
			workflowPanelFingerprint = ""
			return
		}
		workflowPanelRef = ref
		workflowPanelActive = true
		workflowPanelFingerprint = workflowFingerprint()
	}
	forgetWorkflowPanel := func(panelID string) {
		workflowPanelMu.Lock()
		defer workflowPanelMu.Unlock()
		if !workflowPanelActive {
			return
		}
		if panelID == "" || panelID == workflowPanelRef.PanelID {
			workflowPanelActive = false
			workflowPanelFingerprint = ""
		}
	}
	rawSend := func(msg tea.Msg) {
		programMu.RLock()
		p := program
		programMu.RUnlock()
		if p != nil {
			p.Send(msg)
		}
	}
	send := func(msg tea.Msg) {
		switch typed := msg.(type) {
		case modutui.SetPanelMsg:
			rememberWorkflowPanel(typed.Panel)
		case modutui.RefreshPanelMsg:
			rememberWorkflowPanel(typed.Panel)
		case modutui.ClearPanelMsg:
			forgetWorkflowPanel(typed.ID)
		}
		rawSend(msg)
	}
	refreshWorkflowPanel := func() {
		workflowPanelMu.Lock()
		if !workflowPanelActive {
			workflowPanelMu.Unlock()
			return
		}
		ref := workflowPanelRef
		lastFingerprint := workflowPanelFingerprint
		workflowPanelMu.Unlock()

		fingerprint := workflowFingerprint()
		if fingerprint == lastFingerprint {
			return
		}
		panel, ok := ref.Panel(session)
		if !ok {
			return
		}
		workflowPanelMu.Lock()
		shouldSend := false
		if workflowPanelActive && workflowPanelRef == ref {
			workflowPanelFingerprint = fingerprint
			shouldSend = true
		}
		workflowPanelMu.Unlock()
		if !shouldSend {
			return
		}
		rawSend(modutui.RefreshPanelMsg{Panel: panel})
	}
	reportAsyncPanic := func(name string, recovered any) {
		send(modutui.AppendMessageMsg{Message: modutui.Message{
			Role:         modutui.RoleAssistant,
			Text:         fmt.Sprintf("internal panic in %s: %v", name, recovered),
			Preformatted: true,
		}})
		send(modutui.SetStatusMsg{Status: "internal panic", TransientFor: moduTUITerminalStatusTTL})
	}
	safeGo := func(name string, fn func()) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					reportAsyncPanic(name, r)
				}
			}()
			fn()
		}()
	}
	configWizard := newModuTUIConfigWizard(opts.CommandHooks, send)
	markForegroundRunStart := func() {
		foregroundMu.Lock()
		foregroundRuns++
		foregroundMu.Unlock()
	}
	markForegroundRunDone := func() bool {
		foregroundMu.Lock()
		if foregroundRuns > 0 {
			foregroundRuns--
		}
		idle := foregroundRuns == 0
		foregroundMu.Unlock()
		return idle
	}
	isForegroundRunActive := func() bool {
		foregroundMu.Lock()
		active := foregroundRuns > 0
		foregroundMu.Unlock()
		return active
	}
	durationTracker := newModuTUIAgentDurationTracker(time.Now, func(msg modutui.Message) {
		send(modutui.AppendMessageMsg{Message: msg})
	})
	sendFooter := func() {}

	if !noApprove {
		session.SetPrompter(&moduTUIPrompter{ctx: ctx, send: send})
	}
	historyFile := session.InputHistoryFile()
	inputHistory, _ := loadModuTUIInputHistory(historyFile)

	isPromptActive := func() bool {
		promptMu.Lock()
		defer promptMu.Unlock()
		return currentCancel != nil
	}
	interruptPrompt := func() {
		// Invoked synchronously from the Model.Update loop. Calling send
		// (program.Send) or session.Abort here on the event-loop goroutine can
		// deadlock Bubble Tea: Send blocks when the message channel is full —
		// which happens readily over SSH where queued mouse events fill it — and
		// the loop is itself waiting for Update to return. Run it off-loop.
		safeGo("interrupt", func() {
			promptMu.Lock()
			cancel := currentCancel
			continueQueuedAfterCancel = false
			promptMu.Unlock()
			if cancel != nil {
				cancel()
			}
			session.Abort()
			session.AbortBash()
			send(modutui.SetStatusMsg{Status: "interrupting"})
		})
	}
	runAgentLoop := func(run func(context.Context) error) {
		markForegroundRunStart()
		safeGo("agent loop", func() {
			send(modutui.SetBusyMsg{Busy: true})
			send(modutui.SetStatusMsg{Status: "running"})
			defer func() {
				if markForegroundRunDone() {
					send(modutui.SetBusyMsg{Busy: false})
				}
			}()

			nextRun := run
			for {
				promptCtx, cancel := context.WithCancel(ctx)
				started := time.Now()
				promptMu.Lock()
				nextPromptID++
				promptID := nextPromptID
				currentPromptID = promptID
				currentCancel = cancel
				promptMu.Unlock()
				err := nextRun(promptCtx)

				promptMu.Lock()
				if currentPromptID == promptID {
					currentCancel = nil
					currentPromptID = 0
				}
				steeringCancel := errors.Is(err, context.Canceled) && continueQueuedAfterCancel
				continueQueuedAfterCancel = false
				promptMu.Unlock()
				cancel()

				ag := session.GetAgent()
				shouldContinue := ag != nil && ag.HasQueuedMessages() && (err == nil || steeringCancel)
				if shouldContinue {
					send(modutui.SetStatusMsg{Status: "running"})
					nextRun = session.Continue
					continue
				}

				if err != nil && !errors.Is(err, context.Canceled) {
					send(modutui.AppendMessageMsg{Message: modutui.Message{
						Role: modutui.RoleAssistant,
						Text: "error: " + err.Error(),
					}})
					send(modutui.SetStatusMsg{Status: "error", TransientFor: moduTUITerminalStatusTTL})
				} else if errors.Is(err, context.Canceled) {
					send(modutui.SetStatusMsg{Status: "interrupted", TransientFor: moduTUITerminalStatusTTL})
				} else {
					send(modutui.SetStatusMsg{
						Status:       "✓ Completed " + formatModuTUIActivityDuration(time.Since(started)),
						TransientFor: moduTUITerminalStatusTTL,
					})
				}
				sendFooter()
				return
			}
		})
	}
	runPrompt := func(text string) {
		runAgentLoop(func(ctx context.Context) error {
			return session.Prompt(ctx, text)
		})
	}
	// Drive hidden extension turns (goal continuations injected while idle)
	// through the foreground loop instead of a detached background goroutine, so
	// the status line reflects the running agent and ESC can interrupt it.
	session.SetBackgroundPromptDriver(func(run func(context.Context) error) bool {
		runAgentLoop(run)
		return true
	})
	queueFollowUp := func(text string, requireActive bool) {
		// Same hazard as interruptPrompt/queueSteer: invoked synchronously from
		// the Model.Update loop (submit / slash hooks), where send (program.Send)
		// blocks the event loop when the message channel is full — readily so
		// over SSH. Run it off-loop.
		safeGo("follow-up queue", func() {
			if requireActive && !isPromptActive() {
				send(modutui.SetStatusMsg{Status: "no active task to followup"})
				return
			}
			if !isPromptActive() {
				runPrompt(text)
				return
			}
			session.FollowUp(text)
			send(modutui.SetStatusMsg{Status: "queued"})
		})
	}
	queueSteer := func(text string, requireActive bool) {
		// Same hazard as interruptPrompt: this runs synchronously from the
		// Model.Update loop (submit / slash hooks), and session.Abort plus send
		// (program.Send) would block the event loop when the message channel is
		// full — readily so over SSH. Run it off-loop. Ordering is preserved
		// within the goroutine: Steer enqueues and continueQueuedAfterCancel is
		// set before cancel(), all before runAgentLoop reads it post-cancel.
		safeGo("steer queue", func() {
			if requireActive && !isPromptActive() {
				send(modutui.SetStatusMsg{Status: "no active task to steer"})
				return
			}
			if !isPromptActive() {
				runPrompt(text)
				return
			}
			session.Steer(text)
			promptMu.Lock()
			cancel := currentCancel
			continueQueuedAfterCancel = true
			promptMu.Unlock()
			if cancel != nil {
				cancel()
			}
			session.Abort()
			session.AbortBash()
			send(modutui.SetStatusMsg{Status: "steering"})
		})
	}
	submit := func(ev modutui.SubmitEvent) {
		if configWizard.HandleInput(ctx, ev.Text) {
			return
		}
		switch ev.Kind {
		case modutui.SubmitKindFollowUp:
			queueFollowUp(ev.Text, false)
		case modutui.SubmitKindSteer:
			queueSteer(ev.Text, false)
		default:
			runPrompt(ev.Text)
		}
	}

	width, height := initialTerminalSize(int(os.Stdout.Fd()), 120, 35)
	env := os.Environ()
	mouseDisabled := moduTUIMouseDisabledFromEnv(env)
	arrowKeysScroll := moduTUIArrowKeysScrollFromEnv(env)
	ui := modutui.NewModel(modutui.Options{
		Width:           width,
		Height:          height,
		InitialMessages: initial,
		InputHistory:    inputHistory,
		Todos:           moduTUITodos(session),
		Footer:          moduTUIFooter(session),
		InfoCardLines:   moduTUIInfoCardLines(session, model),
		SlashCommands:   moduTUISlashCommands(session),
		DisableMouse:    mouseDisabled,
		ArrowKeysScroll: arrowKeysScroll,
		Hooks: modutui.Hooks{
			InputHistoryChanged: func(history []string) {
				_ = saveModuTUIInputHistory(historyFile, history)
			},
			SubmitMessage: func(ev modutui.SubmitEvent) {
				submit(ev)
			},
			PanelAction: func(action modutui.PanelAction) {
				if command, runID, agentID, status, ok := moduTUIWorkflowAgentControlAction(action); ok {
					safeGo("workflow agent control action", func() {
						send(modutui.SetStatusMsg{Status: status, TransientFor: moduTUITerminalStatusTTL})
						runModuTUISlash(ctx, command, session, model, opts.CommandHooks, send, isForegroundRunActive, func() {
							configWizard.Start(ctx)
						}, func() {
							runModuTUIModelSelect(ctx, session, send)
						})
						send(modutui.SetPanelMsg{Panel: moduTUIWorkflowAgentPanel(session, runID, agentID)})
					})
					return
				}
				if command, runID, status, ok := moduTUIWorkflowControlAction(action); ok {
					safeGo("workflow control action", func() {
						send(modutui.SetStatusMsg{Status: status, TransientFor: moduTUITerminalStatusTTL})
						runModuTUISlash(ctx, command, session, model, opts.CommandHooks, send, isForegroundRunActive, func() {
							configWizard.Start(ctx)
						}, func() {
							runModuTUIModelSelect(ctx, session, send)
						})
						send(modutui.SetPanelMsg{Panel: moduTUIWorkflowRunDetailPanel(session, runID)})
					})
					return
				}
				if panel, ok := moduTUIWorkflowPanelAction(session, action); ok {
					safeGo("workflow panel action", func() {
						send(modutui.SetPanelMsg{Panel: panel})
					})
					return
				}
				command := strings.TrimSpace(action.Command)
				if command == "" {
					return
				}
				safeGo("panel action", func() {
					runModuTUISlash(ctx, command, session, model, opts.CommandHooks, send, isForegroundRunActive, func() {
						configWizard.Start(ctx)
					}, func() {
						runModuTUIModelSelect(ctx, session, send)
					})
				})
			},
			PanelClosed: func(panelID string) {
				forgetWorkflowPanel(panelID)
			},
			SlashCommand: func(line string) {
				if kind, text, ok := moduTUIQueueCommand(line); ok {
					if strings.TrimSpace(text) == "" {
						send(modutui.SetStatusMsg{Status: "/" + string(kind) + " requires a message"})
						return
					}
					if kind == modutui.SubmitKindSteer {
						queueSteer(text, true)
					} else {
						queueFollowUp(text, true)
					}
					return
				}
				if args, ok := moduTUIConfigArgs(line); ok && strings.TrimSpace(args) == "" {
					safeGo("config wizard", func() { configWizard.Start(ctx) })
					return
				}
				if args, ok := moduTUIModelArgs(line); ok && strings.TrimSpace(args) == "" {
					safeGo("model selector", func() { runModuTUIModelSelect(ctx, session, send) })
					return
				}
				safeGo("slash command", func() {
					runModuTUISlash(ctx, line, session, model, opts.CommandHooks, send, isForegroundRunActive, func() {
						configWizard.Start(ctx)
					}, func() {
						runModuTUIModelSelect(ctx, session, send)
					})
				})
			},
			Interrupt: interruptPrompt,
		},
	})
	sendFooter = func() {
		send(modutui.SetFooterMsg{Footer: moduTUIFooter(session)})
	}

	unsubAgent := session.Subscribe(func(ev types.Event) {
		durationTracker.Handle(ev)
		if panel, status, ok := moduTUIWorkflowPanelFromToolEvent(session, ev); ok {
			if status != "" {
				send(modutui.SetStatusMsg{Status: status, TransientFor: moduTUITerminalStatusTTL})
			}
			if panel.ID != "" {
				send(modutui.SetPanelMsg{Panel: panel})
			}
		}
		for _, msg := range messagesFromAgentEventWithCwd(ev, session.Cwd()) {
			send(modutui.AppendMessageMsg{Message: msg})
		}
		if ev.Type == types.EventTypeToolExecutionEnd {
			send(modutui.SetTodosMsg{Todos: moduTUITodos(session)})
		}
		if ev.Type == types.EventTypeMessageEnd {
			sendFooter()
		}
	})
	defer unsubAgent()

	unsubSession := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		if panel, status, ok := moduTUIWorkflowPanelFromNotify(session, ev); ok {
			if status != "" {
				send(modutui.SetStatusMsg{Status: status, TransientFor: moduTUITerminalStatusTTL})
			}
			if panel.ID != "" {
				send(modutui.SetPanelMsg{Panel: panel})
			}
			sendFooter()
			return
		}
		if msg, ok := messageFromSessionEvent(ev); ok {
			send(modutui.AppendMessageMsg{Message: msg})
		}
		sendFooter()
	})
	defer unsubSession()

	prog := tea.NewProgram(ui, tea.WithContext(ctx), tea.WithWindowSize(width, height))
	programMu.Lock()
	program = prog
	programMu.Unlock()
	refreshDone := make(chan struct{})
	defer close(refreshDone)
	safeGo("workflow panel refresh", func() {
		ticker := time.NewTicker(750 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-refreshDone:
				return
			case <-ticker.C:
				refreshWorkflowPanel()
			}
		}
	})
	safeGo("startup event", func() {
		session.EmitStartupEvent()
		session.EmitExtensionEvent("ui_ready")
	})
	_, err = prog.Run()
	return err
}

func restoreModuTUITerminal() {
	// Bubble Tea normally restores terminal state. This is a last-resort panic
	// backstop for raw mode, mouse tracking, cursor visibility, styling, and the
	// alternate screen buffer.
	fmt.Fprint(os.Stdout, "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1049l\x1b[?25h\x1b[0m\r\n")
}

// moduTUIDurationDebounce is how long the tracker waits after an agent_end
// before declaring a task finished. A goal/extension task spans several
// back-to-back agent rounds (the hidden continuation re-prompts the agent the
// instant a round ends), so without debouncing each round would print its own
// "✓ Completed" line. The window only needs to bridge that near-zero gap.
const moduTUIDurationDebounce = 750 * time.Millisecond

type moduTUIDebounceTimer interface{ Stop() bool }

// moduTUIAgentDurationTracker collapses a multi-round agent task into a single
// completion message reporting the total wall time from the first agent_start
// to the last agent_end. Each agent_end (re)arms a debounce timer; a fresh
// agent_start within the window means the task is still going and cancels it.
type moduTUIAgentDurationTracker struct {
	mu       sync.Mutex
	now      func() time.Time
	emit     func(modutui.Message)
	debounce time.Duration
	schedule func(time.Duration, func()) moduTUIDebounceTimer

	started time.Time
	lastEnd time.Time
	active  bool
	gen     int
	timer   moduTUIDebounceTimer
}

func newModuTUIAgentDurationTracker(now func() time.Time, emit func(modutui.Message)) *moduTUIAgentDurationTracker {
	if now == nil {
		now = time.Now
	}
	t := &moduTUIAgentDurationTracker{now: now, emit: emit, debounce: moduTUIDurationDebounce}
	t.schedule = func(d time.Duration, f func()) moduTUIDebounceTimer {
		return time.AfterFunc(d, f)
	}
	return t
}

func (t *moduTUIAgentDurationTracker) Handle(ev types.Event) {
	switch ev.Type {
	case types.EventTypeAgentStart:
		t.mu.Lock()
		t.gen++ // invalidate any pending finalize: the task is still running
		if t.timer != nil {
			t.timer.Stop()
			t.timer = nil
		}
		if !t.active {
			t.started = t.now()
			t.active = true
		}
		t.mu.Unlock()
	case types.EventTypeAgentEnd:
		t.mu.Lock()
		if !t.active {
			t.mu.Unlock()
			return
		}
		t.lastEnd = t.now()
		gen := t.gen
		if t.timer != nil {
			t.timer.Stop()
		}
		t.timer = t.schedule(t.debounce, func() { t.finalize(gen) })
		t.mu.Unlock()
	}
}

func (t *moduTUIAgentDurationTracker) finalize(gen int) {
	t.mu.Lock()
	if !t.active || gen != t.gen {
		t.mu.Unlock()
		return
	}
	t.gen++
	total := t.lastEnd.Sub(t.started)
	t.active = false
	t.started = time.Time{}
	t.timer = nil
	emit := t.emit
	t.mu.Unlock()
	if emit == nil {
		return
	}
	emit(modutui.Message{
		Role:         modutui.RoleAssistant,
		Text:         "✓ Completed (" + formatModuTUIActivityDuration(total) + ")",
		Preformatted: true,
		Plain:        true,
	})
}

func formatModuTUIActivityDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Round(time.Second).Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if seconds == 0 {
		return fmt.Sprintf("%dmin", minutes)
	}
	return fmt.Sprintf("%dmin %02ds", minutes, seconds)
}

func moduTUIMouseDisabledFromEnv(env []string) bool {
	if mode, ok := envValue(env, "MODU_TUI_MOUSE"); ok {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "1", "true", "yes", "on", "cell", "mouse":
			return false
		case "0", "false", "no", "off", "none", "disabled", "disable":
			return true
		}
	}
	return false
}

func moduTUIArrowKeysScrollFromEnv(env []string) bool {
	return moduTUIMouseDisabledFromEnv(env) || moduTUISSHSessionFromEnv(env)
}

func moduTUISSHSessionFromEnv(env []string) bool {
	return envNonEmpty(env, "SSH_TTY") || envNonEmpty(env, "SSH_CONNECTION") || envNonEmpty(env, "SSH_CLIENT")
}

func envNonEmpty(env []string, key string) bool {
	value, ok := envValue(env, key)
	return ok && strings.TrimSpace(value) != ""
}

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix), true
		}
	}
	return "", false
}

type moduTUISlashPrinter struct {
	lines []string
	clear bool
}

func (p *moduTUISlashPrinter) PrintInfo(s string) {
	p.lines = append(p.lines, s)
}

func (p *moduTUISlashPrinter) PrintError(err error) {
	if err != nil {
		p.lines = append(p.lines, "error: "+err.Error())
	}
}

func (p *moduTUISlashPrinter) PrintSection(title string, lines []string) {
	if strings.TrimSpace(title) != "" {
		p.lines = append(p.lines, title)
	}
	p.lines = append(p.lines, lines...)
}

func (p *moduTUISlashPrinter) ClearScreen() {
	p.clear = true
}

func (p *moduTUISlashPrinter) Text() string {
	return strings.TrimSpace(strings.Join(p.lines, "\n"))
}

func runModuTUISlash(ctx context.Context, line string, session *coding_agent.CodingSession, model *types.Model, hooks CommandHooks, send func(tea.Msg), keepAgentBusy func() bool, startConfigWizard func(), startModelSelect func()) {
	send(modutui.SetBusyMsg{Busy: true})
	send(modutui.SetStatusMsg{Status: moduTUISlashRunningStatus(line)})
	defer func() {
		send(modutui.SetTodosMsg{Todos: moduTUITodos(session)})
		if keepAgentBusy != nil && keepAgentBusy() {
			return
		}
		send(modutui.SetBusyMsg{Busy: false})
		send(modutui.SetStatusMsg{Status: "idle"})
	}()

	if args, ok := moduTUIConfigArgs(line); ok {
		if strings.TrimSpace(args) == "" && startConfigWizard != nil {
			startConfigWizard()
			return
		}
		if hooks.Config == nil {
			send(modutui.AppendMessageMsg{Message: modutui.Message{Role: modutui.RoleAssistant, Text: "config command is not available"}})
			return
		}
		out, err := hooks.Config(args)
		text := strings.TrimSpace(out)
		if err != nil {
			if text != "" {
				text += "\n"
			}
			text += "error: " + err.Error()
		}
		if text == "" {
			text = "config command completed"
		}
		send(modutui.AppendMessageMsg{Message: modutui.Message{Role: modutui.RoleAssistant, Text: text, Preformatted: true}})
		return
	}
	if args, ok := moduTUIModelArgs(line); ok && strings.TrimSpace(args) == "" && startModelSelect != nil {
		startModelSelect()
		return
	}
	if args, ok := moduTUIWorkflowArgs(line); ok && strings.TrimSpace(args) == "" {
		send(modutui.SetPanelMsg{Panel: moduTUIWorkflowCockpitPanel(session)})
		return
	}
	if panel, ok := moduTUIWorkflowPanelFromSlash(session, line); ok {
		send(modutui.SetPanelMsg{Panel: panel})
		return
	}

	printer := &moduTUISlashPrinter{}
	handled, exit := slash.Handle(ctx, line, session, printer, model)
	if handled {
		if printer.clear {
			send(modutui.ClearMessagesMsg{})
		}
		if text := printer.Text(); text != "" {
			send(modutui.AppendMessageMsg{Message: modutui.Message{Role: modutui.RoleAssistant, Text: text, Preformatted: true}})
		}
		if exit {
			send(tea.Quit())
		}
		return
	}
	if isSessionAgentSlash(session, line) {
		if err := session.Prompt(ctx, line); err != nil && err != context.Canceled {
			send(modutui.AppendMessageMsg{Message: modutui.Message{Role: modutui.RoleAssistant, Text: "error: " + err.Error()}})
			send(modutui.SetStatusMsg{Status: "error"})
		}
		return
	}
	send(modutui.AppendMessageMsg{Message: modutui.Message{Role: modutui.RoleAssistant, Text: "unknown command: " + line}})
}

func moduTUIConfigArgs(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "/config" {
		return "", true
	}
	if rest, ok := strings.CutPrefix(line, "/config "); ok {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func moduTUIModelArgs(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "/model" {
		return "", true
	}
	if rest, ok := strings.CutPrefix(line, "/model "); ok {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func moduTUIWorkflowArgs(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "/workflows" {
		return "", true
	}
	if rest, ok := strings.CutPrefix(line, "/workflows "); ok {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

func moduTUIWorkflowPanelFromSlash(session *coding_agent.CodingSession, line string) (modutui.Panel, bool) {
	if session == nil {
		return moduTUIWorkflowPanelFromSlashStates(nil, line)
	}
	return moduTUIWorkflowPanelFromSlashStates(session.ExtensionRuntimeStates(), line)
}

func moduTUIWorkflowPanelFromNotify(session *coding_agent.CodingSession, ev coding_agent.SessionEvent) (modutui.Panel, string, bool) {
	if session == nil {
		return moduTUIWorkflowPanelFromNotifyStates(nil, ev)
	}
	return moduTUIWorkflowPanelFromNotifyStates(session.ExtensionRuntimeStates(), ev)
}

func moduTUIWorkflowPanelFromToolEvent(session *coding_agent.CodingSession, ev types.Event) (modutui.Panel, string, bool) {
	if session == nil {
		return moduTUIWorkflowPanelFromToolEventStates(nil, ev)
	}
	return moduTUIWorkflowPanelFromToolEventStates(session.ExtensionRuntimeStates(), ev)
}

func moduTUIWorkflowPanelFromToolEventStates(states map[string]any, ev types.Event) (modutui.Panel, string, bool) {
	if !strings.EqualFold(ev.ToolName, "workflow") {
		return modutui.Panel{}, "", false
	}
	if ev.Type == types.EventTypeToolExecutionStart {
		return moduTUIWorkflowCockpitPanelFromStates(states), "workflow started", true
	}
	if ev.Type != types.EventTypeToolExecutionEnd || ev.IsError {
		return modutui.Panel{}, "", false
	}
	if runID := moduTUIWorkflowRunIDFromToolResult(ev.Result); runID != "" {
		if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, runID); ok {
			return moduTUIWorkflowRunFollowPanelFromStates(states, run), "workflow started: " + run.ID, true
		}
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", runID), "workflow started: " + runID, true
	}
	output := toolOutputFromResult(ev.ToolName, ev.IsError, ev.Result)
	if strings.Contains(output, " completed with ") {
		if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, "latest"); ok {
			return moduTUIWorkflowRunDetailPanelFromStates(states, run.ID), moduTUIWorkflowNotifyStatus(output), true
		}
		return modutui.Panel{}, moduTUIWorkflowNotifyStatus(output), true
	}
	return modutui.Panel{}, "", false
}

func moduTUIWorkflowRunIDFromToolResult(result any) string {
	switch r := result.(type) {
	case types.ToolResult:
		return moduTUIWorkflowRunIDFromToolDetails(r.Details)
	case *types.ToolResult:
		if r == nil {
			return ""
		}
		return moduTUIWorkflowRunIDFromToolDetails(r.Details)
	default:
		return ""
	}
}

func moduTUIWorkflowRunIDFromToolDetails(details any) string {
	if runID, ok := mapStringValue(details, "runID"); ok && strings.TrimSpace(runID) != "" {
		return strings.TrimSpace(runID)
	}
	if runID, ok := mapStringValue(details, "runId"); ok && strings.TrimSpace(runID) != "" {
		return strings.TrimSpace(runID)
	}
	return ""
}

func moduTUIWorkflowPanelFromNotifyStates(states map[string]any, ev coding_agent.SessionEvent) (modutui.Panel, string, bool) {
	if ev.Type != coding_agent.SessionEventExtensionNotify || ev.ExtensionName != "workflow" {
		return modutui.Panel{}, "", false
	}
	text := strings.TrimSpace(ev.Message)
	if text == "" {
		return modutui.Panel{}, "", false
	}
	if runID := moduTUIWorkflowRunIDFromNotify(text); runID != "" {
		if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, runID); ok {
			return moduTUIWorkflowRunFollowPanelFromStates(states, run), moduTUIWorkflowNotifyStatus(text), true
		}
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", runID), moduTUIWorkflowNotifyStatus(text), true
	}
	if strings.Contains(text, " completed with ") {
		if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, "latest"); ok {
			return moduTUIWorkflowRunDetailPanelFromStates(states, run.ID), moduTUIWorkflowNotifyStatus(text), true
		}
		return modutui.Panel{}, moduTUIWorkflowNotifyStatus(text), true
	}
	if strings.HasPrefix(text, "Stop requested for workflow") ||
		strings.HasPrefix(text, "Pause requested for workflow") ||
		strings.HasPrefix(text, "Restart requested for workflow agent") ||
		strings.HasPrefix(text, "Stop requested for workflow agent") ||
		strings.Contains(text, " status persistence failed") ||
		strings.Contains(text, " stopped:") {
		return modutui.Panel{}, moduTUIWorkflowNotifyStatus(text), true
	}
	return modutui.Panel{}, "", false
}

func moduTUIWorkflowRunIDFromNotify(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"Run: ", "New run: "} {
			if runID, ok := strings.CutPrefix(line, prefix); ok {
				return strings.Fields(runID)[0]
			}
		}
	}
	return ""
}

func moduTUIWorkflowRunFollowPanelFromStates(states map[string]any, run moduTUIWorkflowRun) modutui.Panel {
	if moduTUIWorkflowStatusIsRunning(run.Status) {
		return moduTUIWorkflowFeedPanelFromStates(states, run.ID)
	}
	return moduTUIWorkflowRunDetailPanelFromStates(states, run.ID)
}

func moduTUIWorkflowNotifyStatus(text string) string {
	line := strings.TrimSpace(text)
	if first, _, ok := strings.Cut(line, "\n"); ok {
		line = strings.TrimSpace(first)
	}
	line = strings.TrimPrefix(line, "Workflow ")
	if line == "" {
		return "workflow updated"
	}
	return moduTUITruncate("workflow "+line, 96)
}

func moduTUIWorkflowPanelFromSlashStates(states map[string]any, line string) (modutui.Panel, bool) {
	args, ok := moduTUIWorkflowArgs(line)
	if !ok {
		return modutui.Panel{}, false
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		return moduTUIWorkflowCockpitPanelFromStates(states), true
	}
	switch fields[0] {
	case "list":
		if len(fields) == 1 {
			return moduTUIWorkflowCockpitPanelFromStates(states), true
		}
	case "show":
		if len(fields) == 2 {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1]); ok {
				return moduTUIWorkflowRunDetailPanelFromStates(states, run.ID), true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowRunDetailPanelID, "Workflow Run", fields[1]), true
		}
	case "feed":
		if len(fields) == 2 {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1]); ok {
				return moduTUIWorkflowFeedPanelFromStates(states, run.ID), true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", fields[1]), true
		}
	case "guide":
		if len(fields) == 2 {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1]); ok {
				return moduTUIWorkflowGuidePanelFromStates(states, run.ID), true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowGuidePanelID, "Workflow Guide", fields[1]), true
		}
	case "map":
		if len(fields) == 2 {
			if run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1]); ok {
				return moduTUIWorkflowMapPanelFromStates(states, run.ID), true
			}
			return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowMapPanelID, "Workflow Map", fields[1]), true
		}
	case "agent":
		if len(fields) == 3 {
			run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1])
			if !ok {
				return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowAgentPanelID, "Workflow Agent", fields[1]), true
			}
			agentID, err := strconv.Atoi(fields[2])
			if err != nil || agentID <= 0 {
				return moduTUIWorkflowInvalidAgentPanel(run.ID, fields[2]), true
			}
			return moduTUIWorkflowAgentPanelFromStates(states, run.ID, agentID), true
		}
	case "transcript":
		if len(fields) == 3 {
			run, ok := moduTUIWorkflowRunBySelectorFromStates(states, fields[1])
			if !ok {
				return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowTranscriptPanelID, "Workflow Transcript", fields[1]), true
			}
			agentID, err := strconv.Atoi(fields[2])
			if err != nil || agentID <= 0 {
				return moduTUIWorkflowInvalidAgentPanel(run.ID, fields[2]), true
			}
			return moduTUIWorkflowTranscriptPanelFromStates(states, run.ID, agentID), true
		}
	}
	return modutui.Panel{}, false
}

func moduTUIWorkflowCockpitText(session *coding_agent.CodingSession) string {
	if session == nil {
		return moduTUIWorkflowCockpitTextFromStates(nil)
	}
	return moduTUIWorkflowCockpitTextFromStates(session.ExtensionRuntimeStates())
}

const (
	moduTUIWorkflowCockpitPanelID          = "workflow-cockpit"
	moduTUIWorkflowRunDetailPanelID        = "workflow-run-detail"
	moduTUIWorkflowFeedPanelID             = "workflow-feed"
	moduTUIWorkflowGuidePanelID            = "workflow-guide"
	moduTUIWorkflowMapPanelID              = "workflow-map"
	moduTUIWorkflowPhasePanelID            = "workflow-phase"
	moduTUIWorkflowResultPanelID           = "workflow-result"
	moduTUIWorkflowScriptPanelID           = "workflow-script"
	moduTUIWorkflowAgentsPanelID           = "workflow-agents"
	moduTUIWorkflowAgentPanelID            = "workflow-agent"
	moduTUIWorkflowTranscriptPanelID       = "workflow-transcript"
	moduTUIWorkflowPanelBackCommand        = "workflow-panel:back"
	moduTUIWorkflowPanelDetailPrefix       = "workflow-panel:detail:"
	moduTUIWorkflowPanelFeedPrefix         = "workflow-panel:feed:"
	moduTUIWorkflowPanelGuidePrefix        = "workflow-panel:guide:"
	moduTUIWorkflowPanelMapPrefix          = "workflow-panel:map:"
	moduTUIWorkflowPanelResultPrefix       = "workflow-panel:result:"
	moduTUIWorkflowPanelScriptPrefix       = "workflow-panel:script:"
	moduTUIWorkflowPanelAgentsPrefix       = "workflow-panel:agents:"
	moduTUIWorkflowPanelAgentPrefix        = "workflow-panel:agent:"
	moduTUIWorkflowPanelPhasePrefix        = "workflow-panel:phase:"
	moduTUIWorkflowPanelTranscriptPrefix   = "workflow-panel:transcript:"
	moduTUIWorkflowPanelControlPrefix      = "workflow-panel:control:"
	moduTUIWorkflowPanelAgentControlPrefix = "workflow-panel:agent-control:"
	moduTUIWorkflowArtifactLineLimit       = 200
)

type moduTUIWorkflowPanelRef struct {
	PanelID string
	RunID   string
	Phase   string
	AgentID int
}

func (ref moduTUIWorkflowPanelRef) Panel(session *coding_agent.CodingSession) (modutui.Panel, bool) {
	switch ref.PanelID {
	case moduTUIWorkflowCockpitPanelID:
		return moduTUIWorkflowCockpitPanel(session), true
	case moduTUIWorkflowRunDetailPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowRunDetailPanel(session, ref.RunID), true
	case moduTUIWorkflowFeedPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowFeedPanel(session, ref.RunID), true
	case moduTUIWorkflowGuidePanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowGuidePanel(session, ref.RunID), true
	case moduTUIWorkflowMapPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowMapPanel(session, ref.RunID), true
	case moduTUIWorkflowAgentsPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowAgentsPanel(session, ref.RunID), true
	case moduTUIWorkflowPhasePanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowPhasePanel(session, ref.RunID, ref.Phase), true
	case moduTUIWorkflowAgentPanelID:
		if ref.RunID == "" || ref.AgentID <= 0 {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowAgentPanel(session, ref.RunID, ref.AgentID), true
	case moduTUIWorkflowTranscriptPanelID:
		if ref.RunID == "" || ref.AgentID <= 0 {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowTranscriptPanel(session, ref.RunID, ref.AgentID), true
	case moduTUIWorkflowResultPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowResultPanel(session, ref.RunID), true
	case moduTUIWorkflowScriptPanelID:
		if ref.RunID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowScriptPanel(session, ref.RunID), true
	default:
		return modutui.Panel{}, false
	}
}

func moduTUIWorkflowPanelRefFromPanel(panel modutui.Panel) (moduTUIWorkflowPanelRef, bool) {
	ref := moduTUIWorkflowPanelRef{PanelID: strings.TrimSpace(panel.ID)}
	switch ref.PanelID {
	case moduTUIWorkflowCockpitPanelID:
		return ref, true
	case moduTUIWorkflowRunDetailPanelID:
		if runID := moduTUIWorkflowRunIDFromPanelRows(panel.Rows); runID != "" {
			ref.RunID = runID
			return ref, true
		}
	case moduTUIWorkflowFeedPanelID:
		if runID := moduTUIWorkflowRunIDFromPanelRows(panel.Rows); runID != "" {
			ref.RunID = runID
			return ref, true
		}
	case moduTUIWorkflowGuidePanelID:
		if runID := moduTUIWorkflowRunIDFromPanelRows(panel.Rows); runID != "" {
			ref.RunID = runID
			return ref, true
		}
	case moduTUIWorkflowMapPanelID:
		if runID := moduTUIWorkflowRunIDFromPanelRows(panel.Rows); runID != "" {
			ref.RunID = runID
			return ref, true
		}
	case moduTUIWorkflowAgentsPanelID:
		if runID := moduTUIWorkflowRunIDFromPanelRows(panel.Rows); runID != "" {
			ref.RunID = runID
			return ref, true
		}
	case moduTUIWorkflowPhasePanelID:
		if runID, phase, ok := moduTUIWorkflowPhaseRefFromPanelRows(panel.Rows); ok {
			ref.RunID = runID
			ref.Phase = phase
			return ref, true
		}
	case moduTUIWorkflowAgentPanelID, moduTUIWorkflowTranscriptPanelID:
		if runID, agentID, ok := moduTUIWorkflowAgentRefFromPanelRows(panel.Rows); ok {
			ref.RunID = runID
			ref.AgentID = agentID
			return ref, true
		}
	case moduTUIWorkflowResultPanelID, moduTUIWorkflowScriptPanelID:
		if runID := moduTUIWorkflowRunIDFromPanelRows(panel.Rows); runID != "" {
			ref.RunID = runID
			return ref, true
		}
	}
	return moduTUIWorkflowPanelRef{}, false
}

func moduTUIWorkflowRunIDFromPanelRows(rows []modutui.PanelRow) string {
	for _, row := range rows {
		command := strings.TrimSpace(row.Command)
		for _, prefix := range []string{
			moduTUIWorkflowPanelDetailPrefix,
			moduTUIWorkflowPanelFeedPrefix,
			moduTUIWorkflowPanelGuidePrefix,
			moduTUIWorkflowPanelMapPrefix,
			moduTUIWorkflowPanelResultPrefix,
			moduTUIWorkflowPanelScriptPrefix,
			moduTUIWorkflowPanelAgentsPrefix,
		} {
			if runID, ok := strings.CutPrefix(command, prefix); ok && strings.TrimSpace(runID) != "" {
				return strings.TrimSpace(runID)
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix); ok {
			runID, _, hasPhase := strings.Cut(rest, ":")
			if hasPhase && strings.TrimSpace(runID) != "" {
				return strings.TrimSpace(runID)
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelControlPrefix); ok {
			_, runID, hasRunID := strings.Cut(rest, ":")
			if hasRunID && strings.TrimSpace(runID) != "" {
				return strings.TrimSpace(runID)
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, _, hasAgentID := strings.Cut(rest, ":")
			if hasAgentID && strings.TrimSpace(runID) != "" {
				return strings.TrimSpace(runID)
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelTranscriptPrefix); ok {
			runID, _, hasAgentID := strings.Cut(rest, ":")
			if hasAgentID && strings.TrimSpace(runID) != "" {
				return strings.TrimSpace(runID)
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentControlPrefix); ok {
			_, tail, hasVerb := strings.Cut(rest, ":")
			if !hasVerb {
				continue
			}
			runID, _, hasAgentID := strings.Cut(tail, ":")
			if hasAgentID && strings.TrimSpace(runID) != "" {
				return strings.TrimSpace(runID)
			}
		}
	}
	return ""
}

func moduTUIWorkflowAgentRefFromPanelRows(rows []modutui.PanelRow) (string, int, bool) {
	for _, row := range rows {
		command := strings.TrimSpace(row.Command)
		for _, prefix := range []string{moduTUIWorkflowPanelAgentPrefix, moduTUIWorkflowPanelTranscriptPrefix} {
			rest, ok := strings.CutPrefix(command, prefix)
			if !ok {
				continue
			}
			runID, agentIDText, hasAgentID := strings.Cut(rest, ":")
			if !hasAgentID || strings.TrimSpace(runID) == "" {
				continue
			}
			agentID, err := strconv.Atoi(strings.TrimSpace(agentIDText))
			if err != nil || agentID <= 0 {
				continue
			}
			return strings.TrimSpace(runID), agentID, true
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentControlPrefix); ok {
			_, tail, hasVerb := strings.Cut(rest, ":")
			if !hasVerb {
				continue
			}
			runID, agentIDText, hasAgentID := strings.Cut(tail, ":")
			if !hasAgentID || strings.TrimSpace(runID) == "" {
				continue
			}
			agentID, err := strconv.Atoi(strings.TrimSpace(agentIDText))
			if err != nil || agentID <= 0 {
				continue
			}
			return strings.TrimSpace(runID), agentID, true
		}
	}
	return "", 0, false
}

func moduTUIWorkflowPhaseRefFromPanelRows(rows []modutui.PanelRow) (string, string, bool) {
	for _, row := range rows {
		command := strings.TrimSpace(row.Command)
		rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix)
		if !ok {
			if runID, detailOK := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); detailOK && strings.TrimSpace(runID) != "" {
				return strings.TrimSpace(runID), row.Value, true
			}
			continue
		}
		runID, phase, hasPhase := strings.Cut(rest, ":")
		if !hasPhase || strings.TrimSpace(runID) == "" {
			continue
		}
		return strings.TrimSpace(runID), phase, true
	}
	return "", "", false
}

func moduTUIWorkflowRuntimeFingerprint(session *coding_agent.CodingSession) string {
	if session == nil {
		return ""
	}
	state, ok := moduTUIWorkflowState(session.ExtensionRuntimeStates())
	if !ok {
		return ""
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Sprint(state)
	}
	return string(data)
}

func moduTUIWorkflowControlAction(action modutui.PanelAction) (command, runID, status string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(action.Command), moduTUIWorkflowPanelControlPrefix)
	if !ok {
		return "", "", "", false
	}
	verb, runID, ok := strings.Cut(rest, ":")
	if !ok {
		return "", "", "", false
	}
	verb = strings.TrimSpace(verb)
	runID = strings.TrimSpace(runID)
	if verb == "" || runID == "" {
		return "", "", "", false
	}
	switch verb {
	case "pause", "stop", "resume", "restart":
		return "/workflows " + verb + " " + runID, runID, "workflow " + verb + " requested", true
	default:
		return "", "", "", false
	}
}

func moduTUIWorkflowAgentControlAction(action modutui.PanelAction) (command, runID string, agentID int, status string, ok bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(action.Command), moduTUIWorkflowPanelAgentControlPrefix)
	if !ok {
		return "", "", 0, "", false
	}
	verb, tail, ok := strings.Cut(rest, ":")
	if !ok {
		return "", "", 0, "", false
	}
	runID, agentIDText, ok := strings.Cut(tail, ":")
	if !ok {
		return "", "", 0, "", false
	}
	verb = strings.TrimSpace(verb)
	runID = strings.TrimSpace(runID)
	agentID, err := strconv.Atoi(strings.TrimSpace(agentIDText))
	if verb == "" || runID == "" || err != nil || agentID <= 0 {
		return "", "", 0, "", false
	}
	switch verb {
	case "stop":
		return "/workflows agent-stop " + runID + " " + strconv.Itoa(agentID), runID, agentID, "workflow agent stop requested", true
	case "restart":
		return "/workflows agent-restart " + runID + " " + strconv.Itoa(agentID), runID, agentID, "workflow agent restart requested", true
	default:
		return "", "", 0, "", false
	}
}

func moduTUIWorkflowPanelAction(session *coding_agent.CodingSession, action modutui.PanelAction) (modutui.Panel, bool) {
	switch action.PanelID {
	case moduTUIWorkflowCockpitPanelID:
		command := strings.TrimSpace(action.Command)
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		runID := strings.TrimSpace(action.Row.Value)
		if runID == "" {
			return modutui.Panel{}, false
		}
		return moduTUIWorkflowRunDetailPanel(session, runID), true
	case moduTUIWorkflowRunDetailPanelID:
		if strings.TrimSpace(action.Command) == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
		command := strings.TrimSpace(action.Command)
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelResultPrefix); ok {
			return moduTUIWorkflowResultPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelScriptPrefix); ok {
			return moduTUIWorkflowScriptPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix); ok {
			runID, phase, hasPhase := strings.Cut(rest, ":")
			if hasPhase {
				return moduTUIWorkflowPhasePanel(session, runID, phase), true
			}
		}
	case moduTUIWorkflowAgentsPanelID:
		command := strings.TrimSpace(action.Command)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if command == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
	case moduTUIWorkflowPhasePanelID:
		command := strings.TrimSpace(action.Command)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if command == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
	case moduTUIWorkflowFeedPanelID:
		command := strings.TrimSpace(action.Command)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix); ok {
			runID, phase, hasPhase := strings.Cut(rest, ":")
			if hasPhase {
				return moduTUIWorkflowPhasePanel(session, runID, phase), true
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if command == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
	case moduTUIWorkflowResultPanelID, moduTUIWorkflowScriptPanelID, moduTUIWorkflowAgentPanelID, moduTUIWorkflowMapPanelID, moduTUIWorkflowGuidePanelID:
		command := strings.TrimSpace(action.Command)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelPhasePrefix); ok {
			runID, phase, hasPhase := strings.Cut(rest, ":")
			if hasPhase {
				return moduTUIWorkflowPhasePanel(session, runID, phase), true
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelTranscriptPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowTranscriptPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelResultPrefix); ok {
			return moduTUIWorkflowResultPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelScriptPrefix); ok {
			return moduTUIWorkflowScriptPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if command == moduTUIWorkflowPanelBackCommand {
			return moduTUIWorkflowCockpitPanel(session), true
		}
	case moduTUIWorkflowTranscriptPanelID:
		command := strings.TrimSpace(action.Command)
		if rest, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentPrefix); ok {
			runID, agentIDText, ok := strings.Cut(rest, ":")
			if ok {
				agentID, _ := strconv.Atoi(agentIDText)
				return moduTUIWorkflowAgentPanel(session, runID, agentID), true
			}
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelAgentsPrefix); ok {
			return moduTUIWorkflowAgentsPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelFeedPrefix); ok {
			return moduTUIWorkflowFeedPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelGuidePrefix); ok {
			return moduTUIWorkflowGuidePanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelMapPrefix); ok {
			return moduTUIWorkflowMapPanel(session, runID), true
		}
		if runID, ok := strings.CutPrefix(command, moduTUIWorkflowPanelDetailPrefix); ok {
			return moduTUIWorkflowRunDetailPanel(session, runID), true
		}
	}
	return modutui.Panel{}, false
}

func moduTUIWorkflowCockpitPanel(session *coding_agent.CodingSession) modutui.Panel {
	text := moduTUIWorkflowCockpitText(session)
	states := map[string]any(nil)
	if session != nil {
		states = session.ExtensionRuntimeStates()
	}
	return moduTUIWorkflowCockpitPanelFromStatesWithText(states, text)
}

func moduTUIWorkflowCockpitPanelFromStates(states map[string]any) modutui.Panel {
	return moduTUIWorkflowCockpitPanelFromStatesWithText(states, moduTUIWorkflowCockpitTextFromStates(states))
}

func moduTUIWorkflowCockpitPanelFromStatesWithText(states map[string]any, text string) modutui.Panel {
	lines := strings.Split(text, "\n")
	title := "Workflow Cockpit"
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == title {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	subtitle := moduTUIWorkflowCockpitSubtitleFromStates(states)
	rows := moduTUIWorkflowCockpitRowsFromStates(states)
	shortcuts := moduTUIWorkflowCockpitShortcutsFromStates(states)
	return modutui.Panel{
		ID:        moduTUIWorkflowCockpitPanelID,
		Title:     title,
		Subtitle:  subtitle,
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowCockpitSelectedRow(states, rows),
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select run  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowRunDetailPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowRunDetailPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowRunDetailPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowRunDetailPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return modutui.Panel{
			ID:       moduTUIWorkflowRunDetailPanelID,
			Title:    "Workflow Run",
			Subtitle: "run not found: " + strings.TrimSpace(runID),
			Lines: []string{
				"Run not found in workflow runtime state.",
				"Use /workflows list to refresh persisted runs.",
			},
			Rows: []modutui.PanelRow{{
				Label:   "Back to workflow runs",
				Command: moduTUIWorkflowPanelBackCommand,
			}},
			Footer: "[enter] back  [esc/q] close",
		}
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	progress := "no agent progress"
	if run.AgentCount > 0 {
		progress = fmt.Sprintf("%d/%d done, %d running, %d errors", run.DoneCount, run.AgentCount, run.RunningAgentCount, run.ErrorCount)
	}
	var lines []string
	lines = append(lines, "summary")
	lines = append(lines, "  id: "+run.ID)
	lines = append(lines, "  status: "+run.Status)
	lines = append(lines, "  progress: "+progress)
	if run.CurrentPhase != "" {
		lines = append(lines, "  current phase: "+run.CurrentPhase)
	}
	if run.DurationMs > 0 {
		lines = append(lines, "  duration: "+formatModuTUIActivityDuration(time.Duration(run.DurationMs)*time.Millisecond))
	}
	if run.ErrorCount > 0 {
		lines = append(lines, fmt.Sprintf("  errors: %d", run.ErrorCount))
	}
	if board := moduTUIWorkflowRunBoardLines(run); len(board) > 0 {
		lines = append(lines, "", "board")
		lines = append(lines, board...)
	}
	lines = append(lines, "", "flow")
	lines = append(lines, moduTUIWorkflowRunFlowLines(run)...)
	if updates := moduTUIWorkflowRunUpdateLines(run); len(updates) > 0 {
		lines = append(lines, "", "updates")
		lines = append(lines, updates...)
	}
	if timeline := moduTUIWorkflowRunTimelineLines(run); len(timeline) > 0 {
		lines = append(lines, "", "timeline")
		lines = append(lines, timeline...)
	}
	lines = append(lines, "", "actions")
	if controlRows := moduTUIWorkflowControlRows(run); len(controlRows) > 0 {
		controlLabels := make([]string, 0, len(controlRows))
		for _, row := range controlRows {
			controlLabels = append(controlLabels, row.Label)
		}
		lines = append(lines, "  "+strings.Join(controlLabels, ", "))
	}
	lines = append(lines, "  Enter Guide to understand the run views")
	lines = append(lines, "  Enter Map to inspect the full phase and agent tree")
	lines = append(lines, "  Enter Agents to inspect per-agent work")
	lines = append(lines, "  Enter Phase rows to inspect one orchestration stage")
	lines = append(lines, "  Enter Result to inspect final output")
	lines = append(lines, "  Enter Script to inspect workflow definition")
	lines = append(lines, "  Enter Back to workflow runs")
	lines = append(lines, "  /workflows agent "+run.ID+" <agent-id>")
	lines = append(lines, "  /workflows transcript "+run.ID+" <agent-id>")

	rows := moduTUIWorkflowRunQuickRows(run)
	rows = append(rows, moduTUIWorkflowControlRows(run)...)
	rows = append(rows, moduTUIWorkflowPhaseRows(run)...)
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Agents",
		Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Result",
		Detail:  "final output",
		Command: moduTUIWorkflowPanelResultPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Script",
		Detail:  "workflow definition",
		Command: moduTUIWorkflowPanelScriptPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowRunShortcuts(run),
		moduTUIWorkflowAttentionShortcut(run),
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowRunDetailPanelID,
		Title:     "Workflow Run",
		Subtitle:  fmt.Sprintf("%s [%s] %s", name, run.Status, progress),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowRunDetailSelectedRow(run, rows),
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowFeedPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowFeedPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowFeedPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowFeedPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowFeedPanelID, "Workflow Feed", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := moduTUIWorkflowRunCardLines(run)
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	if board := moduTUIWorkflowRunBoardLines(run); len(board) > 0 {
		lines = append(lines, "board")
		lines = append(lines, board...)
		lines = append(lines, "")
	}
	if lanes := moduTUIWorkflowRunLaneLines(run); len(lanes) > 0 {
		lines = append(lines, "lanes")
		lines = append(lines, lanes...)
		lines = append(lines, "  legend: run active | done complete | err attention | wait queued")
		lines = append(lines, "")
	}
	lines = append(lines, "flow")
	lines = append(lines, moduTUIWorkflowRunFlowLines(run)...)
	if updates := moduTUIWorkflowRunUpdateLines(run); len(updates) > 0 {
		lines = append(lines, "", "updates")
		lines = append(lines, updates...)
	}
	if timeline := moduTUIWorkflowRunTimelineLines(run); len(timeline) > 0 {
		lines = append(lines, "", "timeline")
		lines = append(lines, timeline...)
	}
	if len(lines) == 1 {
		lines = append(lines, "  no workflow progress snapshot yet")
	}
	rows := moduTUIWorkflowRunFocusRows(run)
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "All agents",
		Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowRunShortcuts(run),
		moduTUIWorkflowAttentionShortcut(run),
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "detail", "map", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowFeedPanelID,
		Title:     "Workflow Feed",
		Subtitle:  fmt.Sprintf("%s [%s]", name, run.Status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowRunFocusSelectedRow(rows),
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowGuidePanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowGuidePanelFromStates(nil, runID)
	}
	return moduTUIWorkflowGuidePanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowGuidePanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowGuidePanelID, "Workflow Guide", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{
		"workflow guide",
		"  Feed: live cards, board, lanes, updates, timeline",
		"  Map: full phase and agent tree",
		"  Phase: one orchestration stage",
		"  Agent: status, tools, result/error, transcript",
		"  Result: final workflow output",
		"  Script: generated or resumed workflow script",
		"",
		"current route",
		"  /workflows -> running run -> Feed",
		"  Feed -> Phase/Agent for active work",
		"  Map -> Phase/Agent for structure",
	}
	if phase, ok := moduTUIWorkflowCurrentOrRunningPhase(run); ok {
		lines = append(lines, "", "current phase")
		lines = append(lines, fmt.Sprintf("  %s %d/%d %s",
			moduTUIWorkflowPhaseTitle(phase.Title),
			phase.DoneCount,
			phase.AgentCount,
			moduTUIWorkflowPhaseStatus(phase),
		))
	}
	if agent, ok := moduTUIWorkflowFirstAttentionAgent(run.Agents); ok {
		lines = append(lines, "", "attention")
		lines = append(lines, "  "+moduTUIWorkflowAgentPulse(agent))
		if agent.Error != "" {
			lines = append(lines, "  error: "+moduTUITruncate(agent.Error, 120))
		}
	}
	if agent, ok := moduTUIWorkflowFirstRunningAgent(run.Agents); ok {
		lines = append(lines, "", "active")
		lines = append(lines, "  "+moduTUIWorkflowAgentPulse(agent))
		if agent.PromptPreview != "" {
			lines = append(lines, "  prompt: "+moduTUITruncate(agent.PromptPreview, 120))
		}
	}
	rows := moduTUIWorkflowRunFocusRows(run)
	rows = append(rows, modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Run detail",
		Detail:  "metadata, result, script",
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Result",
		Detail:  "final workflow output",
		Command: moduTUIWorkflowPanelResultPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Script",
		Detail:  "generated workflow script",
		Command: moduTUIWorkflowPanelScriptPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "All agents",
		Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowAttentionShortcut(run),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowGuidePanelID,
		Title:     "Workflow Guide",
		Subtitle:  fmt.Sprintf("%s [%s]", name, run.Status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowRunFocusSelectedRow(rows),
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowRunCardLines(run moduTUIWorkflowRun) []string {
	name := strings.TrimSpace(run.Name)
	if name == "" {
		name = strings.TrimSpace(run.ID)
	}
	if name == "" {
		name = "workflow"
	}
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "unknown"
	}
	progress := "no agent progress"
	if run.AgentCount > 0 {
		progress = fmt.Sprintf("%d/%d done | %d running | %s",
			run.DoneCount,
			run.AgentCount,
			run.RunningAgentCount,
			moduTUIWorkflowCountText(run.ErrorCount, "error"),
		)
	}
	statusLines := []string{name + " [" + status + "]", "progress: " + progress}
	if run.CurrentPhase != "" {
		statusLines = append(statusLines, "current: "+run.CurrentPhase)
	}
	if run.DurationMs > 0 {
		statusLines = append(statusLines, "duration: "+formatModuTUIActivityDuration(time.Duration(run.DurationMs)*time.Millisecond))
	}
	lines := []string{"cards"}
	lines = append(lines, moduTUIWorkflowCardLines("Status", statusLines)...)
	if agent, ok := moduTUIWorkflowFirstAttentionAgent(run.Agents); ok {
		cardLines := []string{moduTUIWorkflowAgentPulse(agent)}
		if agent.Error != "" {
			cardLines = append(cardLines, "error: "+moduTUITruncate(agent.Error, 120))
		}
		lines = append(lines, moduTUIWorkflowCardLines("Attention", cardLines)...)
	}
	if agent, ok := moduTUIWorkflowFirstRunningAgent(run.Agents); ok {
		cardLines := []string{moduTUIWorkflowAgentPulse(agent)}
		if agent.PromptPreview != "" {
			cardLines = append(cardLines, "prompt: "+moduTUITruncate(agent.PromptPreview, 120))
		}
		if len(agent.ToolCalls) > 0 {
			cardLines = append(cardLines, "tools: "+moduTUIWorkflowToolSummary(agent.ToolCalls))
		}
		lines = append(lines, moduTUIWorkflowCardLines("Active", cardLines)...)
	}
	if next := moduTUIWorkflowNextPhaseTitle(run); next != "" {
		lines = append(lines, moduTUIWorkflowCardLines("Next", []string{"phase: " + next})...)
	}
	return lines
}

func moduTUIWorkflowCardLines(title string, body []string) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Card"
	}
	lines := []string{"  +-- " + title}
	for _, line := range body {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, "  | "+line)
	}
	return lines
}

func moduTUIWorkflowCountText(count int, singular string) string {
	singular = strings.TrimSpace(singular)
	if singular == "" {
		singular = "item"
	}
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

func moduTUIWorkflowMapPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowMapPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowMapPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowMapPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowMapPanelID, "Workflow Map", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{"orchestration map"}
	lines = append(lines, moduTUIWorkflowOrchestrationLines(run)...)
	rows := moduTUIWorkflowRunFocusRows(run)
	rows = append(rows, moduTUIWorkflowPhaseRows(run)...)
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "flow, updates, timeline",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "All agents",
		Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowAttentionShortcut(run),
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowMapPanelID,
		Title:     "Workflow Map",
		Subtitle:  fmt.Sprintf("%s [%s]", name, run.Status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowRunFocusSelectedRow(rows),
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowControlRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	control := func(label, detail, verb string) modutui.PanelRow {
		return modutui.PanelRow{
			Label:   label,
			Detail:  detail,
			Command: moduTUIWorkflowPanelControlPrefix + verb + ":" + runID,
		}
	}
	switch strings.ToLower(strings.TrimSpace(run.Status)) {
	case "running":
		return []modutui.PanelRow{
			control("Pause", "request cooperative pause", "pause"),
			control("Stop", "request stop", "stop"),
		}
	case "stopped", "paused":
		return []modutui.PanelRow{
			control("Resume", "continue run", "resume"),
			control("Restart", "start from script", "restart"),
		}
	case "completed", "failed", "error", "cancelled", "canceled":
		return []modutui.PanelRow{
			control("Restart", "start from script", "restart"),
		}
	default:
		return nil
	}
}

func moduTUIWorkflowGuideRow(runID string) modutui.PanelRow {
	return modutui.PanelRow{
		Label:   "Guide",
		Detail:  "view map and navigation",
		Command: moduTUIWorkflowPanelGuidePrefix + strings.TrimSpace(runID),
	}
}

func moduTUIWorkflowArtifactNavigationRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	return []modutui.PanelRow{
		moduTUIWorkflowGuideRow(run.ID),
		{
			Label:   "Execution feed",
			Detail:  "flow, updates, timeline",
			Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
		},
		{
			Label:   "Map",
			Detail:  "phase and agent tree",
			Command: moduTUIWorkflowPanelMapPrefix + run.ID,
		},
		{
			Label:   "All agents",
			Detail:  fmt.Sprintf("%d agent(s)", len(run.Agents)),
			Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
		},
		{
			Label:   "Back to run detail",
			Detail:  run.ID,
			Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
		},
		{
			Label:   "Back to workflow runs",
			Detail:  "return",
			Command: moduTUIWorkflowPanelBackCommand,
		},
	}
}

func moduTUIWorkflowRunShortcuts(run moduTUIWorkflowRun) []modutui.PanelShortcut {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	shortcut := func(key, label, verb string) modutui.PanelShortcut {
		return modutui.PanelShortcut{
			Key:     key,
			Label:   label,
			Command: moduTUIWorkflowPanelControlPrefix + verb + ":" + runID,
		}
	}
	switch strings.ToLower(strings.TrimSpace(run.Status)) {
	case "running":
		return []modutui.PanelShortcut{
			shortcut("p", "Pause", "pause"),
			shortcut("x", "Stop", "stop"),
		}
	case "stopped", "paused":
		return []modutui.PanelShortcut{
			shortcut("p", "Resume", "resume"),
			shortcut("r", "Restart", "restart"),
		}
	case "completed", "failed", "error", "cancelled", "canceled":
		return []modutui.PanelShortcut{
			shortcut("r", "Restart", "restart"),
		}
	default:
		return nil
	}
}

func moduTUIWorkflowNavigationShortcuts(runID string, views ...string) []modutui.PanelShortcut {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	shortcuts := make([]modutui.PanelShortcut, 0, len(views))
	for _, view := range views {
		switch strings.TrimSpace(view) {
		case "feed":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "f", Label: "Feed", Command: moduTUIWorkflowPanelFeedPrefix + runID})
		case "guide":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "?", Label: "Guide", Command: moduTUIWorkflowPanelGuidePrefix + runID})
		case "detail":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "d", Label: "Detail", Command: moduTUIWorkflowPanelDetailPrefix + runID})
		case "map":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "m", Label: "Map", Command: moduTUIWorkflowPanelMapPrefix + runID})
		case "agents":
			shortcuts = append(shortcuts, modutui.PanelShortcut{Key: "a", Label: "Agents", Command: moduTUIWorkflowPanelAgentsPrefix + runID})
		}
	}
	return shortcuts
}

func moduTUIWorkflowGuideShortcut(runID string) []modutui.PanelShortcut {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil
	}
	return moduTUIWorkflowNavigationShortcuts(runID, "guide")
}

func moduTUIWorkflowAttentionShortcut(run moduTUIWorkflowRun) []modutui.PanelShortcut {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	agent, ok := moduTUIWorkflowFirstAttentionAgent(run.Agents)
	if !ok {
		return nil
	}
	return []modutui.PanelShortcut{{
		Key:     "!",
		Label:   "Attention",
		Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, runID, agent.ID),
	}}
}

func moduTUIWorkflowAppendShortcuts(groups ...[]modutui.PanelShortcut) []modutui.PanelShortcut {
	var out []modutui.PanelShortcut
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func moduTUIWorkflowPanelFooter(base string, shortcuts []modutui.PanelShortcut) string {
	base = strings.TrimSpace(base)
	if len(shortcuts) == 0 {
		return base
	}
	parts := make([]string, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		key := strings.TrimSpace(shortcut.Key)
		label := strings.TrimSpace(shortcut.Label)
		if key == "" || label == "" {
			continue
		}
		parts = append(parts, "["+key+"] "+label)
	}
	if len(parts) == 0 {
		return base
	}
	if base == "" {
		return strings.Join(parts, "  ")
	}
	return base + "  " + strings.Join(parts, "  ")
}

func moduTUIWorkflowRunQuickRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	rows := []modutui.PanelRow{{
		Label:   "Execution feed",
		Detail:  "flow, updates, timeline",
		Command: moduTUIWorkflowPanelFeedPrefix + runID,
	}}
	rows = append(rows, moduTUIWorkflowRunFocusRows(run)...)
	return rows
}

func moduTUIWorkflowRunFocusRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return nil
	}
	var rows []modutui.PanelRow
	if phase, ok := moduTUIWorkflowCurrentOrRunningPhase(run); ok {
		rows = append(rows, modutui.PanelRow{
			Label:   "Current phase: " + moduTUIWorkflowPhaseTitle(phase.Title),
			Detail:  fmt.Sprintf("%d/%d %s", phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase)),
			Value:   phase.Title,
			Command: moduTUIWorkflowPanelPhasePrefix + runID + ":" + phase.Title,
		})
	}
	if agent, ok := moduTUIWorkflowFirstAttentionAgent(run.Agents); ok {
		rows = append(rows, modutui.PanelRow{
			Label:   "Attention agent: " + moduTUIWorkflowAgentName(agent),
			Detail:  moduTUIWorkflowAgentRowDetail(agent),
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, runID, agent.ID),
		})
	}
	if agent, ok := moduTUIWorkflowFirstRunningAgent(run.Agents); ok {
		rows = append(rows, modutui.PanelRow{
			Label:   "Active agent: " + moduTUIWorkflowAgentName(agent),
			Detail:  moduTUIWorkflowAgentRowDetail(agent),
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, runID, agent.ID),
		})
	}
	return rows
}

func moduTUIWorkflowFirstAttentionAgent(agents []moduTUIWorkflowAgent) (moduTUIWorkflowAgent, bool) {
	for _, agent := range agents {
		if strings.TrimSpace(agent.Error) != "" || strings.EqualFold(strings.TrimSpace(agent.Status), "error") || strings.EqualFold(strings.TrimSpace(agent.Status), "failed") {
			return agent, true
		}
	}
	return moduTUIWorkflowAgent{}, false
}

func moduTUIWorkflowCurrentOrRunningPhase(run moduTUIWorkflowRun) (moduTUIWorkflowPhase, bool) {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	current := strings.TrimSpace(run.CurrentPhase)
	if current != "" {
		for _, phase := range phases {
			if strings.TrimSpace(phase.Title) == current {
				return phase, true
			}
		}
		return moduTUIWorkflowPhase{Title: current}, true
	}
	for _, phase := range phases {
		if phase.RunningCount > 0 {
			return phase, true
		}
	}
	return moduTUIWorkflowPhase{}, false
}

func moduTUIWorkflowFirstRunningAgent(agents []moduTUIWorkflowAgent) (moduTUIWorkflowAgent, bool) {
	for _, agent := range agents {
		if moduTUIWorkflowStatusIsRunning(agent.Status) {
			return agent, true
		}
	}
	return moduTUIWorkflowAgent{}, false
}

func moduTUIWorkflowAgentName(agent moduTUIWorkflowAgent) string {
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	return fmt.Sprintf("#%d %s", agent.ID, label)
}

func moduTUIWorkflowAgentRowDetail(agent moduTUIWorkflowAgent) string {
	parts := []string{agent.Status}
	if agent.Phase != "" {
		parts = append(parts, agent.Phase)
	}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
	}
	return strings.Join(parts, " · ")
}

func moduTUIWorkflowRunFocusSelectedRow(rows []modutui.PanelRow) int {
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelPhasePrefix) ||
			strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentPrefix) {
			return i
		}
	}
	return 0
}

func moduTUIWorkflowCockpitSelectedRow(states map[string]any, rows []modutui.PanelRow) int {
	if len(rows) == 0 {
		return 0
	}
	state, ok := moduTUIWorkflowState(states)
	if !ok {
		return 0
	}
	runs := moduTUIWorkflowRuns(state["runs"])
	for i, run := range runs {
		if i >= len(rows) {
			break
		}
		if moduTUIWorkflowStatusIsRunning(run.Status) {
			return i
		}
	}
	return 0
}

func moduTUIWorkflowRunDetailSelectedRow(run moduTUIWorkflowRun, rows []modutui.PanelRow) int {
	if len(rows) == 0 {
		return 0
	}
	if current := strings.TrimSpace(run.CurrentPhase); current != "" {
		if index, ok := moduTUIWorkflowPhaseRowIndex(rows, current); ok {
			return index
		}
	}
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	for _, phase := range phases {
		if phase.RunningCount > 0 {
			if index, ok := moduTUIWorkflowPhaseRowIndex(rows, phase.Title); ok {
				return index
			}
		}
	}
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelPhasePrefix) {
			return i
		}
	}
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentPrefix) {
			return i
		}
	}
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentsPrefix) {
			return i
		}
	}
	return 0
}

func moduTUIWorkflowPhaseRowIndex(rows []modutui.PanelRow, phaseTitle string) (int, bool) {
	phaseTitle = strings.TrimSpace(phaseTitle)
	for i, row := range rows {
		if !strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelPhasePrefix) {
			continue
		}
		if strings.TrimSpace(row.Value) == phaseTitle {
			return i, true
		}
	}
	return 0, false
}

func moduTUIWorkflowAgentSelectedRow(agents []moduTUIWorkflowAgent, rows []modutui.PanelRow) int {
	if len(rows) == 0 {
		return 0
	}
	for _, agent := range agents {
		if !moduTUIWorkflowStatusIsRunning(agent.Status) {
			continue
		}
		target := strconv.Itoa(agent.ID)
		for i, row := range rows {
			if strings.TrimSpace(row.Value) == target && strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentPrefix) {
				return i
			}
		}
	}
	for i, row := range rows {
		if strings.HasPrefix(strings.TrimSpace(row.Command), moduTUIWorkflowPanelAgentPrefix) {
			return i
		}
	}
	return 0
}

func moduTUIWorkflowStatusIsRunning(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "in_progress", "in-progress":
		return true
	default:
		return false
	}
}

func moduTUIWorkflowRunBoardLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return nil
	}
	lines := make([]string, 0, len(phases)*3)
	for i, phase := range phases {
		if i >= 8 {
			lines = append(lines, fmt.Sprintf("  ... +%d more phase(s)", len(phases)-i))
			break
		}
		agents := moduTUIWorkflowAgentsForPhase(run.Agents, phase.Title)
		summary := moduTUIWorkflowBoardPhaseSummary(phases, i)
		lines = append(lines, fmt.Sprintf("  %d. [%s] %s %d/%d %s",
			i+1,
			moduTUIWorkflowTimelinePhaseStatus(phase),
			moduTUIWorkflowPhaseTitle(phase.Title),
			phase.DoneCount,
			phase.AgentCount,
			summary,
		))
		lines = append(lines, moduTUIWorkflowBoardAgentLines(agents)...)
	}
	return lines
}

func moduTUIWorkflowBoardPhaseSummary(phases []moduTUIWorkflowPhase, index int) string {
	if index < 0 || index >= len(phases) {
		return ""
	}
	phase := phases[index]
	switch moduTUIWorkflowTimelinePhaseStatus(phase) {
	case "error":
		return "needs attention"
	case "running":
		return "running now"
	case "done":
		return "complete"
	case "working":
		return "partially complete"
	}
	if index > 0 && !moduTUIWorkflowPhaseIsComplete(phases[index-1]) {
		return "waits for " + moduTUIWorkflowPhaseTitle(phases[index-1].Title)
	}
	return "waiting"
}

func moduTUIWorkflowPhaseIsComplete(phase moduTUIWorkflowPhase) bool {
	return phase.AgentCount > 0 && phase.DoneCount >= phase.AgentCount && phase.ErrorCount == 0 && phase.RunningCount == 0
}

func moduTUIWorkflowBoardAgentLines(agents []moduTUIWorkflowAgent) []string {
	if len(agents) == 0 {
		return nil
	}
	lines := make([]string, 0, 3)
	add := func(prefix string, agent moduTUIWorkflowAgent) {
		if len(lines) >= 3 {
			return
		}
		lines = append(lines, "     "+prefix+" "+moduTUIWorkflowBoardAgentLine(agent))
	}
	for _, agent := range agents {
		if strings.TrimSpace(agent.Error) != "" || strings.EqualFold(strings.TrimSpace(agent.Status), "error") || strings.EqualFold(strings.TrimSpace(agent.Status), "failed") {
			add("!", agent)
		}
	}
	for _, agent := range agents {
		if moduTUIWorkflowStatusIsRunning(agent.Status) {
			add(">", agent)
		}
	}
	if len(lines) == 0 {
		for _, agent := range agents {
			add("-", agent)
			if len(lines) >= 1 {
				break
			}
		}
	}
	if len(lines) < len(agents) {
		lines = append(lines, fmt.Sprintf("     ... +%d more agent(s)", len(agents)-len(lines)))
	}
	return lines
}

func moduTUIWorkflowBoardAgentLine(agent moduTUIWorkflowAgent) string {
	label := strings.TrimSpace(agent.Label)
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	status := strings.TrimSpace(agent.Status)
	if status == "" {
		status = "unknown"
	}
	parts := []string{fmt.Sprintf("#%d %s %s", agent.ID, label, status)}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
	}
	line := strings.Join(parts, " · ")
	if agent.Error != "" {
		line += ": " + moduTUITruncate(agent.Error, 100)
	} else if agent.ResultPreview != "" {
		line += ": " + moduTUITruncate(agent.ResultPreview, 100)
	} else if agent.PromptPreview != "" {
		line += ": " + moduTUITruncate(agent.PromptPreview, 100)
	}
	return line
}

func moduTUIWorkflowRunLaneLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 || len(run.Agents) == 0 {
		return nil
	}
	lines := make([]string, 0, len(phases))
	for i, phase := range phases {
		if i >= 8 {
			lines = append(lines, fmt.Sprintf("  ... +%d more phase(s)", len(phases)-i))
			break
		}
		agents := moduTUIWorkflowAgentsForPhase(run.Agents, phase.Title)
		if len(agents) == 0 {
			lines = append(lines, "  "+moduTUIWorkflowPhaseTitle(phase.Title)+": no agent snapshot")
			continue
		}
		parts := make([]string, 0, min(len(agents), 4))
		for j, agent := range agents {
			if j >= 4 {
				parts = append(parts, fmt.Sprintf("+%d more", len(agents)-j))
				break
			}
			parts = append(parts, moduTUIWorkflowLaneAgent(agent))
		}
		lines = append(lines, "  "+moduTUIWorkflowPhaseTitle(phase.Title)+": "+strings.Join(parts, " | "))
	}
	return lines
}

func moduTUIWorkflowLaneAgent(agent moduTUIWorkflowAgent) string {
	label := strings.TrimSpace(agent.Label)
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	parts := []string{moduTUIWorkflowLaneStatus(agent), fmt.Sprintf("#%d", agent.ID), label}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
	}
	return strings.Join(parts, " ")
}

func moduTUIWorkflowLaneStatus(agent moduTUIWorkflowAgent) string {
	if strings.TrimSpace(agent.Error) != "" {
		return "err"
	}
	switch strings.ToLower(strings.TrimSpace(agent.Status)) {
	case "done", "completed":
		return "done"
	case "running", "in_progress", "in-progress":
		return "run"
	case "error", "failed":
		return "err"
	case "queued", "pending", "waiting":
		return "wait"
	default:
		if strings.TrimSpace(agent.Status) == "" {
			return "wait"
		}
		return strings.ToLower(strings.TrimSpace(agent.Status))
	}
}

func moduTUIWorkflowRunFlowLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return moduTUIWorkflowRunAgentPulseLines(run)
	}
	lines := []string{"  phases: " + moduTUIWorkflowPhaseFlowLine(phases)}
	if current := moduTUIWorkflowCurrentPhaseLine(run, phases); current != "" {
		lines = append(lines, current)
	}
	lines = append(lines, moduTUIWorkflowRunAgentPulseLines(run)...)
	if next := moduTUIWorkflowNextPhaseLine(phases); next != "" {
		lines = append(lines, next)
	}
	return lines
}

func moduTUIWorkflowPhaseFlowLine(phases []moduTUIWorkflowPhase) string {
	parts := make([]string, 0, min(len(phases), 6))
	for i, phase := range phases {
		if i >= 6 {
			parts = append(parts, fmt.Sprintf("+%d", len(phases)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%s:%s", moduTUIWorkflowPhaseTitle(phase.Title), moduTUIWorkflowPhaseShortStatus(phase)))
	}
	if len(parts) == 0 {
		return "no phases"
	}
	return strings.Join(parts, " -> ")
}

func moduTUIWorkflowPhaseShortStatus(phase moduTUIWorkflowPhase) string {
	switch {
	case phase.ErrorCount > 0:
		return "error"
	case phase.RunningCount > 0:
		return "run"
	case phase.AgentCount > 0 && phase.DoneCount >= phase.AgentCount:
		return "done"
	case phase.DoneCount > 0:
		return "work"
	default:
		return "wait"
	}
}

func moduTUIWorkflowCurrentPhaseLine(run moduTUIWorkflowRun, phases []moduTUIWorkflowPhase) string {
	current := strings.TrimSpace(run.CurrentPhase)
	if current != "" {
		if phase, ok := moduTUIWorkflowPhaseByTitle(moduTUIWorkflowRun{Phases: phases}, current); ok {
			return fmt.Sprintf("  now: %s %d/%d %s", moduTUIWorkflowPhaseTitle(phase.Title), phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase))
		}
		return "  now: " + current
	}
	for _, phase := range phases {
		if phase.RunningCount > 0 {
			return fmt.Sprintf("  now: %s %d/%d %s", moduTUIWorkflowPhaseTitle(phase.Title), phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase))
		}
	}
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "unknown"
	}
	return "  now: workflow " + status
}

func moduTUIWorkflowRunAgentPulseLines(run moduTUIWorkflowRun) []string {
	var lines []string
	running := moduTUIWorkflowAgentsWithStatus(run.Agents, true)
	for i, agent := range running {
		if i >= 3 {
			lines = append(lines, fmt.Sprintf("  active: +%d more running agent(s)", len(running)-i))
			break
		}
		lines = append(lines, "  active: "+moduTUIWorkflowAgentPulse(agent))
		if agent.PromptPreview != "" {
			lines = append(lines, "    prompt: "+moduTUITruncate(agent.PromptPreview, 120))
		}
		if len(agent.ToolCalls) > 0 {
			lines = append(lines, "    tools: "+moduTUIWorkflowToolSummary(agent.ToolCalls))
		}
	}
	errors := moduTUIWorkflowAgentsWithError(run.Agents)
	for i, agent := range errors {
		if i >= 2 {
			lines = append(lines, fmt.Sprintf("  attention: +%d more error agent(s)", len(errors)-i))
			break
		}
		lines = append(lines, "  attention: "+moduTUIWorkflowAgentPulse(agent))
		if agent.Error != "" {
			lines = append(lines, "    error: "+moduTUITruncate(agent.Error, 120))
		}
	}
	if len(lines) == 0 && len(run.Agents) == 0 {
		status := strings.TrimSpace(run.Status)
		if status == "" {
			status = "waiting"
		}
		lines = append(lines, "  active: no agent snapshot yet ("+status+")")
	}
	return lines
}

func moduTUIWorkflowAgentPulse(agent moduTUIWorkflowAgent) string {
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	parts := []string{fmt.Sprintf("#%d %s [%s]", agent.ID, label, agent.Status)}
	if strings.TrimSpace(agent.Phase) != "" {
		parts = append(parts, "@"+agent.Phase)
	}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
	}
	return strings.Join(parts, " ")
}

func moduTUIWorkflowAgentsWithStatus(agents []moduTUIWorkflowAgent, running bool) []moduTUIWorkflowAgent {
	var filtered []moduTUIWorkflowAgent
	for _, agent := range agents {
		if moduTUIWorkflowStatusIsRunning(agent.Status) == running {
			filtered = append(filtered, agent)
		}
	}
	return filtered
}

func moduTUIWorkflowAgentsWithError(agents []moduTUIWorkflowAgent) []moduTUIWorkflowAgent {
	var filtered []moduTUIWorkflowAgent
	for _, agent := range agents {
		if strings.TrimSpace(agent.Error) != "" || strings.EqualFold(strings.TrimSpace(agent.Status), "error") || strings.EqualFold(strings.TrimSpace(agent.Status), "failed") {
			filtered = append(filtered, agent)
		}
	}
	return filtered
}

func moduTUIWorkflowNextPhaseLine(phases []moduTUIWorkflowPhase) string {
	for _, phase := range phases {
		if phase.AgentCount == 0 || phase.DoneCount < phase.AgentCount {
			if phase.RunningCount > 0 || phase.ErrorCount > 0 {
				continue
			}
			return "  next: " + moduTUIWorkflowPhaseTitle(phase.Title)
		}
	}
	return ""
}

func moduTUIWorkflowNextPhaseTitle(run moduTUIWorkflowRun) string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	for _, phase := range phases {
		if phase.AgentCount == 0 || phase.DoneCount < phase.AgentCount {
			if phase.RunningCount > 0 || phase.ErrorCount > 0 {
				continue
			}
			return moduTUIWorkflowPhaseTitle(phase.Title)
		}
	}
	return ""
}

func moduTUIWorkflowRunUpdateLines(run moduTUIWorkflowRun) []string {
	if len(run.Logs) == 0 {
		return nil
	}
	start := 0
	if len(run.Logs) > 5 {
		start = len(run.Logs) - 5
	}
	lines := make([]string, 0, len(run.Logs)-start)
	for _, log := range run.Logs[start:] {
		log = strings.TrimSpace(log)
		if log == "" {
			continue
		}
		lines = append(lines, "  - "+moduTUITruncate(log, 120))
	}
	return lines
}

func moduTUIWorkflowRunTimelineLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return nil
	}
	lines := make([]string, 0, len(phases)*2)
	for i, phase := range phases {
		if i >= 8 {
			lines = append(lines, fmt.Sprintf("  ... +%d more phase(s)", len(phases)-i))
			break
		}
		lines = append(lines, moduTUIWorkflowTimelinePhaseLine(phase))
		lines = append(lines, moduTUIWorkflowTimelineAgentLines(run.Agents, phase.Title)...)
	}
	return lines
}

func moduTUIWorkflowTimelinePhaseLine(phase moduTUIWorkflowPhase) string {
	parts := []string{fmt.Sprintf("%d/%d", phase.DoneCount, phase.AgentCount)}
	if phase.RunningCount > 0 {
		parts = append(parts, fmt.Sprintf("%d running", phase.RunningCount))
	}
	if phase.ErrorCount > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", phase.ErrorCount))
	}
	if phase.EstimatedTokens > 0 {
		parts = append(parts, fmt.Sprintf("est %d", phase.EstimatedTokens))
	}
	if phase.DurationMs > 0 {
		parts = append(parts, formatModuTUIActivityDuration(time.Duration(phase.DurationMs)*time.Millisecond))
	}
	return fmt.Sprintf("  [%s] %s %s", moduTUIWorkflowTimelinePhaseStatus(phase), moduTUIWorkflowPhaseTitle(phase.Title), strings.Join(parts, " · "))
}

func moduTUIWorkflowTimelinePhaseStatus(phase moduTUIWorkflowPhase) string {
	switch {
	case phase.ErrorCount > 0:
		return "error"
	case phase.RunningCount > 0:
		return "running"
	case phase.AgentCount > 0 && phase.DoneCount >= phase.AgentCount:
		return "done"
	case phase.DoneCount > 0:
		return "working"
	default:
		return "waiting"
	}
}

func moduTUIWorkflowTimelineAgentLines(agents []moduTUIWorkflowAgent, phase string) []string {
	phaseAgents := moduTUIWorkflowAgentsForPhase(agents, phase)
	lines := make([]string, 0, 2)
	added := 0
	for _, agent := range phaseAgents {
		if strings.TrimSpace(agent.Error) == "" && !strings.EqualFold(strings.TrimSpace(agent.Status), "error") && !strings.EqualFold(strings.TrimSpace(agent.Status), "failed") {
			continue
		}
		lines = append(lines, "    attention: "+moduTUIWorkflowAgentPulse(agent))
		added++
		if added >= 2 {
			return lines
		}
	}
	for _, agent := range phaseAgents {
		if !moduTUIWorkflowStatusIsRunning(agent.Status) {
			continue
		}
		lines = append(lines, "    active: "+moduTUIWorkflowAgentPulse(agent))
		added++
		if added >= 2 {
			return lines
		}
	}
	return lines
}

func moduTUIWorkflowPhaseRows(run moduTUIWorkflowRun) []modutui.PanelRow {
	if strings.TrimSpace(run.ID) == "" {
		return nil
	}
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	rows := make([]modutui.PanelRow, 0, len(phases))
	for _, phase := range phases {
		label := "Phase: " + moduTUIWorkflowPhaseTitle(phase.Title)
		detail := fmt.Sprintf("%d/%d %s", phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase))
		rows = append(rows, modutui.PanelRow{
			Label:   label,
			Detail:  detail,
			Value:   phase.Title,
			Command: moduTUIWorkflowPanelPhasePrefix + run.ID + ":" + phase.Title,
		})
	}
	return rows
}

func moduTUIWorkflowPhasePanel(session *coding_agent.CodingSession, runID, phaseTitle string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowPhasePanelFromStates(nil, runID, phaseTitle)
	}
	return moduTUIWorkflowPhasePanelFromStates(session.ExtensionRuntimeStates(), runID, phaseTitle)
}

func moduTUIWorkflowPhasePanelFromStates(states map[string]any, runID, phaseTitle string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowPhasePanelID, "Workflow Phase", runID)
	}
	phase, ok := moduTUIWorkflowPhaseByTitle(run, phaseTitle)
	if !ok {
		phase = moduTUIWorkflowPhase{Title: phaseTitle}
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	title := moduTUIWorkflowPhaseTitle(phase.Title)
	status := moduTUIWorkflowPhaseStatus(phase)
	agents := moduTUIWorkflowAgentsForPhase(run.Agents, phase.Title)
	lines := []string{
		"summary",
		"  workflow: " + name,
		"  run: " + run.ID,
		"  phase: " + title,
		fmt.Sprintf("  progress: %d/%d %s", phase.DoneCount, phase.AgentCount, status),
	}
	if phase.RunningCount > 0 {
		lines = append(lines, fmt.Sprintf("  running: %d", phase.RunningCount))
	}
	if phase.ErrorCount > 0 {
		lines = append(lines, fmt.Sprintf("  errors: %d", phase.ErrorCount))
	}
	if phase.EstimatedTokens > 0 {
		lines = append(lines, fmt.Sprintf("  estimated tokens: %d", phase.EstimatedTokens))
	}
	if phase.DurationMs > 0 {
		lines = append(lines, "  duration: "+formatModuTUIActivityDuration(time.Duration(phase.DurationMs)*time.Millisecond))
	}
	lines = append(lines, "", "agents")
	if len(agents) == 0 {
		lines = append(lines, "  no agent snapshot available for this phase")
	}
	rows := make([]modutui.PanelRow, 0, len(agents)+6)
	for _, agent := range agents {
		lines = append(lines, "  "+moduTUIWorkflowAgentLine(agent))
		if agent.Error != "" {
			lines = append(lines, "    error: "+moduTUITruncate(agent.Error, 120))
		} else if agent.ResultPreview != "" {
			lines = append(lines, "    result: "+moduTUITruncate(agent.ResultPreview, 120))
		} else if agent.PromptPreview != "" {
			lines = append(lines, "    prompt: "+moduTUITruncate(agent.PromptPreview, 120))
		}
		if len(agent.ToolCalls) > 0 {
			lines = append(lines, "    tools: "+moduTUIWorkflowToolSummary(agent.ToolCalls))
		}
		label := agent.Label
		if label == "" {
			label = fmt.Sprintf("agent-%d", agent.ID)
		}
		detailParts := []string{agent.Status}
		if agent.TurnTokens > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d tokens", agent.TurnTokens))
		} else if agent.EstimatedTokens > 0 {
			detailParts = append(detailParts, fmt.Sprintf("est %d", agent.EstimatedTokens))
		}
		if agent.RecentToolCalls > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
		}
		rows = append(rows, modutui.PanelRow{
			Label:   fmt.Sprintf("#%d %s", agent.ID, label),
			Detail:  strings.Join(detailParts, " · "),
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, run.ID, agent.ID),
		})
	}
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Value:   phase.Title,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "All agents",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowPhasePanelID,
		Title:     "Workflow Phase",
		Subtitle:  fmt.Sprintf("%s / %s [%s]", name, title, status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowAgentSelectedRow(agents, rows),
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select agent  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowAgentsPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowAgentsPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowAgentsPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowAgentsPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowAgentsPanelID, "Workflow Agents", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{"agents"}
	if len(run.Agents) == 0 {
		lines = append(lines, "  no agent snapshot available")
	}
	rows := make([]modutui.PanelRow, 0, len(run.Agents)+5)
	for _, agent := range run.Agents {
		label := agent.Label
		if label == "" {
			label = fmt.Sprintf("agent-%d", agent.ID)
		}
		label = fmt.Sprintf("#%d [%s] %s", agent.ID, agent.Status, label)
		detailParts := []string{}
		if agent.Phase != "" {
			detailParts = append(detailParts, agent.Phase)
		}
		if agent.TurnTokens > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d tokens", agent.TurnTokens))
		} else if agent.EstimatedTokens > 0 {
			detailParts = append(detailParts, fmt.Sprintf("est %d", agent.EstimatedTokens))
		}
		if agent.RecentToolCalls > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d tools", agent.RecentToolCalls))
		}
		if agent.FailedToolCalls > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d failed", agent.FailedToolCalls))
		}
		rows = append(rows, modutui.PanelRow{
			Label:   label,
			Detail:  strings.Join(detailParts, " · "),
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, run.ID, agent.ID),
		})
	}
	rows = append(rows, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to workflow runs",
		Detail:  "return",
		Command: moduTUIWorkflowPanelBackCommand,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowAgentsPanelID,
		Title:     "Workflow Agents",
		Subtitle:  fmt.Sprintf("%s [%s] %d agent(s)", name, run.Status, len(run.Agents)),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  moduTUIWorkflowAgentSelectedRow(run.Agents, rows),
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select agent  [enter] detail  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowAgentPanel(session *coding_agent.CodingSession, runID string, agentID int) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowAgentPanelFromStates(nil, runID, agentID)
	}
	return moduTUIWorkflowAgentPanelFromStates(session.ExtensionRuntimeStates(), runID, agentID)
}

func moduTUIWorkflowAgentPanelFromStates(states map[string]any, runID string, agentID int) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowAgentPanelID, "Workflow Agent", runID)
	}
	agent, ok := moduTUIWorkflowAgentByID(run.Agents, agentID)
	if !ok {
		return modutui.Panel{
			ID:       moduTUIWorkflowAgentPanelID,
			Title:    "Workflow Agent",
			Subtitle: fmt.Sprintf("agent %d not found in %s", agentID, run.ID),
			Lines:    []string{"Agent not found in workflow runtime state."},
			Rows: []modutui.PanelRow{{
				Label:   "Back to agents",
				Detail:  run.ID,
				Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
			}},
			Footer: "[enter] back  [esc/q] close",
		}
	}
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	var lines []string
	lines = append(lines, "summary")
	lines = append(lines, fmt.Sprintf("  id: %d", agent.ID))
	lines = append(lines, "  label: "+label)
	lines = append(lines, "  status: "+agent.Status)
	if agent.Phase != "" {
		lines = append(lines, "  phase: "+agent.Phase)
	}
	if agent.TurnTokens > 0 {
		lines = append(lines, fmt.Sprintf("  tokens: %d", agent.TurnTokens))
	} else if agent.EstimatedTokens > 0 {
		lines = append(lines, fmt.Sprintf("  estimated tokens: %d", agent.EstimatedTokens))
	}
	if agent.FailedToolCalls > 0 {
		lines = append(lines, fmt.Sprintf("  failed tools: %d", agent.FailedToolCalls))
	}
	if agent.Error != "" {
		lines = append(lines, "", "error")
		lines = append(lines, moduTUIWorkflowTextLines(agent.Error)...)
	}
	if agent.ResultPreview != "" {
		lines = append(lines, "", "result preview")
		lines = append(lines, moduTUIWorkflowTextLines(agent.ResultPreview)...)
	}
	if agent.PromptPreview != "" {
		lines = append(lines, "", "prompt preview")
		lines = append(lines, moduTUIWorkflowTextLines(agent.PromptPreview)...)
	}
	if len(agent.ToolCalls) > 0 {
		lines = append(lines, "", "recent tool calls")
		for _, call := range agent.ToolCalls {
			name := call.ToolName
			if name == "" {
				name = "tool"
			}
			status := "ok"
			if call.IsError {
				status = "error"
			}
			lines = append(lines, fmt.Sprintf("  - %s [%s]", name, status))
			if call.ArgsPreview != "" {
				lines = append(lines, "    args: "+moduTUITruncate(call.ArgsPreview, 160))
			}
			if call.ResultPreview != "" {
				lines = append(lines, "    result: "+moduTUITruncate(call.ResultPreview, 160))
			}
		}
	}
	controlRows := moduTUIWorkflowAgentControlRows(run.ID, agent)
	rows := controlRows
	rows = append(rows, modutui.PanelRow{
		Label:   "Transcript",
		Detail:  "full child transcript",
		Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelTranscriptPrefix, run.ID, agent.ID),
	}, moduTUIWorkflowGuideRow(run.ID), modutui.PanelRow{
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to agents",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, modutui.PanelRow{
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	})
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowAgentShortcuts(run.ID, agent),
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowAgentPanelID,
		Title:     "Workflow Agent",
		Subtitle:  fmt.Sprintf("%s #%d [%s]", run.ID, agent.ID, agent.Status),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Selected:  len(controlRows),
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowAgentControlRows(runID string, agent moduTUIWorkflowAgent) []modutui.PanelRow {
	runID = strings.TrimSpace(runID)
	if runID == "" || agent.ID <= 0 || strings.ToLower(strings.TrimSpace(agent.Status)) != "running" {
		return nil
	}
	control := func(label, detail, verb string) modutui.PanelRow {
		return modutui.PanelRow{
			Label:   label,
			Detail:  detail,
			Value:   strconv.Itoa(agent.ID),
			Command: fmt.Sprintf("%s%s:%s:%d", moduTUIWorkflowPanelAgentControlPrefix, verb, runID, agent.ID),
		}
	}
	return []modutui.PanelRow{
		control("Stop agent", "request stop", "stop"),
		control("Restart agent", "retry this agent", "restart"),
	}
}

func moduTUIWorkflowAgentShortcuts(runID string, agent moduTUIWorkflowAgent) []modutui.PanelShortcut {
	runID = strings.TrimSpace(runID)
	if runID == "" || agent.ID <= 0 || strings.ToLower(strings.TrimSpace(agent.Status)) != "running" {
		return nil
	}
	shortcut := func(key, label, verb string) modutui.PanelShortcut {
		return modutui.PanelShortcut{
			Key:     key,
			Label:   label,
			Command: fmt.Sprintf("%s%s:%s:%d", moduTUIWorkflowPanelAgentControlPrefix, verb, runID, agent.ID),
		}
	}
	return []modutui.PanelShortcut{
		shortcut("x", "Stop agent", "stop"),
		shortcut("r", "Restart agent", "restart"),
	}
}

func moduTUIWorkflowTranscriptPanel(session *coding_agent.CodingSession, runID string, agentID int) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowTranscriptPanelFromStates(nil, runID, agentID)
	}
	return moduTUIWorkflowTranscriptPanelFromStates(session.ExtensionRuntimeStates(), runID, agentID)
}

func moduTUIWorkflowTranscriptPanelFromStates(states map[string]any, runID string, agentID int) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowTranscriptPanelID, "Workflow Transcript", runID)
	}
	agent, _ := moduTUIWorkflowAgentByID(run.Agents, agentID)
	lines, err := moduTUIWorkflowTranscriptLines(run.SnapshotPath, agentID)
	if err != nil {
		lines = []string{"transcript", "  error: " + err.Error()}
	}
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agentID)
	}
	rows := []modutui.PanelRow{{
		Label:   "Back to agent",
		Detail:  fmt.Sprintf("#%d", agentID),
		Command: fmt.Sprintf("%s%s:%d", moduTUIWorkflowPanelAgentPrefix, run.ID, agentID),
	}, moduTUIWorkflowGuideRow(run.ID), {
		Label:   "Execution feed",
		Detail:  "live status surface",
		Command: moduTUIWorkflowPanelFeedPrefix + run.ID,
	}, {
		Label:   "Map",
		Detail:  "phase and agent tree",
		Command: moduTUIWorkflowPanelMapPrefix + run.ID,
	}, {
		Label:   "Back to agents",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelAgentsPrefix + run.ID,
	}, {
		Label:   "Back to run detail",
		Detail:  run.ID,
		Command: moduTUIWorkflowPanelDetailPrefix + run.ID,
	}}
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowTranscriptPanelID,
		Title:     "Workflow Transcript",
		Subtitle:  fmt.Sprintf("%s #%d %s", run.ID, agentID, label),
		Lines:     lines,
		Rows:      rows,
		Shortcuts: shortcuts,
		Footer:    moduTUIWorkflowPanelFooter("[enter] back  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowTranscriptLines(snapshotPath string, agentID int) ([]string, error) {
	snapshotPath = strings.TrimSpace(snapshotPath)
	if snapshotPath == "" {
		return []string{"transcript", "  no snapshot path available"}, nil
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	agent, ok := moduTUIWorkflowSnapshotAgent(snapshot, agentID)
	if !ok {
		return []string{"transcript", fmt.Sprintf("  agent %d not found in snapshot", agentID)}, nil
	}
	transcript := moduTUIRuntimeStateMaps(agent["transcript"])
	if len(transcript) == 0 {
		return []string{"transcript", "  no child transcript captured for this agent"}, nil
	}
	lines := []string{"transcript"}
	for i, entry := range transcript {
		if i > 0 {
			lines = append(lines, "")
		}
		role := strings.ToUpper(moduTUIRuntimeStateString(entry["role"]))
		if role == "" {
			role = "UNKNOWN"
		}
		header := fmt.Sprintf("## %d. %s", i+1, role)
		if toolName := moduTUIRuntimeStateString(entry["toolName"]); toolName != "" {
			header += " " + toolName
		}
		if moduTUIRuntimeStateBool(entry["isError"]) {
			header += " [error]"
		}
		lines = append(lines, header)
		if text := moduTUIRuntimeStateString(entry["text"]); text != "" {
			lines = append(lines, moduTUIWorkflowTextLines(text)...)
		}
		for _, call := range moduTUIRuntimeStateMaps(entry["toolCalls"]) {
			name := moduTUIRuntimeStateString(call["name"])
			if name == "" {
				name = "tool"
			}
			callLine := "  ToolCall: " + name
			if id := moduTUIRuntimeStateString(call["id"]); id != "" {
				callLine += " (" + id + ")"
			}
			lines = append(lines, callLine)
			if args := moduTUIRuntimeStateString(call["args"]); args != "" {
				lines = append(lines, "  Args: "+args)
			}
		}
		if usage, ok := entry["usage"].(map[string]any); ok {
			input := moduTUIRuntimeStateNumber(usage["input"])
			output := moduTUIRuntimeStateNumber(usage["output"])
			total := moduTUIRuntimeStateNumber(usage["totalTokens"])
			if input > 0 || output > 0 || total > 0 {
				lines = append(lines, fmt.Sprintf("  Usage: input=%d output=%d total=%d", input, output, total))
			}
		}
	}
	return lines, nil
}

func moduTUIWorkflowSnapshotAgent(snapshot map[string]any, agentID int) (map[string]any, bool) {
	for _, agent := range moduTUIRuntimeStateMaps(snapshot["agents"]) {
		if moduTUIRuntimeStateNumber(agent["id"]) == agentID {
			return agent, true
		}
	}
	return nil, false
}

func moduTUIWorkflowAgentByID(agents []moduTUIWorkflowAgent, agentID int) (moduTUIWorkflowAgent, bool) {
	for _, agent := range agents {
		if agent.ID == agentID {
			return agent, true
		}
	}
	return moduTUIWorkflowAgent{}, false
}

func moduTUIWorkflowResultPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowResultPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowResultPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowResultPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowResultPanelID, "Workflow Result", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{"result"}
	if run.SnapshotPath != "" {
		lines = append(lines, "  snapshot: "+run.SnapshotPath)
	}
	result, err := moduTUIWorkflowResultLines(run.SnapshotPath)
	if err != nil {
		lines = append(lines, "  error: "+err.Error())
	} else {
		lines = append(lines, moduTUIWorkflowArtifactPreviewLines(result, run.SnapshotPath)...)
	}
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowResultPanelID,
		Title:     "Workflow Result",
		Subtitle:  name + " [" + run.Status + "]",
		Lines:     lines,
		Rows:      moduTUIWorkflowArtifactNavigationRows(run),
		Shortcuts: shortcuts,
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowScriptPanel(session *coding_agent.CodingSession, runID string) modutui.Panel {
	if session == nil {
		return moduTUIWorkflowScriptPanelFromStates(nil, runID)
	}
	return moduTUIWorkflowScriptPanelFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowScriptPanelFromStates(states map[string]any, runID string) modutui.Panel {
	run, ok := moduTUIWorkflowRunByIDFromStates(states, runID)
	if !ok {
		return moduTUIWorkflowMissingRunPanel(moduTUIWorkflowScriptPanelID, "Workflow Script", runID)
	}
	name := run.Name
	if name == "" {
		name = run.ID
	}
	lines := []string{"script"}
	if run.ScriptPath != "" {
		lines = append(lines, "  path: "+run.ScriptPath)
	}
	script, err := moduTUIWorkflowFileLines(run.ScriptPath)
	if err != nil {
		lines = append(lines, "  error: "+err.Error())
	} else {
		lines = append(lines, moduTUIWorkflowArtifactPreviewLines(script, run.ScriptPath)...)
	}
	shortcuts := moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(run.ID),
		moduTUIWorkflowNavigationShortcuts(run.ID, "feed", "map", "detail", "agents"),
	)
	return modutui.Panel{
		ID:        moduTUIWorkflowScriptPanelID,
		Title:     "Workflow Script",
		Subtitle:  name + " [" + run.Status + "]",
		Lines:     lines,
		Rows:      moduTUIWorkflowArtifactNavigationRows(run),
		Shortcuts: shortcuts,
		Footer:    moduTUIWorkflowPanelFooter("[↑/↓] select  [enter] open  [esc/q] close", shortcuts),
	}
}

func moduTUIWorkflowMissingRunPanel(id, title, runID string) modutui.Panel {
	return modutui.Panel{
		ID:       id,
		Title:    title,
		Subtitle: "run not found: " + strings.TrimSpace(runID),
		Lines: []string{
			"Run not found in workflow runtime state.",
			"Use /workflows list to refresh persisted runs.",
		},
		Rows: []modutui.PanelRow{{
			Label:   "Back to workflow runs",
			Command: moduTUIWorkflowPanelBackCommand,
		}},
		Footer: "[enter] back  [esc/q] close",
	}
}

func moduTUIWorkflowInvalidAgentPanel(runID, agentIDText string) modutui.Panel {
	runID = strings.TrimSpace(runID)
	return modutui.Panel{
		ID:       moduTUIWorkflowAgentPanelID,
		Title:    "Workflow Agent",
		Subtitle: "invalid agent id: " + strings.TrimSpace(agentIDText),
		Lines: []string{
			"Agent id must be a positive integer.",
			"Use the Agents or Phase panel to choose an agent row.",
		},
		Rows: []modutui.PanelRow{{
			Label:   "Back to agents",
			Detail:  runID,
			Command: moduTUIWorkflowPanelAgentsPrefix + runID,
		}, {
			Label:   "Back to run detail",
			Detail:  runID,
			Command: moduTUIWorkflowPanelDetailPrefix + runID,
		}},
		Footer: "[enter] back  [esc/q] close",
	}
}

func moduTUIWorkflowResultLines(snapshotPath string) ([]string, error) {
	snapshotPath = strings.TrimSpace(snapshotPath)
	if snapshotPath == "" {
		return []string{"  no snapshot path available"}, nil
	}
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	result, ok := snapshot["result"]
	if !ok || result == nil {
		return []string{"  no result in snapshot"}, nil
	}
	return moduTUIWorkflowValueLines(result), nil
}

func moduTUIWorkflowFileLines(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return []string{"  no file path available"}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return moduTUIWorkflowTextLines(string(data)), nil
}

func moduTUIWorkflowArtifactPreviewLines(lines []string, path string) []string {
	if len(lines) <= moduTUIWorkflowArtifactLineLimit {
		return lines
	}
	preview := append([]string{}, lines[:moduTUIWorkflowArtifactLineLimit]...)
	hidden := len(lines) - moduTUIWorkflowArtifactLineLimit
	truncated := fmt.Sprintf("  ... +%d more line(s) truncated", hidden)
	if strings.TrimSpace(path) != "" {
		truncated += "; full artifact: " + strings.TrimSpace(path)
	}
	preview = append(preview, truncated)
	return preview
}

func moduTUIWorkflowValueLines(value any) []string {
	if text, ok := value.(string); ok {
		return moduTUIWorkflowTextLines(text)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return moduTUIWorkflowTextLines(fmt.Sprint(value))
	}
	return moduTUIWorkflowTextLines(string(data))
}

func moduTUIWorkflowTextLines(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{"  (empty)"}
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = "  " + lines[i]
	}
	return lines
}

func moduTUIWorkflowRunByID(session *coding_agent.CodingSession, runID string) (moduTUIWorkflowRun, bool) {
	if session == nil {
		return moduTUIWorkflowRun{}, false
	}
	return moduTUIWorkflowRunByIDFromStates(session.ExtensionRuntimeStates(), runID)
}

func moduTUIWorkflowRunBySelector(session *coding_agent.CodingSession, selector string) (moduTUIWorkflowRun, bool) {
	if session == nil {
		return moduTUIWorkflowRun{}, false
	}
	return moduTUIWorkflowRunBySelectorFromStates(session.ExtensionRuntimeStates(), selector)
}

func moduTUIWorkflowRunBySelectorFromStates(states map[string]any, selector string) (moduTUIWorkflowRun, bool) {
	state, ok := moduTUIWorkflowState(states)
	if !ok {
		return moduTUIWorkflowRun{}, false
	}
	runs := moduTUIWorkflowRuns(state["runs"])
	if len(runs) == 0 {
		return moduTUIWorkflowRun{}, false
	}
	selector = strings.TrimSpace(selector)
	if selector == "" || selector == "latest" {
		return runs[0], true
	}
	for _, run := range runs {
		if run.ID == selector {
			return run, true
		}
	}
	return moduTUIWorkflowRun{}, false
}

func moduTUIWorkflowRunByIDFromStates(states map[string]any, runID string) (moduTUIWorkflowRun, bool) {
	state, ok := moduTUIWorkflowState(states)
	if !ok {
		return moduTUIWorkflowRun{}, false
	}
	runID = strings.TrimSpace(runID)
	for _, run := range moduTUIWorkflowRuns(state["runs"]) {
		if run.ID == runID {
			return run, true
		}
	}
	return moduTUIWorkflowRun{}, false
}

func moduTUIWorkflowCockpitRows(session *coding_agent.CodingSession) []modutui.PanelRow {
	if session == nil {
		return nil
	}
	return moduTUIWorkflowCockpitRowsFromStates(session.ExtensionRuntimeStates())
}

func moduTUIWorkflowCockpitRowsFromStates(states map[string]any) []modutui.PanelRow {
	state, ok := moduTUIWorkflowState(states)
	if !ok {
		return nil
	}
	runs := moduTUIWorkflowRuns(state["runs"])
	rows := make([]modutui.PanelRow, 0, min(len(runs), 12))
	for i, run := range runs {
		if i >= 12 {
			break
		}
		name := run.Name
		if name == "" {
			name = run.ID
		}
		progress := ""
		if run.AgentCount > 0 {
			progress = fmt.Sprintf(" %d/%d", run.DoneCount, run.AgentCount)
		}
		label := fmt.Sprintf("%s [%s]%s", name, run.Status, progress)
		detailParts := []string{}
		if run.CurrentPhase != "" {
			detailParts = append(detailParts, run.CurrentPhase)
		}
		if run.DurationMs > 0 {
			detailParts = append(detailParts, formatModuTUIActivityDuration(time.Duration(run.DurationMs)*time.Millisecond))
		}
		if run.ErrorCount > 0 {
			detailParts = append(detailParts, fmt.Sprintf("%d errors", run.ErrorCount))
		}
		rows = append(rows, modutui.PanelRow{
			Label:   label,
			Detail:  strings.Join(detailParts, " · "),
			Value:   run.ID,
			Command: moduTUIWorkflowCockpitRunCommand(run),
		})
	}
	return rows
}

func moduTUIWorkflowCockpitShortcutsFromStates(states map[string]any) []modutui.PanelShortcut {
	state, ok := moduTUIWorkflowState(states)
	if !ok {
		return nil
	}
	runs := moduTUIWorkflowRuns(state["runs"])
	if len(runs) == 0 {
		return nil
	}
	return moduTUIWorkflowAppendShortcuts(
		moduTUIWorkflowGuideShortcut(runs[0].ID),
		moduTUIWorkflowNavigationShortcuts(runs[0].ID, "feed", "map", "detail"),
	)
}

func moduTUIWorkflowCockpitRunCommand(run moduTUIWorkflowRun) string {
	runID := strings.TrimSpace(run.ID)
	if runID == "" {
		return moduTUIWorkflowPanelBackCommand
	}
	if moduTUIWorkflowStatusIsRunning(run.Status) {
		return moduTUIWorkflowPanelFeedPrefix + runID
	}
	return moduTUIWorkflowPanelDetailPrefix + runID
}

func moduTUIWorkflowCockpitSubtitle(session *coding_agent.CodingSession) string {
	if session == nil {
		return "workflow runtime state unavailable"
	}
	return moduTUIWorkflowCockpitSubtitleFromStates(session.ExtensionRuntimeStates())
}

func moduTUIWorkflowCockpitSubtitleFromStates(states map[string]any) string {
	state, ok := moduTUIWorkflowState(states)
	if !ok {
		return "workflow runtime state unavailable"
	}
	runs := moduTUIWorkflowRuns(state["runs"])
	latest := "no runs"
	if len(runs) > 0 {
		name := runs[0].Name
		if name == "" {
			name = runs[0].ID
		}
		latest = fmt.Sprintf("latest %s [%s]", name, runs[0].Status)
	}
	return fmt.Sprintf("running %d  stopped %d  completed %d  failed %d  %s",
		moduTUIRuntimeStateNumber(state["runningCount"]),
		moduTUIRuntimeStateNumber(state["stoppedCount"]),
		moduTUIRuntimeStateNumber(state["completedCount"]),
		moduTUIRuntimeStateNumber(state["failedCount"]),
		latest,
	)
}

func moduTUIWorkflowCockpitTextFromStates(states map[string]any) string {
	state, ok := moduTUIWorkflowState(states)
	if !ok {
		return strings.Join([]string{
			"Workflow Cockpit",
			"",
			"overview",
			"  workflow runtime state is not available",
			"",
			"next actions",
			"  enable dynamic workflows in /config",
			"  start a workflow, then rerun /workflows",
		}, "\n")
	}
	runs := moduTUIWorkflowRuns(state["runs"])
	var lines []string
	lines = append(lines, "Workflow Cockpit", "")
	lines = append(lines, "overview")
	lines = append(lines, fmt.Sprintf("  running %d  stopped %d  completed %d  failed %d",
		moduTUIRuntimeStateNumber(state["runningCount"]),
		moduTUIRuntimeStateNumber(state["stoppedCount"]),
		moduTUIRuntimeStateNumber(state["completedCount"]),
		moduTUIRuntimeStateNumber(state["failedCount"]),
	))
	if indicator := strings.TrimSpace(moduTUIRuntimeStateString(state["indicator"])); indicator != "" {
		lines = append(lines, "  "+indicator)
	}
	if len(runs) == 0 {
		lines = append(lines, "  no workflow runs in this session", "", "next actions")
		lines = append(lines, "  start a workflow, then rerun /workflows")
		lines = append(lines, "  /workflows list")
		return strings.Join(lines, "\n")
	}

	latest := runs[0]
	name := latest.Name
	if strings.TrimSpace(name) == "" {
		name = latest.ID
	}
	progress := ""
	if latest.AgentCount > 0 {
		progress = fmt.Sprintf(" %d/%d", latest.DoneCount, latest.AgentCount)
	}
	current := ""
	if latest.CurrentPhase != "" {
		current = " current=" + latest.CurrentPhase
	}
	lines = append(lines, fmt.Sprintf("  latest %s [%s]%s%s", name, latest.Status, progress, current))
	if latest.ErrorCount > 0 {
		lines = append(lines, fmt.Sprintf("  errors %d", latest.ErrorCount))
	}

	if board := moduTUIWorkflowRunBoardLines(latest); len(board) > 0 {
		lines = append(lines, "", "board")
		lines = append(lines, board...)
	}
	lines = append(lines, "", "flow")
	lines = append(lines, moduTUIWorkflowRunFlowLines(latest)...)
	if updates := moduTUIWorkflowRunUpdateLines(latest); len(updates) > 0 {
		lines = append(lines, "", "updates")
		lines = append(lines, updates...)
	}
	if timeline := moduTUIWorkflowRunTimelineLines(latest); len(timeline) > 0 {
		lines = append(lines, "", "timeline")
		lines = append(lines, timeline...)
	}

	lines = append(lines, "", "latest run")
	lines = append(lines, "  id: "+latest.ID)
	if latest.Name != "" {
		lines = append(lines, "  name: "+latest.Name)
	}
	lines = append(lines, "  status: "+latest.Status)
	if latest.CurrentPhase != "" {
		lines = append(lines, "  current phase: "+latest.CurrentPhase)
	}
	lines = append(lines, fmt.Sprintf("  progress: %d/%d done, %d running, %d errors",
		latest.DoneCount, latest.AgentCount, latest.RunningAgentCount, latest.ErrorCount))
	lines = append(lines, "", "next actions")
	lines = append(lines, "  /workflows guide latest")
	lines = append(lines, "  /workflows feed latest")
	lines = append(lines, "  /workflows map latest")
	lines = append(lines, "  /workflows show latest")
	lines = append(lines, "  rerun /workflows to refresh")
	return strings.Join(lines, "\n")
}

func moduTUIWorkflowOrchestrationLines(run moduTUIWorkflowRun) []string {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	if len(phases) == 0 {
		return []string{"  no phase or agent snapshot yet"}
	}
	var lines []string
	for _, phase := range phases {
		title := phase.Title
		if title == "" {
			title = "(no phase)"
		}
		lines = append(lines, fmt.Sprintf("  [%s] %d/%d %s",
			title, phase.DoneCount, phase.AgentCount, moduTUIWorkflowPhaseStatus(phase)))
		agents := moduTUIWorkflowAgentsForPhase(run.Agents, phase.Title)
		if len(agents) == 0 {
			continue
		}
		for i, agent := range agents {
			if i >= 8 {
				lines = append(lines, fmt.Sprintf("    ... +%d more agents", len(agents)-i))
				break
			}
			lines = append(lines, "    "+moduTUIWorkflowAgentLine(agent))
			if agent.Error != "" {
				lines = append(lines, "      error: "+moduTUITruncate(agent.Error, 120))
			} else if agent.ResultPreview != "" {
				lines = append(lines, "      result: "+moduTUITruncate(agent.ResultPreview, 120))
			} else if agent.PromptPreview != "" {
				lines = append(lines, "      prompt: "+moduTUITruncate(agent.PromptPreview, 120))
			}
			if len(agent.ToolCalls) > 0 {
				lines = append(lines, "      tools: "+moduTUIWorkflowToolSummary(agent.ToolCalls))
			}
		}
	}
	return lines
}

func moduTUIWorkflowDerivedPhases(agents []moduTUIWorkflowAgent) []moduTUIWorkflowPhase {
	index := map[string]int{}
	var phases []moduTUIWorkflowPhase
	for _, agent := range agents {
		title := agent.Phase
		if _, ok := index[title]; !ok {
			index[title] = len(phases)
			phases = append(phases, moduTUIWorkflowPhase{Title: title})
		}
		phase := &phases[index[title]]
		phase.AgentCount++
		switch agent.Status {
		case "done", "completed":
			phase.DoneCount++
		case "running":
			phase.RunningCount++
		case "error", "failed":
			phase.ErrorCount++
		}
	}
	return phases
}

func moduTUIWorkflowAgentsForPhase(agents []moduTUIWorkflowAgent, phase string) []moduTUIWorkflowAgent {
	var out []moduTUIWorkflowAgent
	for _, agent := range agents {
		if agent.Phase == phase {
			out = append(out, agent)
		}
	}
	return out
}

func moduTUIWorkflowPhaseByTitle(run moduTUIWorkflowRun, title string) (moduTUIWorkflowPhase, bool) {
	phases := run.Phases
	if len(phases) == 0 {
		phases = moduTUIWorkflowDerivedPhases(run.Agents)
	}
	for _, phase := range phases {
		if phase.Title == title {
			return phase, true
		}
	}
	return moduTUIWorkflowPhase{}, false
}

func moduTUIWorkflowPhaseTitle(title string) string {
	if strings.TrimSpace(title) == "" {
		return "(no phase)"
	}
	return title
}

func moduTUIWorkflowPhaseStatus(phase moduTUIWorkflowPhase) string {
	switch {
	case phase.ErrorCount > 0:
		return fmt.Sprintf("errors=%d", phase.ErrorCount)
	case phase.RunningCount > 0:
		return fmt.Sprintf("running=%d", phase.RunningCount)
	case phase.AgentCount > 0 && phase.DoneCount >= phase.AgentCount:
		return "done"
	case phase.DoneCount > 0:
		return "in-progress"
	default:
		return "waiting"
	}
}

func moduTUIWorkflowAgentLine(agent moduTUIWorkflowAgent) string {
	label := agent.Label
	if label == "" {
		label = fmt.Sprintf("agent-%d", agent.ID)
	}
	parts := []string{fmt.Sprintf("#%d [%s] %s", agent.ID, agent.Status, label)}
	if agent.TurnTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens=%d", agent.TurnTokens))
	} else if agent.EstimatedTokens > 0 {
		parts = append(parts, fmt.Sprintf("estimated=%d", agent.EstimatedTokens))
	}
	if agent.RecentToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("tools=%d", agent.RecentToolCalls))
	}
	if agent.FailedToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("failedTools=%d", agent.FailedToolCalls))
	}
	return strings.Join(parts, " ")
}

func moduTUIWorkflowToolSummary(calls []moduTUIWorkflowToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	parts := make([]string, 0, min(len(calls), 3))
	for i, call := range calls {
		if i >= 3 {
			parts = append(parts, fmt.Sprintf("+%d more", len(calls)-i))
			break
		}
		name := call.ToolName
		if name == "" {
			name = "tool"
		}
		item := name
		if call.IsError {
			item += " error"
		}
		if call.ResultPreview != "" {
			item += " -> " + moduTUITruncate(call.ResultPreview, 60)
		} else if call.ArgsPreview != "" {
			item += " " + moduTUITruncate(call.ArgsPreview, 60)
		}
		parts = append(parts, item)
	}
	return strings.Join(parts, "; ")
}

func moduTUIWorkflowState(states map[string]any) (map[string]any, bool) {
	if states == nil {
		return nil, false
	}
	raw, ok := states["workflow"]
	if !ok {
		return nil, false
	}
	state, ok := raw.(map[string]any)
	if !ok {
		return nil, false
	}
	return state, true
}

type moduTUIWorkflowRun struct {
	ID                string
	Name              string
	Status            string
	ScriptPath        string
	SnapshotPath      string
	AgentCount        int
	DoneCount         int
	RunningAgentCount int
	ErrorCount        int
	CurrentPhase      string
	UpdatedAt         int64
	DurationMs        int64
	Logs              []string
	Phases            []moduTUIWorkflowPhase
	Agents            []moduTUIWorkflowAgent
}

type moduTUIWorkflowPhase struct {
	Title           string
	AgentCount      int
	DoneCount       int
	RunningCount    int
	ErrorCount      int
	EstimatedTokens int
	DurationMs      int64
}

type moduTUIWorkflowAgent struct {
	ID              int
	Label           string
	Phase           string
	Status          string
	PromptPreview   string
	ResultPreview   string
	Error           string
	EstimatedTokens int
	TurnTokens      int
	RecentToolCalls int
	FailedToolCalls int
	ToolCalls       []moduTUIWorkflowToolCall
}

type moduTUIWorkflowToolCall struct {
	ToolName      string
	ArgsPreview   string
	ResultPreview string
	IsError       bool
}

func moduTUIWorkflowRuns(value any) []moduTUIWorkflowRun {
	items, ok := value.([]map[string]any)
	if !ok {
		rawItems, ok := value.([]any)
		if !ok {
			return nil
		}
		items = make([]map[string]any, 0, len(rawItems))
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if ok {
				items = append(items, item)
			}
		}
	}
	runs := make([]moduTUIWorkflowRun, 0, len(items))
	for _, item := range items {
		run := moduTUIWorkflowRun{
			ID:                moduTUIRuntimeStateString(item["id"]),
			Name:              moduTUIRuntimeStateString(item["name"]),
			Status:            moduTUIRuntimeStateString(item["status"]),
			ScriptPath:        moduTUIRuntimeStateString(item["scriptPath"]),
			SnapshotPath:      moduTUIRuntimeStateString(item["snapshotPath"]),
			AgentCount:        moduTUIRuntimeStateNumber(item["agentCount"]),
			DoneCount:         moduTUIRuntimeStateNumber(item["doneCount"]),
			RunningAgentCount: moduTUIRuntimeStateNumber(item["runningAgentCount"]),
			ErrorCount:        moduTUIRuntimeStateNumber(item["errorCount"]),
			CurrentPhase:      moduTUIRuntimeStateString(item["currentPhase"]),
			UpdatedAt:         int64(moduTUIRuntimeStateNumber(item["updatedAt"])),
			DurationMs:        int64(moduTUIRuntimeStateNumber(item["durationMs"])),
			Logs:              moduTUIRuntimeStateStrings(item["logs"]),
			Phases:            moduTUIWorkflowPhases(item["phases"]),
			Agents:            moduTUIWorkflowAgents(item["agents"]),
		}
		if run.Status == "" {
			run.Status = "unknown"
		}
		if run.ID == "" {
			run.ID = "latest"
		}
		if run.AgentCount == 0 && len(run.Agents) > 0 {
			run.AgentCount = len(run.Agents)
		}
		runs = append(runs, run)
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].UpdatedAt > runs[j].UpdatedAt })
	return runs
}

func moduTUIWorkflowPhases(value any) []moduTUIWorkflowPhase {
	items := moduTUIRuntimeStateMaps(value)
	phases := make([]moduTUIWorkflowPhase, 0, len(items))
	for _, item := range items {
		phases = append(phases, moduTUIWorkflowPhase{
			Title:           moduTUIRuntimeStateString(item["title"]),
			AgentCount:      moduTUIRuntimeStateNumber(item["agentCount"]),
			DoneCount:       moduTUIRuntimeStateNumber(item["doneCount"]),
			RunningCount:    moduTUIRuntimeStateNumber(item["runningCount"]),
			ErrorCount:      moduTUIRuntimeStateNumber(item["errorCount"]),
			EstimatedTokens: moduTUIRuntimeStateNumber(item["estimatedTokens"]),
			DurationMs:      int64(moduTUIRuntimeStateNumber(item["durationMs"])),
		})
	}
	return phases
}

func moduTUIWorkflowAgents(value any) []moduTUIWorkflowAgent {
	items := moduTUIRuntimeStateMaps(value)
	agents := make([]moduTUIWorkflowAgent, 0, len(items))
	for _, item := range items {
		agents = append(agents, moduTUIWorkflowAgent{
			ID:              moduTUIRuntimeStateNumber(item["id"]),
			Label:           moduTUIRuntimeStateString(item["label"]),
			Phase:           moduTUIRuntimeStateString(item["phase"]),
			Status:          moduTUIRuntimeStateString(item["status"]),
			PromptPreview:   moduTUIRuntimeStateString(item["promptPreview"]),
			ResultPreview:   moduTUIRuntimeStateString(item["resultPreview"]),
			Error:           moduTUIRuntimeStateString(item["error"]),
			EstimatedTokens: moduTUIRuntimeStateNumber(item["estimatedTokens"]),
			TurnTokens:      moduTUIRuntimeStateNumber(item["turnTokens"]),
			RecentToolCalls: moduTUIRuntimeStateNumber(item["recentToolCalls"]),
			FailedToolCalls: moduTUIRuntimeStateNumber(item["failedToolCalls"]),
			ToolCalls:       moduTUIWorkflowToolCalls(item["recentToolCallPreviews"]),
		})
	}
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	return agents
}

func moduTUIWorkflowToolCalls(value any) []moduTUIWorkflowToolCall {
	items := moduTUIRuntimeStateMaps(value)
	calls := make([]moduTUIWorkflowToolCall, 0, len(items))
	for _, item := range items {
		calls = append(calls, moduTUIWorkflowToolCall{
			ToolName:      moduTUIRuntimeStateString(item["toolName"]),
			ArgsPreview:   moduTUIRuntimeStateString(item["argsPreview"]),
			ResultPreview: moduTUIRuntimeStateString(item["resultPreview"]),
			IsError:       moduTUIRuntimeStateBool(item["isError"]),
		})
	}
	return calls
}

func moduTUIRuntimeStateMaps(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, raw := range items {
			if item, ok := raw.(map[string]any); ok {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func moduTUIRuntimeStateStrings(value any) []string {
	switch items := value.(type) {
	case []string:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(item) != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			text := moduTUIRuntimeStateString(item)
			if strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func moduTUIRuntimeStateString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}

func moduTUIRuntimeStateNumber(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case int32:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func moduTUIRuntimeStateBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func moduTUITruncate(text string, limit int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func runModuTUIModelSelect(ctx context.Context, session *coding_agent.CodingSession, send func(tea.Msg)) {
	if session == nil || send == nil {
		return
	}
	models := session.GetAvailableModels()
	sort.Slice(models, func(i, j int) bool {
		if models[i].ProviderID == models[j].ProviderID {
			return models[i].ID < models[j].ID
		}
		return models[i].ProviderID < models[j].ProviderID
	})
	if len(models) == 0 {
		send(modutui.AppendMessageMsg{Message: modutui.Message{
			Role: modutui.RoleAssistant,
			Text: "no models configured",
		}})
		return
	}
	current := session.GetModel()
	options := make([]modutui.HumanPromptOption, 0, min(len(models), 9))
	for i, model := range models {
		if i >= 9 {
			break
		}
		label := model.Name
		if strings.TrimSpace(label) == "" {
			label = model.ID
		}
		label = fmt.Sprintf("%s (%s / %s)", label, model.ProviderID, model.ID)
		if current != nil && current.ProviderID == model.ProviderID && current.ID == model.ID {
			label = "* " + label
		}
		options = append(options, modutui.HumanPromptOption{
			Label: label,
			Value: model.ProviderID + "/" + model.ID,
		})
	}
	ch := make(chan string, 1)
	send(modutui.RequestHumanPromptMsg{
		Request: modutui.HumanPromptRequest{
			ID:           "model-select",
			Title:        "Model",
			Body:         "Choose active model",
			Options:      options,
			DefaultIndex: -1,
		},
		Respond: ch,
	})
	var target string
	select {
	case target = <-ch:
	case <-ctx.Done():
		send(modutui.CancelHumanPromptMsg{ID: "model-select"})
		return
	}
	target = strings.TrimSpace(target)
	if target == "" {
		send(modutui.SetStatusMsg{Status: "model unchanged", TransientFor: moduTUITerminalStatusTTL})
		return
	}
	providerID, modelID, ok := strings.Cut(target, "/")
	if !ok || providerID == "" || modelID == "" {
		send(modutui.AppendMessageMsg{Message: modutui.Message{Role: modutui.RoleAssistant, Text: "invalid model selection"}})
		return
	}
	before := session.GetModel()
	if err := session.SetModelByID(providerID, modelID); err != nil {
		send(modutui.AppendMessageMsg{Message: modutui.Message{Role: modutui.RoleAssistant, Text: "error: " + err.Error()}})
		send(modutui.SetStatusMsg{Status: "model unchanged", TransientFor: moduTUITerminalStatusTTL})
		return
	}
	after := session.GetModel()
	if before != nil && after != nil && before.ProviderID == after.ProviderID && before.ID == after.ID {
		send(modutui.SetStatusMsg{Status: "model unchanged", TransientFor: moduTUITerminalStatusTTL})
		return
	}
	send(modutui.SetStatusMsg{Status: "model changed; context cleared", TransientFor: moduTUITerminalStatusTTL})
}

func moduTUISlashRunningStatus(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "running slash command"
	}
	return "running " + fields[0]
}

func moduTUIInfoCardLines(session *coding_agent.CodingSession, model *types.Model) []string {
	lines := []string{"modu_code"}
	if model != nil {
		if label := moduTUIModelLabel(model); label != "" {
			lines = append(lines, "model: "+label)
		}
	}
	if session != nil {
		if cwd := strings.TrimSpace(session.RuntimeState().Cwd); cwd != "" {
			lines = append(lines, "cwd: "+cwd)
		}
		if id := shortModuTUISessionID(session.GetSessionID()); id != "" {
			lines = append(lines, "session: "+id)
		}
	}
	lines = append(lines, "commands: type /  send: Enter  quit: Ctrl+C")
	return lines
}

func moduTUIFooter(session *coding_agent.CodingSession) string {
	if session == nil {
		return "ctx - · - · -"
	}
	model := session.GetModel()
	parts := []string{moduTUIContextUsage(session, model)}
	if label := moduTUIModelLabel(model); label != "" {
		parts = append(parts, label)
	} else {
		parts = append(parts, "-")
	}
	cwd := strings.TrimSpace(session.RuntimeState().Cwd)
	if cwd == "" {
		cwd = session.Cwd()
	}
	if cwd == "" {
		cwd = "-"
	}
	parts = append(parts, compactModuTUICwd(cwd))
	return strings.Join(parts, " · ")
}

func moduTUIContextUsage(session *coding_agent.CodingSession, model *types.Model) string {
	used := 0
	if session != nil {
		used = session.GetSessionStats().TotalTokens
	}
	limit := 0
	if model != nil {
		limit = model.ContextWindow
	}
	if limit <= 0 {
		return "ctx " + formatModuTUITokens(used)
	}
	return fmt.Sprintf("ctx %s/%s", formatModuTUITokens(used), formatModuTUITokens(limit))
}

func moduTUIModelLabel(model *types.Model) string {
	if model == nil {
		return ""
	}
	label := strings.TrimSpace(model.Name)
	if label == "" {
		label = strings.TrimSpace(model.ID)
	}
	return label
}

func compactModuTUICwd(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "-" {
		return "-"
	}
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	sep := string(os.PathSeparator)
	parts := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) <= 2 {
		if volume != "" {
			return volume + strings.Join(parts, sep)
		}
		return strings.Join(parts, sep)
	}
	return "…" + sep + filepath.Join(parts[len(parts)-2:]...)
}

func formatModuTUITokens(tokens int) string {
	if tokens < 0 {
		tokens = 0
	}
	if tokens < 1000 {
		return strconv.Itoa(tokens)
	}
	if tokens < 1_000_000 {
		value := float64(tokens) / 1000
		if tokens < 10_000 {
			return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", value), "0"), ".") + "K"
		}
		return fmt.Sprintf("%.0fK", value)
	}
	value := float64(tokens) / 1_000_000
	return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", value), "0"), ".") + "M"
}

func moduTUITodos(session *coding_agent.CodingSession) []modutui.TodoItem {
	if session == nil {
		return nil
	}
	todos := session.GetTodos()
	out := make([]modutui.TodoItem, 0, len(todos))
	for _, todo := range todos {
		out = append(out, modutui.TodoItem{Content: todo.Content, Status: todo.Status})
	}
	return out
}

func shortModuTUISessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func isSessionAgentSlash(session *coding_agent.CodingSession, line string) bool {
	if session == nil || !strings.HasPrefix(strings.TrimSpace(line), "/") {
		return false
	}
	cmd := strings.TrimPrefix(strings.TrimSpace(line), "/")
	if i := strings.IndexAny(cmd, " \t\n\r"); i >= 0 {
		cmd = cmd[:i]
	}
	if cmd == "" {
		return false
	}
	if session.HasSlashCommand(cmd) {
		return true
	}
	for _, skill := range session.GetSkills() {
		if skill.Name == cmd {
			return true
		}
	}
	for _, prompt := range session.GetPromptTemplates() {
		if prompt.Name == cmd {
			return true
		}
	}
	return false
}

func moduTUIQueueCommand(line string) (modutui.SubmitKind, string, bool) {
	line = strings.TrimSpace(line)
	for _, item := range []struct {
		kind  modutui.SubmitKind
		names []string
	}{
		{kind: modutui.SubmitKindSteer, names: []string{"/steer", "/s"}},
		{kind: modutui.SubmitKindFollowUp, names: []string{"/followup", "/f"}},
	} {
		for _, name := range item.names {
			if line == name {
				return item.kind, "", true
			}
			if strings.HasPrefix(line, name+" ") {
				return item.kind, strings.TrimSpace(strings.TrimPrefix(line, name)), true
			}
		}
	}
	return "", "", false
}

func loadModuTUIInputHistory(path string) ([]string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	out := make([]string, 0, min(len(items), 100))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	if len(out) > 100 {
		out = append([]string(nil), out[len(out)-100:]...)
	}
	return out, nil
}

func saveModuTUIInputHistory(path string, history []string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if len(history) > 100 {
		history = history[len(history)-100:]
	}
	return os.WriteFile(path, []byte(strings.Join(history, "\n")+"\n"), 0o600)
}

func moduTUISlashCommands(session *coding_agent.CodingSession) []modutui.SlashCommand {
	seen := map[string]struct{}{}
	add := func(out *[]modutui.SlashCommand, name, desc string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		*out = append(*out, modutui.SlashCommand{Name: name, Description: strings.TrimSpace(desc)})
	}

	var out []modutui.SlashCommand
	for _, cmd := range baseModuTUISlashCommands() {
		add(&out, cmd.Name, cmd.Description)
	}
	if session == nil {
		return out
	}
	for _, cmd := range session.RegisteredSlashCommands() {
		add(&out, cmd.Name, cmd.Description)
	}
	for _, skill := range session.GetSkills() {
		add(&out, skill.Name, skill.Description)
	}
	for _, prompt := range session.GetPromptTemplates() {
		desc := prompt.Description
		if prompt.ArgumentHint != "" {
			if desc != "" {
				desc += " "
			}
			desc += "(" + prompt.ArgumentHint + ")"
		}
		add(&out, prompt.Name, desc)
	}
	return out
}

func baseModuTUISlashCommands() []modutui.SlashCommand {
	return []modutui.SlashCommand{
		{Name: "/help", Description: "Show available commands"},
		{Name: "/clear", Description: "Clear the current session"},
		{Name: "/config", Description: "Configure providers and models"},
		{Name: "/model", Description: "Switch the active model"},
		{Name: "/workflows", Description: "Show workflow cockpit"},
		{Name: "/compact", Description: "Manually trigger context compaction"},
		{Name: "/tokens", Description: "Show token usage"},
		{Name: "/context", Description: "Show loaded context"},
		{Name: "/session", Description: "Show current session information"},
		{Name: "/sessions", Description: "List or manage saved sessions"},
		{Name: "/tree", Description: "Show conversation tree"},
		{Name: "/fork", Description: "Fork from an entry id"},
		{Name: "/tools", Description: "List active tools"},
		{Name: "/skills", Description: "List available skills"},
		{Name: "/prompts", Description: "List prompt templates"},
		{Name: "/steer", Description: "Steer the active task"},
		{Name: "/s", Description: "Alias for /steer"},
		{Name: "/followup", Description: "Queue a follow-up message"},
		{Name: "/f", Description: "Alias for /followup"},
		{Name: "/plan", Description: "Show or update plan mode"},
		{Name: "/worktree", Description: "Manage the current worktree"},
		{Name: "/quit", Description: "Exit modu_code"},
	}
}

func initialTerminalSize(fd int, fallbackWidth, fallbackHeight int) (int, int) {
	width, height, err := term.GetSize(fd)
	if err != nil || width <= 0 || height <= 0 {
		return fallbackWidth, fallbackHeight
	}
	return width, height
}

type moduTUIPrompter struct {
	ctx  context.Context
	send func(tea.Msg)
}

func (p *moduTUIPrompter) Confirm(title, body string, defaultYes bool) bool {
	defaultIndex := 1
	if defaultYes {
		defaultIndex = 0
	}
	choice := p.requestHumanPrompt(modutui.HumanPromptRequest{
		Title: title,
		Body:  body,
		Options: []modutui.HumanPromptOption{
			{Label: "Yes", Value: "yes"},
			{Label: "No", Value: "no"},
		},
		DefaultIndex: defaultIndex,
	})
	if choice == "" {
		return defaultYes
	}
	return choice == "yes"
}

func (p *moduTUIPrompter) Select(title string, options []string) string {
	if len(options) == 0 {
		return ""
	}
	promptOptions := make([]modutui.HumanPromptOption, 0, len(options))
	for _, option := range options {
		promptOptions = append(promptOptions, modutui.HumanPromptOption{Label: option, Value: option})
	}
	choice := p.requestHumanPrompt(modutui.HumanPromptRequest{
		Title:        title,
		Options:      promptOptions,
		DefaultIndex: 0,
	})
	if choice == "" {
		return options[0]
	}
	return choice
}

func (p *moduTUIPrompter) ApprovePlan(plan string, steps []string) string {
	body := strings.TrimSpace(plan)
	if len(steps) > 0 {
		body += "\n\n" + strings.Join(steps, "\n")
	}
	choice := p.requestHumanPrompt(modutui.HumanPromptRequest{
		Title: "Plan approval required",
		Body:  body,
		Options: []modutui.HumanPromptOption{
			{Label: "Approve", Value: "approve"},
			{Label: "Approve + auto", Value: "approve_auto"},
			{Label: "Reject", Value: "reject"},
		},
		DefaultIndex: 2,
	})
	switch choice {
	case "approve", "approve_auto":
		return choice
	default:
		return "reject: rejected in modu-tui"
	}
}

func (p *moduTUIPrompter) ApproveTool(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error) {
	if p == nil || p.send == nil {
		return types.ToolApprovalDeny, nil
	}
	ch := make(chan modutui.ToolApprovalDecision, 1)
	p.send(modutui.RequestToolApprovalMsg{
		Request: modutui.ToolApprovalRequest{
			ID:       toolCallID,
			ToolName: toolName,
			Summary:  "approval required: " + toolName,
			Detail:   toolInputFromArgs(toolName, args),
		},
		Respond: ch,
	})
	select {
	case decision := <-ch:
		return toolApprovalDecisionToTypes(decision), nil
	case <-p.ctx.Done():
		return types.ToolApprovalDeny, p.ctx.Err()
	}
}

func (p *moduTUIPrompter) notify(summary, detail string) {
	if p == nil || p.send == nil {
		return
	}
	p.send(modutui.AppendMessageMsg{Message: modutui.Message{
		Tool:     true,
		ToolName: "approval",
		Summary:  summary,
		Detail:   detail,
		Expanded: true,
	}})
}

func (p *moduTUIPrompter) requestHumanPrompt(req modutui.HumanPromptRequest) string {
	if p == nil || p.send == nil {
		return ""
	}
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ch := make(chan string, 1)
	p.send(modutui.RequestHumanPromptMsg{
		Request: req,
		Respond: ch,
	})
	select {
	case choice := <-ch:
		return choice
	case <-ctx.Done():
		return ""
	}
}

func messagesFromAgentEvent(ev types.Event) []modutui.Message {
	return messagesFromAgentEventWithCwd(ev, "")
}

func messagesFromAgentEventWithCwd(ev types.Event, cwd string) []modutui.Message {
	switch ev.Type {
	case types.EventTypeMessageEnd:
		if isUserMessage(ev.Message) {
			return nil
		}
		return messagesFromAgentMessageWithCwd(ev.Message, cwd)
	case types.EventTypeToolExecutionStart:
		input := toolInputFromArgs(ev.ToolName, ev.Args)
		return []modutui.Message{{
			Tool:           true,
			ToolID:         ev.ToolCallID,
			ToolName:       toolRenderNameFromArgsWithCwd(ev.ToolName, ev.Args, cwd),
			Summary:        toolRunningSummaryFromArgs(ev.ToolName, ev.Args),
			Detail:         input,
			ToolInput:      input,
			ToolOutput:     toolPreviewOutputFromArgsWithCwd(ev.ToolName, ev.Args, cwd),
			ToolCode:       toolCodeFromArgsWithCwd(ev.ToolName, ev.Args, cwd),
			ToolLanguage:   toolLanguageFromArgsWithCwd(ev.ToolName, ev.Args, cwd),
			ToolNoCollapse: isWriteLikeTool(ev.ToolName),
			Expanded:       isWriteLikeTool(ev.ToolName),
		}}
	case types.EventTypeToolExecutionEnd:
		output := toolOutputFromResult(ev.ToolName, ev.IsError, ev.Result)
		return []modutui.Message{{
			Tool:           true,
			ToolID:         ev.ToolCallID,
			ToolName:       toolRenderName(ev.ToolName, ev.Result),
			Summary:        toolDoneSummary(ev.ToolName, ev.IsError, output),
			ToolOutput:     toolDisplayOutput(ev.ToolName, ev.IsError, output),
			ToolCode:       toolCodeFromResult(ev.ToolName, output),
			ToolLanguage:   toolLanguageFromResult(ev.ToolName),
			ToolError:      ev.IsError,
			ToolDone:       true,
			ToolNoCollapse: isWriteLikeTool(ev.ToolName),
			Expanded:       ev.IsError || isWriteLikeTool(ev.ToolName),
		}}
	default:
		return nil
	}
}

func isUserMessage(msg types.AgentMessage) bool {
	switch msg.(type) {
	case types.UserMessage, *types.UserMessage:
		return true
	default:
		return false
	}
}

func messagesFromAgentMessages(messages []types.AgentMessage) []modutui.Message {
	return messagesFromAgentMessagesWithCwd(messages, "")
}

func messagesFromSessionTranscript(session *coding_agent.CodingSession) []modutui.Message {
	if session == nil {
		return nil
	}
	agentMessages := session.GetMessages()
	nodes := session.GetSessionTreeNodes()
	if len(nodes) == 0 {
		return messagesFromAgentMessagesWithCwd(agentMessages, session.Cwd())
	}
	out := make([]modutui.Message, 0, len(agentMessages))
	messageIndex := 0
	for _, node := range nodes {
		if !node.InCurrentPath {
			continue
		}
		switch node.Type {
		case "message":
			if messageIndex >= len(agentMessages) {
				continue
			}
			out = append(out, messagesFromAgentMessageWithCwd(agentMessages[messageIndex], session.Cwd())...)
			messageIndex++
		case "compaction":
			out = append(out, contextCompactMessage())
		}
	}
	for messageIndex < len(agentMessages) {
		out = append(out, messagesFromAgentMessageWithCwd(agentMessages[messageIndex], session.Cwd())...)
		messageIndex++
	}
	return out
}

func messagesFromAgentMessagesWithCwd(messages []types.AgentMessage, cwd string) []modutui.Message {
	out := make([]modutui.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, messagesFromAgentMessageWithCwd(msg, cwd)...)
	}
	return out
}

func messagesFromAgentMessage(msg types.AgentMessage) []modutui.Message {
	return messagesFromAgentMessageWithCwd(msg, "")
}

func messagesFromAgentMessageWithCwd(msg types.AgentMessage, cwd string) []modutui.Message {
	switch m := msg.(type) {
	case types.UserMessage:
		return []modutui.Message{{Role: modutui.RoleUser, Text: contentText(m.Content)}}
	case *types.UserMessage:
		if m == nil {
			return nil
		}
		return []modutui.Message{{Role: modutui.RoleUser, Text: contentText(m.Content)}}
	case types.AssistantMessage:
		return messagesFromAssistantMessageWithCwd(m, cwd)
	case *types.AssistantMessage:
		if m == nil {
			return nil
		}
		return messagesFromAssistantMessageWithCwd(*m, cwd)
	case types.ToolResultMessage:
		return []modutui.Message{messageFromToolResultWithCwd(m, cwd)}
	case *types.ToolResultMessage:
		if m == nil {
			return nil
		}
		return []modutui.Message{messageFromToolResultWithCwd(*m, cwd)}
	default:
		return nil
	}
}

func messagesFromAssistantMessage(msg types.AssistantMessage) []modutui.Message {
	return messagesFromAssistantMessageWithCwd(msg, "")
}

func messagesFromAssistantMessageWithCwd(msg types.AssistantMessage, cwd string) []modutui.Message {
	var thinking []string
	var out []modutui.Message
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *types.TextContent:
			if b != nil && strings.TrimSpace(b.Text) != "" {
				out = append(out, modutui.Message{Role: modutui.RoleAssistant, Text: b.Text})
			}
		case *types.ThinkingContent:
			if b != nil && strings.TrimSpace(b.Thinking) != "" {
				thinking = append(thinking, strings.TrimSpace(b.Thinking))
			}
		case *types.ToolCallContent:
			if b != nil {
				input := toolInputFromArgs(b.Name, b.Arguments)
				out = append(out, modutui.Message{
					Tool:           true,
					ToolID:         b.ID,
					ToolName:       toolRenderNameFromArgsWithCwd(b.Name, b.Arguments, cwd),
					Summary:        toolRunningSummaryFromArgs(b.Name, b.Arguments),
					Detail:         input,
					ToolInput:      input,
					ToolOutput:     toolPreviewOutputFromArgsWithCwd(b.Name, b.Arguments, cwd),
					ToolCode:       toolCodeFromArgsWithCwd(b.Name, b.Arguments, cwd),
					ToolLanguage:   toolLanguageFromArgsWithCwd(b.Name, b.Arguments, cwd),
					ToolNoCollapse: isWriteLikeTool(b.Name),
					Expanded:       isWriteLikeTool(b.Name),
				})
			}
		}
	}
	if len(thinking) > 0 {
		out = append([]modutui.Message{{
			Role:     modutui.RoleAssistant,
			Text:     strings.Join(thinking, "\n\n"),
			Thinking: true,
		}}, out...)
	}
	if len(out) == 0 && msg.ErrorMessage != "" {
		out = append(out, modutui.Message{Role: modutui.RoleAssistant, Text: "error: " + msg.ErrorMessage})
	}
	return out
}

func messageFromToolResult(msg types.ToolResultMessage) modutui.Message {
	return messageFromToolResultWithCwd(msg, "")
}

func messageFromToolResultWithCwd(msg types.ToolResultMessage, cwd string) modutui.Message {
	output := toolOutputFromContent(msg.ToolName, msg.IsError, msg.Content)
	return modutui.Message{
		Tool:           true,
		ToolID:         msg.ToolCallID,
		ToolName:       toolRenderName(msg.ToolName, nil),
		Summary:        toolDoneSummary(msg.ToolName, msg.IsError, output),
		ToolOutput:     toolDisplayOutput(msg.ToolName, msg.IsError, output),
		ToolCode:       toolCodeFromResult(msg.ToolName, output),
		ToolLanguage:   toolLanguageFromResult(msg.ToolName),
		ToolError:      msg.IsError,
		ToolDone:       true,
		ToolNoCollapse: isWriteLikeTool(msg.ToolName),
		Expanded:       msg.IsError || isWriteLikeTool(msg.ToolName),
	}
}

func toolRunningSummary(toolName string) string {
	return toolRunningSummaryFromArgs(toolName, nil)
}

func toolRunningSummaryFromArgs(toolName string, args any) string {
	if strings.EqualFold(toolName, "bash") {
		return "Running shell command"
	}
	if strings.EqualFold(toolName, "read") {
		n := readFileCountFromArgs(args)
		if n == 1 {
			return "Read 1 file"
		}
		return fmt.Sprintf("Read %d files", n)
	}
	if strings.EqualFold(toolName, "grep") {
		return "Search files"
	}
	if strings.EqualFold(toolName, "find") {
		return "Find files"
	}
	if strings.EqualFold(toolName, "ls") {
		return "List directory"
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	return "Running " + name
}

func readFileCountFromArgs(args any) int {
	count := 0
	for _, key := range []string{"path", "file_path"} {
		if value, ok := mapStringValue(args, key); ok && strings.TrimSpace(value) != "" {
			count++
		}
	}
	for _, key := range []string{"paths", "file_paths"} {
		count += mapStringSliceCount(args, key)
	}
	if count == 0 {
		return 1
	}
	return count
}

func grepDoneSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" || strings.EqualFold(output, "No matches found.") || strings.EqualFold(output, "No files found") {
		return "Found 0 matches"
	}
	if n, ok := firstIntAfterPrefix(output, "Found ", " file(s)"); ok {
		return fmt.Sprintf("Found %d files", n)
	}
	if n, ok := firstIntAfterPrefixAfterLastNewline(output, "Found ", " total occurrence(s)"); ok {
		return fmt.Sprintf("Found %d matches", n)
	}
	return fmt.Sprintf("Found %d matches", countResultLines(output))
}

func findDoneSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" || strings.EqualFold(output, "No files found") {
		return "Found 0 files"
	}
	return fmt.Sprintf("Found %d files", countResultLines(output))
}

func lsDoneSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" || strings.EqualFold(output, "(empty directory)") {
		return "Listed 0 entries"
	}
	return fmt.Sprintf("Listed %d entries", countResultLines(output))
}

func countResultLines(output string) int {
	count := 0
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "... (") || strings.HasPrefix(line, "(Results are truncated") {
			continue
		}
		count++
	}
	return count
}

func firstIntAfterPrefix(output, prefix, suffix string) (int, bool) {
	line, _, _ := strings.Cut(output, "\n")
	return parseIntBetween(line, prefix, suffix)
}

func firstIntAfterPrefixAfterLastNewline(output, prefix, suffix string) (int, bool) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if n, ok := parseIntBetween(strings.TrimSpace(lines[i]), prefix, suffix); ok {
			return n, true
		}
	}
	return 0, false
}

func parseIntBetween(text, prefix, suffix string) (int, bool) {
	if !strings.HasPrefix(text, prefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(text, prefix)
	if suffix != "" {
		idx := strings.Index(rest, suffix)
		if idx < 0 {
			return 0, false
		}
		rest = rest[:idx]
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	return n, err == nil
}

func toolDoneSummary(toolName string, isError bool, output string) string {
	if strings.EqualFold(toolName, "workflow") {
		if isError {
			return "Workflow failed"
		}
		if runID := moduTUIWorkflowRunIDFromNotify(output); runID != "" {
			return "Workflow started: " + runID
		}
		if first, _, ok := strings.Cut(strings.TrimSpace(output), "\n"); ok && strings.Contains(first, " completed with ") {
			return first
		}
		return "Workflow completed"
	}
	if strings.EqualFold(toolName, "bash") {
		if isError {
			return "Shell command failed"
		}
		return "Ran 1 shell command"
	}
	if strings.EqualFold(toolName, "read") && !isError {
		if strings.HasPrefix(output, "Read ") {
			return output
		}
		return "Read file"
	}
	if strings.EqualFold(toolName, "grep") {
		if isError {
			return "Search failed"
		}
		return grepDoneSummary(output)
	}
	if strings.EqualFold(toolName, "find") {
		if isError {
			return "Find failed"
		}
		return findDoneSummary(output)
	}
	if strings.EqualFold(toolName, "ls") {
		if isError {
			return "List directory failed"
		}
		return lsDoneSummary(output)
	}
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	if isError {
		return name + " failed"
	}
	return "Ran " + name
}

func isWriteLikeTool(toolName string) bool {
	return strings.EqualFold(toolName, "write") || strings.EqualFold(toolName, "edit")
}

func toolRenderName(toolName string, result any) string {
	if strings.EqualFold(toolName, "edit") {
		return "update"
	}
	if strings.EqualFold(toolName, "write") {
		if strings.EqualFold(toolResultStringDetail(result, "type"), "update") {
			return "update"
		}
	}
	return toolName
}

func toolRenderNameFromArgs(toolName string, args any) string {
	return toolRenderNameFromArgsWithCwd(toolName, args, "")
}

func toolRenderNameFromArgsWithCwd(toolName string, args any, cwd string) string {
	if strings.EqualFold(toolName, "edit") {
		return "update"
	}
	if strings.EqualFold(toolName, "write") && writeArgsExistingFileInCwd(args, cwd) {
		return "update"
	}
	return toolName
}

func toolDisplayOutput(toolName string, isError bool, output string) string {
	if isWriteLikeTool(toolName) && !isError {
		return ""
	}
	if strings.EqualFold(toolName, "workflow") && !isError {
		if runID := moduTUIWorkflowRunIDFromNotify(output); runID != "" {
			return "Opened workflow run panel: " + runID
		}
		if first, _, ok := strings.Cut(strings.TrimSpace(output), "\n"); ok && strings.Contains(first, " completed with ") {
			return "Opened workflow run panel: latest"
		}
	}
	return output
}

func toolPreviewOutputFromArgs(toolName string, args any) string {
	return toolPreviewOutputFromArgsWithCwd(toolName, args, "")
}

func toolPreviewOutputFromArgsWithCwd(toolName string, args any, cwd string) string {
	if strings.EqualFold(toolName, "edit") {
		oldText, _ := firstStringValue(args, "old_text", "old_string")
		newText, _ := firstStringValue(args, "new_text", "new_string")
		removed := countContentLines(oldText)
		added := countContentLines(newText)
		return fmt.Sprintf("Added %d lines, removed %d lines", added, removed)
	}
	if strings.EqualFold(toolName, "write") {
		content, ok := mapStringValue(args, "content")
		if !ok {
			return ""
		}
		if oldContent, ok := previewFileContentFromArgs(args, cwd); ok {
			added, removed := changedLineCounts(oldContent, content)
			return fmt.Sprintf("Added %d lines, removed %d lines", added, removed)
		}
		lines := countContentLines(content)
		bytes := len([]byte(content))
		if lines == 1 {
			return fmt.Sprintf("Wrote 1 line, %d bytes", bytes)
		}
		return fmt.Sprintf("Wrote %d lines, %d bytes", lines, bytes)
	}
	return ""
}

func toolCodeFromArgs(toolName string, args any) string {
	return toolCodeFromArgsWithCwd(toolName, args, "")
}

func toolCodeFromArgsWithCwd(toolName string, args any, cwd string) string {
	if strings.EqualFold(toolName, "write") {
		content, _ := mapStringValue(args, "content")
		if diff := contextualWriteDiffFromArgs(args, content, cwd); diff != "" {
			return diff
		}
		return numberedContent(content)
	}
	if strings.EqualFold(toolName, "edit") {
		oldText, _ := firstStringValue(args, "old_text", "old_string")
		newText, _ := firstStringValue(args, "new_text", "new_string")
		if diff := contextualEditDiffFromArgs(args, oldText, newText, cwd); diff != "" {
			return diff
		}
		return simpleEditDiff(oldText, newText)
	}
	return ""
}

func toolCodeFromResult(toolName string, output string) string {
	if strings.EqualFold(toolName, "edit") {
		return editDiffFromOutput(output)
	}
	return ""
}

func toolLanguageFromResult(toolName string) string {
	if strings.EqualFold(toolName, "edit") {
		return "diff"
	}
	return ""
}

func toolLanguageFromArgs(toolName string, args any) string {
	return toolLanguageFromArgsWithCwd(toolName, args, "")
}

func toolLanguageFromArgsWithCwd(toolName string, args any, cwd string) string {
	if strings.EqualFold(toolName, "edit") {
		return "diff"
	}
	if strings.EqualFold(toolName, "write") {
		if writeArgsExistingFileInCwd(args, cwd) {
			return "diff"
		}
		path, _ := firstStringValue(args, "path", "file_path")
		return languageFromPath(path)
	}
	return ""
}

func simpleEditDiff(oldText, newText string) string {
	var lines []string
	for _, line := range splitContentLines(oldText) {
		lines = append(lines, "- "+line)
	}
	for _, line := range splitContentLines(newText) {
		lines = append(lines, "+ "+line)
	}
	return strings.Join(lines, "\n")
}

func editDiffFromOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	if idx := strings.Index(output, "\n\n--- "); idx >= 0 {
		return strings.TrimSpace(output[idx+2:])
	}
	if strings.HasPrefix(output, "--- ") {
		return output
	}
	return ""
}

func contextualEditDiffFromArgs(args any, oldText, newText, cwd string) string {
	if strings.TrimSpace(oldText) == "" {
		return ""
	}
	path, _ := firstStringValue(args, "path", "file_path")
	fileContent, ok := previewFileContentInCwd(path, cwd)
	if !ok {
		return ""
	}
	return replacementPreviewDiff(path, fileContent, oldText, newText)
}

func contextualWriteDiffFromArgs(args any, newContent string, cwd string) string {
	path, _ := firstStringValue(args, "path", "file_path")
	oldContent, ok := previewFileContentInCwd(path, cwd)
	if !ok || oldContent == newContent {
		return ""
	}
	return contentPreviewDiff(path, oldContent, newContent)
}

func previewFileContentFromArgs(args any, cwd string) (string, bool) {
	path, _ := firstStringValue(args, "path", "file_path")
	return previewFileContentInCwd(path, cwd)
}

func previewFileContent(path string) (string, bool) {
	return previewFileContentInCwd(path, "")
}

func previewFileContentInCwd(path, cwd string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		if strings.TrimSpace(cwd) != "" {
			path = filepath.Join(cwd, path)
		} else {
			path = filepath.Clean(path)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func writeArgsExistingFile(args any) bool {
	return writeArgsExistingFileInCwd(args, "")
}

func writeArgsExistingFileInCwd(args any, cwd string) bool {
	path, _ := firstStringValue(args, "path", "file_path")
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		if strings.TrimSpace(cwd) != "" {
			path = filepath.Join(cwd, path)
		} else {
			path = filepath.Clean(path)
		}
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func replacementPreviewDiff(path, fileContent, oldText, newText string) string {
	idx := strings.Index(fileContent, oldText)
	if idx < 0 {
		return ""
	}
	startLine := strings.Count(fileContent[:idx], "\n") + 1
	oldLines := splitContentLines(oldText)
	newLines := splitContentLines(newText)
	fileLines := splitContentLines(fileContent)
	return localizedPreviewDiff(path, fileLines, startLine, oldLines, newLines)
}

func contentPreviewDiff(path, oldContent, newContent string) string {
	oldLines := splitContentLines(oldContent)
	newLines := splitContentLines(newContent)
	prefix, suffix := commonLineWindow(oldLines, newLines)
	removed := oldLines[prefix : len(oldLines)-suffix]
	added := newLines[prefix : len(newLines)-suffix]
	return localizedPreviewDiff(path, oldLines, prefix+1, removed, added)
}

func localizedPreviewDiff(path string, fileLines []string, startLine int, removed, added []string) string {
	const contextLines = 3
	if len(removed) == 0 && len(added) == 0 {
		return ""
	}
	if startLine < 1 {
		startLine = 1
	}
	contextStart := startLine - 1 - contextLines
	if contextStart < 0 {
		contextStart = 0
	}
	afterStart := startLine - 1 + len(removed)
	contextEnd := afterStart + contextLines
	if contextEnd > len(fileLines) {
		contextEnd = len(fileLines)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", path)
	fmt.Fprintf(&sb, "+++ %s\n", path)
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", startLine, len(removed), startLine, len(added))
	for i := contextStart; i < startLine-1 && i < len(fileLines); i++ {
		fmt.Fprintf(&sb, "  %d  %s\n", i+1, fileLines[i])
	}
	for i, line := range removed {
		fmt.Fprintf(&sb, "- %d  %s\n", startLine+i, line)
	}
	for i, line := range added {
		fmt.Fprintf(&sb, "+ %d  %s\n", startLine+i, line)
	}
	for i := afterStart; i < contextEnd && i < len(fileLines); i++ {
		fmt.Fprintf(&sb, "  %d  %s\n", i+1, fileLines[i])
	}
	return strings.TrimRight(sb.String(), "\n")
}

func commonLineWindow(oldLines, newLines []string) (prefix, suffix int) {
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix &&
		oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	return prefix, suffix
}

func changedLineCounts(oldContent, newContent string) (added, removed int) {
	oldLines := splitContentLines(oldContent)
	newLines := splitContentLines(newContent)
	prefix, suffix := commonLineWindow(oldLines, newLines)
	return len(newLines) - prefix - suffix, len(oldLines) - prefix - suffix
}

func numberedContent(content string) string {
	lines := splitContentLines(content)
	if len(lines) == 0 {
		return ""
	}
	width := len(fmt.Sprintf("%d", len(lines)))
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		out = append(out, fmt.Sprintf("%*d  %s", width, i+1, line))
	}
	return strings.Join(out, "\n")
}

func splitContentLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(text, "\n"), "\n")
}

func countContentLines(text string) int {
	return len(splitContentLines(text))
}

func firstStringValue(v any, keys ...string) (string, bool) {
	for _, key := range keys {
		if value, ok := mapStringValue(v, key); ok {
			return value, true
		}
	}
	return "", false
}

func toolResultStringDetail(result any, key string) string {
	details := toolResultDetails(result)
	if details == nil {
		return ""
	}
	if value, ok := details[key].(string); ok {
		return value
	}
	return ""
}

func toolResultDetails(result any) map[string]any {
	switch r := result.(type) {
	case types.ToolResult:
		if m, ok := r.Details.(map[string]any); ok {
			return m
		}
	case *types.ToolResult:
		if r != nil {
			if m, ok := r.Details.(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

func languageFromPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".jsx":
		return "jsx"
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "markdown"
	case ".py":
		return "python"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".yaml", ".yml":
		return "yaml"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".sql":
		return "sql"
	default:
		return ""
	}
}

func toolInputFromArgs(toolName string, args any) string {
	if strings.EqualFold(toolName, "bash") {
		if command, ok := mapStringValue(args, "command"); ok {
			return command
		}
	}
	if strings.EqualFold(toolName, "read") {
		return readInputFromArgs(args)
	}
	if isWriteLikeTool(toolName) {
		if path, ok := firstStringValue(args, "path", "file_path"); ok {
			return path
		}
	}
	return formatJSON(args)
}

func mapStringValue(v any, key string) (string, bool) {
	switch m := v.(type) {
	case map[string]any:
		value, ok := m[key].(string)
		return value, ok
	case map[string]string:
		value, ok := m[key]
		return value, ok
	default:
		return "", false
	}
}

func mapStringSliceCount(v any, key string) int {
	var raw any
	switch m := v.(type) {
	case map[string]any:
		raw = m[key]
	case map[string][]string:
		raw = m[key]
	case map[string]string:
		return 0
	default:
		return 0
	}
	switch values := raw.(type) {
	case []string:
		count := 0
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				count++
			}
		}
		return count
	case []any:
		count := 0
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				count++
			}
		}
		return count
	default:
		return 0
	}
}

func toolOutputFromResult(toolName string, isError bool, result any) string {
	switch r := result.(type) {
	case types.ToolResult:
		if text := toolOutputFromContent(toolName, isError || r.IsError, r.Content); text != "" {
			return text
		}
		return formatJSON(r.Details)
	case *types.ToolResult:
		if r == nil {
			return ""
		}
		if text := toolOutputFromContent(toolName, isError || r.IsError, r.Content); text != "" {
			return text
		}
		return formatJSON(r.Details)
	default:
		return formatJSON(result)
	}
}

func toolOutputFromContent(toolName string, isError bool, content []types.ContentBlock) string {
	text := contentBlocksText(content)
	if strings.EqualFold(toolName, "read") && !isError {
		return readOutputSummary(text)
	}
	return text
}

func readInputFromArgs(args any) string {
	path, _ := mapStringValue(args, "path")
	if path == "" {
		return formatJSON(args)
	}
	start := intArgValue(args, "offset", 1)
	limit := intArgValue(args, "limit", 0)
	if limit > 0 {
		return fmt.Sprintf("%s · lines %d-%d", path, start, start+limit-1)
	}
	if start > 1 {
		return fmt.Sprintf("%s · lines %d-", path, start)
	}
	return path
}

func readOutputSummary(text string) string {
	count := 0
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if readResultLine(line) {
			count++
		}
	}
	if count == 1 {
		return "Read 1 line"
	}
	return fmt.Sprintf("Read %d lines", count)
}

func readResultLine(line string) bool {
	tab := strings.IndexByte(line, '\t')
	if tab <= 0 {
		return false
	}
	for _, r := range line[:tab] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func intArgValue(v any, key string, fallback int) int {
	switch m := v.(type) {
	case map[string]any:
		return intValue(m[key], fallback)
	case map[string]string:
		return intValue(m[key], fallback)
	default:
		return fallback
	}
}

func intValue(v any, fallback int) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(n, "%d", &parsed); err == nil {
			return parsed
		}
	}
	return fallback
}

func messageFromSessionEvent(ev coding_agent.SessionEvent) (modutui.Message, bool) {
	switch ev.Type {
	case coding_agent.SessionEventModelChange:
		return infoMessage("model: " + ev.Provider + "/" + ev.ModelID), true
	case coding_agent.SessionEventCompactionDone:
		return contextCompactMessage(), true
	case coding_agent.SessionEventThinkingChange:
		return infoMessage("thinking: " + ev.Level), true
	case coding_agent.SessionEventCwdChanged:
		return infoMessage("cwd: " + ev.NewCwd), true
	case coding_agent.SessionEventWorktreeCreate:
		return infoMessage("worktree: " + ev.Path), true
	case coding_agent.SessionEventWorktreeRemove:
		return infoMessage("worktree removed: " + ev.Path), true
	case coding_agent.SessionEventSubagentStart:
		return infoMessage("subagent start: " + ev.SubagentName + "\n" + ev.SubagentTask), true
	case coding_agent.SessionEventSubagentStop:
		text := "subagent stop: " + ev.SubagentName
		if ev.ErrorMessage != "" {
			text += "\nerror: " + ev.ErrorMessage
		}
		if ev.SubagentResult != "" {
			text += "\n" + ev.SubagentResult
		}
		return infoMessage(text), true
	case coding_agent.SessionEventPermissionReq:
		return infoMessage("permission requested: " + ev.ToolName), true
	case coding_agent.SessionEventPermissionDeny:
		text := "permission denied: " + ev.ToolName
		if ev.Reason != "" {
			text += "\n" + ev.Reason
		}
		return infoMessage(text), true
	case coding_agent.SessionEventExtensionNotify:
		text := ev.Message
		if ev.ExtensionName != "" {
			text = ev.ExtensionName + ": " + text
		}
		return infoMessage(text), true
	default:
		return modutui.Message{}, false
	}
}

func contextCompactMessage() modutui.Message {
	return modutui.Message{
		Role:         modutui.RoleAssistant,
		Text:         moduTUIContextCompactDivider,
		Preformatted: true,
		Plain:        true,
	}
}

func infoMessage(text string) modutui.Message {
	return modutui.Message{Role: modutui.RoleAssistant, Text: strings.TrimSpace(text)}
}

func contentText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []types.ContentBlock:
		return contentBlocksText(c)
	default:
		return fmt.Sprint(c)
	}
}

func contentBlocksText(blocks []types.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case *types.TextContent:
			if b != nil && b.Text != "" {
				parts = append(parts, b.Text)
			}
		case *types.ThinkingContent:
			if b != nil && b.Thinking != "" {
				parts = append(parts, b.Thinking)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func formatJSON(v any) string {
	if v == nil {
		return ""
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

func toolApprovalDecisionToTypes(decision modutui.ToolApprovalDecision) types.ToolApprovalDecision {
	switch decision {
	case modutui.ToolApprovalAllow:
		return types.ToolApprovalAllow
	case modutui.ToolApprovalAllowAlways:
		return types.ToolApprovalAllowAlways
	case modutui.ToolApprovalDenyAlways:
		return types.ToolApprovalDenyAlways
	default:
		return types.ToolApprovalDeny
	}
}
