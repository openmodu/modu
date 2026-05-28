package agent

type SessionStatus string

const (
	SessionStatusIdle      SessionStatus = "idle"
	SessionStatusRunning   SessionStatus = "running"
	SessionStatusPaused    SessionStatus = "paused"
	SessionStatusCompleted SessionStatus = "completed"
	SessionStatusFailed    SessionStatus = "failed"
)

type InterruptReason string

const (
	InterruptReasonToolApproval InterruptReason = "tool_use_approval"
	InterruptReasonMaxSteps     InterruptReason = "max_steps_reached"
)

type InterruptEvent struct {
	Reason InterruptReason

	ToolCallID string
	ToolName   string
	ToolArgs   map[string]any

	StepCount int
}

type ResumeDecision struct {
	Allow   bool
	Message string
}
