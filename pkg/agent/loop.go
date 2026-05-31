package agent

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/types"
)

type Loop struct {
	LLM   types.LLM
	Tools types.Tools
}

type discardEvents struct{}

func (discardEvents) Emit(types.Event) {}

func NewLoop(llm types.LLM, tools types.Tools) *Loop {
	if llm == nil {
		llm = DefaultLLM{}
	}
	if tools == nil {
		tools = DefaultTools{}
	}
	return &Loop{LLM: llm, Tools: tools}
}

func (l *Loop) Run(ctx context.Context, input types.LoopInput) (types.LoopResult, error) {
	if l == nil {
		l = NewLoop(nil, nil)
	}
	events := input.Events
	if events == nil {
		events = discardEvents{}
	}

	newMessages := append([]types.AgentMessage{}, input.Prompts...)
	current := types.AgentContext{
		SystemPrompt: input.Context.SystemPrompt,
		Messages:     append(append([]types.AgentMessage{}, input.Context.Messages...), input.Prompts...),
		Tools:        input.Context.Tools,
	}

	events.Emit(types.Event{Type: types.EventTypeAgentStart})
	for _, prompt := range input.Prompts {
		events.Emit(types.Event{Type: types.EventTypeMessageStart, Message: prompt})
		events.Emit(types.Event{Type: types.EventTypeMessageEnd, Message: prompt})
	}

	stepCount := 0
	pending := getMessages(input.Runtime.GetSteeringMessages)
	for {
		if ctx.Err() != nil {
			return finish(events, newMessages, ctx.Err())
		}
		if len(pending) > 0 {
			appendMessages(events, &current, &newMessages, pending)
			pending = nil
		}
		if input.Config.MaxSteps > 0 && stepCount >= input.Config.MaxSteps {
			interrupt := &types.InterruptEvent{Reason: types.InterruptReasonMaxSteps, StepCount: stepCount}
			events.Emit(types.Event{Type: types.EventTypeInterrupt, Interrupt: interrupt})
			if input.Runtime.OnMaxStepsReached == nil {
				return finish(events, newMessages, nil)
			}
			decision := input.Runtime.OnMaxStepsReached(stepCount)
			if !decision.Allow {
				return finish(events, newMessages, nil)
			}
			if decision.Message != "" {
				pending = []types.AgentMessage{types.UserMessage{Role: types.RoleUser, Content: decision.Message}}
			}
			stepCount = 0
			continue
		}

		events.Emit(types.Event{Type: types.EventTypeTurnStart})
		assistantMessage, err := l.LLM.Complete(ctx, types.LLMInput{
			Context: current,
			Options: llmOptions(input.Config),
			Events:  events,
		})
		if err != nil {
			return finish(events, newMessages, err)
		}
		stepCount++
		newMessages = append(newMessages, assistantMessage)
		current.Messages = append(current.Messages, assistantMessage)

		if assistantMessage.StopReason == "error" || assistantMessage.StopReason == "aborted" {
			events.Emit(types.Event{Type: types.EventTypeTurnEnd, Message: assistantMessage})
			return finish(events, newMessages, nil)
		}

		toolCalls := extractToolCalls(assistantMessage)
		if len(toolCalls) == 0 {
			events.Emit(types.Event{Type: types.EventTypeTurnEnd, Message: assistantMessage})
			followUps := getMessages(input.Runtime.GetFollowUpMessages)
			if len(followUps) == 0 {
				return finish(events, newMessages, nil)
			}
			pending = followUps
			continue
		}

		toolOutput, err := l.Tools.Execute(ctx, types.ToolInput{
			Tools:               current.Tools,
			Calls:               toolCalls,
			Events:              events,
			ApproveTool:         input.Runtime.ApproveTool,
			GetSteeringMessages: input.Runtime.GetSteeringMessages,
			EnableInterrupts:    input.Config.EnableInterrupts,
		})
		if err != nil {
			return finish(events, newMessages, err)
		}
		for _, message := range toolOutput.Messages {
			current.Messages = append(current.Messages, message)
			newMessages = append(newMessages, message)
		}
		events.Emit(types.Event{Type: types.EventTypeTurnEnd, Message: assistantMessage, ToolResults: toolOutput.Results})

		if len(toolOutput.Steering) > 0 {
			pending = toolOutput.Steering
		} else {
			pending = getMessages(input.Runtime.GetSteeringMessages)
		}
	}
}

func appendMessages(events types.EventSink, current *types.AgentContext, newMessages *[]types.AgentMessage, messages []types.AgentMessage) {
	for _, message := range messages {
		events.Emit(types.Event{Type: types.EventTypeMessageStart, Message: message})
		events.Emit(types.Event{Type: types.EventTypeMessageEnd, Message: message})
		current.Messages = append(current.Messages, message)
		*newMessages = append(*newMessages, message)
	}
}

func finish(events types.EventSink, messages []types.AgentMessage, err error) (types.LoopResult, error) {
	events.Emit(types.Event{Type: types.EventTypeAgentEnd, Messages: messages})
	if stream, ok := events.(*types.AgentEventStream); ok {
		stream.Resolve(messages, err)
	}
	return types.LoopResult{Messages: messages}, err
}

func llmOptions(config types.Config) types.LLMOptions {
	return types.LLMOptions{
		Model:            config.Model,
		StreamFn:         config.StreamFn,
		ConvertToLLM:     config.ConvertToLLM,
		TransformContext: config.TransformContext,
		GetAPIKey:        config.GetAPIKey,
		Temperature:      config.Temperature,
		MaxTokens:        config.MaxTokens,
		APIKey:           config.APIKey,
		CacheRetention:   config.CacheRetention,
		SessionID:        config.SessionID,
		Headers:          config.Headers,
		Reasoning:        config.Reasoning,
		ThinkingBudgets:  config.ThinkingBudgets,
		MaxRetryDelayMs:  config.MaxRetryDelayMs,
	}
}

func getMessages(fn func() ([]types.AgentMessage, error)) []types.AgentMessage {
	if fn == nil {
		return nil
	}
	messages, err := fn()
	if err != nil {
		return nil
	}
	return messages
}

func extractToolCalls(message *types.AssistantMessage) []types.ToolCallContent {
	if message == nil {
		return nil
	}
	out := make([]types.ToolCallContent, 0)
	for _, block := range message.Content {
		if call, ok := block.(*types.ToolCallContent); ok {
			out = append(out, *call)
		}
	}
	return out
}

func roleOf(message types.AgentMessage) string {
	switch m := message.(type) {
	case types.UserMessage:
		return m.Role
	case *types.UserMessage:
		return m.Role
	case types.AssistantMessage:
		return m.Role
	case *types.AssistantMessage:
		return m.Role
	case types.ToolResultMessage:
		return m.Role
	case *types.ToolResultMessage:
		return m.Role
	default:
		return ""
	}
}

func requireModel(model *types.Model) error {
	if model == nil {
		return fmt.Errorf("model is required")
	}
	return nil
}
