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

// --- New tests for added functionality ---

func TestAgentClearMessages(t *testing.T) {
	agent := NewAgent(AgentOptions{})
	agent.AppendMessage(llm.UserMessage{Role: "user", Content: "hello", Timestamp: time.Now().UnixMilli()})
	if len(agent.GetState().Messages) != 1 {
		t.Fatal("expected 1 message")
	}
	agent.ClearMessages()
	if len(agent.GetState().Messages) != 0 {
		t.Fatal("expected 0 messages after ClearMessages")
	}
}

func TestAgentModeGetters(t *testing.T) {
	agent := NewAgent(AgentOptions{
		SteeringMode: ExecutionModeAll,
		FollowUpMode: ExecutionModeOneAtATime,
	})
	if agent.GetSteeringMode() != ExecutionModeAll {
		t.Fatalf("expected steering mode 'all', got %s", agent.GetSteeringMode())
	}
	if agent.GetFollowUpMode() != ExecutionModeOneAtATime {
		t.Fatalf("expected follow-up mode 'one-at-a-time', got %s", agent.GetFollowUpMode())
	}
}

func TestAgentSessionIDGetterSetter(t *testing.T) {
	agent := NewAgent(AgentOptions{SessionID: "initial"})
	if agent.GetSessionID() != "initial" {
		t.Fatal("expected session ID 'initial'")
	}
	agent.SetSessionID("updated")
	if agent.GetSessionID() != "updated" {
		t.Fatal("expected session ID 'updated'")
	}
}

func TestAgentTransportGetterSetter(t *testing.T) {
	agent := NewAgent(AgentOptions{})
	// Default should be SSE
	if agent.GetTransport() != llm.TransportSSE {
		t.Fatalf("expected default transport 'sse', got %s", agent.GetTransport())
	}
	agent.SetTransport(llm.TransportWebSocket)
	if agent.GetTransport() != llm.TransportWebSocket {
		t.Fatalf("expected transport 'websocket', got %s", agent.GetTransport())
	}
}

func TestAgentThinkingBudgetsGetterSetter(t *testing.T) {
	agent := NewAgent(AgentOptions{})
	if agent.GetThinkingBudgets() != nil {
		t.Fatal("expected nil thinking budgets by default")
	}
	budgets := &llm.ThinkingBudgets{Minimal: 100, Low: 500, Medium: 2000, High: 8000}
	agent.SetThinkingBudgets(budgets)
	if agent.GetThinkingBudgets() != budgets {
		t.Fatal("expected thinking budgets to be set")
	}
}

func TestAgentMaxRetryDelayMsGetterSetter(t *testing.T) {
	agent := NewAgent(AgentOptions{MaxRetryDelayMs: 5000})
	if agent.GetMaxRetryDelayMs() != 5000 {
		t.Fatalf("expected 5000, got %d", agent.GetMaxRetryDelayMs())
	}
	agent.SetMaxRetryDelayMs(10000)
	if agent.GetMaxRetryDelayMs() != 10000 {
		t.Fatalf("expected 10000, got %d", agent.GetMaxRetryDelayMs())
	}
}

func TestAgentErrorMessageAppendedOnFailure(t *testing.T) {
	model := &llm.Model{ID: "mock", Api: "openai-responses", Provider: "openai"}
	streamFn := func(_ *llm.Model, _ *llm.Context, _ *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
		return nil, fmt.Errorf("HTTP 401 Unauthorized")
	}

	agent := NewAgent(AgentOptions{
		InitialState: &AgentState{
			Model:            model,
			ThinkingLevel:    ThinkingLevelOff,
			Tools:            []AgentTool{},
			Messages:         []AgentMessage{},
			PendingToolCalls: map[string]struct{}{},
		},
		StreamFn: streamFn,
	})

	var events []AgentEvent
	agent.Subscribe(func(e AgentEvent) {
		events = append(events, e)
	})

	err := agent.Prompt(context.Background(), "hello")
	// Prompt may or may not return error depending on how the loop terminates
	_ = err

	state := agent.GetState()
	// The agent_end event should have been emitted
	var gotAgentEnd bool
	for _, e := range events {
		if e.Type == EventTypeAgentEnd {
			gotAgentEnd = true
		}
	}
	if !gotAgentEnd {
		t.Fatal("expected agent_end event")
	}
	// Messages should include at least the user message
	if len(state.Messages) < 1 {
		t.Fatal("expected at least one message")
	}
	// IsStreaming should be false
	if state.IsStreaming {
		t.Fatal("expected IsStreaming to be false after error")
	}
}

func TestAgentPromptWithImages(t *testing.T) {
	model := &llm.Model{ID: "mock", Api: "openai-responses", Provider: "openai"}

	var receivedMessages []llm.Message
	streamFn := func(_ *llm.Model, ctx *llm.Context, _ *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
		receivedMessages = ctx.Messages
		stream := utils.NewEventStream()
		go func() {
			msg := &llm.AssistantMessage{
				Role: "assistant", Api: model.Api, Provider: model.Provider, Model: model.ID,
				StopReason: "stop",
				Content:    []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "I see the image"}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(llm.AssistantMessageEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Close()
		}()
		return stream, nil
	}

	agent := NewAgent(AgentOptions{
		InitialState: &AgentState{
			Model:            model,
			ThinkingLevel:    ThinkingLevelOff,
			Tools:            []AgentTool{},
			Messages:         []AgentMessage{},
			PendingToolCalls: map[string]struct{}{},
		},
		StreamFn: streamFn,
	})

	err := agent.PromptWithImages(context.Background(), "What is this?", []llm.ImageContent{
		{Type: "image", Data: "base64data", MimeType: "image/png"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(receivedMessages) == 0 {
		t.Fatal("expected at least one received message")
	}

	state := agent.GetState()
	if len(state.Messages) < 2 {
		t.Fatalf("expected at least 2 messages (user + assistant), got %d", len(state.Messages))
	}
}

func TestHasNonEmptyContent(t *testing.T) {
	tests := []struct {
		name     string
		msg      llm.AssistantMessage
		expected bool
	}{
		{
			name: "empty text",
			msg: llm.AssistantMessage{
				Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: ""}},
			},
			expected: false,
		},
		{
			name: "whitespace text",
			msg: llm.AssistantMessage{
				Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "   "}},
			},
			expected: false,
		},
		{
			name: "non-empty text",
			msg: llm.AssistantMessage{
				Content: []llm.ContentBlock{&llm.TextContent{Type: "text", Text: "hello"}},
			},
			expected: true,
		},
		{
			name: "non-empty thinking",
			msg: llm.AssistantMessage{
				Content: []llm.ContentBlock{&llm.ThinkingContent{Type: "thinking", Thinking: "let me think"}},
			},
			expected: true,
		},
		{
			name: "tool call with name",
			msg: llm.AssistantMessage{
				Content: []llm.ContentBlock{&llm.ToolCall{Type: "toolCall", Name: "read", ID: "1"}},
			},
			expected: true,
		},
		{
			name:     "empty content slice",
			msg:      llm.AssistantMessage{Content: []llm.ContentBlock{}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasNonEmptyContent(tt.msg)
			if got != tt.expected {
				t.Errorf("hasNonEmptyContent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestProcessProxyEvent(t *testing.T) {
	partial := &llm.AssistantMessage{
		Role:    "assistant",
		Content: []llm.ContentBlock{},
	}

	// Test "start" event
	event := processProxyEvent(&ProxyAssistantMessageEvent{Type: "start"}, partial)
	if event == nil || event.Type != "start" {
		t.Fatal("expected start event")
	}
	if event.Partial != partial {
		t.Fatal("expected partial to be set")
	}

	// Test "text_start" event
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "text_start", ContentIndex: 0}, partial)
	if event == nil || event.Type != "text_start" {
		t.Fatal("expected text_start event")
	}
	if len(partial.Content) != 1 {
		t.Fatal("expected 1 content block")
	}

	// Test "text_delta" event
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: "hello"}, partial)
	if event == nil || event.Type != "text_delta" {
		t.Fatal("expected text_delta event")
	}
	tc, ok := partial.Content[0].(*llm.TextContent)
	if !ok || tc.Text != "hello" {
		t.Fatalf("expected text 'hello', got %v", partial.Content[0])
	}

	// Test "text_delta" accumulates
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "text_delta", ContentIndex: 0, Delta: " world"}, partial)
	if event == nil {
		t.Fatal("expected event")
	}
	tc, _ = partial.Content[0].(*llm.TextContent)
	if tc.Text != "hello world" {
		t.Fatalf("expected text 'hello world', got %s", tc.Text)
	}

	// Test "text_end" event
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "text_end", ContentIndex: 0, ContentSignature: "sig123"}, partial)
	if event == nil || event.Type != "text_end" {
		t.Fatal("expected text_end event")
	}
	tc, _ = partial.Content[0].(*llm.TextContent)
	if tc.TextSignature != "sig123" {
		t.Fatalf("expected signature 'sig123', got %s", tc.TextSignature)
	}

	// Test "done" event
	usage := &llm.Usage{Input: 10, Output: 20}
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "done", Reason: "stop", Usage: usage}, partial)
	if event == nil || event.Type != "done" {
		t.Fatal("expected done event")
	}
	if partial.Usage.Input != 10 || partial.Usage.Output != 20 {
		t.Fatal("expected usage to be set")
	}

	// Test "error" event
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "error", Reason: "error", ErrorMessage: "something failed"}, partial)
	if event == nil || event.Type != "error" {
		t.Fatal("expected error event")
	}
	if partial.ErrorMessage != "something failed" {
		t.Fatalf("expected error message, got %s", partial.ErrorMessage)
	}

	// Test unknown event type
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "unknown_type"}, partial)
	if event != nil {
		t.Fatal("expected nil for unknown event type")
	}
}

func TestProcessProxyEventThinking(t *testing.T) {
	partial := &llm.AssistantMessage{
		Role:    "assistant",
		Content: []llm.ContentBlock{},
	}

	// thinking_start
	event := processProxyEvent(&ProxyAssistantMessageEvent{Type: "thinking_start", ContentIndex: 0}, partial)
	if event == nil || event.Type != "thinking_start" {
		t.Fatal("expected thinking_start")
	}

	// thinking_delta
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "thinking_delta", ContentIndex: 0, Delta: "hmm"}, partial)
	if event == nil {
		t.Fatal("expected event")
	}
	tc, ok := partial.Content[0].(*llm.ThinkingContent)
	if !ok || tc.Thinking != "hmm" {
		t.Fatal("expected thinking 'hmm'")
	}

	// thinking_end
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "thinking_end", ContentIndex: 0, ContentSignature: "tsig"}, partial)
	if event == nil || event.Type != "thinking_end" {
		t.Fatal("expected thinking_end")
	}
	tc, _ = partial.Content[0].(*llm.ThinkingContent)
	if tc.ThinkingSignature != "tsig" {
		t.Fatalf("expected signature 'tsig', got %s", tc.ThinkingSignature)
	}
}

func TestProcessProxyEventToolCall(t *testing.T) {
	partial := &llm.AssistantMessage{
		Role:    "assistant",
		Content: []llm.ContentBlock{},
	}

	// toolcall_start
	event := processProxyEvent(&ProxyAssistantMessageEvent{
		Type: "toolcall_start", ContentIndex: 0, ID: "tc1", ToolName: "read_file",
	}, partial)
	if event == nil || event.Type != "toolcall_start" {
		t.Fatal("expected toolcall_start")
	}
	tc, ok := partial.Content[0].(*llm.ToolCall)
	if !ok || tc.Name != "read_file" || tc.ID != "tc1" {
		t.Fatal("expected tool call with correct name and ID")
	}

	// toolcall_delta
	event = processProxyEvent(&ProxyAssistantMessageEvent{
		Type: "toolcall_delta", ContentIndex: 0, Delta: `{"path":`,
	}, partial)
	if event == nil || event.Type != "toolcall_delta" {
		t.Fatal("expected toolcall_delta")
	}

	// toolcall_end
	event = processProxyEvent(&ProxyAssistantMessageEvent{Type: "toolcall_end", ContentIndex: 0}, partial)
	if event == nil || event.Type != "toolcall_end" {
		t.Fatal("expected toolcall_end")
	}
	if event.ToolCall == nil {
		t.Fatal("expected tool call in event")
	}
}
