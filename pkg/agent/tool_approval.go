package agent

// ToolApprovalDecision is the result of a tool approval request.
type ToolApprovalDecision string

const (
	// ToolApprovalAllow allows this tool call once.
	ToolApprovalAllow ToolApprovalDecision = "allow"
	// ToolApprovalAllowAlways allows all future calls to this tool.
	ToolApprovalAllowAlways ToolApprovalDecision = "allow_always"
	// ToolApprovalDeny denies this tool call once.
	ToolApprovalDeny ToolApprovalDecision = "deny"
	// ToolApprovalDenyAlways denies all future calls to this tool.
	ToolApprovalDenyAlways ToolApprovalDecision = "deny_always"
)

// IsAllow returns true if the decision permits execution.
func (d ToolApprovalDecision) IsAllow() bool {
	return d == ToolApprovalAllow || d == ToolApprovalAllowAlways
}
