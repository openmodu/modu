package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

type Agent struct {
	mu          sync.RWMutex
	state       State
	config      Config
	loop        *Loop
	steering    []AgentMessage
	followUp    []AgentMessage
	listeners   map[int]func(Event)
	listenerID  int
	running     chan struct{}
	resume      chan ResumeDecision
	resumeReady chan struct{}
	cancel      context.CancelFunc
}

func NewAgent(cfg Config) *Agent {
	if cfg.SteeringMode == "" {
		cfg.SteeringMode = ExecutionModeOneAtATime
	}
	if cfg.FollowUpMode == "" {
		cfg.FollowUpMode = ExecutionModeOneAtATime
	}
	return &Agent{
		state:     initialState(cfg),
		config:    cfg,
		loop:      NewLoop(DefaultLLM{}, DefaultTools{}),
		listeners: map[int]func(Event){},
	}
}

func (a *Agent) Prompt(ctx context.Context, input any) error {
	messages, err := promptMessages(input)
	if err != nil {
		return err
	}
	return a.run(ctx, messages)
}

func (a *Agent) PromptWithImages(ctx context.Context, text string, images []types.ImageContent) error {
	content := []types.ContentBlock{&types.TextContent{Type: "text", Text: text}}
	for i := range images {
		content = append(content, &images[i])
	}
	return a.run(ctx, []AgentMessage{types.UserMessage{Role: RoleUser, Content: content, Timestamp: time.Now().UnixMilli()}})
}

func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.running != nil {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	messages := append([]AgentMessage{}, a.state.Messages...)
	if len(messages) == 0 {
		a.mu.Unlock()
		return fmt.Errorf("no messages to continue from")
	}
	if roleOf(messages[len(messages)-1]) == RoleAssistant {
		steering := a.dequeueSteeringLocked()
		if len(steering) > 0 {
			a.mu.Unlock()
			return a.run(ctx, steering)
		}
		followUps := a.dequeueFollowUpLocked()
		if len(followUps) > 0 {
			a.mu.Unlock()
			return a.run(ctx, followUps)
		}
		a.mu.Unlock()
		return fmt.Errorf("cannot continue from message role: assistant")
	}
	a.mu.Unlock()
	return a.run(ctx, nil)
}

func (a *Agent) Steer(message AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = append(a.steering, message)
}

func (a *Agent) FollowUp(message AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUp = append(a.followUp, message)
}

func (a *Agent) GetState() State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

func (a *Agent) Resume(decision ResumeDecision) bool {
	a.mu.RLock()
	ch := a.resume
	a.mu.RUnlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- decision:
		return true
	default:
		return false
	}
}

func (a *Agent) GetStatus() SessionStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Status
}

func (a *Agent) GetInterrupt() *InterruptEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Interrupt
}

func (a *Agent) Subscribe(fn func(Event)) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := a.listenerID
	a.listenerID++
	a.listeners[id] = fn
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		delete(a.listeners, id)
	}
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

func (a *Agent) ClearSteeringQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = []AgentMessage{}
}

func (a *Agent) ClearFollowUpQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUp = []AgentMessage{}
}

func (a *Agent) ClearAllQueues() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steering = []AgentMessage{}
	a.followUp = []AgentMessage{}
}

func (a *Agent) HasQueuedMessages() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steering) > 0 || len(a.followUp) > 0
}

func (a *Agent) QueuedMessageCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steering) + len(a.followUp)
}

func (a *Agent) QueuedMessageCounts() (steering, followUp int) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steering), len(a.followUp)
}

func (a *Agent) QueuedMessages() (steering, followUp []AgentMessage) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]AgentMessage{}, a.steering...), append([]AgentMessage{}, a.followUp...)
}

func (a *Agent) DropLastQueuedMessage() (kind string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.followUp) > 0 {
		a.followUp = a.followUp[:len(a.followUp)-1]
		return "follow-up", true
	}
	if len(a.steering) > 0 {
		a.steering = a.steering[:len(a.steering)-1]
		return "steer", true
	}
	return "", false
}

func (a *Agent) GetSteeringMode() ExecutionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.SteeringMode
}

func (a *Agent) SetSteeringMode(mode ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.SteeringMode = mode
}

func (a *Agent) GetFollowUpMode() ExecutionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config.FollowUpMode
}

func (a *Agent) SetFollowUpMode(mode ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.config.FollowUpMode = mode
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

func (a *Agent) WaitForIdle() {
	a.mu.RLock()
	running := a.running
	a.mu.RUnlock()
	if running != nil {
		<-running
	}
}

func (a *Agent) Abort() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

func (a *Agent) run(ctx context.Context, messages []AgentMessage) error {
	a.mu.Lock()
	if a.running != nil {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	if a.state.Model == nil {
		a.mu.Unlock()
		return fmt.Errorf("no model configured")
	}
	runCtx, cancel := context.WithCancel(ctx)
	a.running = make(chan struct{})
	a.cancel = cancel
	a.state.IsStreaming = true
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = map[string]struct{}{}
	a.state.Error = ""
	if a.config.EnableInterrupts {
		a.state.Status = SessionStatusRunning
		a.resumeReady = make(chan struct{})
	}
	agentCtx := AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     append([]AgentMessage{}, a.state.Messages...),
		Tools:        a.state.Tools,
	}
	config := a.config
	config.Model = a.state.Model
	config.Reasoning = a.state.ThinkingLevel
	if config.EnableInterrupts && config.ApproveTool == nil {
		config.ApproveTool = func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error) {
			decision := a.waitForResume(runCtx)
			if decision.Allow {
				return ToolApprovalAllow, nil
			}
			return ToolApprovalDeny, nil
		}
	}
	if config.EnableInterrupts {
		config.onMaxStepsReached = func(stepCount int) ResumeDecision {
			return a.waitForResume(runCtx)
		}
	}
	config.GetSteeringMessages = func() ([]AgentMessage, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.dequeueSteeringLocked(), nil
	}
	config.GetFollowUpMessages = func() ([]AgentMessage, error) {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.dequeueFollowUpLocked(), nil
	}
	a.mu.Unlock()

	stream := NewEventStream()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range stream.Events() {
			a.applyEvent(event)
			a.emit(event)
		}
	}()

	_, err := a.loop.Run(runCtx, LoopInput{
		Prompts: messages,
		Context: agentCtx,
		Config:  config,
		Events:  stream,
	})
	stream.Close()
	<-done

	a.mu.Lock()
	if err != nil {
		a.appendErrorMessageLocked(runCtx, err)
	}
	a.state.IsStreaming = false
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = map[string]struct{}{}
	a.state.Interrupt = nil
	a.resume = nil
	a.resumeReady = nil
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.config.EnableInterrupts {
		if a.state.Error != "" {
			a.state.Status = SessionStatusFailed
		} else {
			a.state.Status = SessionStatusCompleted
		}
	}
	if a.running != nil {
		close(a.running)
		a.running = nil
	}
	a.mu.Unlock()

	return err
}

func (a *Agent) waitForResume(ctx context.Context) ResumeDecision {
	a.mu.RLock()
	ch := a.resume
	ready := a.resumeReady
	a.mu.RUnlock()
	if ch == nil && ready != nil {
		select {
		case <-ready:
		case <-ctx.Done():
			return ResumeDecision{Allow: false}
		}
		a.mu.RLock()
		ch = a.resume
		a.mu.RUnlock()
	}
	if ch == nil {
		return ResumeDecision{Allow: false}
	}
	var decision ResumeDecision
	select {
	case decision = <-ch:
	case <-ctx.Done():
		return ResumeDecision{Allow: false}
	}
	a.mu.Lock()
	a.state.Status = SessionStatusRunning
	a.state.Interrupt = nil
	a.resume = nil
	a.resumeReady = make(chan struct{})
	a.mu.Unlock()
	return decision
}

func (a *Agent) dequeueSteeringLocked() []AgentMessage {
	if a.config.SteeringMode == ExecutionModeOneAtATime {
		if len(a.steering) == 0 {
			return []AgentMessage{}
		}
		first := a.steering[0]
		a.steering = a.steering[1:]
		return []AgentMessage{first}
	}
	messages := append([]AgentMessage{}, a.steering...)
	a.steering = []AgentMessage{}
	return messages
}

func (a *Agent) dequeueFollowUpLocked() []AgentMessage {
	if a.config.FollowUpMode == ExecutionModeOneAtATime {
		if len(a.followUp) == 0 {
			return []AgentMessage{}
		}
		first := a.followUp[0]
		a.followUp = a.followUp[1:]
		return []AgentMessage{first}
	}
	messages := append([]AgentMessage{}, a.followUp...)
	a.followUp = []AgentMessage{}
	return messages
}

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

func (a *Agent) emit(event Event) {
	a.mu.RLock()
	listeners := make([]func(Event), 0, len(a.listeners))
	for _, listener := range a.listeners {
		listeners = append(listeners, listener)
	}
	a.mu.RUnlock()
	for _, listener := range listeners {
		listener(event)
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

func promptMessages(input any) ([]AgentMessage, error) {
	switch v := input.(type) {
	case string:
		return []AgentMessage{types.UserMessage{Role: RoleUser, Content: v, Timestamp: time.Now().UnixMilli()}}, nil
	case []AgentMessage:
		return v, nil
	default:
		if input == nil {
			return nil, fmt.Errorf("invalid input type")
		}
		return []AgentMessage{input}, nil
	}
}
