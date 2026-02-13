package agent

import (
	"context"

	"github.com/crosszan/modu/pkg/llm"
)

// --- Enums & Basic Types ---

type MessageRole string

const (
	RoleUser       MessageRole = "user"
	RoleAssistant  MessageRole = "assistant"
	RoleSystem     MessageRole = "system"
	RoleTool       MessageRole = "tool"
	RoleToolResult MessageRole = "toolResult"
	RoleCustom     MessageRole = "custom" // For extensible message types
)

type ContentType string

const (
	ContentTypeText     ContentType = "text"
	ContentTypeImage    ContentType = "image"
	ContentTypeToolCall ContentType = "toolCall"
	ContentTypeThinking ContentType = "thinking"
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

type ContentBlock struct {
	Type               ContentType    `json:"type"`
	Text               string         `json:"text,omitempty"`
	TextSignature      string         `json:"textSignature,omitempty"`
	Thinking           string         `json:"thinking,omitempty"`
	ThinkingSignature  string         `json:"thinkingSignature,omitempty"`
	ImageData          string         `json:"data,omitempty"`
	ImageMimeType      string         `json:"mimeType,omitempty"`
	ToolCall           *llm.ToolCall  `json:"toolCall,omitempty"`
	ToolCallDelta      string         `json:"toolCallDelta,omitempty"`
	ToolCallSignature  string         `json:"toolCallSignature,omitempty"`
	ToolCallArguments  map[string]any `json:"toolCallArguments,omitempty"`
	ToolCallName       string         `json:"toolCallName,omitempty"`
	ToolCallID         string         `json:"toolCallId,omitempty"`
	ToolCallThoughtSig string         `json:"toolCallThoughtSignature,omitempty"`
}

type Message struct {
	Role      MessageRole    `json:"role"`
	Content   []ContentBlock `json:"content"`
	Timestamp int64          `json:"timestamp"`
	// Custom fields for extension
	CustomType   string         `json:"customType,omitempty"`
	Details      interface{}    `json:"details,omitempty"`
	Api          llm.Api        `json:"api,omitempty"`
	Provider     llm.Provider   `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	Usage        llm.Usage      `json:"usage,omitempty"`
	StopReason   llm.StopReason `json:"stopReason,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	ToolCallID   string         `json:"toolCallId,omitempty"`
	ToolName     string         `json:"toolName,omitempty"`
	IsError      bool           `json:"isError,omitempty"`
}

// --- Agent State ---

type AgentState struct {
	SystemPrompt     string
	Model            *llm.Model
	ThinkingLevel    ThinkingLevel
	Tools            []AgentTool
	Messages         []AgentMessage
	IsStreaming      bool
	StreamMessage    AgentMessage
	PendingToolCalls map[string]struct{} // Set implementation
	Error            string
}

// --- Interfaces ---

type AgentToolResult struct {
	Content []llm.ContentBlock `json:"content"`
	Details any                `json:"details"`
}

type AgentToolUpdateCallback func(partial AgentToolResult)

type AgentTool interface {
	Name() string
	Label() string
	Description() string
	Parameters() any
	Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error)
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
	Messages    []AgentMessage
	Message     AgentMessage
	ToolResults []llm.ToolResultMessage

	// Tool Execution specific
	ToolCallID            string
	ToolName              string
	Args                  any
	Result                interface{}
	IsError               bool
	Partial               interface{}
	AssistantMessageEvent *llm.AssistantMessageEvent
}

type AgentMessage = llm.Message

type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []AgentTool
}

type StreamFn func(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error)

type AgentLoopConfig struct {
	Model               *llm.Model
	ConvertToLlm        func(messages []AgentMessage) ([]llm.Message, error)
	TransformContext    func(messages []AgentMessage, ctx context.Context) ([]AgentMessage, error)
	GetAPIKey           func(provider string) (string, error)
	GetSteeringMessages func() ([]AgentMessage, error)
	GetFollowUpMessages func() ([]AgentMessage, error)
	Temperature         *float64
	MaxTokens           *int
	APIKey              string
	CacheRetention      llm.CacheRetention
	SessionID           string
	Headers             map[string]string
	Reasoning           ThinkingLevel
	ThinkingBudgets     *llm.ThinkingBudgets
	MaxRetryDelayMs     int
}
