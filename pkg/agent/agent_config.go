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
	//
	// When EnableInterrupts is true, ApproveTool is managed internally and should
	// not be set; use Agent.Resume() instead.
	ApproveTool func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error)

	// EnableInterrupts enables the interrupt/resume pattern (Managed Agents style).
	// When true:
	//   - Tool calls without an explicit ApproveTool emit EventTypeInterrupt and
	//     pause execution until Agent.Resume() is called.
	//   - MaxSteps exceeded emits EventTypeInterrupt instead of silently stopping.
	//   - AgentState.Status tracks the session lifecycle.
	EnableInterrupts bool

	// MaxSteps limits the number of LLM turns per session. 0 means unlimited.
	// When EnableInterrupts is true, exceeding MaxSteps emits EventTypeInterrupt
	// (max_steps_reached) and pauses until Agent.Resume() decides to continue or stop.
	// When EnableInterrupts is false, exceeding MaxSteps silently ends the session.
	MaxSteps int

	// Moved from former AgentOptions
	InitialState *AgentState
	SteeringMode ExecutionMode
	FollowUpMode ExecutionMode
	StreamFn     StreamFn

	// onMaxStepsReached is set internally by Agent when EnableInterrupts is true.
	// It is called from the loop goroutine after EventTypeInterrupt is pushed to the stream.
	onMaxStepsReached func(stepCount int) ResumeDecision
}
