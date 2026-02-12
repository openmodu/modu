package agent

import (
	"context"
)

// --- Enums & Basic Types ---

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
	RoleTool      MessageRole = "tool"
	RoleCustom    MessageRole = "custom" // For extensible message types
)

type ContentType string

const (
	ContentTypeText     ContentType = "text"
	ContentTypeImage    ContentType = "image"
	ContentTypeToolCall ContentType = "tool_call"
)

type ThinkingLevel string

const (
	ThinkingLevelOff     ThinkingLevel = "off"
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
)

type ExecutionMode string

const (
	ExecutionModeAll        ExecutionMode = "all"
	ExecutionModeOneAtATime ExecutionMode = "one-at-a-time"
)

// --- Content Structures ---

type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

type ContentBlock struct {
	Type     ContentType `json:"type"`
	Text     string      `json:"text,omitempty"`
	ImageURL string      `json:"image_url,omitempty"` // Simplified for Go
	ToolCall *ToolCall   `json:"tool_call,omitempty"`
}

type Message struct {
	Role      MessageRole    `json:"role"`
	Content   []ContentBlock `json:"content"`
	Timestamp int64          `json:"timestamp"`
	// Custom fields for extension
	CustomType string      `json:"customType,omitempty"`
	Details    interface{} `json:"details,omitempty"`
}

// --- Agent State ---

type AgentState struct {
	SystemPrompt     string
	Model            Model
	ThinkingLevel    ThinkingLevel
	Tools            []Tool
	Messages         []Message
	IsStreaming      bool
	StreamMessage    *Message
	PendingToolCalls map[string]struct{} // Set implementation
	Error            error
}

// --- Interfaces ---

// Tool interface matching AgentTool in TS
type Tool interface {
	Name() string
	Description() string
	// Execute now supports ID, Context, and Update callback
	Execute(ctx context.Context, toolCallID string, args string, onUpdate func(partial interface{})) (string, error)
}

// Model interface (Abstracted)
type Model interface {
	Stream(ctx context.Context, messages []Message, tools []Tool) (<-chan ModelStreamEvent, error)
}

// --- Events ---

type EventType string

const (
	EventTypeAgentStart          EventType = "agent_start"
	EventTypeAgentEnd            EventType = "agent_end"
	EventTypeTurnStart           EventType = "turn_start"
	EventTypeTurnEnd             EventType = "turn_end"
	EventTypeMessageStart        EventType = "message_start"
	EventTypeMessageUpdate       EventType = "message_update"
	EventTypeMessageEnd          EventType = "message_end"
	EventTypeToolExecutionStart  EventType = "tool_execution_start"
	EventTypeToolExecutionUpdate EventType = "tool_execution_update"
	EventTypeToolExecutionEnd    EventType = "tool_execution_end"
)

// AgentEvent union type in Go struct
type AgentEvent struct {
	Type        EventType
	Messages    []Message // For AgentEnd
	Message     *Message  // For Message/Turn events
	ToolResults []Message // For TurnEnd (ToolResultMessages)

	// Tool Execution specific
	ToolCallID string
	ToolName   string
	Result     interface{}
	IsError    bool
	Partial    interface{}
}

// ModelStreamEvent for the low-level model stream
type ModelStreamEvent struct {
	Type    string // "text_delta", "tool_call", "error", "done"
	Payload interface{}
}
