package agent

import (
	"context"

	"github.com/openmodu/modu/pkg/types"
)

type AgentMessage = types.AgentMessage
type ThinkingLevel = types.ThinkingLevel

type ExecutionMode string

const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleToolResult = "toolResult"

	ThinkingLevelOff     = types.ThinkingLevelOff
	ThinkingLevelMinimal = types.ThinkingLevelMinimal
	ThinkingLevelLow     = types.ThinkingLevelLow
	ThinkingLevelMedium  = types.ThinkingLevelMedium
	ThinkingLevelHigh    = types.ThinkingLevelHigh
	ThinkingLevelXHigh   = types.ThinkingLevelXHigh

	ExecutionModeAll        ExecutionMode = "all"
	ExecutionModeOneAtATime ExecutionMode = "one-at-a-time"
)

type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []Tool
}

type Config struct {
	Model            *types.Model
	InitialState     *State
	StreamFn         StreamFn
	ConvertToLLM     func(messages []AgentMessage) ([]types.AgentMessage, error)
	TransformContext func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)
	GetAPIKey        func(provider string) (string, error)
	ApproveTool      func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error)
	EnableInterrupts bool

	Temperature     *float64
	MaxTokens       *int
	APIKey          string
	CacheRetention  types.CacheRetention
	SessionID       string
	Headers         map[string]string
	Reasoning       ThinkingLevel
	ThinkingBudgets *types.ThinkingBudgets
	MaxRetryDelayMs int
	MaxSteps        int
	SteeringMode    ExecutionMode
	FollowUpMode    ExecutionMode
}

type StreamFn func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error)

type EventSink interface {
	Emit(event Event)
}

type RuntimeHooks struct {
	GetSteeringMessages func() ([]AgentMessage, error)
	GetFollowUpMessages func() ([]AgentMessage, error)
	ApproveTool         func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error)
	OnMaxStepsReached   func(stepCount int) ResumeDecision
}

type LLM interface {
	Complete(ctx context.Context, input LLMInput) (*types.AssistantMessage, error)
}

type Tools interface {
	Execute(ctx context.Context, input ToolInput) (ToolOutput, error)
}

type Loop struct {
	LLM   LLM
	Tools Tools
}

type LoopInput struct {
	Prompts []AgentMessage
	Context AgentContext
	Config  Config
	Runtime RuntimeHooks
	Events  EventSink
}

type LoopResult struct {
	Messages []AgentMessage
}

type LLMInput struct {
	Context AgentContext
	Options LLMOptions
	Events  EventSink
}

type LLMOptions struct {
	Model            *types.Model
	StreamFn         StreamFn
	ConvertToLLM     func(messages []AgentMessage) ([]types.AgentMessage, error)
	TransformContext func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)
	GetAPIKey        func(provider string) (string, error)

	Temperature     *float64
	MaxTokens       *int
	APIKey          string
	CacheRetention  types.CacheRetention
	SessionID       string
	Headers         map[string]string
	Reasoning       ThinkingLevel
	ThinkingBudgets *types.ThinkingBudgets
	MaxRetryDelayMs int
}

type ToolInput struct {
	Tools               []Tool
	Calls               []types.ToolCallContent
	Events              EventSink
	ApproveTool         func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error)
	GetSteeringMessages func() ([]AgentMessage, error)
	EnableInterrupts    bool
}

type ToolOutput struct {
	Messages []AgentMessage
	Results  []types.ToolResultMessage
	Steering []AgentMessage
}
