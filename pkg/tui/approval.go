package tui

import (
	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/approval"
)

// permissionKeyMap is active while a tool is awaiting approval. The hotkeys
// match Claude Code: y/Y allow once, a/A always, n/N or Esc deny once,
// d/D deny always. Enter is treated as "allow".
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

func (r *goTUIRoot) handleApprovalRequest(req approval.Request) {
	r.model.pendingPerm = &req
	r.model.state = uiStatePermission
	r.model.statusMsg = "permission required"
	r.setInlineHeight(5)
	r.bump()
	if req.Cancel != nil {
		r.watchApprovalCancel(req.ToolCallID, req.Cancel)
	}
}

// watchApprovalCancel dismisses the inline prompt when an external channel
// (e.g. Telegram) responds for the user.
func (r *goTUIRoot) watchApprovalCancel(toolCallID string, cancel <-chan struct{}) {
	go func() {
		select {
		case <-cancel:
			r.queue(func() {
				if r.model.pendingPerm == nil || r.model.pendingPerm.ToolCallID != toolCallID {
					return
				}
				r.dismissPendingApproval()
				r.model.state = uiStateQuerying
				r.model.statusMsg = "approval dismissed"
				r.bump()
			})
		case <-r.appStopCh():
		}
	}()
}

func (r *goTUIRoot) resolvePendingApproval(decision string) bool {
	if r.model.pendingPerm == nil {
		return false
	}
	req := r.model.pendingPerm
	r.model.pendingPerm = nil
	if req.Response != nil {
		req.Response <- decision
	}
	return true
}

func (r *goTUIRoot) dismissPendingApproval() bool {
	if r.model.pendingPerm == nil {
		return false
	}
	r.model.pendingPerm = nil
	return true
}

func (r *goTUIRoot) approve(decision string) {
	if !r.resolvePendingApproval(decision) {
		return
	}
	r.model.state = uiStateQuerying
	r.model.statusMsg = "thinking"
	r.bump()
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
	r.resolvePendingApproval("deny")
	r.model.state = uiStateInput
	r.model.statusMsg = "interrupted"
	r.setInlineHeight(5)
	r.bump()
}

// renderApprovalWidget builds the permission dialog that replaces the input
// area while approval is pending:
//
//	⏺ toolName(args)
//	  [Y]es  [N]o  [A]lways allow  [D]eny always
func (r *goTUIRoot) renderApprovalWidget() *gotui.Element {
	perm := r.model.pendingPerm
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)

	toolRow := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Row),
		gotui.WithFlexShrink(0),
	)
	toolRow.AddChild(gotui.New(
		gotui.WithText("⏺ "),
		gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow)),
		gotui.WithFlexShrink(0),
	))
	toolRow.AddChild(gotui.New(
		gotui.WithText(perm.ToolName),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))
	if args := formatToolInput(perm.ToolName, perm.Args); args != "" {
		if len(args) > 80 {
			args = args[:80] + "…"
		}
		toolRow.AddChild(gotui.New(
			gotui.WithText("("+args+")"),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}
	container.AddChild(toolRow)

	hintRow := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Row),
		gotui.WithFlexShrink(0),
	)
	sp := func() *gotui.Element {
		return gotui.New(gotui.WithText("  "), gotui.WithFlexShrink(0))
	}
	hintRow.AddChild(gotui.New(gotui.WithText("  "), gotui.WithFlexShrink(0)))
	hintRow.AddChild(gotui.New(gotui.WithText("[Y]es"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Green).Bold()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(sp())
	hintRow.AddChild(gotui.New(gotui.WithText("[N]o"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Red).Bold()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(sp())
	hintRow.AddChild(gotui.New(gotui.WithText("[A]lways allow"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow).Bold()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(sp())
	hintRow.AddChild(gotui.New(gotui.WithText("[D]eny always"), gotui.WithTextStyle(gotui.NewStyle().Dim()), gotui.WithFlexShrink(0)))
	container.AddChild(hintRow)

	return container
}
