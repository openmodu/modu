package agent

import (
	"context"

	"github.com/crosszan/modu/pkg/types"
)

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
