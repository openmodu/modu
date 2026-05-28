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
	runtime := RuntimeHooks{
		ApproveTool: config.ApproveTool,
		GetSteeringMessages: func() ([]AgentMessage, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.dequeueSteeringLocked(), nil
		},
		GetFollowUpMessages: func() ([]AgentMessage, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.dequeueFollowUpLocked(), nil
		},
	}
	if config.EnableInterrupts && runtime.ApproveTool == nil {
		runtime.ApproveTool = func(toolName, toolCallID string, args map[string]any) (ToolApprovalDecision, error) {
			decision := a.waitForResume(runCtx)
			if decision.Allow {
				return ToolApprovalAllow, nil
			}
			return ToolApprovalDeny, nil
		}
	}
	if config.EnableInterrupts {
		runtime.OnMaxStepsReached = func(stepCount int) ResumeDecision {
			return a.waitForResume(runCtx)
		}
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
		Runtime: runtime,
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
