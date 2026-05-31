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
	state       types.State
	config      types.Config
	loop        *Loop
	steering    []types.AgentMessage
	followUp    []types.AgentMessage
	listeners   map[int]func(types.Event)
	listenerID  int
	running     chan struct{}
	resume      chan types.ResumeDecision
	resumeReady chan struct{}
	cancel      context.CancelFunc
}

func NewAgent(cfg types.Config) *Agent {
	if cfg.SteeringMode == "" {
		cfg.SteeringMode = types.ExecutionModeOneAtATime
	}
	if cfg.FollowUpMode == "" {
		cfg.FollowUpMode = types.ExecutionModeOneAtATime
	}
	return &Agent{
		state:     initialState(cfg),
		config:    cfg,
		loop:      NewLoop(DefaultLLM{}, DefaultTools{}),
		listeners: map[int]func(types.Event){},
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
	return a.run(ctx, []types.AgentMessage{types.UserMessage{Role: types.RoleUser, Content: content, Timestamp: time.Now().UnixMilli()}})
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

func (a *Agent) run(ctx context.Context, messages []types.AgentMessage) error {
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
		a.state.Status = types.SessionStatusRunning
		a.resumeReady = make(chan struct{})
	}
	agentCtx := types.AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     append([]types.AgentMessage{}, a.state.Messages...),
		Tools:        a.state.Tools,
	}
	config := a.config
	config.Model = a.state.Model
	config.Reasoning = a.state.ThinkingLevel
	runtime := types.RuntimeHooks{
		ApproveTool: config.ApproveTool,
		GetSteeringMessages: func() ([]types.AgentMessage, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.dequeueSteeringLocked(), nil
		},
		GetFollowUpMessages: func() ([]types.AgentMessage, error) {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.dequeueFollowUpLocked(), nil
		},
	}
	if config.EnableInterrupts && runtime.ApproveTool == nil {
		runtime.ApproveTool = func(toolName, toolCallID string, args map[string]any) (types.ToolApprovalDecision, error) {
			decision := a.waitForResume(runCtx)
			if decision.Allow {
				return types.ToolApprovalAllow, nil
			}
			return types.ToolApprovalDeny, nil
		}
	}
	if config.EnableInterrupts {
		runtime.OnMaxStepsReached = func(stepCount int) types.ResumeDecision {
			return a.waitForResume(runCtx)
		}
	}
	a.mu.Unlock()

	stream := types.NewAgentEventStream()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range stream.Events() {
			a.applyEvent(event)
			a.emit(event)
		}
	}()

	_, err := a.loop.Run(runCtx, types.LoopInput{
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
			a.state.Status = types.SessionStatusFailed
		} else {
			a.state.Status = types.SessionStatusCompleted
		}
	}
	if a.running != nil {
		close(a.running)
		a.running = nil
	}
	a.mu.Unlock()

	return err
}

func promptMessages(input any) ([]types.AgentMessage, error) {
	switch v := input.(type) {
	case string:
		return []types.AgentMessage{types.UserMessage{Role: types.RoleUser, Content: v, Timestamp: time.Now().UnixMilli()}}, nil
	case []types.AgentMessage:
		return v, nil
	default:
		if input == nil {
			return nil, fmt.Errorf("invalid input type")
		}
		return []types.AgentMessage{input}, nil
	}
}
