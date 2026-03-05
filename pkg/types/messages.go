package types

// StopReason is the reason the model stopped generating.
type StopReason = string

// AgentMessage is the interface for all message types in the conversation history.
type AgentMessage interface{}

// UserMessage is a message from the user.
type UserMessage struct {
	Role      string `json:"role"`
	Content   any    `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

// AssistantMessage is a message from the assistant, containing rich content blocks.
type AssistantMessage struct {
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	ProviderID   string         `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	Usage        AgentUsage     `json:"usage"`
	StopReason   StopReason     `json:"stopReason,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Timestamp    int64          `json:"timestamp"`
}

// ToolResultMessage carries the result of a tool call back to the model.
type ToolResultMessage struct {
	Role       string         `json:"role"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Content    []ContentBlock `json:"content"`
	Details    any            `json:"details,omitempty"`
	IsError    bool           `json:"isError"`
	Timestamp  int64          `json:"timestamp"`
}

// ToolDefinition describes a tool available to the model.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// LLMContext holds the full context passed to the LLM for a single turn.
type LLMContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []ToolDefinition
}
