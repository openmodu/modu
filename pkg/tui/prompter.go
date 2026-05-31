package tui

import (
	"context"
	"strings"

	"github.com/openmodu/modu/pkg/approval"
	"github.com/openmodu/modu/pkg/types"
)

// channelPrompter implements coding_agent.Prompter by funnelling every kind of
// prompt through a single approval.Request channel that the TUI's approval
// watcher renders. It replaces the four near-identical Set*Callback closures the
// TUI entry points used to register.
type channelPrompter struct {
	ctx        context.Context
	approvalCh chan<- approval.Request
	stop       <-chan struct{} // optional extra cancellation (e.g. legacy app.StopCh); may be nil
}

func newChannelPrompter(ctx context.Context, approvalCh chan<- approval.Request, stop <-chan struct{}) *channelPrompter {
	return &channelPrompter{ctx: ctx, approvalCh: approvalCh, stop: stop}
}

// ask sends req and blocks for the reply, honouring ctx and the optional stop
// channel (a nil stop never fires, so the case is a no-op when unused).
func (p *channelPrompter) ask(req approval.Request) (string, error) {
	respCh := make(chan string, 1)
	req.Response = respCh
	select {
	case p.approvalCh <- req:
	case <-p.ctx.Done():
		return "", p.ctx.Err()
	case <-p.stop:
		return "", context.Canceled
	}
	select {
	case d := <-respCh:
		return d, nil
	case <-p.ctx.Done():
		return "", p.ctx.Err()
	case <-p.stop:
		return "", context.Canceled
	}
}

func (p *channelPrompter) ApproveTool(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error) {
	d, err := p.ask(approval.Request{ToolName: toolName, ToolCallID: toolCallID, Args: args})
	if err != nil {
		return types.ToolApprovalDeny, err
	}
	return types.ToolApprovalDecision(d), nil
}

func (p *channelPrompter) ApprovePlan(plan string, steps []string) string {
	anySteps := make([]any, len(steps))
	for i, s := range steps {
		anySteps[i] = s
	}
	d, err := p.ask(approval.Request{
		ToolName:   "exit_plan_mode",
		ToolCallID: "plan",
		Args:       map[string]any{"plan": plan, "steps": anySteps},
	})
	if err != nil {
		return "reject"
	}
	return d
}

func (p *channelPrompter) Confirm(title, body string, defaultYes bool) bool {
	d, err := p.ask(approval.Request{
		ToolName:   "extension_confirm",
		ToolCallID: "extension_confirm",
		Args:       map[string]any{"title": title, "body": body, "defaultYes": defaultYes},
	})
	if err != nil {
		return defaultYes
	}
	switch strings.TrimSpace(strings.ToLower(d)) {
	case "allow", "allow_always", "approve", "yes", "y":
		return true
	case "deny", "deny_always", "reject", "no", "n":
		return false
	default:
		return defaultYes
	}
}

func (p *channelPrompter) Select(title string, options []string) string {
	first := ""
	if len(options) > 0 {
		first = options[0]
	}
	d, err := p.ask(approval.Request{
		ToolName:   "extension_select",
		ToolCallID: "extension_select",
		Args:       map[string]any{"title": title, "options": options},
	})
	if err != nil {
		return first
	}
	d = strings.TrimSpace(d)
	for _, option := range options {
		if d == option {
			return option
		}
	}
	return first
}
