package agent

import "github.com/openmodu/modu/pkg/types"

func (a *Agent) GetState() types.State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

func (a *Agent) AppendMessage(message types.AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append(a.state.Messages, message)
}

func (a *Agent) ReplaceMessages(messages []types.AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append([]types.AgentMessage{}, messages...)
}

func (a *Agent) ClearMessages() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = []types.AgentMessage{}
}

func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.state.Messages = []types.AgentMessage{}
	a.state.IsStreaming = false
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = map[string]struct{}{}
	a.state.Error = ""
	a.state.Interrupt = nil
	a.state.Status = types.SessionStatusIdle
	a.steering = []types.AgentMessage{}
	a.followUp = []types.AgentMessage{}
}

func (a *Agent) SetSystemPrompt(value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.SystemPrompt = value
}

func (a *Agent) SetModel(model *types.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Model = model
}

func (a *Agent) SetThinkingLevel(level types.ThinkingLevel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ThinkingLevel = level
}

func (a *Agent) SetTools(tools []types.Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Tools = tools
}

func (a *Agent) GetSessionID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.SessionID
}

func (a *Agent) SetSessionID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.SessionID = id
}

func (a *Agent) GetThinkingBudgets() *types.ThinkingBudgets {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.ThinkingBudgets
}

func (a *Agent) SetThinkingBudgets(budgets *types.ThinkingBudgets) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.ThinkingBudgets = budgets
}

func (a *Agent) GetMaxRetryDelayMs() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.MaxRetryDelayMs
}

func (a *Agent) SetMaxRetryDelayMs(ms int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.MaxRetryDelayMs = ms
}
