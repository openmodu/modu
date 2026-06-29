package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func runModuTUI(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions) error {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		_ = n
	}

	initial := messagesFromAgentMessagesWithCwd(session.GetMessages(), session.Cwd())
	var program *tea.Program
	var programMu sync.RWMutex
	var promptMu sync.Mutex
	var currentCancel context.CancelFunc
	var currentPromptID int
	var nextPromptID int
	var continueQueuedAfterCancel bool
	var foregroundMu sync.Mutex
	var foregroundRuns int
	send := func(msg tea.Msg) {
		programMu.RLock()
		p := program
		programMu.RUnlock()
		if p != nil {
			p.Send(msg)
		}
	}
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
		go func() {
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
		}()
	}
	runAgentLoop := func(run func(context.Context) error) {
		markForegroundRunStart()
		go func() {
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
					nextRun = ag.Continue
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
		}()
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
		go func() {
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
		}()
	}
	queueSteer := func(text string, requireActive bool) {
		// Same hazard as interruptPrompt: this runs synchronously from the
		// Model.Update loop (submit / slash hooks), and session.Abort plus send
		// (program.Send) would block the event loop when the message channel is
		// full — readily so over SSH. Run it off-loop. Ordering is preserved
		// within the goroutine: Steer enqueues and continueQueuedAfterCancel is
		// set before cancel(), all before runAgentLoop reads it post-cancel.
		go func() {
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
		}()
	}
	submit := func(ev modutui.SubmitEvent) {
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
				go runModuTUISlash(ctx, line, session, model, send, isForegroundRunActive)
			},
			Interrupt: interruptPrompt,
		},
	})
	sendFooter = func() {
		send(modutui.SetFooterMsg{Footer: moduTUIFooter(session)})
	}

	unsubAgent := session.Subscribe(func(ev types.Event) {
		durationTracker.Handle(ev)
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
	go func() {
		session.EmitStartupEvent()
		session.EmitExtensionEvent("ui_ready")
	}()
	_, err := prog.Run()
	return err
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

func runModuTUISlash(ctx context.Context, line string, session *coding_agent.CodingSession, model *types.Model, send func(tea.Msg), keepAgentBusy func() bool) {
	send(modutui.SetBusyMsg{Busy: true})
	send(modutui.SetStatusMsg{Status: "running slash command"})
	defer func() {
		send(modutui.SetTodosMsg{Todos: moduTUITodos(session)})
		if keepAgentBusy != nil && keepAgentBusy() {
			return
		}
		send(modutui.SetBusyMsg{Busy: false})
		send(modutui.SetStatusMsg{Status: "idle"})
	}()

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
		{Name: "/model", Description: "Switch the active model"},
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
