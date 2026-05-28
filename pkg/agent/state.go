package agent

import "github.com/openmodu/modu/pkg/types"

type State struct {
	SystemPrompt     string
	Model            *types.Model
	ThinkingLevel    ThinkingLevel
	Tools            []Tool
	Messages         []AgentMessage
	IsStreaming      bool
	StreamMessage    AgentMessage
	PendingToolCalls map[string]struct{}
	Error            string
	Status           SessionStatus
	Interrupt        *InterruptEvent
}

func initialState(cfg Config) State {
	state := State{
		Model:            cfg.Model,
		ThinkingLevel:    ThinkingLevelOff,
		Tools:            []Tool{},
		Messages:         []AgentMessage{},
		PendingToolCalls: map[string]struct{}{},
		Status:           SessionStatusIdle,
	}
	if cfg.InitialState != nil {
		state = *cfg.InitialState
	}
	if state.Model == nil {
		state.Model = cfg.Model
	}
	if state.Messages == nil {
		state.Messages = []AgentMessage{}
	}
	if state.Tools == nil {
		state.Tools = []Tool{}
	}
	if state.PendingToolCalls == nil {
		state.PendingToolCalls = map[string]struct{}{}
	}
	return state
}
