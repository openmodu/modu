package agent

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

const (
	defaultMaxRetries    = 3
	defaultBaseDelayMs   = 1000
	defaultMaxDelayMs    = 30000
	defaultRetryJitterMs = 500
)

func AgentLoop(prompts []AgentMessage, context AgentContext, config AgentConfig, ctx context.Context, streamFn StreamFn) *EventStream {
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

func AgentLoopContinue(context AgentContext, config AgentConfig, ctx context.Context, streamFn StreamFn) (*EventStream, error) {
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

func runLoop(currentContext AgentContext, newMessages []AgentMessage, config AgentConfig, ctx context.Context, stream *EventStream, streamFn StreamFn) {
	firstTurn := true
	pendingMessages := getSteeringMessages(config)
	for {
		hasMoreToolCalls := true
		var steeringAfterTools []AgentMessage
		for hasMoreToolCalls || len(pendingMessages) > 0 {
			if ctx.Err() != nil {
				stream.Push(AgentEvent{Type: EventTypeAgentEnd, Messages: newMessages})
				stream.Resolve(newMessages, ctx.Err())
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
				stream.Resolve(newMessages, err)
				return
			}
			newMessages = append(newMessages, assistantMessage)
			currentContext.Messages = append(currentContext.Messages, assistantMessage)
			if assistantMessage.StopReason == "error" || assistantMessage.StopReason == "aborted" {
				stream.Push(AgentEvent{Type: EventTypeTurnEnd, Message: assistantMessage, ToolResults: []types.ToolResultMessage{}})
				stream.Push(AgentEvent{Type: EventTypeAgentEnd, Messages: newMessages})
				stream.Resolve(newMessages, nil)
				return
			}
			toolCalls := extractToolCalls(assistantMessage)
			hasMoreToolCalls = len(toolCalls) > 0
		var execResults []types.ToolResultMessage
			if hasMoreToolCalls {
				execResults, steering := executeToolCalls(currentContext.Tools, toolCalls, ctx, stream, config)
				steeringAfterTools = steering
				for i := range execResults {
					currentContext.Messages = append(currentContext.Messages, execResults[i])
					newMessages = append(newMessages, execResults[i])
				}
			}
			stream.Push(AgentEvent{Type: EventTypeTurnEnd, Message: assistantMessage, ToolResults: execResults})
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
	stream.Resolve(newMessages, nil)
}

func streamAssistantResponse(context AgentContext, config AgentConfig, ctx context.Context, stream *EventStream, streamFn StreamFn) (*types.AssistantMessage, error) {
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
	var llmMessages []types.AgentMessage
	var err error
	if config.ConvertToLlm != nil {
		llmMessages, err = config.ConvertToLlm(messages)
		if err != nil {
			return nil, err
		}
	} else {
		// Just pass messages down since StreamDefault now handles it
		llmMessages = messages
	}
	toolDefs := buildToolDefinitions(context.Tools)
	llmCtx := &types.LLMContext{
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
	opts := &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			Temperature:     config.Temperature,
			MaxTokens:       config.MaxTokens,
			APIKey:          resolvedKey,
			CacheRetention:  config.CacheRetention,
			SessionID:       config.SessionID,
			Headers:         config.Headers,
			MaxRetryDelayMs: config.MaxRetryDelayMs,
		},
		Reasoning:       mapThinkingLevel(config.Reasoning),
		ThinkingBudgets: config.ThinkingBudgets,
	}
	if streamFn == nil {
		streamFn = StreamDefault
	}
	response, err := streamFn(ctx, config.Model, llmCtx, opts)
	if err != nil {
		return nil, err
	}
	var partial *types.AssistantMessage
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
				stream.Push(AgentEvent{Type: EventTypeMessageUpdate, Message: *partial, StreamEvent: &event})
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

func executeToolCalls(tools []AgentTool, toolCalls []types.ToolCallContent, ctx context.Context, stream *EventStream, config AgentConfig) ([]types.ToolResultMessage, []AgentMessage) {
	results := []types.ToolResultMessage{}
	var steeringMessages []AgentMessage
	for index, toolCall := range toolCalls {
		stream.Push(AgentEvent{Type: EventTypeToolExecutionStart, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments})
		var result AgentToolResult
		isError := false
		tool := findTool(tools, toolCall.Name)
		if tool == nil {
			result = AgentToolResult{
				Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Tool not found"}},
				Details: map[string]any{},
			}
			isError = true
		} else {
			args := toolCall.Arguments
			toolDef := types.ToolDefinition{Name: tool.Name(), Description: tool.Description(), Parameters: tool.Parameters()}
			parsed, err := ValidateToolArguments(toolDef, toolCall)
			if err != nil {
				result = AgentToolResult{
					Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: err.Error()}},
					Details: map[string]any{},
				}
				isError = true
			} else {
				_ = args
				// Check approval before executing
				if config.ApproveTool != nil {
					decision, approveErr := config.ApproveTool(toolCall.Name, toolCall.ID, parsed)
					if approveErr != nil || !decision.IsAllow() {
						msg := "Tool execution denied by user."
						if approveErr != nil {
							msg = fmt.Sprintf("Tool approval error: %v", approveErr)
						}
						result = AgentToolResult{
							Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: msg}},
							Details: map[string]any{"denied": true},
						}
						isError = true
					}
				}
				if !isError {
					r, err := tool.Execute(ctx, toolCall.ID, parsed, func(partial AgentToolResult) {
						stream.Push(AgentEvent{Type: EventTypeToolExecutionUpdate, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments, Partial: partial})
					})
					result = r
					if err != nil {
						result = AgentToolResult{
							Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: err.Error()}},
							Details: map[string]any{},
						}
						isError = true
					}
				}
			}
		}
		stream.Push(AgentEvent{Type: EventTypeToolExecutionEnd, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments, Result: result, IsError: isError})
		toolResult := types.ToolResultMessage{
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

func skipToolCall(toolCall types.ToolCallContent, stream *EventStream) types.ToolResultMessage {
	result := AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Skipped due to queued user message."}},
		Details: map[string]any{},
	}
	stream.Push(AgentEvent{Type: EventTypeToolExecutionStart, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Args: toolCall.Arguments})
	stream.Push(AgentEvent{Type: EventTypeToolExecutionEnd, ToolCallID: toolCall.ID, ToolName: toolCall.Name, Result: result, IsError: true})
	toolResult := types.ToolResultMessage{
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

func buildToolDefinitions(tools []AgentTool) []types.ToolDefinition {
	out := make([]types.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		out = append(out, types.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		})
	}
	return out
}

func mapThinkingLevel(level ThinkingLevel) types.ThinkingLevel {
	switch level {
	case ThinkingLevelMinimal:
		return types.ThinkingLevelMinimal
	case ThinkingLevelLow:
		return types.ThinkingLevelLow
	case ThinkingLevelMedium:
		return types.ThinkingLevelMedium
	case ThinkingLevelHigh:
		return types.ThinkingLevelHigh
	case ThinkingLevelXHigh:
		return types.ThinkingLevelXHigh
	default:
		return ""
	}
}

func extractToolCalls(message *types.AssistantMessage) []types.ToolCallContent {
	var out []types.ToolCallContent
	for _, block := range message.Content {
		if v, ok := block.(*types.ToolCallContent); ok {
			out = append(out, *v)
		}
	}
	return out
}

func getSteeringMessages(config AgentConfig) []AgentMessage {
	if config.GetSteeringMessages == nil {
		return nil
	}
	msgs, err := config.GetSteeringMessages()
	if err != nil {
		return nil
	}
	return msgs
}

func getFollowUpMessages(config AgentConfig) []AgentMessage {
	if config.GetFollowUpMessages == nil {
		return nil
	}
	msgs, err := config.GetFollowUpMessages()
	if err != nil {
		return nil
	}
	return msgs
}

func streamAssistantResponseWithRetry(context AgentContext, config AgentConfig, ctx context.Context, stream *EventStream, streamFn StreamFn) (*types.AssistantMessage, error) {
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
