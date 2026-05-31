package coding_agent

import "github.com/openmodu/modu/pkg/types"

// Prompter is the unified host-interaction contract (L5). A host implements it
// once and registers it with SetPrompter, instead of wiring the four separate
// Set*Callback hooks. Each method is one kind of user prompt; signatures match
// the underlying callbacks exactly so SetPrompter is a plain fan-out.
//
// All methods are called synchronously from the agent turn and should block
// until the user answers (or return the given fallback when the host cannot
// prompt, e.g. headless runs).
type Prompter interface {
	// Confirm asks a yes/no question and returns the decision; defaultYes is
	// the value to assume when the host cannot prompt.
	Confirm(title, body string, defaultYes bool) bool
	// Select asks the user to pick one of options and returns the chosen one.
	Select(title string, options []string) string
	// ApprovePlan presents a plan for approval. It returns one of "approve",
	// "approve_auto", "reject", or "reject:<feedback>".
	ApprovePlan(plan string, steps []string) string
	// ApproveTool gates a single tool execution.
	ApproveTool(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error)
}

// SetPrompter wires every interactive callback from a single Prompter. It is
// the one-call replacement for SetExtensionConfirmCallback /
// SetExtensionSelectCallback / SetPlanDecisionCallback / SetToolApprovalCallback.
// Passing nil is a no-op (existing callbacks are left untouched).
func (s *CodingSession) SetPrompter(p Prompter) {
	if p == nil {
		return
	}
	s.SetExtensionConfirmCallback(p.Confirm)
	s.SetExtensionSelectCallback(p.Select)
	s.SetPlanDecisionCallback(p.ApprovePlan)
	s.SetToolApprovalCallback(p.ApproveTool)
}
