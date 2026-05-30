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
	SessionEventExtensionNotify SessionEventType = "extension_notify"
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

	// Extension notification fields
	ExtensionName string `json:"extensionName,omitempty"`
	Message       string `json:"message,omitempty"`
}

const sessionEventChannel = "session_event"

type HarnessToolCall struct {
	ToolName string
	Args     map[string]any
}

type HarnessSubagentRun struct {
	Name       string `json:"name"`
	Task       string `json:"task"`
	Background bool   `json:"background"`
}

func (s *engine) runHarnessPermissionRequest(call HarnessToolCall) {
	s.emitSessionEvent(SessionEvent{
		Type:     SessionEventPermissionReq,
		ToolName: call.ToolName,
	})
}

func (s *engine) runHarnessPermissionDenied(call HarnessToolCall, reason string) {
	s.emitSessionEvent(SessionEvent{
		Type:     SessionEventPermissionDeny,
		ToolName: call.ToolName,
		Reason:   reason,
	})
}

func (s *engine) runHarnessCwdChanged(oldCwd, newCwd string) {
	s.emitSessionEvent(SessionEvent{
		Type:   SessionEventCwdChanged,
		OldCwd: oldCwd,
		NewCwd: newCwd,
	})
}

func (s *engine) runHarnessWorktreeCreate(path string) {
	s.emitSessionEvent(SessionEvent{
		Type: SessionEventWorktreeCreate,
		Path: path,
	})
}

func (s *engine) runHarnessWorktreeRemove(path string) {
	s.emitSessionEvent(SessionEvent{
		Type: SessionEventWorktreeRemove,
		Path: path,
	})
}

func (s *engine) OnSubagentStart(name, task string, background bool) {
	s.onSubagentStart(HarnessSubagentRun{Name: name, Task: task, Background: background})
}

func (s *engine) OnSubagentStop(name, task string, background bool, result string, err error) {
	s.onSubagentStop(HarnessSubagentRun{Name: name, Task: task, Background: background}, result, err)
}

func (s *engine) onSubagentStart(run HarnessSubagentRun) {
	s.emitSessionEvent(SessionEvent{
		Type:               SessionEventSubagentStart,
		SubagentName:       run.Name,
		SubagentTask:       run.Task,
		SubagentBackground: run.Background,
	})
}

func (s *engine) onSubagentStop(run HarnessSubagentRun, result string, err error) {
	evt := SessionEvent{
		Type:               SessionEventSubagentStop,
		SubagentName:       run.Name,
		SubagentTask:       run.Task,
		SubagentBackground: run.Background,
	}
	if result != "" {
		preview := result
		if len(preview) > 240 {
			preview = preview[:237] + "..."
		}
		evt.SubagentResult = preview
	}
	if err != nil {
		evt.ErrorMessage = err.Error()
	}
	s.emitSessionEvent(evt)
}
