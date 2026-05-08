package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	gotui "github.com/grindlemire/go-tui"
	"golang.org/x/term"

	"github.com/openmodu/modu/cmd/modu_code/internal/mailboxrt"
	"github.com/openmodu/modu/cmd/modu_code/internal/slash"
	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"
)

type goTUIRoot struct {
	ctx            context.Context
	session        *coding_agent.CodingSession
	modelInfo      *types.Model
	mailboxRuntime *mailboxrt.Runtime
	histFile       string
	approvalCh     <-chan tui.ApprovalRequest
	promptMu       *sync.Mutex

	app     *gotui.App
	model   *uiModel
	draft   *gotui.State[string]
	refresh *gotui.State[int]
	cursor  int

	history      []string
	historyIndex int
	tgUsername   string
}

func newGoTUIRoot(ctx context.Context, session *coding_agent.CodingSession, modelInfo *types.Model, mailboxRuntime *mailboxrt.Runtime, histFile string, approvalCh <-chan tui.ApprovalRequest, promptMu *sync.Mutex) *goTUIRoot {
	width, _, err := term.GetSize(1)
	if err != nil || width <= 0 {
		width = 80
	}
	m := newUIModel(ctx, session, modelInfo, mailboxRuntime, histFile, nil, promptMu, "")
	m.ready = true
	m.state = uiStateInput
	m.width = width
	m.height = 24

	return &goTUIRoot{
		ctx:            ctx,
		session:        session,
		modelInfo:      modelInfo,
		mailboxRuntime: mailboxRuntime,
		histFile:       histFile,
		approvalCh:     approvalCh,
		promptMu:       promptMu,
		model:          m,
		draft:          gotui.NewState(""),
		refresh:        gotui.NewState(0),
	}
}

func (r *goTUIRoot) BindApp(app *gotui.App) {
	r.app = app
	r.draft.BindApp(app)
	r.refresh.BindApp(app)
}

func (r *goTUIRoot) Watchers() []gotui.Watcher {
	watchers := []gotui.Watcher{
		gotui.OnTimer(time.Second, r.tick),
	}
	if r.approvalCh != nil {
		watchers = append(watchers, gotui.Watch(r.approvalCh, r.handleApprovalRequest))
	}
	return watchers
}

func (r *goTUIRoot) tick() {
	if r.model.state == uiStateQuerying || r.model.statusMsg != "" {
		r.bump()
	}
}

func (r *goTUIRoot) bump() {
	r.refresh.Set(r.refresh.Get() + 1)
}

func (r *goTUIRoot) KeyMap() gotui.KeyMap {
	if r.model.pendingPerm != nil {
		return r.permissionKeyMap()
	}
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) {
			if r.model.state == uiStateQuerying || r.model.state == uiStatePermission {
				r.abortQuery()
				return
			}
			ke.App().Stop()
		}),
		gotui.OnStop(gotui.KeyCtrlD, func(ke gotui.KeyEvent) {
			if strings.TrimSpace(r.draft.Get()) == "" {
				ke.App().Stop()
			}
		}),
		gotui.OnStop(gotui.KeyCtrlL, func(ke gotui.KeyEvent) {
			r.model.blocks = nil
			r.model.errMsg = ""
			r.model.statusMsg = "cleared"
			r.bump()
		}),
		gotui.OnStop(gotui.KeyCtrlO, func(ke gotui.KeyEvent) {
			r.model.transcriptMode = !r.model.transcriptMode
			r.bump()
		}),
		gotui.OnStop(gotui.KeyCtrlJ, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) {
			if r.model.state == uiStateQuerying || r.model.state == uiStatePermission {
				r.abortQuery()
				return
			}
			r.model.state = uiStateInput
			r.model.statusMsg = ""
			r.bump()
		}),
		gotui.OnStop(gotui.KeyHome, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyEnd, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyBackspace, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyDelete, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyLeft, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyRight, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyUp, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyDown, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
		gotui.OnStop(gotui.AnyRune, func(ke gotui.KeyEvent) {
			r.handleInputKey(ke)
		}),
	}
}

func (r *goTUIRoot) permissionKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.abortQuery() }),
		gotui.OnStop(gotui.KeyCtrlO, func(ke gotui.KeyEvent) {
			r.model.transcriptMode = !r.model.transcriptMode
			r.bump()
		}),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.approve("allow") }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.approve("deny") }),
		gotui.OnStop(gotui.Rune('y'), func(ke gotui.KeyEvent) { r.approve("allow") }),
		gotui.OnStop(gotui.Rune('Y'), func(ke gotui.KeyEvent) { r.approve("allow") }),
		gotui.OnStop(gotui.Rune('n'), func(ke gotui.KeyEvent) { r.approve("deny") }),
		gotui.OnStop(gotui.Rune('N'), func(ke gotui.KeyEvent) { r.approve("deny") }),
		gotui.OnStop(gotui.Rune('a'), func(ke gotui.KeyEvent) { r.approve("allow_always") }),
		gotui.OnStop(gotui.Rune('A'), func(ke gotui.KeyEvent) { r.approve("allow_always") }),
		gotui.OnStop(gotui.Rune('d'), func(ke gotui.KeyEvent) { r.approve("deny_always") }),
		gotui.OnStop(gotui.Rune('D'), func(ke gotui.KeyEvent) { r.approve("deny_always") }),
	}
}

func (r *goTUIRoot) handleInputKey(ke gotui.KeyEvent) {
	if r.model.state == uiStatePermission {
		return
	}
	rs := []rune(r.draft.Get())
	if r.cursor < 0 {
		r.cursor = 0
	}
	if r.cursor > len(rs) {
		r.cursor = len(rs)
	}
	switch ke.Key {
	case gotui.KeyRune:
		if ke.Rune == 0 {
			return
		}
		if ke.Mod == gotui.ModCtrl && ke.Rune == 'j' {
			ke.Rune = '\n'
		} else if ke.Mod != 0 {
			return
		}
		rs = append(rs[:r.cursor], append([]rune{ke.Rune}, rs[r.cursor:]...)...)
		r.cursor++
		r.draft.Set(string(rs))
	case gotui.KeyBackspace:
		if r.cursor == 0 {
			return
		}
		rs = append(rs[:r.cursor-1], rs[r.cursor:]...)
		r.cursor--
		r.draft.Set(string(rs))
	case gotui.KeyDelete:
		if r.cursor >= len(rs) {
			return
		}
		rs = append(rs[:r.cursor], rs[r.cursor+1:]...)
		r.draft.Set(string(rs))
	case gotui.KeyLeft:
		if r.cursor > 0 {
			r.cursor--
			r.bump()
		}
	case gotui.KeyRight:
		if r.cursor < len(rs) {
			r.cursor++
			r.bump()
		}
	case gotui.KeyHome:
		r.cursor = inputLineStart(rs, r.cursor)
		r.bump()
	case gotui.KeyEnd:
		r.cursor = inputLineEnd(rs, r.cursor)
		r.bump()
	case gotui.KeyUp:
		r.cursor = moveInputCursorVertical(rs, r.cursor, -1)
		r.bump()
	case gotui.KeyDown:
		r.cursor = moveInputCursorVertical(rs, r.cursor, 1)
		r.bump()
	case gotui.KeyEnter:
		r.submit(r.draft.Get())
	}
}

func inputLineStart(rs []rune, cursor int) int {
	cursor = clampInt(cursor, 0, len(rs))
	for cursor > 0 && rs[cursor-1] != '\n' {
		cursor--
	}
	return cursor
}

func inputLineEnd(rs []rune, cursor int) int {
	cursor = clampInt(cursor, 0, len(rs))
	for cursor < len(rs) && rs[cursor] != '\n' {
		cursor++
	}
	return cursor
}

func moveInputCursorVertical(rs []rune, cursor, delta int) int {
	cursor = clampInt(cursor, 0, len(rs))
	start := inputLineStart(rs, cursor)
	col := cursor - start
	if delta < 0 {
		if start == 0 {
			return cursor
		}
		prevEnd := start - 1
		prevStart := inputLineStart(rs, prevEnd)
		return min(prevStart+col, prevEnd)
	}
	end := inputLineEnd(rs, cursor)
	if end >= len(rs) {
		return cursor
	}
	nextStart := end + 1
	nextEnd := inputLineEnd(rs, nextStart)
	return min(nextStart+col, nextEnd)
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func (r *goTUIRoot) submit(text string) {
	line := strings.TrimSpace(text)
	if line == "" {
		return
	}
	r.draft.Set("")
	r.cursor = 0
	r.appendHistory(line)

	if strings.HasPrefix(line, "! ") {
		r.runShell(strings.TrimPrefix(line, "! "))
		return
	}
	if strings.HasPrefix(line, "/") {
		r.runSlash(line)
		return
	}
	r.runPrompt(line)
}

func (r *goTUIRoot) appendHistory(line string) {
	r.history = append(r.history, line)
	r.historyIndex = len(r.history)
	if r.histFile != "" {
		_ = saveHistoryFile(r.histFile, r.history)
	}
}

func (r *goTUIRoot) runShell(shellCmd string) {
	block := uiBlock{Kind: "system", Content: "$ " + shellCmd, Timestamp: time.Now()}
	r.model.appendBlock(block)
	r.pushBlockAbove(block)
	go func() {
		out, err := exec.Command("bash", "-c", shellCmd).CombinedOutput()
		r.queue(func() {
			text := strings.TrimSpace(string(out))
			if err != nil {
				if text != "" {
					text += "\n"
				}
				text += err.Error()
			}
			b := uiBlock{Kind: "system", Content: text, Timestamp: time.Now()}
			r.model.appendBlock(b)
			r.pushBlockAbove(b)
			r.bump()
		})
	}()
}

func (r *goTUIRoot) runSlash(line string) {
	go func() {
		printer := &uiSlashPrinter{}
		handled, exit := slash.Handle(r.ctx, line, r.session, printer, r.modelInfo, r.mailboxRuntime)
		r.queue(func() {
			switch {
			case !handled:
				b := uiBlock{Kind: "system", Content: "unknown command: " + line, Timestamp: time.Now()}
				r.model.appendBlock(b)
				r.pushBlockAbove(b)
			case printer.clear:
				r.model.blocks = nil
			case strings.TrimSpace(strings.Join(printer.lines, "\n")) != "":
				b := uiBlock{Kind: "section", Title: "modu_code", Content: strings.Join(printer.lines, "\n"), Timestamp: time.Now()}
				r.model.appendBlock(b)
				r.pushBlockAbove(b)
			}
			r.bump()
			if exit && r.app != nil {
				r.app.Stop()
			}
		})
	}()
}

func (r *goTUIRoot) runPrompt(line string) {
	block := uiBlock{Kind: "user", Content: line, Timestamp: time.Now()}
	r.model.appendBlock(block)
	r.pushBlockAbove(block)
	r.model.queryActive = true
	r.model.state = uiStateQuerying
	r.model.statusMsg = "thinking"
	r.model.queryStartTime = time.Now()
	r.model.thinkingStart = time.Time{}
	queryCtx, queryCancel := context.WithCancel(r.ctx)
	r.model.queryCancel = queryCancel
	r.bump()

	go func() {
		defer queryCancel()
		r.promptMu.Lock()
		defer r.promptMu.Unlock()
		// Bail if context was cancelled while waiting for the lock.
		select {
		case <-queryCtx.Done():
			return
		default:
		}
		err := r.session.Prompt(queryCtx, line)
		r.queue(func() {
			if err != nil && err != context.Canceled {
				r.model.errMsg = err.Error()
			}
			r.model.queryActive = false
			if r.model.statusMsg != "interrupted" {
				r.model.statusMsg = ""
			}
			r.model.state = uiStateInput
			r.bump()
		})
	}()
}

func (r *goTUIRoot) abortQuery() {
	if r.model.queryCancel != nil {
		r.model.queryCancel()
		r.model.queryCancel = nil
	}
	if r.session != nil {
		r.session.Abort()
		r.session.AbortBash()
	}
	r.model.queryActive = false
	r.model.pendingPerm = nil
	r.model.state = uiStateInput
	r.model.statusMsg = "interrupted"
	r.setInlineHeight(5)
	r.bump()
}

func (r *goTUIRoot) handleApprovalRequest(req tui.ApprovalRequest) {
	r.model.pendingPerm = &req
	r.model.state = uiStatePermission
	r.model.statusMsg = "permission required"
	// Push the permission box to scrollback so it stays visible.
	if r.app != nil {
		width, _ := r.app.Size()
		r.app.PrintAboveElement(r.renderPermission(width))
	}
	r.setInlineHeight(5)
	r.bump()
	if req.Cancel != nil {
		r.watchApprovalCancel(req.ToolCallID, req.Cancel)
	}
}

func (r *goTUIRoot) watchApprovalCancel(toolCallID string, cancel <-chan struct{}) {
	go func() {
		select {
		case <-cancel:
			r.queue(func() {
				if r.model.pendingPerm == nil || r.model.pendingPerm.ToolCallID != toolCallID {
					return
				}
				r.model.pendingPerm = nil
				r.model.state = uiStateQuerying
				r.model.statusMsg = "approval dismissed"
				r.bump()
			})
		case <-r.appStopCh():
		}
	}()
}

func (r *goTUIRoot) appStopCh() <-chan struct{} {
	if r.app == nil {
		return r.ctx.Done()
	}
	return r.app.StopCh()
}

func (r *goTUIRoot) approve(decision string) {
	if r.model.pendingPerm == nil {
		return
	}
	req := r.model.pendingPerm
	r.model.pendingPerm = nil
	r.model.state = uiStateQuerying
	r.model.statusMsg = "thinking"
	req.Response <- decision
	r.bump()
}

func (r *goTUIRoot) handleAgentEvent(ev agent.AgentEvent) {
	r.model.handleAgentEvent(ev)

	switch ev.Type {
	case agent.EventTypeAgentEnd:
		// model already cleared queryActive; also reset UI state so the
		// working indicator stops before session.Prompt() returns.
		r.model.state = uiStateInput

	case agent.EventTypeMessageEnd:
		// Push the completed assistant block to scrollback (at most once per block).
		for i := len(r.model.blocks) - 1; i >= 0; i-- {
			if r.model.blocks[i].Kind == "assistant" {
				if !r.model.blocks[i].pushed {
					r.model.blocks[i].pushed = true
					r.pushBlockAbove(r.model.blocks[i])
				}
				break
			}
		}
	case agent.EventTypeToolExecutionEnd:
		// Push the specific completed tool by ToolCallID only (name-based matching
		// would re-match already-printed tools with the same name).
		for i := len(r.model.blocks) - 1; i >= 0; i-- {
			if r.model.blocks[i].Kind != "tool" {
				continue
			}
			for _, tool := range r.model.blocks[i].Tools {
				var matched bool
				if ev.ToolCallID != "" {
					matched = tool.ID == ev.ToolCallID
				} else {
					matched = tool.Name == ev.ToolName && (tool.Status == "done" || tool.Status == "error")
				}
				if matched {
					s := strings.TrimRight(renderUITool(tool, r.model.transcriptMode, r.model.viewportContentWidth()), "\n")
					if strings.TrimSpace(stripANSIForGoTUI(s)) != "" {
						r.printAbove(s)
					}
					break
				}
			}
			break
		}
	}
	r.bump()
}

func (r *goTUIRoot) handleSessionEvent(ev coding_agent.SessionEvent) {
	switch ev.Type {
	case coding_agent.SessionEventCwdChanged:
		r.model.statusMsg = "cwd changed"
	case coding_agent.SessionEventCompactionStart:
		r.model.statusMsg = "compacting"
	case coding_agent.SessionEventCompactionDone:
		r.model.statusMsg = "compacted"
	case coding_agent.SessionEventWorktreeCreate:
		r.model.statusMsg = "worktree"
	case coding_agent.SessionEventWorktreeRemove:
		r.model.statusMsg = "worktree closed"
	case coding_agent.SessionEventSubagentStart:
		r.model.statusMsg = "subagent started"
	case coding_agent.SessionEventSubagentStop:
		r.model.statusMsg = "subagent stopped"
	}
	r.bump()
}

func (r *goTUIRoot) externalInfo(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.queue(func() {
		b := uiBlock{Kind: "section", Title: "modu_code", Content: text, Timestamp: time.Now()}
		r.model.appendBlock(b)
		r.pushBlockAbove(b)
		r.bump()
	})
}

func (r *goTUIRoot) externalUser(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	r.queue(func() {
		b := uiBlock{Kind: "user", Content: text, Timestamp: time.Now()}
		r.model.appendBlock(b)
		r.pushBlockAbove(b)
		r.bump()
	})
}

// pushBlockAbove renders a block and prints it to the inline scrollback.
// Must be called from the main event loop.
func (r *goTUIRoot) pushBlockAbove(block uiBlock) {
	if r.app == nil {
		return
	}
	s := r.model.renderSingleBlock(block)
	if strings.TrimSpace(stripANSIForGoTUI(s)) == "" {
		return
	}
	r.app.PrintAboveStyledln("%s", s)
}

// printAbove prints a pre-rendered ANSI string to the inline scrollback.
func (r *goTUIRoot) printAbove(s string) {
	if r.app == nil {
		return
	}
	r.app.PrintAboveStyledln("%s", s)
}

func (r *goTUIRoot) setInlineHeight(h int) {
	if r.app != nil {
		r.app.SetInlineHeight(h)
	}
}

func (r *goTUIRoot) queue(fn func()) {
	if r.app == nil {
		fn()
		return
	}
	r.app.QueueUpdate(fn)
}

// Render builds the inline widget: input box + meta + status.
func (r *goTUIRoot) Render(app *gotui.App) *gotui.Element {
	_ = r.refresh.Get()
	width, _ := app.Size()
	if width <= 0 {
		width = 80
	}
	r.model.width = max(20, width-2)
	r.model.tgUsername = r.tgUsername

	// Dynamically resize widget to fit input lines.
	draftLines := strings.Count(r.draft.Get(), "\n") + 1
	neededH := draftLines + 4 // border(2) + meta(1) + status(1)
	if neededH < 5 {
		neededH = 5
	}
	if neededH != app.InlineHeight() {
		app.SetInlineHeight(neededH)
	}

	root := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithHeightPercent(100),
	)

	root.AddChild(r.renderInput(width))

	if meta := strings.TrimSpace(stripANSIForGoTUI(r.model.renderInputMeta())); meta != "" {
		root.AddChild(gotui.New(
			gotui.WithText(meta),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}

	if status := r.statusLine(); status != "" {
		statusStyle := gotui.NewStyle().Dim()
		if r.model.pendingPerm != nil {
			statusStyle = gotui.NewStyle().Foreground(gotui.Yellow)
		} else if r.model.errMsg != "" {
			statusStyle = gotui.NewStyle().Foreground(gotui.Red)
		}
		root.AddChild(gotui.New(
			gotui.WithText(status),
			gotui.WithTextStyle(statusStyle),
			gotui.WithFlexShrink(0),
		))
	}

	return root
}

func (r *goTUIRoot) statusLine() string {
	if r.model.pendingPerm != nil {
		return r.model.pendingPerm.ToolName + "  [Y]es  [N]o  [A]lways allow  [D]eny always"
	}
	if r.model.state == uiStateQuerying {
		if activity := r.model.renderActivityLine(); strings.TrimSpace(stripANSIForGoTUI(activity)) != "" {
			return strings.TrimSpace(stripANSIForGoTUI(activity))
		}
		return "working..."
	}
	var parts []string
	if r.model.statusMsg != "" && r.model.statusMsg != "thinking" {
		parts = append(parts, r.model.statusMsg)
	}
	if r.model.errMsg != "" {
		parts = append(parts, "! "+r.model.errMsg)
	}
	return strings.Join(parts, " · ")
}

func (r *goTUIRoot) renderPermission(width int) *gotui.Element {
	perm := r.model.pendingPerm

	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)

	// Header: ⏺ ToolName(args) — same visual pattern as a running tool call.
	header := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Row),
		gotui.WithFlexShrink(0),
	)
	header.AddChild(gotui.New(
		gotui.WithText("⏺ "),
		gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow)),
		gotui.WithFlexShrink(0),
	))
	header.AddChild(gotui.New(
		gotui.WithText(perm.ToolName),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))
	if input := formatToolInput(perm.ToolName, perm.Args); input != "" {
		if len(input) > 200 {
			input = input[:200] + "..."
		}
		header.AddChild(gotui.New(
			gotui.WithText("("+input+")"),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}
	container.AddChild(header)

	// Action hints indented under the bullet.
	actions := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Row),
		gotui.WithFlexShrink(0),
		gotui.WithPaddingTRBL(0, 0, 0, 2),
	)
	addAction := func(label string, style gotui.Style) {
		actions.AddChild(gotui.New(
			gotui.WithText(label+"  "),
			gotui.WithTextStyle(style),
			gotui.WithFlexShrink(0),
		))
	}
	addAction("[Y]es", gotui.NewStyle().Foreground(gotui.Green).Bold())
	addAction("[N]o", gotui.NewStyle().Foreground(gotui.Red).Bold())
	addAction("[A]lways allow", gotui.NewStyle().Foreground(gotui.Yellow).Bold())
	actions.AddChild(gotui.New(
		gotui.WithText("[D]eny always"),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	container.AddChild(actions)
	return container
}

func (r *goTUIRoot) renderInput(width int) *gotui.Element {
	rs := []rune(r.draft.Get())
	r.cursor = clampInt(r.cursor, 0, len(rs))
	box := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithWidth(max(20, width-4)),
		gotui.WithFlexShrink(0),
		gotui.WithBorder(gotui.BorderRounded),
		gotui.WithBorderStyle(gotui.NewStyle().Foreground(gotui.Cyan)),
		gotui.WithPaddingTRBL(0, 1, 0, 1),
	)
	if len(rs) == 0 {
		row := gotui.New(gotui.WithDisplay(gotui.DisplayFlex), gotui.WithDirection(gotui.Row))
		row.AddChild(gotui.New(
			gotui.WithText("Ask modu_code..."),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
		row.AddChild(gotui.New(
			gotui.WithText(" "),
			gotui.WithTextStyle(gotui.NewStyle().Reverse()),
			gotui.WithFlexShrink(0),
		))
		box.AddChild(row)
		return box
	}

	line := gotui.New(gotui.WithDisplay(gotui.DisplayFlex), gotui.WithDirection(gotui.Row))
	box.AddChild(line)
	for i, ch := range rs {
		if ch == '\n' {
			if i == r.cursor {
				line.AddChild(gotui.New(
					gotui.WithText(" "),
					gotui.WithTextStyle(gotui.NewStyle().Reverse()),
					gotui.WithFlexShrink(0),
				))
			}
			line = gotui.New(gotui.WithDisplay(gotui.DisplayFlex), gotui.WithDirection(gotui.Row))
			box.AddChild(line)
			continue
		}
		style := gotui.NewStyle()
		if i == r.cursor {
			style = style.Reverse()
		}
		line.AddChild(gotui.New(
			gotui.WithText(string(ch)),
			gotui.WithTextStyle(style),
			gotui.WithFlexShrink(0),
		))
	}
	if r.cursor == len(rs) {
		line.AddChild(gotui.New(
			gotui.WithText(" "),
			gotui.WithTextStyle(gotui.NewStyle().Reverse()),
			gotui.WithFlexShrink(0),
		))
	}
	return box
}

func stripANSIForGoTUI(s string) string {
	return uiANSIPattern.ReplaceAllString(s, "")
}

type goTUIANSITextSegment struct {
	Text  string
	Style gotui.Style
}

func goTUIANSIStyledLine(line string) *gotui.Element {
	segments := parseGoTUIANSIText(line)
	row := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Row),
		gotui.WithFlexShrink(0),
	)
	if len(segments) == 0 {
		row.AddChild(gotui.New(gotui.WithText(" ")))
		return row
	}
	for _, segment := range segments {
		if segment.Text == "" {
			continue
		}
		row.AddChild(gotui.New(
			gotui.WithText(segment.Text),
			gotui.WithTextStyle(segment.Style),
			gotui.WithFlexShrink(0),
		))
	}
	return row
}

func parseGoTUIANSIText(text string) []goTUIANSITextSegment {
	var segments []goTUIANSITextSegment
	style := gotui.NewStyle()
	for len(text) > 0 {
		idx := strings.Index(text, "\x1b[")
		if idx < 0 {
			if text != "" {
				segments = append(segments, goTUIANSITextSegment{Text: text, Style: style})
			}
			break
		}
		if idx > 0 {
			segments = append(segments, goTUIANSITextSegment{Text: text[:idx], Style: style})
			text = text[idx:]
			continue
		}
		end := strings.IndexByte(text, 'm')
		if end < 0 {
			segments = append(segments, goTUIANSITextSegment{Text: text, Style: style})
			break
		}
		applyGoTUISGR(&style, text[2:end])
		text = text[end+1:]
	}
	return segments
}

func applyGoTUISGR(style *gotui.Style, params string) {
	if params == "" {
		*style = gotui.NewStyle()
		return
	}
	codes := parseSGRCodes(params)
	for i := 0; i < len(codes); i++ {
		switch code := codes[i]; code {
		case 0:
			*style = gotui.NewStyle()
		case 1:
			*style = style.Bold()
		case 2:
			*style = style.Dim()
		case 3:
			*style = style.Italic()
		case 4:
			*style = style.Underline()
		case 22:
			style.Attrs &^= gotui.AttrBold | gotui.AttrDim
		case 23:
			style.Attrs &^= gotui.AttrItalic
		case 24:
			style.Attrs &^= gotui.AttrUnderline
		case 39:
			style.Fg = gotui.DefaultColor()
		case 38:
			if i+1 >= len(codes) {
				continue
			}
			switch codes[i+1] {
			case 2:
				if i+4 < len(codes) {
					*style = style.Foreground(gotui.RGBColor(uint8(clampSGRByte(codes[i+2])), uint8(clampSGRByte(codes[i+3])), uint8(clampSGRByte(codes[i+4]))))
					i += 4
				}
			case 5:
				if i+2 < len(codes) {
					*style = style.Foreground(gotui.ANSIColor(uint8(clampSGRByte(codes[i+2]))))
					i += 2
				}
			}
		default:
			if color, ok := goTUIANSIColor(code); ok {
				*style = style.Foreground(color)
			}
		}
	}
}

func parseSGRCodes(params string) []int {
	parts := strings.Split(params, ";")
	codes := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			codes = append(codes, 0)
			continue
		}
		code, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		codes = append(codes, code)
	}
	return codes
}

func goTUIANSIColor(code int) (gotui.Color, bool) {
	switch code {
	case 30:
		return gotui.ANSIColor(0), true
	case 31:
		return gotui.Red, true
	case 32:
		return gotui.Green, true
	case 33:
		return gotui.Yellow, true
	case 34:
		return gotui.Blue, true
	case 35:
		return gotui.Magenta, true
	case 36:
		return gotui.Cyan, true
	case 37:
		return gotui.White, true
	case 90:
		return gotui.BrightBlack, true
	case 91:
		return gotui.BrightRed, true
	case 92:
		return gotui.BrightGreen, true
	case 93:
		return gotui.BrightYellow, true
	case 94:
		return gotui.BrightBlue, true
	case 95:
		return gotui.BrightMagenta, true
	case 96:
		return gotui.BrightCyan, true
	case 97:
		return gotui.BrightWhite, true
	default:
		return gotui.DefaultColor(), false
	}
}

func clampSGRByte(value int) int {
	if value < 0 {
		return 0
	}
	if value > 255 {
		return 255
	}
	return value
}

type goTUIBridgePrinter struct {
	root *goTUIRoot
}

func (p *goTUIBridgePrinter) PrintInfo(msg string) {
	if p.root != nil {
		p.root.externalInfo(msg)
	}
}

func (p *goTUIBridgePrinter) PrintError(err error) {
	if err != nil && p.root != nil {
		p.root.externalInfo("error: " + err.Error())
	}
}

func (p *goTUIBridgePrinter) PrintUser(msg string) {
	if p.root != nil {
		p.root.externalUser(msg)
	}
}

func (p *goTUIBridgePrinter) ClearLine() {}

func (p *goTUIBridgePrinter) PrintSection(title string, lines []string) {
	if p.root != nil {
		p.root.externalInfo(fmt.Sprintf("%s\n%s", title, strings.Join(lines, "\n")))
	}
}

func (p *goTUIBridgePrinter) ClearScreen() {
	if p.root != nil {
		p.root.queue(func() {
			p.root.model.blocks = nil
			p.root.bump()
		})
	}
}
