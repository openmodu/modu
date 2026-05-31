package types

import "context"

type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []Tool
}

type LLM interface {
	Complete(ctx context.Context, input LLMInput) (*AssistantMessage, error)
}

type Tools interface {
	Execute(ctx context.Context, input ToolInput) (ToolOutput, error)
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
	Model            *Model
	StreamFn         StreamFn
	ConvertToLLM     func(messages []AgentMessage) ([]AgentMessage, error)
	TransformContext func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error)
	GetAPIKey        func(provider string) (string, error)

	Temperature     *float64
	MaxTokens       *int
	APIKey          string
	CacheRetention  CacheRetention
	SessionID       string
	Headers         map[string]string
	Reasoning       ThinkingLevel
	ThinkingBudgets *ThinkingBudgets
	MaxRetryDelayMs int
}
