package agent

import "github.com/openmodu/modu/pkg/types"

func initialState(cfg types.Config) types.State {
	state := types.State{
		Model:            cfg.Model,
		ThinkingLevel:    types.ThinkingLevelOff,
		Tools:            []types.Tool{},
		Messages:         []types.AgentMessage{},
		PendingToolCalls: map[string]struct{}{},
		Status:           types.SessionStatusIdle,
	}
	if cfg.InitialState != nil {
		state = *cfg.InitialState
	}
	if state.Model == nil {
		state.Model = cfg.Model
	}
	if state.Messages == nil {
		state.Messages = []types.AgentMessage{}
	}
	if state.Tools == nil {
		state.Tools = []types.Tool{}
	}
	if state.PendingToolCalls == nil {
		state.PendingToolCalls = map[string]struct{}{}
	}
	return state
}
