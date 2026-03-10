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

// ThinkingLevel is an alias for types.ThinkingLevel, re-exported for convenience.
type ThinkingLevel = types.ThinkingLevel

// Re-export ThinkingLevel constants from pkg/types so callers can use agent.ThinkingLevelXxx.
const (
	ThinkingLevelOff     = types.ThinkingLevelOff
	ThinkingLevelMinimal = types.ThinkingLevelMinimal
	ThinkingLevelLow     = types.ThinkingLevelLow
	ThinkingLevelMedium  = types.ThinkingLevelMedium
	ThinkingLevelHigh    = types.ThinkingLevelHigh
	ThinkingLevelXHigh   = types.ThinkingLevelXHigh
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
