package tui

import (
	"fmt"
	"strings"
	"time"

	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/approval"
)

// planMarkdown turns the exit_plan_mode args into a markdown document so the
// plan renders with headings/lists in the transcript.
func planMarkdown(args map[string]any) string {
	plan, _ := args["plan"].(string)
	var b strings.Builder
	b.WriteString("## 📋 Proposed plan\n\n")
	b.WriteString(strings.TrimSpace(plan))
	if raw, ok := args["steps"].([]any); ok && len(raw) > 0 {
		b.WriteString("\n\n### Steps\n")
		for i, s := range raw {
			if str, ok := s.(string); ok && str != "" {
				fmt.Fprintf(&b, "\n%d. %s", i+1, str)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// permissionKeyMap is active while a tool is awaiting approval. The hotkeys
// are: y/Y allow once, a/A always, n/N or Esc deny once, d/D deny always.
// Enter is treated as "allow".
func (r *goTUIRoot) permissionKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.abortQuery() }),
		gotui.OnStop(gotui.KeyCtrlO, func(ke gotui.KeyEvent) {
			r.toggleTranscriptMode()
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

// planApprovalKeyMap is active while a plan is awaiting approval:
// y/Y/Enter approve, a/A approve & auto-accept edits, n/N/Esc reject (which
// switches to free-form feedback capture).
func (r *goTUIRoot) planApprovalKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.abortQuery() }),
		gotui.OnStop(gotui.KeyCtrlO, func(ke gotui.KeyEvent) {
			r.toggleTranscriptMode()
		}),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.resolvePlan("approve") }),
		gotui.OnStop(gotui.Rune('y'), func(ke gotui.KeyEvent) { r.resolvePlan("approve") }),
		gotui.OnStop(gotui.Rune('Y'), func(ke gotui.KeyEvent) { r.resolvePlan("approve") }),
		gotui.OnStop(gotui.Rune('a'), func(ke gotui.KeyEvent) { r.resolvePlan("approve_auto") }),
		gotui.OnStop(gotui.Rune('A'), func(ke gotui.KeyEvent) { r.resolvePlan("approve_auto") }),
		gotui.OnStop(gotui.Rune('n'), func(ke gotui.KeyEvent) { r.beginPlanReject() }),
		gotui.OnStop(gotui.Rune('N'), func(ke gotui.KeyEvent) { r.beginPlanReject() }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.beginPlanReject() }),
	}
}

// planRejectKeyMap captures the free-form rejection reason. Enter sends it
// (empty = plain rejection), Esc cancels to a plain rejection.
func (r *goTUIRoot) planRejectKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.abortQuery() }),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) {
			reason := strings.TrimSpace(r.model.planRejectBuf)
			if reason == "" {
				r.resolvePlan("reject")
			} else {
				r.resolvePlan("reject:" + reason)
			}
		}),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.resolvePlan("reject") }),
		gotui.OnStop(gotui.KeyBackspace, func(ke gotui.KeyEvent) {
			rs := []rune(r.model.planRejectBuf)
			if len(rs) > 0 {
				r.model.planRejectBuf = string(rs[:len(rs)-1])
				r.bump()
			}
		}),
		gotui.OnStop(gotui.AnyRune, func(ke gotui.KeyEvent) {
			if ke.Rune == 0 || ke.Mod != 0 {
				return
			}
			r.model.planRejectBuf += string(ke.Rune)
			r.bump()
		}),
	}
}

// resolvePlan sends the plan decision back to the waiting callback and
// returns the UI to its normal querying state.
func (r *goTUIRoot) resolvePlan(decision string) {
	r.model.planRejectBuf = ""
	if !r.resolvePendingApproval(decision) {
		return
	}
	r.model.state = uiStateQuerying
	r.model.statusMsg = "thinking"
	r.bump()
}

// beginPlanReject switches from the approve/reject prompt to free-form
// feedback capture without yet resolving the request.
func (r *goTUIRoot) beginPlanReject() {
	if r.model.pendingPerm == nil {
		return
	}
	r.model.planRejectBuf = ""
	r.model.state = uiStatePlanReject
	r.model.statusMsg = "rejecting plan"
	r.bump()
}

func (r *goTUIRoot) handleApprovalRequest(req approval.Request) {
	// For the plan gate, render the full plan as a markdown block in the
	// transcript (glamour-formatted) instead of cramming it into the inline
	// approval widget. The widget then only carries the decision prompt.
	if req.ToolName == "exit_plan_mode" {
		if md := planMarkdown(req.Args); md != "" {
			block := uiBlock{Kind: "assistant", Content: md, Timestamp: time.Now()}
			r.model.appendBlock(block)
			r.pushBlockAbove(block)
		}
	}
	r.model.pendingPerm = &req
	r.model.state = uiStatePermission
	r.model.statusMsg = "permission required"
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
	r.continueQueuedAfterCancel = false
	if r.model.queryCancel != nil {
		r.model.queryCancel()
		r.model.queryCancel = nil
	}
	if r.session != nil {
		if ag := r.session.GetAgent(); ag != nil {
			ag.ClearAllQueues()
		}
		r.session.Abort()
		r.session.AbortBash()
	}
	r.model.queryActive = false
	r.resolvePendingApproval("deny")
	r.model.state = uiStateInput
	r.model.statusMsg = "interrupted"
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

	if perm.ToolName == "exit_plan_mode" {
		return r.renderPlanApprovalWidget(perm, container)
	}

	container.AddChild(gotui.New(
		gotui.WithText("⏺ Permission required"),
		gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow).Bold()),
		gotui.WithFlexShrink(0),
	))
	container.AddChild(gotui.New(
		gotui.WithText("  tool: "+perm.ToolName),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))
	if args := formatToolInput(perm.ToolName, perm.Args); args != "" {
		if len(args) > 80 {
			args = args[:80] + "…"
		}
		container.AddChild(gotui.New(
			gotui.WithText("  args: "+args),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}

	hintRow := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Row),
		gotui.WithFlexShrink(0),
	)
	sp := func() *gotui.Element {
		return gotui.New(gotui.WithText("  "), gotui.WithFlexShrink(0))
	}
	hintRow.AddChild(gotui.New(gotui.WithText("  actions: "), gotui.WithTextStyle(gotui.NewStyle().Dim()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(gotui.New(gotui.WithText("[Y]es"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Green).Bold()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(sp())
	hintRow.AddChild(gotui.New(gotui.WithText("[N]o"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Red).Bold()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(sp())
	allowLabel := "[A]lways allow"
	denyLabel := "[D]eny always"
	if perm.ToolName == "bash" {
		allowLabel = "[A]llow this command"
		denyLabel = "[D]eny this command"
	}
	hintRow.AddChild(gotui.New(gotui.WithText(allowLabel), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow).Bold()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(sp())
	hintRow.AddChild(gotui.New(gotui.WithText(denyLabel), gotui.WithTextStyle(gotui.NewStyle().Dim()), gotui.WithFlexShrink(0)))
	container.AddChild(hintRow)

	return container
}

// renderPlanRejectWidget renders the free-form rejection-reason input shown
// after the user rejects a plan.
func (r *goTUIRoot) renderPlanRejectWidget() *gotui.Element {
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)
	container.AddChild(gotui.New(
		gotui.WithText("⏺ Rejecting plan — what should change? (Enter to send, empty Enter = just reject, Esc cancels)"),
		gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow).Bold()),
		gotui.WithFlexShrink(0),
	))
	row := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Row),
		gotui.WithFlexShrink(0),
	)
	row.AddChild(gotui.New(gotui.WithText("> "), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Red)), gotui.WithFlexShrink(0)))
	row.AddChild(gotui.New(gotui.WithText(r.model.planRejectBuf), gotui.WithFlexShrink(0)))
	row.AddChild(gotui.New(gotui.WithText("▋"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Green)), gotui.WithFlexShrink(0)))
	container.AddChild(row)
	return container
}

// renderPlanApprovalWidget renders the slim plan-approval gate. The plan
// itself is already rendered as a markdown block in the transcript, so the
// widget only carries the three-way decision prompt:
//
//	⏺ Plan ready (above) — proceed?
//	  [Y]es, start coding   [A] auto-accept edits   [N]o, keep planning
func (r *goTUIRoot) renderPlanApprovalWidget(perm *approval.Request, container *gotui.Element) *gotui.Element {
	container.AddChild(gotui.New(
		gotui.WithText("⏺ Plan approval"),
		gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow).Bold()),
		gotui.WithFlexShrink(0),
	))
	container.AddChild(gotui.New(
		gotui.WithText(fmt.Sprintf("  plan shown above  steps=%d", planApprovalStepCount(perm.Args))),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	container.AddChild(gotui.New(
		gotui.WithText("  auto-accept allows write/edit/bash for this session"),
		gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow)),
		gotui.WithFlexShrink(0),
	))

	sp := func() *gotui.Element {
		return gotui.New(gotui.WithText("   "), gotui.WithFlexShrink(0))
	}
	hintRow := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Row),
		gotui.WithFlexShrink(0),
	)
	hintRow.AddChild(gotui.New(gotui.WithText("  actions: "), gotui.WithTextStyle(gotui.NewStyle().Dim()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(gotui.New(gotui.WithText("[Y]es, start coding"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Green).Bold()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(sp())
	hintRow.AddChild(gotui.New(gotui.WithText("[A] auto-accept edits"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow).Bold()), gotui.WithFlexShrink(0)))
	hintRow.AddChild(sp())
	hintRow.AddChild(gotui.New(gotui.WithText("[N]o, keep planning"), gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Red).Bold()), gotui.WithFlexShrink(0)))
	container.AddChild(hintRow)

	return container
}

func planApprovalStepCount(args map[string]any) int {
	raw, ok := args["steps"].([]any)
	if !ok {
		return 0
	}
	count := 0
	for _, step := range raw {
		if text, ok := step.(string); ok && strings.TrimSpace(text) != "" {
			count++
		}
	}
	return count
}
