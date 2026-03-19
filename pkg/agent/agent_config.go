package agent

import (
	"context"

	"github.com/openmodu/modu/pkg/types"
)

type AgentConfig struct {
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

	// ApproveTool is called before each tool execution. If set, it must return
	// a ToolApprovalDecision. A nil callback means all tools are auto-approved.
	// The callback may block until the user responds.
	ApproveTool func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error)

	// Moved from former AgentOptions
	InitialState *AgentState
	SteeringMode ExecutionMode
	FollowUpMode ExecutionMode
	StreamFn     StreamFn
}
