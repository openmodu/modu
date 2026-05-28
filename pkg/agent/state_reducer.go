package agent

import (
	"context"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

func (a *Agent) applyEvent(event Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch event.Type {
	case EventTypeMessageStart, EventTypeMessageUpdate:
		a.state.StreamMessage = event.Message
	case EventTypeMessageEnd:
		a.state.StreamMessage = nil
		a.state.Messages = append(a.state.Messages, event.Message)
	case EventTypeToolExecutionStart:
		a.state.PendingToolCalls[event.ToolCallID] = struct{}{}
	case EventTypeToolExecutionEnd:
		delete(a.state.PendingToolCalls, event.ToolCallID)
	case EventTypeInterrupt:
		if event.Interrupt != nil && a.config.EnableInterrupts {
			a.state.Status = SessionStatusPaused
			a.state.Interrupt = event.Interrupt
			a.resume = make(chan ResumeDecision, 1)
			if a.resumeReady != nil {
				close(a.resumeReady)
				a.resumeReady = nil
			}
		}
	case EventTypeAgentEnd:
		a.state.IsStreaming = false
		a.state.StreamMessage = nil
	}
}

func (a *Agent) appendErrorMessageLocked(ctx context.Context, err error) {
	stopReason := types.StopReason("error")
	if ctx.Err() != nil {
		stopReason = "aborted"
	}
	providerID := ""
	modelID := ""
	if a.state.Model != nil {
		providerID = a.state.Model.ProviderID
		modelID = a.state.Model.ID
	}
	message := types.AssistantMessage{
		Role:         RoleAssistant,
		Content:      []types.ContentBlock{&types.TextContent{Type: "text", Text: ""}},
		ProviderID:   providerID,
		Model:        modelID,
		Usage:        types.AgentUsage{},
		StopReason:   stopReason,
		ErrorMessage: err.Error(),
		Timestamp:    time.Now().UnixMilli(),
	}
	a.state.Messages = append(a.state.Messages, message)
	a.state.Error = err.Error()
}
