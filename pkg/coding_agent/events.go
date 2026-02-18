package coding_agent

// SessionEventType identifies the type of a session-level event.
type SessionEventType string

const (
	SessionEventAutoRetryStart SessionEventType = "auto_retry_start"
	SessionEventAutoRetryEnd   SessionEventType = "auto_retry_end"
	SessionEventModelChange    SessionEventType = "model_change"
	SessionEventThinkingChange SessionEventType = "thinking_change"
	SessionEventCompactionDone SessionEventType = "compaction_done"
	SessionEventSessionSwitch  SessionEventType = "session_switch"
)

// SessionEvent represents a session-level event emitted via the EventBus.
type SessionEvent struct {
	Type SessionEventType `json:"type"`

	// Auto-retry fields
	Attempt      int    `json:"attempt,omitempty"`
	MaxAttempts  int    `json:"maxAttempts,omitempty"`
	DelayMs      int    `json:"delayMs,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	Success      *bool  `json:"success,omitempty"`

	// Model/thinking fields
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"modelId,omitempty"`
	Level    string `json:"level,omitempty"`
}

const sessionEventChannel = "session_event"
