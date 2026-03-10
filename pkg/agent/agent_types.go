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

type AgentMessage = types.AgentMessage

type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []AgentTool
}

type StreamFn func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error)
