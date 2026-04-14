package coding_agent

// SessionEventType identifies the type of a session-level event.
type SessionEventType string

const (
	SessionEventAutoRetryStart  SessionEventType = "auto_retry_start"
	SessionEventAutoRetryEnd    SessionEventType = "auto_retry_end"
	SessionEventModelChange     SessionEventType = "model_change"
	SessionEventThinkingChange  SessionEventType = "thinking_change"
	SessionEventCompactionStart SessionEventType = "compaction_start"
	SessionEventCompactionDone  SessionEventType = "compaction_done"
	SessionEventSessionSwitch   SessionEventType = "session_switch"
	SessionEventCwdChanged      SessionEventType = "cwd_changed"
	SessionEventWorktreeCreate  SessionEventType = "worktree_create"
	SessionEventWorktreeRemove  SessionEventType = "worktree_remove"
	SessionEventSubagentStart   SessionEventType = "subagent_start"
	SessionEventSubagentStop    SessionEventType = "subagent_stop"
	SessionEventPermissionReq   SessionEventType = "permission_request"
	SessionEventPermissionDeny  SessionEventType = "permission_denied"
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

	// Cwd/worktree fields
	OldCwd string `json:"oldCwd,omitempty"`
	NewCwd string `json:"newCwd,omitempty"`
	Path   string `json:"path,omitempty"`

	// Subagent fields
	SubagentName       string `json:"subagentName,omitempty"`
	SubagentTask       string `json:"subagentTask,omitempty"`
	SubagentBackground bool   `json:"subagentBackground,omitempty"`
	SubagentResult     string `json:"subagentResult,omitempty"`

	// Permission fields
	ToolName string `json:"toolName,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

const sessionEventChannel = "session_event"
