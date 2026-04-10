package agent

// SessionStatus is the lifecycle state of an agent session.
// Inspired by Anthropic Managed Agents API session states.
type SessionStatus string

const (
	// SessionStatusIdle means the agent is ready but not running.
	SessionStatusIdle SessionStatus = "idle"
	// SessionStatusRunning means the agent is actively processing.
	SessionStatusRunning SessionStatus = "running"
	// SessionStatusPaused means the agent is paused at an interrupt point, waiting for Resume().
	SessionStatusPaused SessionStatus = "paused"
	// SessionStatusCompleted means the agent finished successfully.
	SessionStatusCompleted SessionStatus = "completed"
	// SessionStatusFailed means the agent finished with an error.
	SessionStatusFailed SessionStatus = "failed"
)

// InterruptReason identifies why the agent paused execution.
type InterruptReason string

const (
	// InterruptReasonToolApproval is raised when a tool call needs human approval.
	InterruptReasonToolApproval InterruptReason = "tool_use_approval"
	// InterruptReasonMaxSteps is raised when the agent has reached its configured step limit.
	InterruptReasonMaxSteps InterruptReason = "max_steps_reached"
)

// InterruptEvent describes a point where the agent has paused and needs a decision.
// It is emitted as EventTypeInterrupt on the event stream.
type InterruptEvent struct {
	Reason InterruptReason

	// Set for InterruptReasonToolApproval:
	ToolCallID string
	ToolName   string
	ToolArgs   map[string]any

	// Set for InterruptReasonMaxSteps:
	StepCount int
}

// ResumeDecision is the caller's response to an interrupt.
// Pass it to Agent.Resume() to unblock the paused session.
type ResumeDecision struct {
	// Allow continues execution: approves the tool call or continues past max steps.
	// When false, the tool is denied or the session is stopped.
	Allow bool
	// Message is an optional user message to inject into the conversation on resume.
	Message string
}
