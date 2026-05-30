package coding_agent

import "github.com/openmodu/modu/pkg/agent"

// This file is the session-side wiring for the approval service
// (pkg/coding_agent/approval). The session owns an *approval.Manager, exposes a
// thin API over it, and implements approval.Observer.

// SetToolApprovalCallback sets the interactive approval callback on the session.
// When set, the callback is called before each tool execution that is not
// already covered by an always-allow or always-deny rule.
// Passing nil disables interactive approval (all tools auto-approved).
func (cs *CodingSession) SetToolApprovalCallback(fn func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error)) {
	cs.approvalManager.SetCallback(fn)
}

// AllowToolAlways marks a tool as always allowed for this session.
func (cs *CodingSession) AllowToolAlways(toolName string) {
	cs.approvalManager.AllowAlways(toolName)
}

// DenyToolAlways marks a tool as always denied for this session.
func (cs *CodingSession) DenyToolAlways(toolName string) {
	cs.approvalManager.DenyAlways(toolName)
}

// ClearToolDecision removes any cached always-allow or always-deny decision for a tool.
func (cs *CodingSession) ClearToolDecision(toolName string) {
	cs.approvalManager.ClearDecision(toolName)
}

// ResetToolApprovals clears all cached tool approval rules and the callback.
func (cs *CodingSession) ResetToolApprovals() {
	cs.approvalManager.Reset()
}

// OnPermissionRequest / OnPermissionDenied implement approval.Observer.

func (cs *CodingSession) OnPermissionRequest(toolName, toolCallID string, args map[string]any) {
	cs.runHarnessPermissionRequest(HarnessToolCall{ToolName: toolName, Args: args})
	cs.writeRuntimeState()
}

func (cs *CodingSession) OnPermissionDenied(toolName, toolCallID string, args map[string]any, reason string) {
	cs.runHarnessPermissionDenied(HarnessToolCall{ToolName: toolName, Args: args}, reason)
	cs.writeRuntimeState()
}
