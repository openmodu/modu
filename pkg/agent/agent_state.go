package agent

import "github.com/openmodu/modu/pkg/types"

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

	// Lifecycle state (populated when EnableInterrupts is true in AgentConfig)
	Status    SessionStatus
	Interrupt *InterruptEvent // non-nil when Status == SessionStatusPaused
}
