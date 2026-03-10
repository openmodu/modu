package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/crosszan/modu/pkg/types"
)

type AgentOptions struct {
	InitialState     *AgentState
	ConvertToLlm     func(messages []AgentMessage) ([]types.AgentMessage, error)
	TransformContext func(messages []AgentMessage, ctx context.Context) ([]AgentMessage, error)
	SteeringMode     ExecutionMode
	FollowUpMode     ExecutionMode
	StreamFn         StreamFn
	SessionID        string
	GetAPIKey        func(provider string) (string, error)
	ThinkingBudgets  *types.ThinkingBudgets
	MaxRetryDelayMs  int
}

type Agent struct {
	state            AgentState
	steeringMode     ExecutionMode
	followUpMode     ExecutionMode
	convertToLlm     func(messages []AgentMessage) ([]types.AgentMessage, error)
	transformContext func(messages []AgentMessage, ctx context.Context) ([]AgentMessage, error)
	streamFn         StreamFn
	sessionID        string
	getAPIKey        func(provider string) (string, error)
	thinkingBudgets  *types.ThinkingBudgets
	maxRetryDelayMs  int

	// Queues
	steeringQueue []AgentMessage
	followUpQueue []AgentMessage

	// Concurrency
	mu         sync.RWMutex
	listeners  map[int]func(AgentEvent)
	listenerID int
	running    chan struct{}
}

func NewAgent(opts AgentOptions) *Agent {
	initial := AgentState{
		SystemPrompt:     "",
		Model:            nil,
		ThinkingLevel:    ThinkingLevelOff,
		Tools:            []AgentTool{},
		Messages:         []AgentMessage{},
		IsStreaming:      false,
		StreamMessage:    nil,
		PendingToolCalls: map[string]struct{}{},
		Error:            "",
	}
	if opts.InitialState != nil {
		initial = *opts.InitialState
	}
	if initial.PendingToolCalls == nil {
		initial.PendingToolCalls = make(map[string]struct{})
	}
	if initial.Messages == nil {
		initial.Messages = []AgentMessage{}
	}
	if initial.Tools == nil {
		initial.Tools = []AgentTool{}
	}
	if opts.SteeringMode == "" {
		opts.SteeringMode = ExecutionModeOneAtATime
	}
	if opts.FollowUpMode == "" {
		opts.FollowUpMode = ExecutionModeOneAtATime
	}
	convertToLlm := opts.ConvertToLlm
	if convertToLlm == nil {
		convertToLlm = defaultConvertToProviders
	}
	streamFn := opts.StreamFn
	if streamFn == nil {
		streamFn = StreamDefault
	}
	return &Agent{
		state:            initial,
		steeringMode:     opts.SteeringMode,
		followUpMode:     opts.FollowUpMode,
		convertToLlm:     convertToLlm,
		transformContext: opts.TransformContext,
		streamFn:         streamFn,
		sessionID:        opts.SessionID,
		getAPIKey:        opts.GetAPIKey,
		thinkingBudgets:  opts.ThinkingBudgets,
		maxRetryDelayMs:  opts.MaxRetryDelayMs,
		listeners:        map[int]func(AgentEvent){},
	}
}

// --- Public API ---

// Prompt sends a message to the agent. Input can be:
//   - string: creates a user message with text content
//   - []AgentMessage: sends multiple messages
//   - AgentMessage: sends a single message
func (a *Agent) Prompt(ctx context.Context, input interface{}) error {
	a.mu.Lock()
	if a.state.IsStreaming {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	a.mu.Unlock()

	var newMessages []AgentMessage

	switch v := input.(type) {
	case string:
		newMessages = []AgentMessage{types.UserMessage{Role: "user", Content: v, Timestamp: time.Now().UnixMilli()}}
	case []AgentMessage:
		newMessages = v
	default:
		if input == nil {
			return fmt.Errorf("invalid input type")
		}
		newMessages = []AgentMessage{input}
	}

	return a.runLoop(ctx, newMessages, false)
}

// PromptWithImages sends a text message with attached images to the agent.
func (a *Agent) PromptWithImages(ctx context.Context, text string, images []types.ImageContent) error {
	a.mu.Lock()
	if a.state.IsStreaming {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	a.mu.Unlock()

	content := []types.ContentBlock{&types.TextContent{Type: "text", Text: text}}
	for i := range images {
		content = append(content, &images[i])
	}
	msg := types.UserMessage{
		Role:      "user",
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	}
	return a.runLoop(ctx, []AgentMessage{msg}, false)
}

func (a *Agent) Continue(ctx context.Context) error {
	a.mu.Lock()
	if a.state.IsStreaming {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	a.mu.Unlock()
	a.mu.RLock()
	messages := a.state.Messages
	a.mu.RUnlock()
	if len(messages) == 0 {
		return fmt.Errorf("no messages to continue from")
	}
	if extractRole(messages[len(messages)-1]) == string(RoleAssistant) {
		steering := a.dequeueSteeringMessages()
		if len(steering) > 0 {
			return a.runLoop(ctx, steering, true)
		}
		followUps := a.dequeueFollowUpMessages()
		if len(followUps) > 0 {
			return a.runLoop(ctx, followUps, false)
		}
		return fmt.Errorf("cannot continue from message role: assistant")
	}
	return a.runLoop(ctx, nil, false)
}

func (a *Agent) Steer(msg AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue = append(a.steeringQueue, msg)
}

func (a *Agent) FollowUp(msg AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue = append(a.followUpQueue, msg)
}

// --- State Access ---

func (a *Agent) GetState() AgentState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

func (a *Agent) Subscribe(fn func(AgentEvent)) func() {
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

func (a *Agent) SetSystemPrompt(v string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.SystemPrompt = v
}

func (a *Agent) SetModel(m *types.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Model = m
}

func (a *Agent) SetThinkingLevel(l ThinkingLevel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.ThinkingLevel = l
}

func (a *Agent) SetSteeringMode(mode ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringMode = mode
}

func (a *Agent) SetFollowUpMode(mode ExecutionMode) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpMode = mode
}

func (a *Agent) SetTools(t []AgentTool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Tools = t
}

func (a *Agent) ReplaceMessages(ms []AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append([]AgentMessage{}, ms...)
}

func (a *Agent) AppendMessage(msg AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append(a.state.Messages, msg)
}

func (a *Agent) ClearSteeringQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue = []AgentMessage{}
}

func (a *Agent) ClearFollowUpQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue = []AgentMessage{}
}

func (a *Agent) ClearAllQueues() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue = []AgentMessage{}
	a.followUpQueue = []AgentMessage{}
}

func (a *Agent) HasQueuedMessages() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steeringQueue) > 0 || len(a.followUpQueue) > 0
}

func (a *Agent) QueuedMessageCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.steeringQueue) + len(a.followUpQueue)
}

func (a *Agent) Abort() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running != nil {
		close(a.running)
		a.running = nil
	}
}

func (a *Agent) WaitForIdle() {
	a.mu.RLock()
	running := a.running
	a.mu.RUnlock()
	if running != nil {
		<-running
	}
}

func (a *Agent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = []AgentMessage{}
	a.state.IsStreaming = false
	a.state.StreamMessage = nil
	a.state.PendingToolCalls = map[string]struct{}{}
	a.state.Error = ""
	a.steeringQueue = []AgentMessage{}
	a.followUpQueue = []AgentMessage{}
}

func (a *Agent) ClearMessages() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = []AgentMessage{}
}

func (a *Agent) GetSteeringMode() ExecutionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.steeringMode
}

func (a *Agent) GetFollowUpMode() ExecutionMode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.followUpMode
}

func (a *Agent) GetSessionID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessionID
}

func (a *Agent) SetSessionID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionID = id
}

func (a *Agent) GetThinkingBudgets() *types.ThinkingBudgets {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinkingBudgets
}

func (a *Agent) SetThinkingBudgets(b *types.ThinkingBudgets) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkingBudgets = b
}

func (a *Agent) GetMaxRetryDelayMs() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.maxRetryDelayMs
}

func (a *Agent) SetMaxRetryDelayMs(ms int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maxRetryDelayMs = ms
}

func (a *Agent) dequeueSteeringMessages() []AgentMessage {
	if a.steeringMode == ExecutionModeOneAtATime {
		if len(a.steeringQueue) > 0 {
			first := a.steeringQueue[0]
			a.steeringQueue = a.steeringQueue[1:]
			return []AgentMessage{first}
		}
		return []AgentMessage{}
	}
	steering := a.steeringQueue
	a.steeringQueue = []AgentMessage{}
	return steering
}

func (a *Agent) dequeueFollowUpMessages() []AgentMessage {
	if a.followUpMode == ExecutionModeOneAtATime {
		if len(a.followUpQueue) > 0 {
			first := a.followUpQueue[0]
			a.followUpQueue = a.followUpQueue[1:]
			return []AgentMessage{first}
		}
		return []AgentMessage{}
	}
	followUp := a.followUpQueue
	a.followUpQueue = []AgentMessage{}
	return followUp
}

func (a *Agent) runLoop(ctx context.Context, messages []AgentMessage, skipInitialSteeringPoll bool) error {
	a.mu.Lock()
	if a.state.Model == nil {
		a.mu.Unlock()
		return fmt.Errorf("no model configured")
	}
	if a.running != nil {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	a.running = make(chan struct{})
	a.state.IsStreaming = true
	a.state.StreamMessage = nil
	a.state.Error = ""
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		if a.running != nil {
			close(a.running)
			a.running = nil
		}
		a.state.IsStreaming = false
		a.state.StreamMessage = nil
		a.state.PendingToolCalls = map[string]struct{}{}
		a.mu.Unlock()
	}()

	a.mu.RLock()
	agentCtx := AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     append([]AgentMessage{}, a.state.Messages...),
		Tools:        a.state.Tools,
	}
	model := a.state.Model
	reasoning := a.state.ThinkingLevel
	a.mu.RUnlock()

	config := AgentLoopConfig{
		Model:            model,
		Reasoning:        reasoning,
		SessionID:        a.sessionID,
		ThinkingBudgets:  a.thinkingBudgets,
		MaxRetryDelayMs:  a.maxRetryDelayMs,
		ConvertToLlm:     a.convertToLlm,
		TransformContext: a.transformContext,
		GetAPIKey:        a.getAPIKey,
		GetSteeringMessages: func() ([]AgentMessage, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			if skipInitialSteeringPoll {
				skipInitialSteeringPoll = false
				return []AgentMessage{}, nil
			}
			return a.dequeueSteeringMessages(), nil
		},
		GetFollowUpMessages: func() ([]AgentMessage, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.dequeueFollowUpMessages(), nil
		},
	}

	var stream *EventStream
	var loopErr error
	if messages != nil {
		stream = AgentLoop(messages, agentCtx, config, ctx, a.streamFn)
	} else {
		cont, err := AgentLoopContinue(agentCtx, config, ctx, a.streamFn)
		if err != nil {
			return err
		}
		stream = cont
	}

	var partial AgentMessage

	for event := range stream.Events() {
		switch event.Type {
		case EventTypeMessageStart:
			partial = event.Message
			a.mu.Lock()
			a.state.StreamMessage = event.Message
			a.mu.Unlock()
		case EventTypeMessageUpdate:
			partial = event.Message
			a.mu.Lock()
			a.state.StreamMessage = event.Message
			a.mu.Unlock()
		case EventTypeMessageEnd:
			partial = nil
			a.mu.Lock()
			a.state.StreamMessage = nil
			a.state.Messages = append(a.state.Messages, event.Message)
			a.mu.Unlock()
		case EventTypeToolExecutionStart:
			a.mu.Lock()
			s := a.state.PendingToolCalls
			s[event.ToolCallID] = struct{}{}
			a.state.PendingToolCalls = s
			a.mu.Unlock()
		case EventTypeToolExecutionEnd:
			a.mu.Lock()
			s := a.state.PendingToolCalls
			delete(s, event.ToolCallID)
			a.state.PendingToolCalls = s
			a.mu.Unlock()
		case EventTypeTurnEnd:
			if msg, ok := event.Message.(types.AssistantMessage); ok && msg.ErrorMessage != "" {
				a.mu.Lock()
				a.state.Error = msg.ErrorMessage
				a.mu.Unlock()
			}
			if msg, ok := event.Message.(*types.AssistantMessage); ok && msg.ErrorMessage != "" {
				a.mu.Lock()
				a.state.Error = msg.ErrorMessage
				a.mu.Unlock()
			}
		case EventTypeAgentEnd:
			a.mu.Lock()
			a.state.IsStreaming = false
			a.state.StreamMessage = nil
			a.mu.Unlock()
		}
		a.emit(event)
	}

	_, resultErr := stream.Result()
	if resultErr != nil {
		loopErr = resultErr
	}

	if partial != nil {
		if msg, ok := partial.(types.AssistantMessage); ok && hasNonEmptyContent(msg) {
			a.mu.Lock()
			a.state.Messages = append(a.state.Messages, partial)
			a.mu.Unlock()
		} else if msg, ok := partial.(*types.AssistantMessage); ok && hasNonEmptyContent(*msg) {
			a.mu.Lock()
			a.state.Messages = append(a.state.Messages, partial)
			a.mu.Unlock()
		}
	}

	if loopErr != nil {
		a.mu.RLock()
		m := a.state.Model
		a.mu.RUnlock()

		stopReason := types.StopReason("error")
		if ctx.Err() != nil {
			stopReason = "aborted"
		}
		providerID := ""
		if m != nil {
			providerID = m.ProviderID
		}
		modelID := ""
		if m != nil {
			modelID = m.ID
		}
		errorMsg := types.AssistantMessage{
			Role:         "assistant",
			Content:      []types.ContentBlock{&types.TextContent{Type: "text", Text: ""}},
			ProviderID:   providerID,
			Model:        modelID,
			Usage:        types.AgentUsage{},
			StopReason:   stopReason,
			ErrorMessage: loopErr.Error(),
			Timestamp:    time.Now().UnixMilli(),
		}
		a.mu.Lock()
		a.state.Messages = append(a.state.Messages, errorMsg)
		a.state.Error = loopErr.Error()
		a.mu.Unlock()
		a.emit(AgentEvent{Type: EventTypeAgentEnd, Messages: []AgentMessage{errorMsg}})
	}

	return loopErr
}

func (a *Agent) emit(event AgentEvent) {
	a.mu.RLock()
	listeners := make([]func(AgentEvent), 0, len(a.listeners))
	for _, l := range a.listeners {
		listeners = append(listeners, l)
	}
	a.mu.RUnlock()
	for _, l := range listeners {
		l(event)
	}
}

func hasNonEmptyContent(msg types.AssistantMessage) bool {
	for _, block := range msg.Content {
		switch v := block.(type) {
		case *types.ThinkingContent:
			if v != nil && strings.TrimSpace(v.Thinking) != "" {
				return true
			}
		case types.ThinkingContent:
			if strings.TrimSpace(v.Thinking) != "" {
				return true
			}
		case *types.TextContent:
			if v != nil && strings.TrimSpace(v.Text) != "" {
				return true
			}
		case types.TextContent:
			if strings.TrimSpace(v.Text) != "" {
				return true
			}
		case *types.ToolCallContent:
			if v != nil && strings.TrimSpace(v.Name) != "" {
				return true
			}
		case types.ToolCallContent:
			if strings.TrimSpace(v.Name) != "" {
				return true
			}
		}
	}
	return false
}

func defaultConvertToProviders(messages []AgentMessage) ([]types.AgentMessage, error) {
	out := make([]types.AgentMessage, 0, len(messages))
	for _, m := range messages {
		role := extractRole(m)
		if role == "user" || role == "assistant" || role == "toolResult" {
			out = append(out, m)
		}
	}
	return out, nil
}
