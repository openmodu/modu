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

type DefaultLLM struct{}

func (DefaultLLM) Complete(ctx context.Context, input LLMInput) (*types.AssistantMessage, error) {
	return completeWithRetry(ctx, input)
}

func completeWithRetry(ctx context.Context, input LLMInput) (*types.AssistantMessage, error) {
	maxDelayMs := defaultMaxDelayMs
	if input.Config.MaxRetryDelayMs > 0 {
		maxDelayMs = input.Config.MaxRetryDelayMs
	}

	var lastErr error
	for attempt := 0; attempt <= defaultMaxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepBeforeRetry(ctx, attempt, maxDelayMs); err != nil {
				return nil, err
			}
		}
		msg, err := completeOnce(ctx, input)
		if err == nil {
			return msg, nil
		}
		lastErr = err
		if !isRetryableError(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("max retries (%d) exceeded: %w", defaultMaxRetries, lastErr)
}

func completeOnce(ctx context.Context, input LLMInput) (*types.AssistantMessage, error) {
	if err := requireModel(input.Config.Model); err != nil {
		return nil, err
	}
	if input.Config.StreamFn == nil {
		return nil, fmt.Errorf("stream function is required")
	}

	messages := input.Context.Messages
	if input.Config.TransformContext != nil {
		next, err := input.Config.TransformContext(ctx, messages)
		if err != nil {
			return nil, err
		}
		messages = next
	}

	llmMessages := []types.AgentMessage(messages)
	if input.Config.ConvertToLLM != nil {
		next, err := input.Config.ConvertToLLM(messages)
		if err != nil {
			return nil, err
		}
		llmMessages = next
	}

	apiKey := input.Config.APIKey
	if input.Config.GetAPIKey != nil {
		key, err := input.Config.GetAPIKey(input.Config.Model.ProviderID)
		if err == nil && key != "" {
			apiKey = key
		}
	}

	response, err := input.Config.StreamFn(ctx, input.Config.Model, &types.LLMContext{
		SystemPrompt: input.Context.SystemPrompt,
		Messages:     llmMessages,
		Tools:        toolDefinitions(input.Context.Tools),
	}, &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			Temperature:     input.Config.Temperature,
			MaxTokens:       input.Config.MaxTokens,
			APIKey:          apiKey,
			CacheRetention:  input.Config.CacheRetention,
			SessionID:       input.Config.SessionID,
			Headers:         input.Config.Headers,
			MaxRetryDelayMs: input.Config.MaxRetryDelayMs,
		},
		Reasoning:       input.Config.Reasoning,
		ThinkingBudgets: input.Config.ThinkingBudgets,
	})
	if err != nil {
		return nil, err
	}
	return collectAssistantMessage(response, input.Events)
}

func collectAssistantMessage(response types.EventStream, events *EventStream) (*types.AssistantMessage, error) {
	addedStart := false
	for event := range response.Events() {
		switch event.Type {
		case types.EventStart:
			if event.Partial != nil {
				addedStart = true
				events.Push(Event{Type: EventTypeMessageStart, Message: *event.Partial})
			}
		case types.EventTextStart, types.EventTextDelta, types.EventTextEnd,
			types.EventThinkingStart, types.EventThinkingDelta, types.EventThinkingEnd,
			types.EventToolCallStart, types.EventToolCallDelta, types.EventToolCallEnd:
			if event.Partial != nil {
				events.Push(Event{Type: EventTypeMessageUpdate, Message: *event.Partial, StreamEvent: &event})
			}
		case types.EventDone, types.EventError:
			finalMessage, err := response.Result()
			if err != nil {
				return nil, err
			}
			if finalMessage == nil {
				return nil, fmt.Errorf("missing final message")
			}
			if !addedStart {
				events.Push(Event{Type: EventTypeMessageStart, Message: *finalMessage})
			}
			events.Push(Event{Type: EventTypeMessageEnd, Message: *finalMessage})
			return finalMessage, nil
		}
	}

	finalMessage, err := response.Result()
	if err != nil {
		return nil, err
	}
	if finalMessage == nil {
		return nil, fmt.Errorf("missing final message")
	}
	return finalMessage, nil
}

func sleepBeforeRetry(ctx context.Context, attempt int, maxDelayMs int) error {
	baseDelay := float64(defaultBaseDelayMs) * math.Pow(2, float64(attempt-1))
	if baseDelay > float64(maxDelayMs) {
		baseDelay = float64(maxDelayMs)
	}
	jitter := rand.Float64() * float64(defaultRetryJitterMs)
	delay := time.Duration(baseDelay+jitter) * time.Millisecond

	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(net.Error); ok {
		return true
	}
	lower := strings.ToLower(err.Error())
	for _, pattern := range []string{
		"429", "rate limit", "too many requests",
		"500", "internal server error",
		"502", "bad gateway",
		"503", "service unavailable", "overloaded",
		"504", "gateway timeout",
		"connection refused", "connection reset",
		"eof", "broken pipe",
		"timeout", "timed out",
		"temporary", "unavailable",
	} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}
