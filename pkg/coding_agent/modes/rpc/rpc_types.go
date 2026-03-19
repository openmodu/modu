package rpc

import "encoding/json"

// RpcCommandType identifies the type of an RPC command.
type RpcCommandType string

const (
	RpcCmdPrompt            RpcCommandType = "prompt"
	RpcCmdSteer             RpcCommandType = "steer"
	RpcCmdFollowUp          RpcCommandType = "follow_up"
	RpcCmdAbort             RpcCommandType = "abort"
	RpcCmdGetState          RpcCommandType = "get_state"
	RpcCmdSetModel          RpcCommandType = "set_model"
	RpcCmdCycleModel        RpcCommandType = "cycle_model"
	RpcCmdSetThinkingLevel  RpcCommandType = "set_thinking_level"
	RpcCmdCycleThinking     RpcCommandType = "cycle_thinking_level"
	RpcCmdCompact           RpcCommandType = "compact"
	RpcCmdSetAutoCompaction RpcCommandType = "set_auto_compaction"
	RpcCmdSetAutoRetry      RpcCommandType = "set_auto_retry"
	RpcCmdAbortRetry        RpcCommandType = "abort_retry"
	RpcCmdGetMessages       RpcCommandType = "get_messages"
	RpcCmdNewSession        RpcCommandType = "new_session"
	RpcCmdGetCommands       RpcCommandType = "get_commands"

	// New commands (pi-mono parity)
	RpcCmdGetAvailableModels   RpcCommandType = "get_available_models"
	RpcCmdSetSteeringMode      RpcCommandType = "set_steering_mode"
	RpcCmdSetFollowUpMode      RpcCommandType = "set_follow_up_mode"
	RpcCmdBash                 RpcCommandType = "bash"
	RpcCmdAbortBash            RpcCommandType = "abort_bash"
	RpcCmdGetSessionStats      RpcCommandType = "get_session_stats"
	RpcCmdExportHTML           RpcCommandType = "export_html"
	RpcCmdSwitchSession        RpcCommandType = "switch_session"
	RpcCmdFork                 RpcCommandType = "fork"
	RpcCmdGetForkMessages      RpcCommandType = "get_fork_messages"
	RpcCmdGetLastAssistantText  RpcCommandType = "get_last_assistant_text"
	RpcCmdSetSessionName        RpcCommandType = "set_session_name"
	RpcCmdToolApprovalResponse  RpcCommandType = "tool_approval_response"
)

// RpcCommand is an incoming RPC command.
// Supports both "type" (pi-mono style) and "command" (legacy) fields for the command discriminator.
// For prompt/steer/follow_up, the "message" field can be used as a flat alternative to data.
type RpcCommand struct {
	ID      string          `json:"id,omitempty"`
	Type    RpcCommandType  `json:"type"`
	Command RpcCommandType  `json:"command"`           // legacy compat
	Message string          `json:"message,omitempty"`  // flat field for prompt/steer/follow_up
	Data    json.RawMessage `json:"data,omitempty"`
}

// CommandType returns the effective command type, preferring Type over Command.
func (c *RpcCommand) CommandType() RpcCommandType {
	if c.Type != "" {
		return c.Type
	}
	return c.Command
}

// RpcResponse is the response to an RPC command.
type RpcResponse struct {
	ID      string         `json:"id,omitempty"`
	Type    string         `json:"type"` // always "response"
	Command RpcCommandType `json:"command"`
	Success bool           `json:"success"`
	Data    any            `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// RpcSessionState represents the current state of a session.
type RpcSessionState struct {
	Model               string `json:"model"`
	Provider            string `json:"provider"`
	ThinkingLevel       string `json:"thinkingLevel"`
	IsStreaming         bool   `json:"isStreaming"`
	SessionID           string `json:"sessionId"`
	AutoCompaction      bool   `json:"autoCompactionEnabled"`
	AutoRetry           bool   `json:"autoRetryEnabled"`
	MessageCount        int    `json:"messageCount"`
	IsCompacting        bool   `json:"isCompacting"`
	SteeringMode        string `json:"steeringMode"`
	FollowUpMode        string `json:"followUpMode"`
	SessionFile         string `json:"sessionFile"`
	SessionName         string `json:"sessionName"`
	PendingMessageCount int    `json:"pendingMessageCount"`
}

// RpcEvent is an event emitted over the RPC stream.
type RpcEvent struct {
	Type string `json:"type"` // "event"
	Data any    `json:"data"`
}

// PromptData is the data payload for a prompt command.
type PromptData struct {
	Message string `json:"message"`
}

// SetModelData is the data payload for set_model command.
type SetModelData struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// SetThinkingLevelData is the data payload for set_thinking_level command.
type SetThinkingLevelData struct {
	Level string `json:"level"`
}

// SetBoolData is the data payload for boolean toggle commands.
type SetBoolData struct {
	Enabled bool `json:"enabled"`
}

// SetModeData is the data payload for set_steering_mode / set_follow_up_mode commands.
type SetModeData struct {
	Mode string `json:"mode"`
}

// BashData is the data payload for the bash command.
type BashData struct {
	Command   string `json:"command"`
	TimeoutMs int    `json:"timeoutMs,omitempty"`
}

// ForkData is the data payload for the fork command.
type ForkData struct {
	EntryID string `json:"entryId"`
}

// SwitchSessionData is the data payload for the switch_session command.
type SwitchSessionData struct {
	SessionFile string `json:"sessionFile"`
}

// SetSessionNameData is the data payload for the set_session_name command.
type SetSessionNameData struct {
	Name string `json:"name"`
}

// ExportHTMLData is the data payload for the export_html command.
type ExportHTMLData struct {
	Path string `json:"path"`
}

// CompactData is the data payload for the compact command.
type CompactData struct {
	CustomInstructions string `json:"customInstructions,omitempty"`
}

// RpcSlashCommand represents a command available to the RPC client.
type RpcSlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
}

// ToolApprovalResponseData is the data payload for the tool_approval_response command.
type ToolApprovalResponseData struct {
	ToolCallID string `json:"toolCallId"`
	// Decision is one of: "allow", "allow_always", "deny", "deny_always"
	Decision string `json:"decision"`
}
