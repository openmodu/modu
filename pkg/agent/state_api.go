package agent

import "github.com/openmodu/modu/pkg/types"

func (a *Agent) GetState() State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

func (a *Agent) AppendMessage(message AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append(a.state.Messages, message)
}

func (a *Agent) ReplaceMessages(messages []AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append([]AgentMessage{}, messages...)
}

func (a *Agent) ClearMessages() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = []AgentMessage{}
}

func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.state.Messages = []AgentMessage{}
	a.state.IsStreaming = false
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = map[string]struct{}{}
	a.state.Error = ""
	a.state.Interrupt = nil
	a.state.Status = SessionStatusIdle
	a.steering = []AgentMessage{}
	a.followUp = []AgentMessage{}
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

func (a *Agent) SetThinkingLevel(level ThinkingLevel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ThinkingLevel = level
}

func (a *Agent) SetTools(tools []Tool) {
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
