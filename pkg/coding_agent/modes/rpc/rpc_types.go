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
)

// RpcCommand is an incoming RPC command.
type RpcCommand struct {
	ID      string          `json:"id,omitempty"`
	Command RpcCommandType  `json:"command"`
	Data    json.RawMessage `json:"data,omitempty"`
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
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	ThinkingLevel  string `json:"thinkingLevel"`
	IsStreaming     bool   `json:"isStreaming"`
	SessionID      string `json:"sessionId"`
	AutoCompaction bool   `json:"autoCompactionEnabled"`
	AutoRetry      bool   `json:"autoRetryEnabled"`
	MessageCount   int    `json:"messageCount"`
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
