package agent

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"
)

type AgentOptions struct {
	InitialState AgentState // Partial state in TS, full struct here for simplicity
	SteeringMode ExecutionMode
	FollowUpMode ExecutionMode
	GetAPIKey    func(provider string) (string, error)
}

type Agent struct {
	state        AgentState
	steeringMode ExecutionMode
	followUpMode ExecutionMode
	getAPIKey    func(provider string) (string, error)

	// Queues
	steeringQueue []Message
	followUpQueue []Message

	// Concurency
	mu        sync.RWMutex
	listeners []func(AgentEvent)
}

func NewAgent(opts AgentOptions) *Agent {
	// Initialize defaults
	if opts.InitialState.ThinkingLevel == "" {
		opts.InitialState.ThinkingLevel = ThinkingLevelOff
	}
	if opts.SteeringMode == "" {
		opts.SteeringMode = ExecutionModeOneAtATime
	}
	if opts.FollowUpMode == "" {
		opts.FollowUpMode = ExecutionModeOneAtATime
	}
	if opts.InitialState.PendingToolCalls == nil {
		opts.InitialState.PendingToolCalls = make(map[string]struct{})
	}
	if opts.InitialState.Messages == nil {
		opts.InitialState.Messages = make([]Message, 0)
	}

	return &Agent{
		state:        opts.InitialState,
		steeringMode: opts.SteeringMode,
		followUpMode: opts.FollowUpMode,
		getAPIKey:    opts.GetAPIKey,
		listeners:    make([]func(AgentEvent), 0),
	}
}

// --- Public API ---

func (a *Agent) Subscribe(listener func(AgentEvent)) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listeners = append(a.listeners, listener)

	// Return unsubscribe function
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for i, l := range a.listeners {
			// Compare function pointers (simplified)
			// In real Go utilizing list element pointers or IDs is safer
			if reflect.ValueOf(l).Pointer() == reflect.ValueOf(listener).Pointer() {
				a.listeners = append(a.listeners[:i], a.listeners[i+1:]...)
				break
			}
		}
	}
}

func (a *Agent) Prompt(ctx context.Context, input interface{}) error {
	a.mu.Lock()
	if a.state.IsStreaming {
		a.mu.Unlock()
		return fmt.Errorf("agent is already processing")
	}
	a.mu.Unlock()

	var newMessages []Message

	switch v := input.(type) {
	case string:
		newMessages = []Message{{Role: RoleUser, Content: []ContentBlock{{Type: ContentTypeText, Text: v}}, Timestamp: time.Now().UnixMilli()}}
	case Message:
		newMessages = []Message{v}
	case []Message:
		newMessages = v
	default:
		return fmt.Errorf("invalid input type")
	}

	return a.RunLoop(ctx, newMessages)
}

func (a *Agent) Steer(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringQueue = append(a.steeringQueue, msg)
}

func (a *Agent) FollowUp(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.followUpQueue = append(a.followUpQueue, msg)
}

// --- State Access ---

func (a *Agent) GetState() AgentState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	// Deep copy needed for safety in real prod code
	return a.state
}

func (a *Agent) GetSteeringMessages() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.steeringMode == ExecutionModeOneAtATime {
		if len(a.steeringQueue) > 0 {
			msg := a.steeringQueue[0]
			a.steeringQueue = a.steeringQueue[1:]
			return []Message{msg}
		}
		return []Message{}
	}

	msgs := a.steeringQueue
	a.steeringQueue = []Message{}
	return msgs
}

func (a *Agent) GetFollowUpMessages() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.followUpMode == ExecutionModeOneAtATime {
		if len(a.followUpQueue) > 0 {
			msg := a.followUpQueue[0]
			a.followUpQueue = a.followUpQueue[1:]
			return []Message{msg}
		}
		return []Message{}
	}

	msgs := a.followUpQueue
	a.followUpQueue = []Message{}
	return msgs
}

// Internal helper to emit events
func (a *Agent) Emit(event AgentEvent) {
	a.mu.RLock()
	listeners := make([]func(AgentEvent), len(a.listeners))
	copy(listeners, a.listeners)
	a.mu.RUnlock()

	for _, l := range listeners {
		l(event)
	}
}

// Internal state mutators
func (a *Agent) AppendMessage(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append(a.state.Messages, msg)
}

func (a *Agent) SetStreaming(streaming bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.IsStreaming = streaming
}

func (a *Agent) SetError(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Error = err
}
