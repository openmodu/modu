package types

import "context"

type ExecutionMode string

const (
	ExecutionModeAll        ExecutionMode = "all"
	ExecutionModeOneAtATime ExecutionMode = "one-at-a-time"
)

type Config struct {
	Model            *Model
	InitialState     *State
	StreamFn         StreamFn
	ConvertToLLM     func(messages []AgentMessage) ([]AgentMessage, error)
	TransformContext func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)
	GetAPIKey        func(provider string) (string, error)
	ApproveTool      func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error)
	EnableInterrupts bool

	Temperature     *float64
	MaxTokens       *int
	APIKey          string
	CacheRetention  CacheRetention
	SessionID       string
	Headers         map[string]string
	Reasoning       ThinkingLevel
	ThinkingBudgets *ThinkingBudgets
	MaxRetryDelayMs int
	MaxSteps        int
	SteeringMode    ExecutionMode
	FollowUpMode    ExecutionMode
}

type StreamFn func(ctx context.Context, model *Model, llmCtx *LLMContext, opts *SimpleStreamOptions) (EventStream, error)

type RuntimeHooks struct {
	GetSteeringMessages func() ([]AgentMessage, error)
	GetFollowUpMessages func() ([]AgentMessage, error)
	ApproveTool         func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error)
	OnMaxStepsReached   func(stepCount int) ResumeDecision
}
