package agent

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/providers"
)

const (
	defaultMaxRetries    = 3
	defaultBaseDelayMs   = 1000
	defaultMaxDelayMs    = 30000
	defaultRetryJitterMs = 500
)

func AgentLoop(prompts []AgentMessage, context AgentContext, config AgentLoopConfig, ctx context.Context, streamFn StreamFn) *EventStream {
	stream := NewEventStream()
	go func() {
		defer stream.Close()
		newMessages := append([]AgentMessage{}, prompts...)
		currentContext := AgentContext{
			SystemPrompt: context.SystemPrompt,
			Messages:     append(append([]AgentMessage{}, context.Messages...), prompts...),
			Tools:        context.Tools,
		}
		stream.Push(AgentEvent{Type: EventTypeAgentStart})
		stream.Push(AgentEvent{Type: EventTypeTurnStart})
		for _, prompt := range prompts {
			stream.Push(AgentEvent{Type: EventTypeMessageStart, Message: prompt})
			stream.Push(AgentEvent{Type: EventTypeMessageEnd, Message: prompt})
		}
		runLoop(currentContext, newMessages, config, ctx, stream, streamFn)
	}()
	return stream
}

func AgentLoopContinue(context AgentContext, config AgentLoopConfig, ctx context.Context, streamFn StreamFn) (*EventStream, error) {
	if len(context.Messages) == 0 {
		return nil, fmt.Errorf("cannot continue: no messages in context")
	}
	lastRole := extractRole(context.Messages[len(context.Messages)-1])
	if lastRole == string(RoleAssistant) {
		return nil, fmt.Errorf("cannot continue from message role: assistant")
	}
	stream := NewEventStream()
	go func() {
		defer stream.Close()
		stream.Push(AgentEvent{Type: EventTypeAgentStart})
		stream.Push(AgentEvent{Type: EventTypeTurnStart})
		runLoop(context, []AgentMessage{}, config, ctx, stream, streamFn)
	}()
	return stream, nil
}

func runLoop(currentContext AgentContext, newMessages []AgentMessage, config AgentLoopConfig, ctx context.Context, stream *EventStream, streamFn StreamFn) {
	firstTurn := true
	pendingMessages := getSteeringMessages(config)
	for {
		hasMoreToolCalls := true
		var steeringAfterTools []AgentMessage
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if ctx.Err() != nil {
				stream.Push(AgentEvent{Type: EventTypeAgentEnd, Messages: newMessages})
				return
			}
			if !firstTurn {
				stream.Push(AgentEvent{Type: EventTypeTurnStart})
			} else {
				firstTurn = false
			}
			if len(pendingMessages) > 0 {
				for _, message := range pendingMessages {
					stream.Push(AgentEvent{Type: EventTypeMessageStart, Message: message})
					stream.Push(AgentEvent{Type: EventTypeMessageEnd, Message: message})
					currentContext.Messages = append(currentContext.Messages, message)
					newMessages = append(newMessages, message)
				}
				pendingMessages = nil
			}
			assistantMessage, err := streamAssistantResponseWithRetry(currentContext, config, ctx, stream, streamFn)
			if err != nil {
				stream.Push(AgentEvent{Type: EventTypeAgentEnd, Messages: newMessages})
				return
			}
			newMessages = append(newMessages, assistantMessage)
			currentContext.Messages = append(currentContext.Messages, assistantMessage)
			if assistantMessage.StopReason == "error" || assistantMessage.StopReason == "aborted" {
				stream.Push(AgentEvent{Type: EventTypeTurnEnd, Message: assistantMessage, ToolResults: []providers.ToolResultMessage{}})
				stream.Push(AgentEvent{Type: EventTypeAgentEnd, Messages: newMessages})
				return
			}
			toolCalls := extractToolCalls(assistantMessage)
			hasMoreToolCalls = len(toolCalls) > 0
			toolResults := []providers.ToolResultMessage{}
			if hasMoreToolCalls {
				execResults, steering := executeToolCalls(currentContext.Tools, toolCalls, ctx, stream, config)
				toolResults = append(toolResults, execResults...)
				steeringAfterTools = steering
				for i := range toolResults {
					currentContext.Messages = append(currentContext.Messages, toolResults[i])
					newMessages = append(newMessages, toolResults[i])
				}
			}
			stream.Push(AgentEvent{Type: EventTypeTurnEnd, Message: assistantMessage, ToolResults: toolResults})
			if len(steeringAfterTools) > 0 {
				pendingMessages = steeringAfterTools
				steeringAfterTools = nil
			} else {
				pendingMessages = getSteeringMessages(config)
			}
		}
		followUps := getFollowUpMessages(config)
		if len(followUps) > 0 {
			pendingMessages = followUps
			continue
		}
		break
	}
	stream.Push(AgentEvent{Type: EventTypeAgentEnd, Messages: newMessages})
}

func streamAssistantResponse(context AgentContext, config AgentLoopConfig, ctx context.Context, stream *EventStream, streamFn StreamFn) (*providers.AssistantMessage, error) {
	if config.Model == nil {
		return nil, fmt.Errorf("model is required")
	}
	messages := context.Messages
	if config.TransformContext != nil {
		next, err := config.TransformContext(messages, ctx)
		if err != nil {
			return nil, err
		}
		messages = next
	}
	if config.ConvertToLlm == nil {
		return nil, fmt.Errorf("convertToLlm is required")
	}
	llmMessages, err := config.ConvertToLlm(messages)
	if err != nil {
		return nil, err
	}
	toolDefs := buildToolDefinitions(context.Tools)
	llmCtx := &providers.LLMContext{
		SystemPrompt: context.SystemPrompt,
		Messages:     llmMessages,
		Tools:        toolDefs,
	}
	resolvedKey := config.APIKey
	if config.GetAPIKey != nil {
		key, err := config.GetAPIKey(config.Model.ProviderID)
		if err == nil && key != "" {
			resolvedKey = key
		}
	}
	opts := &providers.SimpleStreamOptions{
		StreamOptions: providers.StreamOptions{
			Temperature:     config.Temperature,
			MaxTokens:       config.MaxTokens,
			APIKey:          resolvedKey,
			CacheRetention:  config.CacheRetention,
			SessionID:       config.SessionID,
			Headers:         config.Headers,
			MaxRetryDelayMs: config.MaxRetryDelayMs,
			Transport:       config.Transport,
		},
		Reasoning:       mapThinkingLevel(config.Reasoning),
		ThinkingBudgets: config.ThinkingBudgets,
	}
	if streamFn == nil {
		streamFn = providers.StreamDefault
	}
	response, err := streamFn(ctx, config.Model, llmCtx, opts)
	if err != nil {
		return nil, err
	}
	var partial *providers.AssistantMessage
	addedPartial := false
	for event := range response.Events() {
		switch event.Type {
		case "start":
			partial = event.Partial
			if partial != nil {
				context.Messages = append(context.Messages, *partial)
				addedPartial = true
				stream.Push(AgentEvent{Type: EventTypeMessageStart, Message: *partial})
			}
		case "text_start", "text_delta", "text_end", "thinking_start", "thinking_delta", "thinking_end", "toolcall_start", "toolcall_delta", "toolcall_end":
			if event.Partial != nil {
				partial = event.Partial
				if addedPartial && len(context.Messages) > 0 {
					context.Messages[len(context.Messages)-1] = *partial
				} else if !addedPartial {
					context.Messages = append(context.Messages, *partial)
					addedPartial = true
				}
				stream.Push(AgentEvent{Type: EventTypeMessageUpdate, Message: *partial, AssistantMessageEvent: &event})
			}
		case "done", "error":
			finalMessage, err := response.Result()
			if err != nil || finalMessage == nil {
				if err == nil {
					err = fmt.Errorf("missing final message")
				}
				return nil, err
			}
			if addedPartial {
				context.Messages[len(context.Messages)-1] = *finalMessage
			} else {
				context.Messages = append(context.Messages, *finalMessage)
				stream.Push(AgentEvent{Type: EventTypeMessageStart, Message: *finalMessage})
			}
			stream.Push(AgentEvent{Type: EventTypeMessageEnd, Message: *finalMessage})
			return finalMessage, nil
		}
	}
	finalMessage, err := response.Result()
	if err != nil || finalMessage == nil {
		if err == nil {
			err = fmt.Errorf("missing final message")
		}
		return nil, err
	}
	return finalMessage, nil
}

func executeToolCalls(tools []AgentTool, toolCalls []providers.ToolCallContent, ctx context.Context, stream *EventStream, config AgentLoopConfig) ([]providers.ToolResultMessage, []AgentMessage) {
	results := []providers.ToolResultMessage{}
	var steeringMessages []AgentMessage
	for index, toolCall := range toolCalls {
		stream.Push(AgentEvent{Type: EventTypeToolExecutionStart, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments})
		var result AgentToolResult
		isError := false
		tool := findTool(tools, toolCall.Name)
		if tool == nil {
			result = AgentToolResult{
				Content: []providers.ContentBlock{&providers.TextContent{Type: "text", Text: "Tool not found"}},
				Details: map[string]any{},
			}
			isError = true
		} else {
			args := toolCall.Arguments
			toolDef := providers.ToolDefinition{Name: tool.Name(), Description: tool.Description(), Parameters: tool.Parameters()}
			parsed, err := providers.ValidateToolArguments(toolDef, toolCall)
			if err != nil {
				result = AgentToolResult{
					Content: []providers.ContentBlock{&providers.TextContent{Type: "text", Text: err.Error()}},
					Details: map[string]any{},
				}
				isError = true
			} else {
				_ = args
				r, err := tool.Execute(ctx, toolCall.ID, parsed, func(partial AgentToolResult) {
					stream.Push(AgentEvent{Type: EventTypeToolExecutionUpdate, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments, Partial: partial})
				})
				result = r
				if err != nil {
					result = AgentToolResult{
						Content: []providers.ContentBlock{&providers.TextContent{Type: "text", Text: err.Error()}},
						Details: map[string]any{},
					}
					isError = true
				}
			}
		}
		stream.Push(AgentEvent{Type: EventTypeToolExecutionEnd, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Result: result, IsError: isError})
		toolResult := providers.ToolResultMessage{
			Role:       "toolResult",
			ToolCallID: toolCall.ID,
			ToolName:   toolCall.Name,
			Content:    result.Content,
			Details:    result.Details,
			IsError:    isError,
			Timestamp:  time.Now().UnixMilli(),
		}
		results = append(results, toolResult)
		stream.Push(AgentEvent{Type: EventTypeMessageStart, Message: toolResult})
		stream.Push(AgentEvent{Type: EventTypeMessageEnd, Message: toolResult})
		if config.GetSteeringMessages != nil {
			steering, err := config.GetSteeringMessages()
			if err == nil && len(steering) > 0 {
				steeringMessages = steering
				for _, skipped := range toolCalls[index+1:] {
					results = append(results, skipToolCall(skipped, stream))
				}
				break
			}
		}
	}
	return results, steeringMessages
}

func skipToolCall(toolCall providers.ToolCallContent, stream *EventStream) providers.ToolResultMessage {
	result := AgentToolResult{
		Content: []providers.ContentBlock{&providers.TextContent{Type: "text", Text: "Skipped due to queued user message."}},
		Details: map[string]any{},
	}
	stream.Push(AgentEvent{Type: EventTypeToolExecutionStart, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments})
	stream.Push(AgentEvent{Type: EventTypeToolExecutionEnd, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Result: result, IsError: true})
	toolResult := providers.ToolResultMessage{
		Role:       "toolResult",
		ToolCallID: toolCall.ID,
		ToolName:   toolCall.Name,
		Content:    result.Content,
		Details:    result.Details,
		IsError:    true,
		Timestamp:  time.Now().UnixMilli(),
	}
	stream.Push(AgentEvent{Type: EventTypeMessageStart, Message: toolResult})
	stream.Push(AgentEvent{Type: EventTypeMessageEnd, Message: toolResult})
	return toolResult
}

func findTool(tools []AgentTool, name string) AgentTool {
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	return nil
}

func buildToolDefinitions(tools []AgentTool) []providers.ToolDefinition {
	out := make([]providers.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		out = append(out, providers.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		})
	}
	return out
}

func mapThinkingLevel(level ThinkingLevel) providers.ThinkingLevel {
	switch level {
	case ThinkingLevelMinimal:
		return providers.ThinkingLevelMinimal
	case ThinkingLevelLow:
		return providers.ThinkingLevelLow
	case ThinkingLevelMedium:
		return providers.ThinkingLevelMedium
	case ThinkingLevelHigh:
		return providers.ThinkingLevelHigh
	case ThinkingLevelXHigh:
		return providers.ThinkingLevelXHigh
	default:
		return ""
	}
}

func extractToolCalls(message *providers.AssistantMessage) []providers.ToolCallContent {
	var out []providers.ToolCallContent
	for _, block := range message.Content {
		switch v := block.(type) {
		case providers.ToolCallContent:
			out = append(out, v)
		case *providers.ToolCallContent:
			out = append(out, *v)
		}
	}
	return out
}

func getSteeringMessages(config AgentLoopConfig) []AgentMessage {
	if config.GetSteeringMessages == nil {
		return nil
	}
	msgs, err := config.GetSteeringMessages()
	if err != nil {
		return nil
	}
	return msgs
}

func getFollowUpMessages(config AgentLoopConfig) []AgentMessage {
	if config.GetFollowUpMessages == nil {
		return nil
	}
	msgs, err := config.GetFollowUpMessages()
	if err != nil {
		return nil
	}
	return msgs
}

func streamAssistantResponseWithRetry(context AgentContext, config AgentLoopConfig, ctx context.Context, stream *EventStream, streamFn StreamFn) (*providers.AssistantMessage, error) {
	maxRetries := defaultMaxRetries
	maxDelayMs := defaultMaxDelayMs
	if config.MaxRetryDelayMs > 0 {
		maxDelayMs = config.MaxRetryDelayMs
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			baseDelay := float64(defaultBaseDelayMs) * math.Pow(2, float64(attempt-1))
			if baseDelay > float64(maxDelayMs) {
				baseDelay = float64(maxDelayMs)
			}
			jitter := rand.Float64() * float64(defaultRetryJitterMs)
			delay := time.Duration(baseDelay+jitter) * time.Millisecond

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		msg, err := streamAssistantResponse(context, config, ctx, stream, streamFn)
		if err == nil {
			return msg, nil
		}

		lastErr = err
		if !isRetryableError(err) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", maxRetries, lastErr)
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if _, ok := err.(net.Error); ok {
		return true
	}
	retryablePatterns := []string{
		"429", "rate limit", "too many requests",
		"500", "internal server error",
		"502", "bad gateway",
		"503", "service unavailable", "overloaded",
		"504", "gateway timeout",
		"connection refused", "connection reset",
		"eof", "broken pipe",
		"timeout", "timed out",
		"temporary", "unavailable",
	}
	lower := strings.ToLower(msg)
	for _, pattern := range retryablePatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func extractRole(message AgentMessage) string {
	switch m := message.(type) {
	case providers.UserMessage:
		return m.Role
	case *providers.UserMessage:
		return m.Role
	case providers.AssistantMessage:
		return m.Role
	case *providers.AssistantMessage:
		return m.Role
	case providers.ToolResultMessage:
		return m.Role
	case *providers.ToolResultMessage:
		return m.Role
	case Message:
		return string(m.Role)
	case *Message:
		return string(m.Role)
	default:
		return ""
	}
}
