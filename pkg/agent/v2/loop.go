package agent

import (
	"context"
	"fmt"

	"github.com/openmodu/modu/pkg/types"
)

func NewLoop(llm LLM, tools Tools) *Loop {
	if llm == nil {
		llm = DefaultLLM{}
	}
	if tools == nil {
		tools = DefaultTools{}
	}
	return &Loop{LLM: llm, Tools: tools}
}

func (l *Loop) Run(ctx context.Context, input LoopInput) (LoopResult, error) {
	if l == nil {
		l = NewLoop(nil, nil)
	}
	stream := input.Events
	if stream == nil {
		stream = NewEventStream()
		go func() {
			for range stream.Events() {
			}
		}()
		defer stream.Close()
	}

	newMessages := append([]AgentMessage{}, input.Prompts...)
	current := AgentContext{
		SystemPrompt: input.Context.SystemPrompt,
		Messages:     append(append([]AgentMessage{}, input.Context.Messages...), input.Prompts...),
		Tools:        input.Context.Tools,
	}

	stream.Push(Event{Type: EventTypeAgentStart})
	for _, prompt := range input.Prompts {
		emitMessage(stream, prompt)
	}

	stepCount := 0
	pending := getMessages(input.Config.GetSteeringMessages)
	for {
		if ctx.Err() != nil {
			return finish(stream, newMessages, ctx.Err())
		}
		if len(pending) > 0 {
			appendMessages(stream, &current, &newMessages, pending)
			pending = nil
		}
		if input.Config.MaxSteps > 0 && stepCount >= input.Config.MaxSteps {
			return finish(stream, newMessages, nil)
		}

		stream.Push(Event{Type: EventTypeTurnStart})
		assistantMessage, err := l.LLM.Complete(ctx, LLMInput{
			Context: current,
			Config:  input.Config,
			Events:  stream,
		})
		if err != nil {
			return finish(stream, newMessages, err)
		}
		stepCount++
		newMessages = append(newMessages, assistantMessage)
		current.Messages = append(current.Messages, assistantMessage)

		if assistantMessage.StopReason == "error" || assistantMessage.StopReason == "aborted" {
			stream.Push(Event{Type: EventTypeTurnEnd, Message: assistantMessage})
			return finish(stream, newMessages, nil)
		}

		toolCalls := extractToolCalls(assistantMessage)
		if len(toolCalls) == 0 {
			stream.Push(Event{Type: EventTypeTurnEnd, Message: assistantMessage})
			followUps := getMessages(input.Config.GetFollowUpMessages)
			if len(followUps) == 0 {
				return finish(stream, newMessages, nil)
			}
			pending = followUps
			continue
		}

		toolOutput, err := l.Tools.Execute(ctx, ToolInput{
			Tools:               current.Tools,
			Calls:               toolCalls,
			Events:              stream,
			ApproveTool:         input.Config.ApproveTool,
			GetSteeringMessages: input.Config.GetSteeringMessages,
		})
		if err != nil {
			return finish(stream, newMessages, err)
		}
		for _, message := range toolOutput.Messages {
			current.Messages = append(current.Messages, message)
			newMessages = append(newMessages, message)
		}
		stream.Push(Event{Type: EventTypeTurnEnd, Message: assistantMessage, ToolResults: toolOutput.Results})

		if len(toolOutput.Steering) > 0 {
			pending = toolOutput.Steering
		} else {
			pending = getMessages(input.Config.GetSteeringMessages)
		}
	}
}

func appendMessages(stream *EventStream, current *AgentContext, newMessages *[]AgentMessage, messages []AgentMessage) {
	for _, message := range messages {
		emitMessage(stream, message)
		current.Messages = append(current.Messages, message)
		*newMessages = append(*newMessages, message)
	}
}

func finish(stream *EventStream, messages []AgentMessage, err error) (LoopResult, error) {
	stream.Push(Event{Type: EventTypeAgentEnd, Messages: messages})
	stream.Resolve(messages, err)
	return LoopResult{Messages: messages}, err
}

func getMessages(fn func() ([]AgentMessage, error)) []AgentMessage {
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

func roleOf(message AgentMessage) string {
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
