package agent

import (
	"context"

	"github.com/crosszan/modu/pkg/types"
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

// --- Agent State ---

type AgentState struct {
	SystemPrompt     string
	Model            *types.Model
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
	Content []types.ContentBlock `json:"content"`
	Details any                  `json:"details"`
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
	ToolResults []types.ToolResultMessage

	// Tool Execution specific
	ToolCallID  string
	ToolName    string
	Args        any
	Result      interface{}
	IsError     bool
	Partial     interface{}
	StreamEvent *types.StreamEvent
}

type AgentMessage = types.AgentMessage

type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []AgentTool
}

type StreamFn func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error)

type AgentLoopConfig struct {
	Model               *types.Model
	ConvertToLlm        func(messages []AgentMessage) ([]types.AgentMessage, error)
	TransformContext    func(messages []AgentMessage, ctx context.Context) ([]AgentMessage, error)
	GetAPIKey           func(provider string) (string, error)
	GetSteeringMessages func() ([]AgentMessage, error)
	GetFollowUpMessages func() ([]AgentMessage, error)
	Temperature         *float64
	MaxTokens           *int
	APIKey              string
	CacheRetention      types.CacheRetention
	SessionID           string
	Headers             map[string]string
	Reasoning           ThinkingLevel
	ThinkingBudgets     *types.ThinkingBudgets
	MaxRetryDelayMs     int
}
