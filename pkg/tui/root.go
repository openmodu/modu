// Package tui hosts the modu_code interactive terminal UI.
//
// Files in this package are organised by component / concern:
//
//	root.go          — goTUIRoot struct, lifecycle, top-level KeyMap, Render compose
//	input.go         — input box rendering + key dispatch + cursor + history
//	approval.go      — tool-approval dialog (state, key map, rendering)
//	suggestions.go   — slash-command autocomplete (state + rendering)
//	prompt.go        — submit / shell / slash / agent prompt routing
//	events.go        — agent + session event handling, scrollback push helpers
//	statusbar.go     — single-line status text below the input
//	bridge.go        — channel-bridge (e.g. Telegram) printer adapter
//	ansi.go          — ANSI escape stripper used when feeding text to go-tui
//	render.go        — block / tool / markdown rendering for scrollback
//	model.go         — shared UI state (blocks, tools, draft history file I/O)
//	theme.go         — colors / lipgloss styles
//	agent_events.go  — agent.Event → uiBlock translation (model layer)
//	run.go           — public Run entry point that wires everything together
package tui

import (
	"context"
	"strings"
	"sync"
	"time"

	gotui "github.com/grindlemire/go-tui"
	runewidth "github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/openmodu/modu/pkg/approval"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/mailboxrt"
	"github.com/openmodu/modu/pkg/types"
)

// goTUIRoot is the single root component for the inline TUI. It owns the
// shared UI state and routes events to the per-component method receivers
// defined in this package's other files.
type goTUIRoot struct {
	ctx            context.Context
	session        *coding_agent.CodingSession
	modelInfo      *types.Model
	mailboxRuntime *mailboxrt.Runtime
	histFile       string
	approvalCh     <-chan approval.Request
	promptMu       *sync.Mutex

	app     *gotui.App
	model   *uiModel
	draft   *gotui.State[string]
	refresh *gotui.State[int]
	cursor  int

	history      []string
	historyIndex int
	historyDraft string // draft saved when entering history navigation
	tgUsername   string

	slashMatches      []slashCommandDef
	slashMatchIdx     int
	slashScrollOffset int
}

func newGoTUIRoot(
	ctx context.Context,
	session *coding_agent.CodingSession,
	modelInfo *types.Model,
	mailboxRuntime *mailboxrt.Runtime,
	histFile string,
	approvalCh <-chan approval.Request,
	promptMu *sync.Mutex,
) *goTUIRoot {
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

// ─── go-tui lifecycle ────────────────────────────────────────────────────────

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

// bump forces a re-render by mutating the refresh state.
func (r *goTUIRoot) bump() {
	r.refresh.Set(r.refresh.Get() + 1)
}

func (r *goTUIRoot) appStopCh() <-chan struct{} {
	if r.app == nil {
		return r.ctx.Done()
	}
	return r.app.StopCh()
}

func (r *goTUIRoot) setInlineHeight(h int) {
	if r.app != nil {
		r.app.SetInlineHeight(h)
	}
}

// queue schedules fn to run on the main event loop. Outside the loop (e.g.
// tests with no app), the function runs synchronously.
func (r *goTUIRoot) queue(fn func()) {
	if r.app == nil {
		fn()
		return
	}
	r.app.QueueUpdate(fn)
}

// positionCursor moves the real terminal cursor to the text-input position
// so the OS knows where to anchor the IME candidate window. Must be called
// after each app.Render().
func (r *goTUIRoot) positionCursor(app *gotui.App) {
	if app == nil {
		return
	}
	_, termHeight := app.Terminal().Size()
	inlineHeight := app.InlineHeight()
	if inlineHeight <= 0 {
		return
	}
	inlineStartRow := termHeight - inlineHeight

	rs := []rune(r.draft.Get())
	cursor := clampInt(r.cursor, 0, len(rs))

	// Row 0 of widget = top separator; row 1 = first input line.
	widgetRow := 1
	col := 2 // after "> " (2 chars)
	for i := 0; i < cursor; i++ {
		ch := rs[i]
		if ch == '\n' {
			widgetRow++
			col = 2 // continuation indent "  "
		} else {
			col += runewidth.RuneWidth(ch)
		}
	}

	app.Terminal().SetCursor(col, inlineStartRow+widgetRow)
}

// ─── KeyMap ──────────────────────────────────────────────────────────────────

func (r *goTUIRoot) KeyMap() gotui.KeyMap {
	if r.model.pendingPerm != nil {
		return r.permissionKeyMap()
	}
	dispatch := func(ke gotui.KeyEvent) { r.handleInputKey(ke) }
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
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) {
			if len(r.slashMatches) > 0 {
				r.slashMatches = nil
				r.slashMatchIdx = 0
				r.bump()
				return
			}
			if r.model.state == uiStateQuerying || r.model.state == uiStatePermission {
				r.abortQuery()
				return
			}
			r.model.state = uiStateInput
			r.model.statusMsg = ""
			r.bump()
		}),
		// Editing keys all funnel into handleInputKey (defined in input.go).
		gotui.OnStop(gotui.KeyCtrlJ, dispatch),
		gotui.OnStop(gotui.KeyHome, dispatch),
		gotui.OnStop(gotui.KeyEnd, dispatch),
		gotui.OnStop(gotui.KeyBackspace, dispatch),
		gotui.OnStop(gotui.KeyDelete, dispatch),
		gotui.OnStop(gotui.KeyLeft, dispatch),
		gotui.OnStop(gotui.KeyRight, dispatch),
		gotui.OnStop(gotui.KeyUp, dispatch),
		gotui.OnStop(gotui.KeyDown, dispatch),
		gotui.OnStop(gotui.KeyTab, dispatch),
		gotui.OnStop(gotui.KeyEnter, dispatch),
		gotui.OnStop(gotui.AnyRune, dispatch),
	}
}

// ─── Render ──────────────────────────────────────────────────────────────────

// Render builds the inline widget. Two layouts:
//   - Permission mode: sep / approval-dialog / sep / meta
//   - Normal mode    : sep / input / sep / [suggestions | status] / meta
func (r *goTUIRoot) Render(app *gotui.App) *gotui.Element {
	_ = r.refresh.Get()
	width, _ := app.Size()
	if width <= 0 {
		width = 80
	}
	r.model.width = max(20, width-2)
	r.model.tgUsername = r.tgUsername

	sep := strings.Repeat("─", width)
	sepStyle := gotui.NewStyle().Dim()
	meta := strings.TrimSpace(stripANSIForGoTUI(r.model.renderInputMeta()))

	addSep := func(root *gotui.Element) {
		root.AddChild(gotui.New(
			gotui.WithText(sep),
			gotui.WithTextStyle(sepStyle),
			gotui.WithFlexShrink(0),
		))
	}
	addMeta := func(root *gotui.Element) {
		if meta != "" {
			root.AddChild(gotui.New(
				gotui.WithText(meta),
				gotui.WithTextStyle(gotui.NewStyle().Dim()),
				gotui.WithFlexShrink(0),
			))
		}
	}

	root := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithHeightPercent(100),
	)

	if r.model.pendingPerm != nil {
		// Permission mode replaces the input area with the approval dialog.
		neededH := 4 // sep + tool + hints + sep
		if meta != "" {
			neededH++
		}
		if neededH != app.InlineHeight() {
			app.SetInlineHeight(neededH)
		}
		addSep(root)
		root.AddChild(r.renderApprovalWidget())
		addSep(root)
		addMeta(root)
		return root
	}

	// Normal mode: compute height accounting for suggestion list (if any).
	draftLines := strings.Count(r.draft.Get(), "\n") + 1
	neededH := draftLines + 3 // sep + input + sep + status/suggestion
	if meta != "" {
		neededH++
	}
	visibleSuggestions := len(r.slashMatches)
	if visibleSuggestions > slashVisibleRows {
		visibleSuggestions = slashVisibleRows
	}
	if visibleSuggestions > 0 {
		// Suggestions replace the single status line; add the extra rows
		// plus one detail row for the highlighted entry.
		neededH += visibleSuggestions - 1
		neededH++
	}
	if neededH < 5 {
		neededH = 5
	}
	if neededH != app.InlineHeight() {
		app.SetInlineHeight(neededH)
	}

	addSep(root)
	root.AddChild(r.renderInput(width))
	addSep(root)
	if rows := r.renderSlashSuggestions(); rows != nil {
		for _, row := range rows {
			root.AddChild(row)
		}
	} else {
		bottomText, bottomStyle := r.bottomLine()
		root.AddChild(gotui.New(
			gotui.WithText(bottomText),
			gotui.WithTextStyle(bottomStyle),
			gotui.WithFlexShrink(0),
		))
	}
	addMeta(root)
	return root
}
