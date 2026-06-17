package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/approval"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/slash"
	"github.com/openmodu/modu/pkg/tgbot"
	"github.com/openmodu/modu/pkg/types"
)

type bubbleTUI struct {
	ctx          context.Context
	session      *coding_agent.CodingSession
	modelInfo    *types.Model
	histFile     string
	promptMu     *sync.Mutex
	commandHooks CommandHooks
	program      *tea.Program
	inline       bool

	model *uiModel

	useDiff  bool
	renderer *diffRenderer
	// pendingScroll holds completed lines waiting to be committed to real
	// terminal scrollback on the next paint (diff mode only). Completed turns
	// live in native scrollback — reflowed by the terminal on resize, never
	// re-owned by the renderer — so resizing no longer wipes history.
	pendingScroll []string

	// Incremental scrollback streaming (diff mode): while an assistant block
	// streams, its already-rendered top lines are committed to native scrollback
	// so the live region the diff renderer owns never grows past the screen.
	// A live region taller than the screen would scroll its top into scrollback
	// where a resize repaint can't clear it (stacked ghost frames). streamBlockIdx
	// is the index of the block currently being streamed-out (-1 = none),
	// streamCommitN how many of its clamped lines are already in scrollback, and
	// streamLines caches this paint's clamped block lines so renderInlineLive
	// reuses them instead of re-glamouring.
	streamBlockIdx         int
	streamCommitN          int
	streamCommittedContent string
	streamLines            []string

	// Diff-mode render throttle: coalesce paints to ~60fps so a burst of
	// streaming/agent events doesn't repaint the whole frame per token. A
	// suppressed paint sets paintPending and schedules a trailing flush
	// (bubblePaintMsg) so the final frame of a burst always lands.
	lastPaintNano int64
	paintPending  bool

	// Real-cursor caret position for the active input, computed each frame by
	// fullScreenLines and placed by the diff renderer (see PlaceCaret). Drives
	// IME composition-window anchoring; caretActive is false for popup/approval
	// states that draw their own markers.
	caretActive bool
	caretRow    int
	caretCol    int

	draft       string
	cursor      int
	history     []string
	historyIdx  int
	historyHold string
	pastes      []pasteEntry

	slashMatches []slashCommandDef
	slashIndex   int

	modelChoices      []*types.Model
	modelAllChoices   []*types.Model
	modelSelectIdx    int
	modelSelectScroll int
	modelSearch       string
	modelScopedOnly   bool
	modelScopeEdit    bool
	modelScopedIDs    map[string]bool

	configAction          string
	configFields          []configInputField
	configFieldIdx        int
	configInput           ConfigModelInput
	configChoices         []ConfigModelEntry
	configAllChoices      []ConfigModelEntry
	configSelectIdx       int
	configSelectScroll    int
	configSearch          string
	configMenuChoices     []configMenuChoice
	configMenuIdx         int
	configProviderInput   ConfigProviderInput
	configProviderChoices []ConfigProviderEntry
	configProviderAll     []ConfigProviderEntry

	width  int
	height int

	lastFailedPrompt          string
	continueQueuedAfterCancel bool
	quitting                  bool
}

type bubbleTickMsg time.Time

// bubblePaintMsg flushes a paint that the render throttle deferred.
type bubblePaintMsg struct{}

// paintInterval bounds diff-mode repaints to ~60fps.
const paintInterval = 16 * time.Millisecond

type bubbleAgentMsg struct {
	event types.Event
}

type bubbleSessionMsg struct {
	event coding_agent.SessionEvent
}

type bubbleApprovalMsg struct {
	request approval.Request
}

type bubbleApprovalCancelMsg struct {
	toolCallID string
}

type bubblePromptDoneMsg struct {
	err          error
	failedPrompt string
}

type bubbleSlashDoneMsg struct {
	line        string
	handled     bool
	exit        bool
	clear       bool
	lines       []string
	routePrompt bool
}

type bubbleConfigDoneMsg struct {
	out string
	err error
}

type bubbleShellDoneMsg struct {
	command     string
	output      string
	sendToModel bool
}

type bubbleModelSwitchDoneMsg struct {
	modelID string
	changed bool
	err     error
}

type bubbleExternalInfoMsg struct {
	text string
}

type bubbleExternalUserMsg struct {
	text string
}

type bubbleClearScreenMsg struct{}

func RunBubbleTeaWithOptions(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions) error {
	return runBubbleWithOptions(ctx, session, model, noApprove, opts, false)
}

func RunBubbleInlineWithOptions(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions) error {
	return runBubbleWithOptions(ctx, session, model, noApprove, opts, true)
}

func runBubbleWithOptions(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts RunOptions, inline bool) error {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		_ = n
	}

	histFile := session.InputHistoryFile()
	var approvalCh chan approval.Request
	if !noApprove {
		approvalCh = make(chan approval.Request)
	}
	promptMu := &sync.Mutex{}

	root := newBubbleTUI(ctx, session, model, histFile, promptMu, opts.CommandHooks)
	root.inline = inline
	root.loadHistory()

	progOpts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithoutSignalHandler()}
	if os.Getenv("MODU_TUI_DIFF") == "1" {
		// Hybrid diff-renderer mode: bubbletea handles input/events only, we own
		// rendering via diffRenderer for pi-style clean resize. See bubble_diff.go.
		root.useDiff = true
		root.renderer = newDiffRenderer(os.Stdout)
		progOpts = append(progOpts, tea.WithoutRenderer())
	}
	prog := tea.NewProgram(root, progOpts...)
	root.program = prog
	// diffCleanup restores cooked mode + the hardware cursor. It is idempotent
	// (sync.Once) and must run BEFORE the exit prints below, otherwise the
	// fmt.Println output staircases — in raw mode "\n" line-feeds without a
	// carriage return. Deferred as a backstop for early/panic returns.
	diffCleanup := func() {}
	if root.useDiff {
		diffCleanup = startDiffMode(ctx, prog, root)
		defer diffCleanup()
	}

	if approvalCh != nil {
		go watchBubbleApprovals(ctx, prog, approvalCh)
		installBubbleApprovalCallbacks(ctx, session, approvalCh)
		go session.EmitExtensionEvent("ui_ready")
	}

	unsub := session.Subscribe(func(ev types.Event) {
		prog.Send(bubbleAgentMsg{event: ev})
	})
	defer unsub()

	unsubSession := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		prog.Send(bubbleSessionMsg{event: ev})
	})
	defer unsubSession()

	printer := &bubbleBridgePrinter{program: prog}
	token := os.Getenv("MOMS_TG_TOKEN")
	if tgCfg, err := tgbot.LoadConfig(); err == nil && tgCfg.Token != "" {
		token = tgCfg.Token
	}
	if token != "" {
		attachDir := os.TempDir() + "/modu_code_tg"
		if username, err := tgbot.Start(ctx, token, attachDir, session, printer, promptMu, approvalCh); err == nil {
			root.model.tgUsername = username
		}
	}

	_, err := prog.Run()
	diffCleanup() // restore cooked mode before the plain exit prints below
	if meta := strings.TrimSpace(root.model.renderExitSessionMeta()); meta != "" {
		fmt.Println(meta)
	}
	if hint := strings.TrimSpace(root.model.renderResumeHint()); hint != "" {
		fmt.Println(hint)
	}
	return err
}

func newBubbleTUI(ctx context.Context, session *coding_agent.CodingSession, modelInfo *types.Model, histFile string, promptMu *sync.Mutex, hooks CommandHooks) *bubbleTUI {
	m := newUIModel(ctx, session, modelInfo, histFile, nil, promptMu, "")
	m.ready = true
	m.state = uiStateInput
	m.width = 80
	m.height = 24
	return &bubbleTUI{
		ctx:            ctx,
		session:        session,
		modelInfo:      modelInfo,
		histFile:       histFile,
		promptMu:       promptMu,
		commandHooks:   hooks,
		model:          m,
		width:          80,
		height:         24,
		historyIdx:     0,
		streamBlockIdx: -1,
	}
}

func watchBubbleApprovals(ctx context.Context, prog *tea.Program, approvalCh <-chan approval.Request) {
	for {
		select {
		case req := <-approvalCh:
			prog.Send(bubbleApprovalMsg{request: req})
		case <-ctx.Done():
			return
		}
	}
}

func installBubbleApprovalCallbacks(ctx context.Context, session *coding_agent.CodingSession, approvalCh chan<- approval.Request) {
	session.SetPrompter(newChannelPrompter(ctx, approvalCh, nil))
}

func (b *bubbleTUI) Init() tea.Cmd {
	if b.session != nil {
		b.session.RefreshRuntimeStateAsync()
	}
	if b.inline {
		return tea.Sequence(b.printInlineHeaderCmd(), bubbleTick())
	}
	return bubbleTick()
}

func bubbleTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return bubbleTickMsg(t)
	})
}

func (b *bubbleTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := b.dispatch(msg)
	// In diff mode we own output: repaint the whole frame after every message
	// so the renderer reconciles the terminal (cheap — only changed lines, full
	// clear+repaint only on resize). The repaint is throttled to ~60fps so a
	// stream of events coalesces into one frame instead of one repaint each.
	//
	// A bubblePaintMsg already painted in dispatch — it must NOT call requestPaint
	// again, or the throttle would schedule a fresh flush after every flush and
	// spin forever (a burst kicks it off, then it self-sustains at ~60fps).
	if b.useDiff {
		if _, isFlush := msg.(bubblePaintMsg); !isFlush {
			if pc := b.requestPaint(); pc != nil {
				cmd = tea.Batch(cmd, pc)
			}
		}
	}
	return model, cmd
}

// requestPaint paints immediately if at least paintInterval has elapsed since
// the last paint; otherwise it defers a single trailing flush so the last frame
// of a burst still renders. Returns the flush command, or nil if it painted now
// (or a flush is already scheduled).
func (b *bubbleTUI) requestPaint() tea.Cmd {
	now := time.Now().UnixNano()
	if now-b.lastPaintNano >= int64(paintInterval) {
		b.actualPaint(now)
		return nil
	}
	if b.paintPending {
		return nil
	}
	b.paintPending = true
	wait := time.Duration(int64(paintInterval) - (now - b.lastPaintNano))
	return tea.Tick(wait, func(time.Time) tea.Msg { return bubblePaintMsg{} })
}

func (b *bubbleTUI) actualPaint(now int64) {
	b.lastPaintNano = now
	b.paintPending = false
	b.paint()
}

func (b *bubbleTUI) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bubbleTickMsg:
		return b, bubbleTick()
	case bubblePaintMsg:
		if b.paintPending {
			b.actualPaint(time.Now().UnixNano())
		}
		return b, nil
	case tea.WindowSizeMsg:
		// bubbletea emits a bogus {0,0} at startup under WithoutRenderer (it
		// never set ttyOutput); our SIGWINCH watcher feeds the real size.
		if b.useDiff && msg.Width <= 0 {
			return b, nil
		}
		b.width = max(20, msg.Width)
		b.height = max(8, msg.Height)
		b.model.width = max(20, msg.Width-2)
		b.model.height = b.height
		return b, nil
	case tea.KeyPressMsg:
		return b.updateKey(msg)
	case tea.PasteMsg:
		return b.updatePaste(msg)
	case bubbleAgentMsg:
		return b, b.handleAgentEvent(msg.event)
	case bubbleSessionMsg:
		return b, b.handleSessionEvent(msg.event)
	case bubbleApprovalMsg:
		return b, b.handleApprovalRequest(msg.request)
	case bubbleApprovalCancelMsg:
		if b.model.pendingPerm != nil && b.model.pendingPerm.ToolCallID == msg.toolCallID {
			b.model.pendingPerm = nil
			b.model.state = uiStateQuerying
			b.model.statusMsg = "approval dismissed"
		}
		return b, nil
	case bubblePromptDoneMsg:
		return b, b.finishPromptOperation(msg.err, msg.failedPrompt)
	case bubbleSlashDoneMsg:
		return b.handleSlashDone(msg)
	case bubbleConfigDoneMsg:
		return b, b.handleConfigDone(msg)
	case bubbleConfigModelsMsg:
		b.handleConfigModels(msg)
		return b, nil
	case bubbleConfigProvidersMsg:
		b.handleConfigProviders(msg)
		return b, nil
	case bubbleShellDoneMsg:
		return b.handleShellDone(msg)
	case bubbleModelSwitchDoneMsg:
		return b, b.handleModelSwitchDone(msg)
	case bubbleExternalInfoMsg:
		block := uiBlock{Kind: "section", Title: "modu_code", Content: msg.text, Timestamp: time.Now()}
		b.appendBlock(block)
		return b, b.printBlockCmd(block)
	case bubbleExternalUserMsg:
		block := uiBlock{Kind: "user", Content: msg.text, Source: "external", Timestamp: time.Now()}
		b.appendBlock(block)
		return b, b.printBlockCmd(block)
	case bubbleClearScreenMsg:
		b.model.blocks = nil
		b.model.clearPromptError()
		b.model.setTransientStatus("cleared")
		return b, nil
	}
	return b, nil
}

func (b *bubbleTUI) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if b.model.state == uiStatePermission {
		return b, b.updatePermissionKey(msg)
	}
	if b.model.state == uiStatePlanReject {
		return b, b.updatePlanRejectKey(msg)
	}
	if b.model.state == uiStateModelSelect {
		return b.updateModelSelectKey(msg)
	}
	if b.model.state == uiStateConfigMenu {
		return b.updateConfigMenuKey(msg)
	}
	if b.model.state == uiStateConfigInput {
		return b, b.updateConfigInputKey(msg)
	}
	if b.model.state == uiStateConfigSelect {
		return b.updateConfigSelectKey(msg)
	}

	switch msg.String() {
	case "ctrl+c":
		if b.model.queryActive || b.model.pendingPerm != nil {
			b.abortQuery()
			return b, nil
		}
		b.quitting = true
		return b, tea.Quit
	case "ctrl+d":
		if strings.TrimSpace(b.draft) == "" {
			b.quitting = true
			return b, tea.Quit
		}
	case "esc":
		if b.model.queryActive {
			b.abortQuery()
			return b, nil
		}
	case "ctrl+o":
		b.model.transcriptMode = !b.model.transcriptMode
		if b.model.transcriptMode {
			b.model.setTransientStatus("tool output expanded")
		} else {
			b.model.setTransientStatus("tool output collapsed")
		}
		return b, nil
	case "ctrl+p":
		b.cycleModel("forward")
		return b, nil
	case "ctrl+n":
		b.cycleModel("backward")
		return b, nil
	case "ctrl+l":
		b.model.blocks = nil
		b.model.clearPromptError()
		b.model.setTransientStatus("cleared")
		return b, nil
	case "ctrl+j", "alt+enter":
		b.insertRune('\n')
		return b, nil
	case "enter":
		if len(b.slashMatches) > 0 {
			chosen := b.slashMatches[clampInt(b.slashIndex, 0, len(b.slashMatches)-1)].Name
			b.slashMatches = nil
			b.slashIndex = 0
			cmd := b.submit(chosen, submitModeNormal)
			return b, cmd
		}
		cmd := b.submit(b.draft, submitModeNormal)
		return b, cmd
	case "shift+enter":
		cmd := b.submit(b.draft, submitModeSteer)
		return b, cmd
	case "backspace":
		b.backspaceDraft()
		return b, nil
	case "delete":
		b.deleteDraft()
		return b, nil
	case "left":
		if b.cursor > 0 {
			b.cursor--
		}
		return b, nil
	case "right":
		if b.cursor < len([]rune(b.draft)) {
			b.cursor++
		}
		return b, nil
	case "home":
		b.cursor = inputLineStart([]rune(b.draft), b.cursor)
		return b, nil
	case "end":
		b.cursor = inputLineEnd([]rune(b.draft), b.cursor)
		return b, nil
	case "up":
		if len(b.slashMatches) > 0 {
			b.slashIndex = (b.slashIndex - 1 + len(b.slashMatches)) % len(b.slashMatches)
			return b, nil
		}
		rs := []rune(b.draft)
		if moved := moveInputCursorVertical(rs, b.cursor, -1); moved != b.cursor {
			b.cursor = moved
		} else {
			b.navigateHistory(-1)
		}
		return b, nil
	case "down":
		if len(b.slashMatches) > 0 {
			b.slashIndex = (b.slashIndex + 1) % len(b.slashMatches)
			return b, nil
		}
		rs := []rune(b.draft)
		if moved := moveInputCursorVertical(rs, b.cursor, 1); moved != b.cursor {
			b.cursor = moved
		} else {
			b.navigateHistory(1)
		}
	}

	for _, r := range msg.Text {
		b.insertRune(r)
	}
	if msg.String() == "tab" && b.completeSlashMatch() {
		return b, nil
	}
	return b, nil
}

// updatePaste handles bracketed-paste input. Under bubbletea v2 a paste arrives
// as a dedicated tea.PasteMsg rather than a key event with a Paste flag, so the
// collapse-into-placeholder logic that used to live in updateKey lives here.
//
// Multi-line pastes are collapsed into a single atomic placeholder rune
// (rendered as "[Pasted text +N lines]"), the way Claude Code does. Keeping the
// input box to one short line means it never wraps, so the terminal never
// reflows it on resize — avoiding the "串行" corruption. Paste is only consumed
// while editing the prompt draft; the modal states below ignore it, matching
// the pre-v2 behavior where updateKey returned early for those states.
func (b *bubbleTUI) updatePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	switch b.model.state {
	case uiStatePermission, uiStatePlanReject, uiStateModelSelect,
		uiStateConfigMenu, uiStateConfigInput, uiStateConfigSelect:
		return b, nil
	}
	text := msg.Content
	if strings.ContainsRune(text, '\n') {
		b.insertPaste(text)
		return b, nil
	}
	for _, r := range text {
		b.insertRune(r)
	}
	return b, nil
}

func (b *bubbleTUI) updatePermissionKey(msg tea.KeyPressMsg) tea.Cmd {
	perm := b.model.pendingPerm
	if perm == nil {
		b.model.state = uiStateInput
		return nil
	}
	if perm.ToolName == "exit_plan_mode" {
		switch msg.String() {
		case "enter", "y", "Y":
			b.resolvePlan("approve")
		case "a", "A":
			b.resolvePlan("approve_auto")
		case "esc", "n", "N":
			b.beginPlanReject()
		case "ctrl+c":
			b.abortQuery()
		}
		return nil
	}
	switch msg.String() {
	case "enter", "y", "Y":
		b.approve("allow")
	case "a", "A":
		b.approve("allow_always")
	case "esc", "n", "N":
		b.approve("deny")
	case "d", "D":
		b.approve("deny_always")
	case "ctrl+c":
		b.abortQuery()
	}
	return nil
}

func (b *bubbleTUI) updatePlanRejectKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c", "esc":
		b.resolvePlan("reject")
	case "enter":
		reason := strings.TrimSpace(b.model.planRejectBuf)
		if reason == "" {
			b.resolvePlan("reject")
		} else {
			b.resolvePlan("reject:" + reason)
		}
	case "backspace":
		rs := []rune(b.model.planRejectBuf)
		if len(rs) > 0 {
			b.model.planRejectBuf = string(rs[:len(rs)-1])
		}
	default:
		b.model.planRejectBuf += msg.Text
	}
	return nil
}

func (b *bubbleTUI) updateModelSelectKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		b.cancelModelSelect()
	case "up":
		b.moveModelSelect(-1)
	case "down":
		b.moveModelSelect(1)
	case "home":
		b.jumpModelSelect(0)
	case "end":
		b.jumpModelSelect(len(b.modelChoices) - 1)
	case "pgup":
		b.jumpModelSelect(b.modelSelectIdx - modelSelectVisibleRows)
	case "pgdown":
		b.jumpModelSelect(b.modelSelectIdx + modelSelectVisibleRows)
	case "backspace", "ctrl+h":
		b.backspaceModelSearch()
	case "tab":
		b.toggleModelScope()
	case "enter", "ctrl+j":
		return b, b.confirmModelSelect()
	case "space":
		b.toggleScopedModelSelection()
	default:
		runes := []rune(msg.Text)
		if len(runes) == 0 {
			return b, nil
		}
		if len(runes) == 1 {
			switch runes[0] {
			case 'j':
				b.moveModelSelect(1)
				return b, nil
			case 'k':
				b.moveModelSelect(-1)
				return b, nil
			case 'q', 'Q':
				b.cancelModelSelect()
				return b, nil
			case 'y', 'Y', 'l':
				return b, b.confirmModelSelect()
			case '1', '2', '3', '4', '5', '6', '7', '8', '9':
				idx := b.modelSelectScroll + int(runes[0]-'1')
				if idx >= 0 && idx < len(b.modelChoices) {
					b.modelSelectIdx = idx
					return b, b.confirmModelSelect()
				}
				return b, nil
			}
		}
		for _, r := range runes {
			if r >= 0x20 {
				b.appendModelSearch(r)
			}
		}
	}
	return b, nil
}

func (b *bubbleTUI) submit(text string, mode submitMode) tea.Cmd {
	line := strings.TrimSpace(b.expandPastes(text))
	if line == "" {
		if mode == submitModeSteer && b.model.queryActive {
			b.model.setTransientStatus("steer requires a message")
		}
		return nil
	}
	b.draft = ""
	b.cursor = 0
	b.pastes = nil
	b.slashMatches = nil
	b.slashIndex = 0
	b.appendHistory(line)

	if arg, ok := queueCommandArg(line, "/steer", "/s"); ok {
		b.submitQueueCommand("steer", arg)
		return nil
	}
	if arg, ok := queueCommandArg(line, "/followup", "/f"); ok {
		b.submitQueueCommand("followup", arg)
		return nil
	}
	if rest, ok := strings.CutPrefix(line, "!!"); ok {
		return b.runShell(strings.TrimSpace(rest), false)
	}
	if rest, ok := strings.CutPrefix(line, "!"); ok {
		return b.runShell(strings.TrimSpace(rest), true)
	}
	if strings.HasPrefix(line, "/") {
		return b.runLocalOrSlash(line)
	}
	if b.model.queryActive {
		if mode == submitModeSteer {
			b.queueSteer(line)
			return nil
		}
		b.queueFollowUp(line)
		return nil
	}
	return b.runPrompt(line)
}

func (b *bubbleTUI) runLocalOrSlash(line string) tea.Cmd {
	switch {
	case line == "/retry":
		return b.retryLastFailedPrompt()
	case line == "/config" || strings.HasPrefix(line, "/config "):
		return b.runConfigCommand(strings.TrimSpace(strings.TrimPrefix(line, "/config")))
	case line == "/settings":
		return b.appendSystemSection("Settings", "Bubble Tea settings selector is not migrated yet.\nUse /model list, /plan, /worktree, /sessions, /skills, or /prompts for command output.")
	case line == "/model" || strings.HasPrefix(line, "/model "):
		b.openModelSelect(strings.TrimSpace(strings.TrimPrefix(line, "/model")))
		return nil
	case line == "/scoped-models" || strings.HasPrefix(line, "/scoped-models "):
		return b.runScopedModelsCommand(strings.TrimSpace(strings.TrimPrefix(line, "/scoped-models")))
	case line == "/hotkeys":
		return b.appendSystemSection("Hotkeys", hotkeyHelpText())
	case line == "/plan":
		return b.appendSystemSection("Plan", planPanelContent(b.session))
	case line == "/worktree":
		return b.appendSystemSection("Worktree", worktreePanelContent(b.session))
	case line == "/queue" || strings.HasPrefix(line, "/queue "):
		b.runQueueCommand(strings.TrimSpace(strings.TrimPrefix(line, "/queue")))
		return nil
	}
	return b.runSlash(line)
}

func (b *bubbleTUI) runPrompt(line string) tea.Cmd {
	block := uiBlock{Kind: "user", Content: line, Source: "local", Timestamp: time.Now()}
	b.appendBlock(block)
	runCmd := b.runPromptOperation(line, func(ctx context.Context) error {
		return b.session.Prompt(ctx, line)
	})
	return tea.Sequence(b.printBlockCmd(block), runCmd)
}

func (b *bubbleTUI) runPromptOperation(failedPrompt string, run func(context.Context) error) tea.Cmd {
	b.model.queryActive = true
	b.model.state = uiStateQuerying
	b.model.setStatus("thinking")
	b.model.clearActivity()
	b.model.queryStartTime = time.Now()
	b.model.thinkingStart = time.Time{}
	queryCtx, queryCancel := context.WithCancel(b.ctx)
	b.model.queryCancel = queryCancel

	return func() tea.Msg {
		defer queryCancel()
		if b.promptMu != nil {
			b.promptMu.Lock()
			defer b.promptMu.Unlock()
		}
		select {
		case <-queryCtx.Done():
			return bubblePromptDoneMsg{err: queryCtx.Err(), failedPrompt: failedPrompt}
		default:
		}
		err := run(queryCtx)
		return bubblePromptDoneMsg{err: err, failedPrompt: failedPrompt}
	}
}

func (b *bubbleTUI) finishPromptOperation(err error, failedPrompt string) tea.Cmd {
	wasCanceled := errors.Is(err, context.Canceled)
	steeringCancel := wasCanceled && b.continueQueuedAfterCancel
	shouldContinue := b.session != nil &&
		b.session.GetAgent() != nil &&
		b.session.GetAgent().HasQueuedMessages() &&
		(err == nil || steeringCancel)
	b.continueQueuedAfterCancel = false
	if shouldContinue {
		b.markRunningQueuedUserBlock("done")
	}
	if shouldContinue {
		if cmd := b.continueQueuedPrompt(); cmd != nil {
			return cmd
		}
	}
	if err != nil && !wasCanceled {
		if failedPrompt != "" {
			b.lastFailedPrompt = failedPrompt
		}
		b.model.setPromptError(err)
	} else if err == nil || steeringCancel {
		b.lastFailedPrompt = ""
		b.model.clearPromptError()
	}
	finishErr := err
	if steeringCancel {
		finishErr = nil
	}
	b.markRunningQueuedUserBlock(queueStateForPromptError(err, steeringCancel))
	summary := b.model.finishActivity(finishErr)
	b.model.queryActive = false
	if b.model.statusMsg != "interrupted" {
		b.model.setStatus("")
	}
	b.model.state = uiStateInput
	// In inline mode, commit the completion summary to the permanent scrollback
	// (like Claude Code) instead of leaving it in the transient live region,
	// where it would vanish after transientActivityTTL. The summary must land
	// above the turn separator, so emit it first — in diff mode the scrollback
	// queue is ordered by call order, not by Cmd sequencing.
	var cmds []tea.Cmd
	if b.inline && summary != "" {
		b.model.clearActivity()
		if line := b.printStringCmd("  " + uiDimText.Render(summary)); line != nil {
			cmds = append(cmds, line)
		}
	}
	if sep := b.printTurnSeparatorCmd(); sep != nil {
		cmds = append(cmds, sep)
	}
	return tea.Sequence(cmds...)
}

func (b *bubbleTUI) continueQueuedPrompt() tea.Cmd {
	if b.session == nil {
		return nil
	}
	ag := b.session.GetAgent()
	if ag == nil || !ag.HasQueuedMessages() {
		return nil
	}
	b.markNextQueuedUserBlockRunning()
	return b.runPromptOperation("", func(ctx context.Context) error {
		return ag.Continue(ctx)
	})
}

func (b *bubbleTUI) runShell(shellCmd string, sendToModel bool) tea.Cmd {
	if shellCmd == "" {
		b.model.setTransientStatus("shell command is empty")
		return nil
	}
	block := uiBlock{Kind: "system", Content: "$ " + shellCmd, Timestamp: time.Now()}
	b.appendBlock(block)
	shellCmdExec := func() tea.Msg {
		cmd := exec.Command("bash", "-c", shellCmd)
		if cwd := b.currentWorkingDir(); cwd != "" {
			cmd.Dir = cwd
		}
		out, err := cmd.CombinedOutput()
		return bubbleShellDoneMsg{
			command:     shellCmd,
			output:      formatShellResult(out, err),
			sendToModel: sendToModel,
		}
	}
	return tea.Sequence(b.printBlockCmd(block), shellCmdExec)
}

func (b *bubbleTUI) handleShellDone(msg bubbleShellDoneMsg) (tea.Model, tea.Cmd) {
	block := uiBlock{Kind: "system", Content: msg.output, Timestamp: time.Now()}
	b.appendBlock(block)
	if msg.sendToModel {
		return b, tea.Sequence(b.printBlockCmd(block), b.runPrompt(formatShellPrompt(msg.command, msg.output)))
	}
	return b, b.printBlockCmd(block)
}

func (b *bubbleTUI) runSlash(line string) tea.Cmd {
	routePrompt := b.isSessionAgentSlash(line)
	return func() tea.Msg {
		printer := &bubbleSlashPrinter{}
		handled, exit := slash.Handle(b.ctx, line, b.session, printer, b.modelInfo)
		return bubbleSlashDoneMsg{
			line:        line,
			handled:     handled,
			exit:        exit,
			clear:       printer.clear,
			lines:       printer.lines,
			routePrompt: routePrompt && !handled,
		}
	}
}

func (b *bubbleTUI) handleSlashDone(msg bubbleSlashDoneMsg) (tea.Model, tea.Cmd) {
	if msg.routePrompt {
		return b, b.runPrompt(msg.line)
	}
	var block *uiBlock
	switch {
	case !msg.handled:
		next := uiBlock{Kind: "system", Content: "unknown command: " + msg.line, Timestamp: time.Now()}
		block = &next
		b.appendBlock(next)
	case msg.clear:
		b.model.blocks = nil
	case strings.TrimSpace(strings.Join(msg.lines, "\n")) != "":
		next := uiBlock{Kind: "section", Title: "modu_code", Content: strings.Join(msg.lines, "\n"), Timestamp: time.Now()}
		block = &next
		b.appendBlock(next)
	}
	if msg.exit {
		b.quitting = true
		return b, tea.Sequence(b.printBlockPtrCmd(block), tea.Quit)
	}
	return b, b.printBlockPtrCmd(block)
}

func (b *bubbleTUI) runConfigHook(args string) tea.Cmd {
	if b.commandHooks.Config == nil {
		b.model.setTransientStatus("config command is not available")
		return nil
	}
	return func() tea.Msg {
		out, err := b.commandHooks.Config(args)
		return bubbleConfigDoneMsg{out: out, err: err}
	}
}

func (b *bubbleTUI) handleConfigDone(msg bubbleConfigDoneMsg) tea.Cmd {
	content := strings.TrimSpace(msg.out)
	if msg.err != nil {
		if content != "" {
			content += "\n"
		}
		content += "error: " + msg.err.Error()
	}
	if content == "" {
		content = "config command completed"
	}
	block := uiBlock{Kind: "section", Title: "Config", Content: content, Timestamp: time.Now()}
	b.appendBlock(block)
	return b.printBlockCmd(block)
}

func (b *bubbleTUI) retryLastFailedPrompt() tea.Cmd {
	prompt := strings.TrimSpace(b.lastFailedPrompt)
	if prompt == "" {
		b.model.setTransientStatus("no failed prompt to retry")
		return nil
	}
	b.model.setTransientStatus("retrying last prompt")
	return b.runPrompt(prompt)
}

func (b *bubbleTUI) submitQueueCommand(kind, text string) {
	if text == "" {
		b.model.setTransientStatus("/" + kind + " requires a message")
		return
	}
	if !b.model.queryActive {
		b.model.setTransientStatus("no active task to " + kind)
		return
	}
	if kind == "steer" {
		b.queueSteer(text)
		return
	}
	b.queueFollowUp(text)
}

func (b *bubbleTUI) queueFollowUp(line string) {
	if b.session == nil {
		b.model.setTransientStatus("session is not available")
		return
	}
	b.session.FollowUp(line)
	b.appendBlock(uiBlock{Kind: "user", Content: line, Source: "followup", QueueState: "queued", Timestamp: time.Now()})
	b.model.setStatus("queued follow-up")
}

func (b *bubbleTUI) queueSteer(line string) {
	if b.session == nil {
		b.model.setTransientStatus("session is not available")
		return
	}
	b.session.Steer(line)
	b.appendBlock(uiBlock{Kind: "user", Content: line, Source: "steer", QueueState: "queued", Timestamp: time.Now()})
	b.continueQueuedAfterCancel = true
	if b.model.queryCancel != nil {
		b.model.queryCancel()
	}
	b.session.Abort()
	b.session.AbortBash()
	b.model.setStatus("steering")
}

func (b *bubbleTUI) runQueueCommand(args string) {
	if b.session == nil || b.session.GetAgent() == nil {
		b.model.setTransientStatus("session is not available")
		return
	}
	ag := b.session.GetAgent()
	fields := strings.Fields(args)
	if len(fields) == 0 {
		b.appendSystemSection("Queue", queuePanelContent(ag))
		return
	}
	switch fields[0] {
	case "clear":
		b.runQueueClearCommand(ag, fields[1:])
	case "drop":
		if len(fields) > 1 {
			b.model.setTransientStatus("usage: /queue drop")
			return
		}
		kind, ok := ag.DropLastQueuedMessage()
		if !ok {
			b.model.setTransientStatus("queue is empty")
			return
		}
		b.model.setTransientStatus("dropped " + kind)
	default:
		b.model.setTransientStatus("usage: /queue [clear [steer|followup]|drop]")
	}
}

func (b *bubbleTUI) runQueueClearCommand(ag *agent.Agent, fields []string) {
	if len(fields) == 0 {
		ag.ClearAllQueues()
		b.model.setTransientStatus("queue cleared")
		return
	}
	if len(fields) > 1 {
		b.model.setTransientStatus("usage: /queue clear [steer|followup]")
		return
	}
	switch fields[0] {
	case "steer", "steering":
		ag.ClearSteeringQueue()
		b.model.setTransientStatus("steer queue cleared")
	case "followup", "follow-up", "followups":
		ag.ClearFollowUpQueue()
		b.model.setTransientStatus("follow-up queue cleared")
	default:
		b.model.setTransientStatus("usage: /queue clear [steer|followup]")
	}
}

func (b *bubbleTUI) openModelSelect(initialSearch ...string) {
	if b.session == nil {
		b.model.statusMsg = "no session"
		return
	}
	allChoices := b.session.GetAllAvailableModels()
	sortTUIModels(allChoices, b.session.GetModel())
	if len(allChoices) == 0 {
		b.model.statusMsg = "no models configured"
		return
	}
	b.modelAllChoices = allChoices
	b.modelScopedOnly = len(b.session.GetScopedModelIDs()) > 0
	b.modelSearch = ""
	if len(initialSearch) > 0 {
		b.modelSearch = strings.TrimSpace(initialSearch[0])
	}
	b.filterModelChoices()
	b.modelSelectIdx = currentModelChoiceIndex(b.modelChoices, b.session.GetModel())
	b.adjustModelSelectScroll()
	b.model.state = uiStateModelSelect
	b.model.statusMsg = ""
	b.slashMatches = nil
}

func (b *bubbleTUI) openScopedModelsSelect() {
	b.openModelSelect()
	if b.model.state != uiStateModelSelect {
		return
	}
	b.modelScopeEdit = true
	b.modelScopedOnly = false
	b.modelScopedIDs = make(map[string]bool)
	scoped := b.session.GetScopedModelIDs()
	if len(scoped) == 0 {
		for _, model := range b.modelAllChoices {
			b.modelScopedIDs[model.ID] = true
		}
	} else {
		for _, id := range scoped {
			b.modelScopedIDs[id] = true
		}
	}
	b.filterModelChoices()
}

func sortTUIModels(models []*types.Model, current *types.Model) {
	sort.Slice(models, func(i, j int) bool {
		if sameTUIModel(models[i], current) {
			return true
		}
		if sameTUIModel(models[j], current) {
			return false
		}
		if models[i].ProviderID == models[j].ProviderID {
			return models[i].ID < models[j].ID
		}
		return models[i].ProviderID < models[j].ProviderID
	})
}

func (b *bubbleTUI) filterModelChoices() {
	choices := b.modelAllChoices
	if b.modelScopedOnly && b.session != nil {
		scoped := make(map[string]struct{})
		for _, id := range b.session.GetScopedModelIDs() {
			scoped[id] = struct{}{}
		}
		if len(scoped) > 0 {
			filtered := make([]*types.Model, 0, len(choices))
			for _, model := range choices {
				if _, ok := scoped[model.ID]; ok {
					filtered = append(filtered, model)
				}
			}
			choices = filtered
		}
	}
	query := strings.ToLower(strings.TrimSpace(b.modelSearch))
	if query != "" {
		filtered := make([]*types.Model, 0, len(choices))
		for _, model := range choices {
			haystack := strings.ToLower(model.ProviderID + " " + model.ID + " " + model.Name + " " + model.ProviderID + "/" + model.ID)
			if strings.Contains(haystack, query) {
				filtered = append(filtered, model)
			}
		}
		choices = filtered
	}
	b.modelChoices = choices
	if b.modelSelectIdx >= len(b.modelChoices) {
		b.modelSelectIdx = max(0, len(b.modelChoices)-1)
	}
	b.adjustModelSelectScroll()
}

func (b *bubbleTUI) cancelModelSelect() {
	if b.modelScopeEdit {
		b.closeModelSelect("model scope closed")
		return
	}
	b.closeModelSelect("model unchanged")
}

func (b *bubbleTUI) moveModelSelect(delta int) {
	if len(b.modelChoices) == 0 {
		return
	}
	b.modelSelectIdx = (b.modelSelectIdx + delta + len(b.modelChoices)) % len(b.modelChoices)
	b.adjustModelSelectScroll()
}

func (b *bubbleTUI) jumpModelSelect(idx int) {
	if len(b.modelChoices) == 0 {
		return
	}
	b.modelSelectIdx = clampInt(idx, 0, len(b.modelChoices)-1)
	b.adjustModelSelectScroll()
}

func (b *bubbleTUI) appendModelSearch(ch rune) {
	if ch == 0 {
		return
	}
	b.modelSearch += string(ch)
	b.modelSelectIdx = 0
	b.filterModelChoices()
}

func (b *bubbleTUI) backspaceModelSearch() {
	rs := []rune(b.modelSearch)
	if len(rs) == 0 {
		return
	}
	b.modelSearch = string(rs[:len(rs)-1])
	b.modelSelectIdx = 0
	b.filterModelChoices()
}

func (b *bubbleTUI) toggleModelScope() {
	if b.modelScopeEdit {
		return
	}
	if b.session == nil || len(b.session.GetScopedModelIDs()) == 0 {
		b.model.statusMsg = "no scoped models configured"
		return
	}
	b.modelScopedOnly = !b.modelScopedOnly
	b.modelSelectIdx = 0
	b.filterModelChoices()
}

func (b *bubbleTUI) adjustModelSelectScroll() {
	if len(b.modelChoices) <= modelSelectVisibleRows {
		b.modelSelectScroll = 0
		return
	}
	if b.modelSelectIdx < b.modelSelectScroll {
		b.modelSelectScroll = b.modelSelectIdx
	} else if b.modelSelectIdx >= b.modelSelectScroll+modelSelectVisibleRows {
		b.modelSelectScroll = b.modelSelectIdx - modelSelectVisibleRows + 1
	}
	if b.modelSelectScroll < 0 {
		b.modelSelectScroll = 0
	}
	if maxOffset := len(b.modelChoices) - modelSelectVisibleRows; b.modelSelectScroll > maxOffset {
		b.modelSelectScroll = maxOffset
	}
}

func (b *bubbleTUI) confirmModelSelect() tea.Cmd {
	if b.session == nil || len(b.modelChoices) == 0 || b.modelSelectIdx >= len(b.modelChoices) {
		b.closeModelSelect("model unchanged")
		return nil
	}
	if b.modelScopeEdit {
		b.toggleScopedModelSelection()
		return nil
	}
	choice := b.modelChoices[b.modelSelectIdx]
	before := b.session.GetModel()
	b.closeModelSelect("switching model: " + choice.ID)
	providerID := choice.ProviderID
	modelID := choice.ID
	changed := !sameTUIModel(before, choice)
	return func() tea.Msg {
		err := b.session.SetModelByID(providerID, modelID)
		return bubbleModelSwitchDoneMsg{
			modelID: modelID,
			changed: changed,
			err:     err,
		}
	}
}

func (b *bubbleTUI) handleModelSwitchDone(msg bubbleModelSwitchDoneMsg) tea.Cmd {
	if msg.err != nil {
		b.model.errMsg = msg.err.Error()
		b.model.statusMsg = "model unchanged"
		return nil
	}
	if b.session != nil {
		b.model.model = b.session.GetModel()
		b.modelInfo = b.model.model
	}
	if msg.changed {
		b.model.statusMsg = "model changed; context cleared"
	} else {
		b.model.statusMsg = "model unchanged"
	}
	return b.printInlineHeaderCmd()
}

func (b *bubbleTUI) toggleScopedModelSelection() {
	if !b.modelScopeEdit || b.session == nil || len(b.modelChoices) == 0 || b.modelSelectIdx >= len(b.modelChoices) {
		return
	}
	choice := b.modelChoices[b.modelSelectIdx]
	b.modelScopedIDs[choice.ID] = !b.modelScopedIDs[choice.ID]
	ids := make([]string, 0, len(b.modelScopedIDs))
	for _, model := range b.modelAllChoices {
		if b.modelScopedIDs[model.ID] {
			ids = append(ids, model.ID)
		}
	}
	if len(ids) == len(b.modelAllChoices) {
		ids = nil
	}
	if err := b.saveScopedModelIDs(ids); err != nil {
		b.model.statusMsg = "model scope not saved"
		b.model.errMsg = err.Error()
		return
	}
	b.model.statusMsg = "model scope updated"
}

func (b *bubbleTUI) cycleModel(direction string) {
	if b.session == nil {
		return
	}
	choices := b.session.GetAvailableModels()
	if len(choices) <= 1 {
		if len(choices) == 0 {
			b.model.statusMsg = "no models configured"
		} else {
			b.model.statusMsg = "only one model available"
		}
		return
	}
	sortTUIModels(choices, nil)
	current := currentModelChoiceIndex(choices, b.session.GetModel())
	next := (current + 1) % len(choices)
	if direction == "backward" {
		next = (current - 1 + len(choices)) % len(choices)
	}
	choice := choices[next]
	if err := b.session.SetModelByID(choice.ProviderID, choice.ID); err != nil {
		b.model.errMsg = err.Error()
	} else {
		b.model.statusMsg = "model: " + choice.ID
	}
}

func (b *bubbleTUI) closeModelSelect(status string) {
	b.model.state = uiStateInput
	b.modelChoices = nil
	b.modelAllChoices = nil
	b.modelSelectIdx = 0
	b.modelSelectScroll = 0
	b.modelSearch = ""
	b.modelScopedOnly = false
	b.modelScopeEdit = false
	b.modelScopedIDs = nil
	b.model.statusMsg = status
}

func (b *bubbleTUI) handleApprovalRequest(req approval.Request) tea.Cmd {
	var cmd tea.Cmd
	if req.ToolName == "exit_plan_mode" {
		if md := planMarkdown(req.Args); md != "" {
			block := uiBlock{Kind: "assistant", Content: md, Timestamp: time.Now()}
			b.appendBlock(block)
			cmd = b.printBlockCmd(block)
		}
	}
	b.model.pendingPerm = &req
	b.model.state = uiStatePermission
	b.model.statusMsg = "permission required"
	if req.Cancel != nil {
		b.watchApprovalCancel(req.ToolCallID, req.Cancel)
	}
	return cmd
}

func (b *bubbleTUI) watchApprovalCancel(toolCallID string, cancel <-chan struct{}) {
	go func() {
		select {
		case <-cancel:
			if b.program != nil {
				b.program.Send(bubbleApprovalCancelMsg{toolCallID: toolCallID})
			}
		case <-b.ctx.Done():
		}
	}()
}

func (b *bubbleTUI) approve(decision string) {
	if !b.resolvePendingApproval(decision) {
		return
	}
	b.model.state = uiStateQuerying
	b.model.statusMsg = "thinking"
}

func (b *bubbleTUI) resolvePlan(decision string) {
	b.model.planRejectBuf = ""
	if !b.resolvePendingApproval(decision) {
		return
	}
	b.model.state = uiStateQuerying
	b.model.statusMsg = "thinking"
}

func (b *bubbleTUI) beginPlanReject() {
	if b.model.pendingPerm == nil {
		return
	}
	b.model.planRejectBuf = ""
	b.model.state = uiStatePlanReject
	b.model.statusMsg = "rejecting plan"
}

func (b *bubbleTUI) resolvePendingApproval(decision string) bool {
	if b.model.pendingPerm == nil {
		return false
	}
	req := b.model.pendingPerm
	b.model.pendingPerm = nil
	if req.Response != nil {
		req.Response <- decision
	}
	return true
}

func (b *bubbleTUI) abortQuery() {
	b.continueQueuedAfterCancel = false
	if b.model.queryCancel != nil {
		b.model.queryCancel()
		b.model.queryCancel = nil
	}
	if b.session != nil {
		if ag := b.session.GetAgent(); ag != nil {
			ag.ClearAllQueues()
		}
		b.session.Abort()
		b.session.AbortBash()
	}
	b.model.queryActive = false
	b.resolvePendingApproval("deny")
	b.model.state = uiStateInput
	b.model.statusMsg = "interrupted"
}

func (b *bubbleTUI) handleAgentEvent(ev types.Event) tea.Cmd {
	b.model.handleAgentEvent(ev)
	switch ev.Type {
	case types.EventTypeAgentEnd:
		b.model.state = uiStateInput
	case types.EventTypeMessageEnd:
		for i := len(b.model.blocks) - 1; i >= 0; i-- {
			if b.model.blocks[i].Kind == "assistant" {
				if !b.model.blocks[i].pushed {
					b.model.blocks[i].pushed = true
					return b.printAssistantTailCmd(b.model.blocks[i], i)
				}
				break
			}
		}
	case types.EventTypeToolExecutionEnd:
		for i := len(b.model.blocks) - 1; i >= 0; i-- {
			if b.model.blocks[i].Kind != "tool" {
				continue
			}
			for _, tool := range b.model.blocks[i].Tools {
				var matched bool
				if ev.ToolCallID != "" {
					matched = tool.ID == ev.ToolCallID
				} else {
					matched = tool.Name == ev.ToolName && (tool.Status == "done" || tool.Status == "error")
				}
				if matched {
					s := strings.TrimRight(renderUITool(tool, b.model.transcriptMode, b.model.viewportContentWidth()), "\n")
					return b.printStringCmd(s)
				}
			}
			break
		}
	}
	return nil
}

func (b *bubbleTUI) handleSessionEvent(ev coding_agent.SessionEvent) tea.Cmd {
	switch ev.Type {
	case coding_agent.SessionEventModelChange:
		if b.session != nil {
			b.model.model = b.session.GetModel()
			b.modelInfo = b.model.model
		}
		b.model.statusMsg = "model changed; context cleared"
	case coding_agent.SessionEventCwdChanged:
		b.model.statusMsg = "cwd changed"
	case coding_agent.SessionEventCompactionStart:
		b.model.statusMsg = "compacting"
	case coding_agent.SessionEventCompactionDone:
		b.model.statusMsg = "compacted"
	case coding_agent.SessionEventWorktreeCreate:
		b.model.statusMsg = "worktree"
	case coding_agent.SessionEventWorktreeRemove:
		b.model.statusMsg = "worktree closed"
	case coding_agent.SessionEventSubagentStart:
		b.model.statusMsg = "subagent started"
	case coding_agent.SessionEventSubagentStop:
		b.model.statusMsg = "subagent stopped"
	case coding_agent.SessionEventExtensionNotify:
		title := strings.TrimSpace(ev.ExtensionName)
		if title == "" {
			title = "extension"
		}
		msg := strings.TrimSpace(ev.Message)
		if msg != "" {
			block := uiBlock{Kind: "section", Title: title, Content: msg, Timestamp: time.Now()}
			b.appendBlock(block)
			return b.printBlockCmd(block)
		}
	}
	return nil
}

func (b *bubbleTUI) markNextQueuedUserBlockRunning() {
	if b.session == nil || b.session.GetAgent() == nil {
		return
	}
	steering, followUp := b.session.GetAgent().QueuedMessageCounts()
	source := ""
	switch {
	case steering > 0:
		source = "steer"
	case followUp > 0:
		source = "followup"
	default:
		return
	}
	for i := range b.model.blocks {
		block := &b.model.blocks[i]
		if block.Kind == "user" && block.Source == source && block.QueueState == "queued" {
			block.QueueState = "running"
			return
		}
	}
}

func (b *bubbleTUI) markRunningQueuedUserBlock(state string) {
	if state == "" {
		return
	}
	for i := len(b.model.blocks) - 1; i >= 0; i-- {
		block := &b.model.blocks[i]
		if block.Kind == "user" && (block.Source == "steer" || block.Source == "followup") && block.QueueState == "running" {
			block.QueueState = state
			return
		}
	}
}

func (b *bubbleTUI) appendSystemSection(title, content string) tea.Cmd {
	block := uiBlock{Kind: "section", Title: title, Content: content, Timestamp: time.Now()}
	b.appendBlock(block)
	return b.printBlockCmd(block)
}

func (b *bubbleTUI) appendBlock(block uiBlock) {
	b.model.appendBlock(block)
}

func (b *bubbleTUI) printBlockPtrCmd(block *uiBlock) tea.Cmd {
	if block == nil {
		return nil
	}
	return b.printBlockCmd(*block)
}

func (b *bubbleTUI) printBlockCmd(block uiBlock) tea.Cmd {
	if !b.inline {
		return nil
	}
	return b.printStringCmd(b.model.renderSingleBlock(block))
}

// printAssistantTailCmd commits a finalized assistant block to scrollback. In
// diff mode its top lines may already have streamed into scrollback during the
// turn (commitStreamingPrefix); this commits only the remaining uncommitted tail
// so nothing is duplicated. idx is the block's index, matched against the
// stream-commit tracking. Outside diff mode (or with nothing pre-committed) it
// falls back to committing the whole block.
func (b *bubbleTUI) printAssistantTailCmd(block uiBlock, idx int) tea.Cmd {
	if !b.inline {
		return nil
	}
	if !b.useDiff || b.streamBlockIdx != idx || b.streamCommitN <= 0 {
		b.resetStreamTracking()
		return b.printBlockCmd(block)
	}
	committed := b.streamCommitN
	b.resetStreamTracking()
	lines := b.streamBlockClampedLines(block)
	if committed >= len(lines) {
		// Whole block already streamed out; just add the block separator blank.
		b.pendingScroll = append(b.pendingScroll, "")
		return nil
	}
	return b.printStringCmd(strings.Join(lines[committed:], "\n"))
}

func (b *bubbleTUI) printInlineHeaderCmd() tea.Cmd {
	if !b.inline {
		return nil
	}
	return b.printStringCmd(b.renderInlineHeader())
}

func (b *bubbleTUI) printStringCmd(s string) tea.Cmd {
	if !b.inline {
		return nil
	}
	s = strings.TrimRight(s, "\n")
	if strings.TrimSpace(stripANSIForGoTUI(s)) == "" {
		return nil
	}
	if b.useDiff {
		b.enqueueScrollback(s)
		return nil
	}
	return tea.Println(s + "\n")
}

// enqueueScrollback queues a completed block's lines (plus a trailing blank for
// separation) for commit to real terminal scrollback on the next paint. Used in
// diff mode in place of tea.Println, which is a no-op under WithoutRenderer.
func (b *bubbleTUI) enqueueScrollback(s string) {
	b.pendingScroll = append(b.pendingScroll, strings.Split(s, "\n")...)
	b.pendingScroll = append(b.pendingScroll, "")
}

// turnSeparatorWidth is a fixed, width-independent length for the turn divider.
// The rule is emitted into terminal scrollback via tea.Println, where the
// program can no longer touch it — so on a window shrink the terminal would
// reflow a full-width rule into a broken 1.5-line wrap. Keeping it short (well
// under the enforced minimum terminal width of 20) means it never needs to
// reflow at any size.
const turnSeparatorWidth = 16

// printTurnSeparatorCmd emits a dim, fixed-width horizontal rule into the
// scrollback to visually divide one completed turn from the next. Inline mode
// only.
func (b *bubbleTUI) printTurnSeparatorCmd() tea.Cmd {
	if !b.inline {
		return nil
	}
	if b.useDiff {
		b.enqueueScrollback(uiDimText.Render(strings.Repeat("─", turnSeparatorWidth)))
		return nil
	}
	return tea.Println(uiDimText.Render(strings.Repeat("─", turnSeparatorWidth)))
}

func (b *bubbleTUI) isSessionAgentSlash(line string) bool {
	cmd := strings.TrimPrefix(strings.TrimSpace(line), "/")
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		cmd = cmd[:i]
	}
	if cmd == "" || b.session == nil {
		return false
	}
	if b.session.HasSlashCommand(cmd) {
		return true
	}
	for _, s := range b.session.GetSkills() {
		if s.Name == cmd {
			return true
		}
	}
	for _, p := range b.session.GetPromptTemplates() {
		if p.Name == cmd {
			return true
		}
	}
	return false
}

func (b *bubbleTUI) sessionSlashCommands() []slashCommandDef {
	if b.session == nil {
		return nil
	}
	list := b.session.GetSkills()
	prompts := b.session.GetPromptTemplates()
	registered := b.session.RegisteredSlashCommands()
	builtins := make(map[string]struct{}, len(uiSlashCommands))
	for _, cmd := range uiSlashCommands {
		builtins[cmd.Name] = struct{}{}
	}
	out := make([]slashCommandDef, 0, len(registered)+len(list)+len(prompts))
	for _, cmd := range registered {
		name := "/" + cmd.Name
		if _, ok := builtins[name]; ok {
			continue
		}
		out = append(out, slashCommandDef{Name: name, Description: cmd.Description})
	}
	for _, s := range list {
		out = append(out, slashCommandDef{Name: "/" + s.Name, Description: s.Description})
	}
	for _, p := range prompts {
		desc := p.Description
		if p.ArgumentHint != "" {
			if desc != "" {
				desc += " "
			}
			desc += "(" + p.ArgumentHint + ")"
		}
		out = append(out, slashCommandDef{Name: "/" + p.Name, Description: desc})
	}
	return out
}

func (b *bubbleTUI) currentWorkingDir() string {
	if b.session == nil {
		return ""
	}
	if cwd := b.session.RuntimeState().Cwd; cwd != "" {
		return cwd
	}
	return b.session.Cwd()
}

// pasteRuneBase is the start of a Unicode Private-Use range used as atomic
// placeholders for collapsed pastes. Paste i is represented in the draft by
// the single rune pasteRuneBase+i, so the existing rune-based cursor/backspace
// logic treats each collapsed paste as one indivisible unit for free.
const pasteRuneBase rune = 0xE000

type pasteEntry struct {
	content string
	lines   int
}

func isPasteRune(r rune, n int) bool {
	return r >= pasteRuneBase && r < pasteRuneBase+rune(n)
}

// pasteLabel is the single-line text shown in place of a collapsed paste.
func pasteLabel(e pasteEntry) string {
	return fmt.Sprintf("[Pasted text +%d lines]", e.lines)
}

func (b *bubbleTUI) insertPaste(content string) {
	idx := len(b.pastes)
	b.pastes = append(b.pastes, pasteEntry{
		content: content,
		lines:   strings.Count(content, "\n") + 1,
	})
	b.insertRune(pasteRuneBase + rune(idx))
}

// expandPastes replaces every paste-placeholder rune with its stored content,
// turning the collapsed draft back into the real text before it is submitted.
func (b *bubbleTUI) expandPastes(text string) string {
	if len(b.pastes) == 0 {
		return text
	}
	var sb strings.Builder
	for _, r := range text {
		if isPasteRune(r, len(b.pastes)) {
			sb.WriteString(b.pastes[r-pasteRuneBase].content)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func (b *bubbleTUI) insertRune(r rune) {
	rs := []rune(b.draft)
	b.cursor = clampInt(b.cursor, 0, len(rs))
	rs = append(rs[:b.cursor], append([]rune{r}, rs[b.cursor:]...)...)
	b.cursor++
	b.draft = string(rs)
	b.updateSlashMatches()
}

func (b *bubbleTUI) backspaceDraft() {
	rs := []rune(b.draft)
	b.cursor = clampInt(b.cursor, 0, len(rs))
	if b.cursor == 0 {
		return
	}
	rs = append(rs[:b.cursor-1], rs[b.cursor:]...)
	b.cursor--
	b.draft = string(rs)
	b.updateSlashMatches()
}

func (b *bubbleTUI) deleteDraft() {
	rs := []rune(b.draft)
	b.cursor = clampInt(b.cursor, 0, len(rs))
	if b.cursor >= len(rs) {
		return
	}
	rs = append(rs[:b.cursor], rs[b.cursor+1:]...)
	b.draft = string(rs)
	b.updateSlashMatches()
}

func (b *bubbleTUI) appendHistory(line string) {
	b.history = append(b.history, line)
	b.historyIdx = len(b.history)
	b.historyHold = ""
	if b.histFile != "" {
		_ = saveHistoryFile(b.histFile, b.history)
	}
}

func (b *bubbleTUI) navigateHistory(delta int) {
	if len(b.history) == 0 {
		return
	}
	if b.historyIdx == len(b.history) {
		b.historyHold = b.draft
	}
	next := b.historyIdx + delta
	next = clampInt(next, 0, len(b.history))
	b.historyIdx = next
	if b.historyIdx == len(b.history) {
		b.draft = b.historyHold
	} else {
		b.draft = b.history[b.historyIdx]
	}
	b.cursor = len([]rune(b.draft))
	b.updateSlashMatches()
}

func (b *bubbleTUI) updateSlashMatches() {
	trimmed := strings.TrimLeft(b.draft, " \t")
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, " \t\n\r") {
		b.slashMatches = nil
		b.slashIndex = 0
		return
	}
	matches := matchSlashCommands(trimmed, b.sessionSlashCommands())
	if len(matches) == 0 {
		b.slashMatches = nil
		b.slashIndex = 0
		return
	}
	if b.slashIndex >= len(matches) {
		b.slashIndex = len(matches) - 1
	}
	b.slashMatches = matches
}

func (b *bubbleTUI) completeSlashMatch() bool {
	if len(b.slashMatches) == 0 {
		return false
	}
	chosen := b.slashMatches[clampInt(b.slashIndex, 0, len(b.slashMatches)-1)]
	b.draft = chosen.Name + " "
	b.cursor = len([]rune(b.draft))
	b.slashMatches = nil
	b.slashIndex = 0
	return true
}

func (b *bubbleTUI) loadHistory() {
	if b.histFile == "" {
		return
	}
	history, err := loadHistoryFile(b.histFile)
	if err != nil {
		return
	}
	b.history = history
	b.historyIdx = len(history)
}

// View satisfies the bubbletea v2 Model interface. v2's renderer takes a
// tea.View (cellbuf-backed) rather than a raw string, so the actual layout is
// produced by viewString and wrapped here. Inline mode (no AltScreen) is the
// default for the returned View, which is what keeps scrollback intact while
// the cellbuf renderer repaints the active region cleanly on resize.
func (b *bubbleTUI) View() tea.View {
	return tea.NewView(b.clampViewWidth(b.viewString()))
}

// clampViewWidth soft-wraps every line of the active region to the terminal
// width. The v2 cellbuf renderer *clips* (rather than wraps) any line wider than
// the screen, so an over-wide line would silently lose its tail on a narrow
// terminal — visible as truncated hints/status after a shrink. Wrapping here
// keeps the active region within bounds so the renderer can repaint it cleanly
// at any size. ansi.Wrap breaks on whitespace and force-breaks overlong words.
func (b *bubbleTUI) clampViewWidth(s string) string {
	if s == "" {
		return s
	}
	width := b.width
	if width <= 0 {
		width = 80
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if ansi.StringWidth(line) > width {
			lines[i] = ansi.Wrap(line, width, "")
		}
	}
	return strings.Join(lines, "\n")
}

func (b *bubbleTUI) viewString() string {
	if b.quitting {
		return ""
	}
	if b.width <= 0 {
		b.width = 80
	}
	b.model.width = max(20, b.width-2)
	if b.inline {
		return b.renderInlineView()
	}

	header := b.renderHeader()
	status := b.renderStatusLine()
	control := b.renderInputControl()
	selector := b.renderSlashSuggestions()

	chromeHeight := lipgloss.Height(header) + lipgloss.Height(status) + lipgloss.Height(control) + 3
	if selector != "" {
		chromeHeight += lipgloss.Height(selector) + 1
	}
	bodyRows := b.height - chromeHeight
	if bodyRows < 1 {
		bodyRows = 1
	}
	body := b.renderTranscript(bodyRows)

	parts := []string{header, body}
	if selector != "" {
		parts = append(parts, selector)
	}
	parts = append(parts, control, status)
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

func (b *bubbleTUI) renderInlineView() string {
	status := b.renderStatusLine()
	control := b.renderInputControl()
	selector := b.renderSlashSuggestions()
	live := b.renderInlineLive()
	var parts []string
	if live != "" {
		parts = append(parts, live)
	}
	if selector != "" {
		parts = append(parts, selector)
	}
	parts = append(parts, control, status)
	return strings.TrimRight(strings.Join(parts, "\n"), "\n")
}

func (b *bubbleTUI) renderInlineLive() string {
	if b.model.state != uiStateQuerying {
		if activity := b.model.effectiveLastActivity(time.Now()); strings.TrimSpace(stripANSIForGoTUI(activity)) != "" {
			return strings.TrimSpace(stripANSIForGoTUI(activity))
		}
		return ""
	}
	if len(b.model.blocks) > 0 {
		last := b.model.blocks[len(b.model.blocks)-1]
		if last.Kind == "assistant" && last.Streaming {
			// In diff mode the block's already-committed top lines live in
			// scrollback (see commitStreamingPrefix); show only the uncommitted
			// tail, reusing this paint's cached clamped lines so we don't re-render.
			var rendered string
			if b.useDiff && b.streamLines != nil {
				tail := b.streamLines
				if b.streamCommitN > 0 && b.streamCommitN <= len(tail) {
					tail = tail[b.streamCommitN:]
				}
				rendered = strings.Join(tail, "\n")
			} else {
				rendered = b.model.renderSingleBlock(last)
			}
			if strings.TrimSpace(stripANSIForGoTUI(rendered)) != "" {
				// Keep the "Working (…)" activity line visible underneath the
				// streaming block. Without this the timer/hints disappear the
				// moment any thinking or text starts streaming, because this
				// branch returns early before reaching renderActivityLine below.
				activity := strings.TrimSpace(stripANSIForGoTUI(b.model.renderActivityLine()))
				if activity != "" {
					return strings.TrimRight(rendered, "\n") + "\n" + activity
				}
				return rendered
			}
		}
		if last.Kind == "tool" {
			for i := len(last.Tools) - 1; i >= 0; i-- {
				if last.Tools[i].Status == "running" {
					rendered := strings.TrimRight(renderUITool(last.Tools[i], b.model.transcriptMode, b.model.viewportContentWidth()), "\n")
					// Keep the "Working (…)" activity line visible while a
					// long-running tool executes, so the elapsed timer and
					// hints don't vanish. Without this the branch returns
					// early before reaching renderActivityLine below.
					activity := strings.TrimSpace(stripANSIForGoTUI(b.model.renderActivityLine()))
					if activity != "" {
						return rendered + "\n" + activity
					}
					return rendered
				}
			}
		}
	}
	return strings.TrimSpace(stripANSIForGoTUI(b.model.renderActivityLine()))
}

func (b *bubbleTUI) renderHeader() string {
	width := max(24, b.width)
	return lipgloss.NewStyle().Width(width).Render(b.renderHeaderLine(width))
}

func (b *bubbleTUI) renderInlineHeader() string {
	width := max(24, b.width-2)
	contentWidth := max(12, width-6)
	info := b.headerInfo()
	lines := []string{uiWhiteText.Bold(true).Render("modu_code")}
	if info.sessionID != "" {
		lines = append(lines, uiDimText.Render("session ")+uiWhiteText.Render(info.sessionID))
	}
	lines = append(lines, uiDimText.Render("model  ")+uiWhiteText.Render(info.model))
	if context := b.contextStatusLine(); context != "" {
		lines = append(lines, uiDimText.Render("context ")+uiWhiteText.Render(strings.TrimPrefix(context, "ctx ")))
	}
	if info.cwd != "" {
		lines = append(lines, uiDimText.Render("cwd    ")+uiWhiteText.Render(info.cwd))
	}
	mode := "default"
	if len(info.modes) > 0 {
		mode = strings.Join(info.modes, " · ")
	}
	lines = append(lines, uiDimText.Render("mode   ")+uiWhiteText.Render(mode))
	if info.channel != "" {
		lines = append(lines, uiDimText.Render("channel ")+uiWhiteText.Render(info.channel))
	}
	return uiBubbleHeader.Width(contentWidth).Render(strings.Join(lines, "\n"))
}

func (b *bubbleTUI) renderHeaderLine(width int) string {
	info := b.headerInfo()
	var leftParts []string
	leftParts = append(leftParts, uiWhiteText.Bold(true).Render("* modu_code"))
	if info.cwd != "" {
		leftParts = append(leftParts, uiDimText.Render(info.cwd))
	}

	rightParts := []string{uiDimText.Render(info.model)}
	for _, mode := range info.modes {
		rightParts = append(rightParts, uiSecondaryText.Render(mode))
	}
	if info.sessionID != "" {
		rightParts = append(rightParts, uiSecondaryText.Render("session "+info.sessionID))
	}
	if info.channel != "" {
		rightParts = append(rightParts, uiSecondaryText.Render(info.channel))
	}

	left := strings.Join(leftParts, uiDimText.Render(" · "))
	right := strings.Join(rightParts, uiDimText.Render(" · "))
	pad := width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

type bubbleHeaderInfo struct {
	model     string
	sessionID string
	cwd       string
	modes     []string
	channel   string
}

func (b *bubbleTUI) headerInfo() bubbleHeaderInfo {
	info := bubbleHeaderInfo{model: "none"}
	model := "none"
	if b.session != nil && b.session.GetModel() != nil {
		m := b.session.GetModel()
		model = m.Name
		if strings.TrimSpace(model) == "" {
			model = m.ID
		}
		if m.ProviderID != "" {
			model += " (" + m.ProviderID + "/" + m.ID + ")"
		}
	} else if b.modelInfo != nil {
		model = b.modelInfo.Name
		if strings.TrimSpace(model) == "" {
			model = b.modelInfo.ID
		}
		if b.modelInfo.ProviderID != "" {
			model += " (" + b.modelInfo.ProviderID + "/" + b.modelInfo.ID + ")"
		}
	}
	info.model = model
	if b.session != nil {
		info.sessionID = shortSessionID(b.session.GetSessionID())
	}

	cwd := ""
	if b.session != nil {
		cwd = b.session.RuntimeState().Cwd
		if cwd == "" {
			cwd = b.session.Cwd()
		}
	}
	if cwd != "" {
		cwd = shortenUIPath(cwd)
	}
	info.cwd = cwd
	if b.session != nil {
		if b.session.IsPlanMode() {
			info.modes = append(info.modes, "plan")
		}
		if b.session.ActiveWorktree() != "" {
			info.modes = append(info.modes, "worktree")
		}
	}
	if b.model.tgUsername != "" {
		info.channel = "@" + strings.TrimPrefix(b.model.tgUsername, "@")
	}
	return info
}

func shortSessionID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func (b *bubbleTUI) renderInputControl() string {
	width := max(24, b.width-2)
	input := b.renderInput()
	if b.model.state == uiStatePermission {
		input = b.renderApproval()
	} else if b.model.state == uiStatePlanReject {
		input = b.renderPlanReject()
	} else if b.model.state == uiStateModelSelect {
		input = b.renderModelSelect()
	} else if b.model.state == uiStateConfigMenu {
		input = b.renderConfigMenu()
	} else if b.model.state == uiStateConfigInput {
		input = b.renderConfigInput()
	} else if b.model.state == uiStateConfigSelect {
		input = b.renderConfigSelect()
	}
	if b.model.state == uiStatePermission ||
		b.model.state == uiStatePlanReject ||
		b.model.state == uiStateModelSelect ||
		b.model.state == uiStateConfigMenu ||
		b.model.state == uiStateConfigInput ||
		b.model.state == uiStateConfigSelect {
		return uiBubblePopup.Width(width).Render(input)
	}
	// Render the prompt at its natural width — no full-width border or
	// width-padding. Even under the v2 cellbuf renderer, inline mode (no
	// AltScreen) repaints the active region with relative cursor moves: the
	// terminal reflows the on-screen box at the new width *before* bubbletea
	// gets the resize event, so the renderer's stale line count overwrites it
	// mid-reflow and leaves orphan rows ("串行"). Short, natural-width lines
	// never reflow, so the redraw stays correct at any size.
	return input
}

func (b *bubbleTUI) renderTranscript(maxRows int) string {
	var lines []string
	for _, block := range b.model.blocks {
		s := strings.TrimRight(b.model.renderSingleBlock(block), "\n")
		if s == "" {
			continue
		}
		lines = append(lines, strings.Split(s, "\n")...)
		lines = append(lines, "")
	}
	if b.model.state == uiStateQuerying {
		if activity := strings.TrimSpace(stripANSIForGoTUI(b.model.renderActivityLine())); activity != "" {
			lines = append(lines, uiDimText.Render(activity))
		}
	}
	if maxRows < 1 {
		maxRows = 1
	}
	if len(lines) > maxRows {
		lines = lines[len(lines)-maxRows:]
	}
	if len(lines) == 0 {
		lines = append(lines,
			uiDimText.Render("modu_code Bubble Tea TUI"),
			uiDimText.Render("/ commands · /model switch model · enter send · ctrl+j newline"),
		)
	}
	return strings.Join(lines, "\n")
}

func (b *bubbleTUI) renderSlashSuggestions() string {
	if len(b.slashMatches) == 0 {
		return ""
	}
	const maxVisible = 8
	total := len(b.slashMatches)
	start, end := bubbleWindowRange(clampInt(b.slashIndex, 0, total-1), total, maxVisible)
	maxName := 0
	for _, item := range b.slashMatches[start:end] {
		if w := lipgloss.Width(item.Name); w > maxName {
			maxName = w
		}
	}
	var lines []string
	for i := start; i < end; i++ {
		item := b.slashMatches[i]
		marker := "  "
		style := uiDimText
		if i == b.slashIndex {
			marker = uiPrimaryText.Render("> ")
			style = uiPrimaryText
		}
		pad := strings.Repeat(" ", maxName-lipgloss.Width(item.Name))
		line := marker + style.Render(item.Name) + pad
		if item.Description != "" {
			line += "  " + uiDimText.Render(item.Description)
		}
		lines = append(lines, line)
	}
	if total > maxVisible {
		lines = append(lines, uiDimText.Render(fmt.Sprintf("  %d/%d", b.slashIndex+1, total)))
	}
	return uiBubblePopup.Width(max(24, b.width-2)).Render(strings.Join(lines, "\n"))
}

func bubbleWindowRange(cursor, total, size int) (start, end int) {
	if total <= size {
		return 0, total
	}
	start = cursor - size/2
	if start < 0 {
		start = 0
	}
	end = start + size
	if end > total {
		end = total
		start = end - size
	}
	return start, end
}

func (b *bubbleTUI) inputHeight() int {
	if b.model.state == uiStateModelSelect {
		return min(len(b.modelChoices), modelSelectVisibleRows) + 4
	}
	if b.model.state == uiStateConfigSelect {
		return min(b.configSelectLen(), modelSelectVisibleRows) + 4
	}
	if b.model.state == uiStateConfigMenu {
		return min(len(b.configMenuChoices), modelSelectVisibleRows) + 4
	}
	if b.model.state == uiStateConfigInput {
		if b.configAction == "provider" {
			return len(configProviderFields) + 4
		}
		return 6
	}
	if b.model.state == uiStatePermission {
		return 5
	}
	if b.model.state == uiStatePlanReject {
		return 4
	}
	return inputVisibleRows(b.draft) + 2
}

func (b *bubbleTUI) renderModelSelect() string {
	scope := "all"
	if b.modelScopedOnly {
		scope = "scoped"
	}
	if b.modelScopeEdit {
		scope = "edit"
	}
	query := b.modelSearch
	if query == "" {
		query = "type to search"
	}
	lines := []string{
		uiWhiteText.Render(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Select model",
			Selected: b.modelSelectIdx,
			Visible:  len(b.modelChoices),
			Total:    len(b.modelAllChoices),
			Query:    b.modelSearch,
			Mode:     "scope=" + scope,
		})),
		uiDimText.Render("  search: " + query),
	}

	end := b.modelSelectScroll + modelSelectVisibleRows
	if end > len(b.modelChoices) {
		end = len(b.modelChoices)
	}
	current := (*types.Model)(nil)
	if b.session != nil {
		current = b.session.GetModel()
	}
	if len(b.modelChoices) == 0 {
		lines = append(lines, uiDimText.Render("  no matching models"))
	}
	for i := b.modelSelectScroll; i < end; i++ {
		choice := b.modelChoices[i]
		selected := i == b.modelSelectIdx
		active := current != nil && current.ProviderID == choice.ProviderID && current.ID == choice.ID
		line := modelChoiceLine(choice, selected, active)
		if n := i - b.modelSelectScroll + 1; n >= 1 && n <= 9 {
			line = fmt.Sprintf("%d %s", n, line)
		} else {
			line = "  " + line
		}
		if b.modelScopeEdit {
			enabled := b.modelScopedIDs[choice.ID]
			marker := "[ ] "
			if enabled {
				marker = "[x] "
			}
			line = strings.Replace(line, " ", " "+marker, 1)
		}
		if i == b.modelSelectScroll && b.modelSelectScroll > 0 {
			line += "  ^"
		} else if i == end-1 && end < len(b.modelChoices) {
			line += "  v"
		}
		if selected {
			line = uiPrimaryText.Render(line)
		} else {
			line = uiDimText.Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, uiDimText.Render(b.modelSelectHint()))
	return strings.Join(lines, "\n")
}

func (b *bubbleTUI) modelSelectHint() string {
	if b.modelScopeEdit {
		return "  up/down or j/k select  enter/y/space toggle  esc/q close"
	}
	return "  up/down or j/k select  tab scope  enter/y confirm  esc/q cancel"
}

func (b *bubbleTUI) renderStatusLine() string {
	if b.model.errMsg != "" {
		return uiErrorText.Render("! " + b.model.errMsg)
	}
	if b.model.state == uiStateModelSelect {
		return uiDimText.Render(strings.TrimSpace(b.modelSelectHint()))
	}
	if b.model.state == uiStateConfigMenu || b.model.state == uiStateConfigInput || b.model.state == uiStateConfigSelect {
		return uiDimText.Render(strings.TrimSpace(b.configHint()))
	}
	if status := b.model.effectiveStatusMsg(time.Now()); status != "" && status != "thinking" {
		if context := b.contextStatusLine(); context != "" {
			status += "  |  " + context
		}
		if queue := b.queueStatusLine(); queue != "" {
			status += "  |  " + queue
		}
		return uiDimText.Render(status)
	}
	var parts []string
	if b.session != nil {
		if b.session.IsPlanMode() {
			parts = append(parts, "plan")
		}
		if b.session.ActiveWorktree() != "" {
			parts = append(parts, "worktree")
		}
		if context := b.contextStatusLine(); context != "" {
			parts = append(parts, context)
		}
		if queue := b.queueStatusLine(); queue != "" {
			parts = append(parts, queue)
		}
		if indicator := goalWatchIndicator(b.session.ExtensionRuntimeStates()); indicator != "" {
			parts = append(parts, indicator)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "enter send", "ctrl+j newline", "/help", "ctrl+c exit")
	} else {
		parts = append(parts, "enter send", "ctrl+c exit")
	}
	return uiDimText.Render(strings.Join(parts, "  |  "))
}

func (b *bubbleTUI) contextStatusLine() string {
	if b.session == nil {
		return ""
	}
	model := b.session.GetModel()
	if model == nil || model.ContextWindow <= 0 {
		return ""
	}
	used := b.session.GetSessionStats().TotalTokens
	if used < 0 {
		used = 0
	}
	percent := used * 100 / model.ContextWindow
	return fmt.Sprintf("ctx %s/%s %d%%", formatCompactTokens(used), formatCompactTokens(model.ContextWindow), percent)
}

func formatCompactTokens(n int) string {
	if n < 0 {
		n = 0
	}
	switch {
	case n >= 1000000:
		if n%1000000 == 0 {
			return fmt.Sprintf("%dM", n/1000000)
		}
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		if n%1000 == 0 {
			return fmt.Sprintf("%dK", n/1000)
		}
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func (b *bubbleTUI) queueStatusLine() string {
	if b.session == nil || b.session.GetAgent() == nil {
		return ""
	}
	steering, followUp := b.session.GetAgent().QueuedMessageCounts()
	var parts []string
	if steering > 0 {
		parts = append(parts, fmt.Sprintf("steer %d", steering))
	}
	if followUp > 0 {
		parts = append(parts, fmt.Sprintf("follow-up %d", followUp))
	}
	return strings.Join(parts, " ")
}

// isPlainInputState reports whether the bottom chrome is the normal text input
// (not a popup/approval), i.e. whether a real input caret should be drawn.
func (b *bubbleTUI) isPlainInputState() bool {
	switch b.model.state {
	case uiStatePermission, uiStatePlanReject, uiStateModelSelect,
		uiStateConfigMenu, uiStateConfigInput, uiStateConfigSelect:
		return false
	}
	return true
}

// inputCaretPos returns the caret's row offset (within renderInput's output) and
// column, mirroring renderInput's layout: a 2-cell prefix plus the display width
// of the runes before the caret on its line (paste tokens and wide CJK glyphs
// counted at their rendered width).
func (b *bubbleTUI) inputCaretPos() (lineOffset, col int) {
	rs := []rune(b.draft)
	cur := clampInt(b.cursor, 0, len(rs))
	ranges := inputLineRanges(rs)
	cursorLine := inputCursorLine(ranges, cur)
	start, _, above, _ := inputVisibleRange(len(ranges), cursorLine, maxInputVisibleRows)
	lineOffset = cursorLine - start
	if above {
		lineOffset++
	}
	rng := ranges[cursorLine]
	col = 2 // "❯ " / "  " prefix width
	for i := rng.Start; i < cur; i++ {
		if isPasteRune(rs[i], len(b.pastes)) {
			col += lipgloss.Width(pasteLabel(b.pastes[rs[i]-pasteRuneBase]))
		} else {
			col += lipgloss.Width(string(rs[i]))
		}
	}
	return lineOffset, col
}

func (b *bubbleTUI) renderInput() string {
	rs := []rune(b.draft)
	b.cursor = clampInt(b.cursor, 0, len(rs))
	ranges := inputLineRanges(rs)
	cursorLine := inputCursorLine(ranges, b.cursor)
	start, end, above, below := inputVisibleRange(len(ranges), cursorLine, maxInputVisibleRows)

	// In diff mode the renderer places a real hardware cursor at the caret (for
	// IME anchoring), so the input must not also draw a fake block — that would
	// double the cursor.
	fakeCursor := !b.useDiff

	var lines []string
	if above {
		lines = append(lines, uiDimText.Render("  ... "+itoa(start)+" lines above"))
	}
	for idx := start; idx < end; idx++ {
		rng := ranges[idx]
		prefix := "  "
		if idx == 0 {
			prefix = uiPrimaryText.Render("❯ ")
		}
		var row strings.Builder
		row.WriteString(prefix)
		for i := rng.Start; i < rng.End; i++ {
			ch := string(rs[i])
			if isPasteRune(rs[i], len(b.pastes)) {
				ch = uiDimText.Render(pasteLabel(b.pastes[rs[i]-pasteRuneBase]))
			}
			if fakeCursor && i == b.cursor {
				if rs[i] == ' ' || rs[i] == '\t' {
					ch = "█"
				} else {
					ch = lipgloss.NewStyle().Reverse(true).Render(ch)
				}
			}
			row.WriteString(ch)
		}
		if fakeCursor && b.cursor == rng.End {
			row.WriteString("█")
		}
		lines = append(lines, row.String())
	}
	if below {
		lines = append(lines, uiDimText.Render("  ... "+itoa(len(ranges)-end)+" lines below"))
	}
	return strings.Join(lines, "\n")
}

func (b *bubbleTUI) renderApproval() string {
	perm := b.model.pendingPerm
	if perm == nil {
		return ""
	}
	if perm.ToolName == "exit_plan_mode" {
		return strings.Join([]string{
			uiSecondaryText.Bold(true).Render("⏺ Plan approval"),
			uiDimText.Render(fmt.Sprintf("  plan shown above  steps=%d", planApprovalStepCount(perm.Args))),
			uiSecondaryText.Render("  auto-accept allows write/edit/bash for this session"),
			b.renderApprovalActions([]approvalActionLabel{
				{Text: "[Y]es, start coding", Style: uiSuccessText.Bold(true)},
				{Text: "[A] auto-accept edits", Style: uiSecondaryText.Bold(true)},
				{Text: "[N]o, keep planning", Style: uiErrorText},
			}),
		}, "\n")
	}
	if perm.ToolName == "extension_confirm" {
		title, _ := perm.Args["title"].(string)
		body, _ := perm.Args["body"].(string)
		if strings.TrimSpace(title) == "" {
			title = "Confirm action"
		}
		var lines []string
		lines = append(lines, uiSecondaryText.Bold(true).Render("⏺ "+strings.TrimSpace(title)))
		for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines = append(lines, uiDimText.Render("  "+truncateRunes(line, 100)))
		}
		lines = append(lines, b.renderApprovalActions([]approvalActionLabel{
			{Text: "[Y]es", Style: uiSuccessText.Bold(true)},
			{Text: "[N]o", Style: uiErrorText},
		}))
		return strings.Join(lines, "\n")
	}
	lines := []string{
		uiSecondaryText.Bold(true).Render("⏺ Permission required"),
		uiWhiteText.Bold(true).Render("  tool: " + perm.ToolName),
	}
	if args := formatToolInput(perm.ToolName, perm.Args); args != "" {
		lines = append(lines, uiDimText.Render("  args: "+truncateRunes(args, 80)))
	}
	allowLabel := "[A]lways allow"
	denyLabel := "[D]eny always"
	if perm.ToolName == "bash" {
		allowLabel = "[A]llow this command"
		denyLabel = "[D]eny this command"
	}
	lines = append(lines, b.renderApprovalActions([]approvalActionLabel{
		{Text: "[Y]es", Style: uiSuccessText.Bold(true)},
		{Text: "[N]o", Style: uiErrorText},
		{Text: allowLabel, Style: uiSecondaryText.Bold(true)},
		{Text: denyLabel, Style: uiDimText},
	}))
	return strings.Join(lines, "\n")
}

type approvalActionLabel struct {
	Text  string
	Style lipgloss.Style
}

func (b *bubbleTUI) renderApprovalActions(actions []approvalActionLabel) string {
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, action.Style.Render(action.Text))
	}
	return uiDimText.Render("  actions: ") + strings.Join(parts, "  ")
}

func truncateRunes(s string, maxRunes int) string {
	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}
	if maxRunes < 1 {
		return ""
	}
	return string(rs[:maxRunes-1]) + "…"
}

func (b *bubbleTUI) renderPlanReject() string {
	return strings.Join([]string{
		uiSecondaryText.Render("Reject plan"),
		"> " + b.model.planRejectBuf + "█",
		uiDimText.Render("enter submit  esc reject without feedback"),
	}, "\n")
}

type bubbleSlashPrinter struct {
	lines []string
	clear bool
}

func (p *bubbleSlashPrinter) PrintInfo(msg string) {
	p.lines = append(p.lines, msg)
}

func (p *bubbleSlashPrinter) PrintError(err error) {
	if err != nil {
		p.lines = append(p.lines, "error: "+err.Error())
	}
}

func (p *bubbleSlashPrinter) PrintSection(title string, lines []string) {
	p.lines = append(p.lines, title)
	p.lines = append(p.lines, lines...)
}

func (p *bubbleSlashPrinter) ClearScreen() {
	p.clear = true
}

type bubbleBridgePrinter struct {
	program *tea.Program
}

func (p *bubbleBridgePrinter) PrintInfo(msg string) {
	if p.program != nil {
		p.program.Send(bubbleExternalInfoMsg{text: msg})
	}
}

func (p *bubbleBridgePrinter) PrintError(err error) {
	if err != nil && p.program != nil {
		p.program.Send(bubbleExternalInfoMsg{text: "error: " + err.Error()})
	}
}

func (p *bubbleBridgePrinter) PrintUser(msg string) {
	if p.program != nil {
		p.program.Send(bubbleExternalUserMsg{text: msg})
	}
}

func (p *bubbleBridgePrinter) ClearLine() {}

func (p *bubbleBridgePrinter) ClearScreen() {
	if p.program != nil {
		p.program.Send(bubbleClearScreenMsg{})
	}
}
