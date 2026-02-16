package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/llm/utils"
)

type mockTool struct {
	executed []string
}

func (t *mockTool) Name() string        { return "echo" }
func (t *mockTool) Label() string       { return "Echo" }
func (t *mockTool) Description() string { return "Echo tool" }
func (t *mockTool) Parameters() any     { return nil }
func (t *mockTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate AgentToolUpdateCallback) (AgentToolResult, error) {
	value, _ := args["value"].(string)
	t.executed = append(t.executed, value)
	onUpdate(AgentToolResult{
		Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "partial"}},
		Details: map[string]any{"value": value},
	})
	return AgentToolResult{
		Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "echoed: " + value}},
		Details: map[string]any{"value": value},
	}, nil
}

func TestAgentLoopBasic(t *testing.T) {
	model := &llm.Model{ID: "mock", Api: "openai-responses", Provider: "openai"}
	user := llm.UserMessage{Role: "user", Content: "Hello", Timestamp: time.Now().UnixMilli()}

	streamFn := func(_ *llm.Model, _ *llm.Context, _ *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
		stream := utils.NewEventStream()
		go func() {
			msg := &llm.AssistantMessage{
				Role:       "assistant",
				Api:        model.Api,
				Provider:   model.Provider,
				Model:      model.ID,
				Usage:      llm.Usage{},
				StopReason: "stop",
				Content:    []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "Hi"}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(llm.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Close()
		}()
		return stream, nil
	}

	stream := AgentLoop([]AgentMessage{user}, AgentContext{SystemPrompt: "", Messages: []AgentMessage{}}, AgentLoopConfig{
		Model:        model,
		ConvertToLlm: defaultConvertToLlm,
	}, context.Background(), streamFn)

	var gotEnd bool
	for event := range stream.Events() {
		t.Logf("event=%s", event.Type)
		if event.Type == EventTypeAgentEnd {
			gotEnd = true
		}
	}
	if !gotEnd {
		t.Fatalf("expected agent_end")
	}
}

func TestAgentLoopToolCalls(t *testing.T) {
	model := &llm.Model{ID: "mock", Api: "openai-responses", Provider: "openai"}
	tool := &mockTool{}
	callIndex := 0

	streamFn := func(_ *llm.Model, _ *llm.Context, _ *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
		stream := utils.NewEventStream()
		go func() {
			if callIndex == 0 {
				msg := &llm.AssistantMessage{
					Role:       "assistant",
					Api:        model.Api,
					Provider:   model.Provider,
					Model:      model.ID,
					Usage:      llm.Usage{},
					StopReason: "toolUse",
					Content:    []llm.ContentBlock{llm.ToolCall{Type: "toolCall", ID: "tool-1", Name: "echo", Arguments: map[string]any{"value": "hello"}}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(llm.AssistantMessageEvent{Type: "done", Reason: "toolUse", Message: msg})
			} else {
				msg := &llm.AssistantMessage{
					Role:       "assistant",
					Api:        model.Api,
					Provider:   model.Provider,
					Model:      model.ID,
					Usage:      llm.Usage{},
					StopReason: "stop",
					Content:    []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "done"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(llm.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			}
			callIndex++
			stream.Close()
		}()
		return stream, nil
	}

	stream := AgentLoop([]AgentMessage{llm.UserMessage{Role: "user", Content: "go", Timestamp: time.Now().UnixMilli()}}, AgentContext{
		SystemPrompt: "",
		Messages:     []AgentMessage{},
		Tools:        []AgentTool{tool},
	}, AgentLoopConfig{
		Model:        model,
		ConvertToLlm: defaultConvertToLlm,
	}, context.Background(), streamFn)

	for range stream.Events() {
		if len(tool.executed) > 0 {
			t.Logf("tool executed=%v", tool.executed)
		}
	}

	if len(tool.executed) != 1 || tool.executed[0] != "hello" {
		t.Fatalf("expected tool executed once")
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		err       error
		retryable bool
	}{
		{nil, false},
		{fmt.Errorf("connection refused"), true},
		{fmt.Errorf("HTTP 429 Too Many Requests"), true},
		{fmt.Errorf("HTTP 503 Service Unavailable"), true},
		{fmt.Errorf("server returned 502 bad gateway"), true},
		{fmt.Errorf("read: connection reset by peer"), true},
		{fmt.Errorf("request timed out"), true},
		{fmt.Errorf("unexpected EOF"), true},
		{fmt.Errorf("server overloaded"), true},
		// Permanent errors — should NOT retry
		{fmt.Errorf("HTTP 401 Unauthorized"), false},
		{fmt.Errorf("model not found"), false},
		{fmt.Errorf("invalid JSON schema"), false},
		{fmt.Errorf("missing required parameter"), false},
	}

	for _, tt := range tests {
		got := isRetryableError(tt.err)
		if got != tt.retryable {
			t.Errorf("isRetryableError(%q) = %v, want %v", tt.err, got, tt.retryable)
		}
	}
}

// drainStream starts a goroutine that consumes all events from a stream.
func drainStream(s *EventStream) {
	go func() {
		for range s.Events() {
		}
	}()
}

func TestStreamAssistantResponseWithRetry_Success(t *testing.T) {
	model := &llm.Model{ID: "mock", Api: "openai-responses", Provider: "openai"}

	streamFn := func(_ *llm.Model, _ *llm.Context, _ *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
		stream := utils.NewEventStream()
		go func() {
			msg := &llm.AssistantMessage{
				Role: "assistant", Api: model.Api, Provider: model.Provider, Model: model.ID,
				StopReason: "stop",
				Content:    []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "ok"}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(llm.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Close()
		}()
		return stream, nil
	}

	agentStream := NewEventStream()
	drainStream(agentStream)
	defer agentStream.Close()

	agentCtx := AgentContext{SystemPrompt: "", Messages: []AgentMessage{}, Tools: []AgentTool{}}
	cfg := AgentLoopConfig{Model: model, ConvertToLlm: defaultConvertToLlm}

	msg, err := streamAssistantResponseWithRetry(agentCtx, cfg, context.Background(), agentStream, streamFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.StopReason != "stop" {
		t.Fatalf("expected stop reason 'stop', got %s", msg.StopReason)
	}
}

func TestStreamAssistantResponseWithRetry_RetriesTransient(t *testing.T) {
	model := &llm.Model{ID: "mock", Api: "openai-responses", Provider: "openai"}

	var attempts int32
	streamFn := func(_ *llm.Model, _ *llm.Context, _ *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			// Transient error — streamFn itself fails, no events pushed to agentStream
			return nil, fmt.Errorf("HTTP 503 service unavailable")
		}
		stream := utils.NewEventStream()
		go func() {
			msg := &llm.AssistantMessage{
				Role: "assistant", Api: model.Api, Provider: model.Provider, Model: model.ID,
				StopReason: "stop",
				Content:    []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "recovered"}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(llm.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Close()
		}()
		return stream, nil
	}

	agentStream := NewEventStream()
	drainStream(agentStream)
	defer agentStream.Close()

	agentCtx := AgentContext{SystemPrompt: "", Messages: []AgentMessage{}, Tools: []AgentTool{}}
	cfg := AgentLoopConfig{
		Model:           model,
		ConvertToLlm:    defaultConvertToLlm,
		MaxRetryDelayMs: 50, // very fast retries for test
	}

	msg, err := streamAssistantResponseWithRetry(agentCtx, cfg, context.Background(), agentStream, streamFn)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected 3 attempts, got %d", atomic.LoadInt32(&attempts))
	}
	if msg.StopReason != "stop" {
		t.Fatalf("expected stop, got %s", msg.StopReason)
	}
}

func TestStreamAssistantResponseWithRetry_NoPermanentRetry(t *testing.T) {
	model := &llm.Model{ID: "mock", Api: "openai-responses", Provider: "openai"}

	var attempts int32
	streamFn := func(_ *llm.Model, _ *llm.Context, _ *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, fmt.Errorf("HTTP 401 Unauthorized")
	}

	agentStream := NewEventStream()
	drainStream(agentStream)
	defer agentStream.Close()

	agentCtx := AgentContext{SystemPrompt: "", Messages: []AgentMessage{}, Tools: []AgentTool{}}
	cfg := AgentLoopConfig{Model: model, ConvertToLlm: defaultConvertToLlm}

	_, err := streamAssistantResponseWithRetry(agentCtx, cfg, context.Background(), agentStream, streamFn)
	if err == nil {
		t.Fatal("expected error for permanent failure")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("permanent errors should not retry, got %d attempts", atomic.LoadInt32(&attempts))
	}
}
