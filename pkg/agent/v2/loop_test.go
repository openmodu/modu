package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

type fakeLLM struct {
	messages []*types.AssistantMessage
	calls    int
}

func (f *fakeLLM) Complete(ctx context.Context, input LLMInput) (*types.AssistantMessage, error) {
	msg := f.messages[f.calls]
	f.calls++
	input.Events.Push(Event{Type: EventTypeMessageStart, Message: *msg})
	input.Events.Push(Event{Type: EventTypeMessageEnd, Message: *msg})
	return msg, nil
}

type fakeTool struct {
	executed []string
}

func (t *fakeTool) Name() string        { return "echo" }
func (t *fakeTool) Label() string       { return "Echo" }
func (t *fakeTool) Description() string { return "Echo tool" }
func (t *fakeTool) Parameters() any     { return nil }
func (t *fakeTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate ToolUpdateCallback) (ToolResult, error) {
	value, _ := args["value"].(string)
	t.executed = append(t.executed, value)
	onUpdate(ToolResult{Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "partial"}}})
	return ToolResult{Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "echoed: " + value}}}, nil
}

func TestLoopReturnsAssistantMessageWithoutTools(t *testing.T) {
	events := NewEventStream()
	drain(events)

	llm := &fakeLLM{messages: []*types.AssistantMessage{
		assistantText("done"),
	}}
	loop := NewLoop(llm, DefaultTools{})
	result, err := loop.Run(context.Background(), LoopInput{
		Prompts: []AgentMessage{types.UserMessage{Role: RoleUser, Content: "hello", Timestamp: time.Now().UnixMilli()}},
		Context: AgentContext{},
		Config:  Config{},
		Events:  events,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected user + assistant messages, got %d", len(result.Messages))
	}
	if llm.calls != 1 {
		t.Fatalf("expected one LLM call, got %d", llm.calls)
	}
}

func TestLoopExecutesToolAndContinues(t *testing.T) {
	events := NewEventStream()
	drain(events)

	tool := &fakeTool{}
	llm := &fakeLLM{messages: []*types.AssistantMessage{
		{
			Role:       RoleAssistant,
			StopReason: "toolUse",
			Content: []types.ContentBlock{
				&types.ToolCallContent{Type: "toolCall", ID: "tool-1", Name: "echo", Arguments: map[string]any{"value": "hello"}},
			},
			Timestamp: time.Now().UnixMilli(),
		},
		assistantText("finished"),
	}}

	loop := NewLoop(llm, DefaultTools{})
	result, err := loop.Run(context.Background(), LoopInput{
		Prompts: []AgentMessage{types.UserMessage{Role: RoleUser, Content: "go", Timestamp: time.Now().UnixMilli()}},
		Context: AgentContext{Tools: []Tool{tool}},
		Config:  Config{},
		Events:  events,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tool.executed) != 1 || tool.executed[0] != "hello" {
		t.Fatalf("expected tool to execute once, got %#v", tool.executed)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("expected user, assistant tool call, tool result, final assistant; got %d", len(result.Messages))
	}
	if llm.calls != 2 {
		t.Fatalf("expected two LLM calls, got %d", llm.calls)
	}
}

func TestV1CompatLoopBasic(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	user := types.UserMessage{Role: RoleUser, Content: "Hello", Timestamp: time.Now().UnixMilli()}

	events := NewEventStream()
	drain(events)
	defer events.Close()

	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			msg := &types.AssistantMessage{
				Role:       RoleAssistant,
				ProviderID: model.ProviderID,
				Model:      model.ID,
				Usage:      types.AgentUsage{},
				StopReason: "stop",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "Hi"}},
				Timestamp:  time.Now().UnixMilli(),
			}
			stream.Push(types.StreamEvent{Type: types.EventDone, Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}

	loop := NewLoop(DefaultLLM{}, DefaultTools{})
	result, err := loop.Run(context.Background(), LoopInput{
		Prompts: []AgentMessage{user},
		Context: AgentContext{SystemPrompt: "", Messages: []AgentMessage{}},
		Config:  Config{Model: model, StreamFn: streamFn},
		Events:  events,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected user + assistant messages, got %d", len(result.Messages))
	}
}

func TestV1CompatLoopToolCalls(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	tool := &fakeTool{}
	callIndex := 0

	events := NewEventStream()
	drain(events)
	defer events.Close()

	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			if callIndex == 0 {
				msg := &types.AssistantMessage{
					Role:       RoleAssistant,
					ProviderID: model.ProviderID,
					Model:      model.ID,
					Usage:      types.AgentUsage{},
					StopReason: "toolUse",
					Content: []types.ContentBlock{
						&types.ToolCallContent{Type: "toolCall", ID: "tool-1", Name: "echo", Arguments: map[string]any{"value": "hello"}},
					},
					Timestamp: time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: types.EventDone, Reason: "toolUse", Message: msg})
				stream.Resolve(msg, nil)
			} else {
				msg := &types.AssistantMessage{
					Role:       RoleAssistant,
					ProviderID: model.ProviderID,
					Model:      model.ID,
					Usage:      types.AgentUsage{},
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "done"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: types.EventDone, Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
			}
			callIndex++
			stream.Close()
		}()
		return stream, nil
	}

	loop := NewLoop(DefaultLLM{}, DefaultTools{})
	result, err := loop.Run(context.Background(), LoopInput{
		Prompts: []AgentMessage{types.UserMessage{Role: RoleUser, Content: "go", Timestamp: time.Now().UnixMilli()}},
		Context: AgentContext{SystemPrompt: "", Messages: []AgentMessage{}, Tools: []Tool{tool}},
		Config:  Config{Model: model, StreamFn: streamFn},
		Events:  events,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tool.executed) != 1 || tool.executed[0] != "hello" {
		t.Fatalf("expected tool executed once, got %#v", tool.executed)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("expected user, assistant tool call, tool result, final assistant; got %d", len(result.Messages))
	}
}

func TestV1CompatIsRetryableError(t *testing.T) {
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
		{fmt.Errorf("HTTP 401 Unauthorized"), false},
		{fmt.Errorf("model not found"), false},
		{fmt.Errorf("invalid JSON schema"), false},
		{fmt.Errorf("missing required parameter"), false},
	}

	for _, tt := range tests {
		if got := isRetryableError(tt.err); got != tt.retryable {
			t.Errorf("isRetryableError(%q) = %v, want %v", tt.err, got, tt.retryable)
		}
	}
}

func TestV1CompatCompleteWithRetrySuccess(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	events := NewEventStream()
	drain(events)
	defer events.Close()

	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		return assistantStream(model, "ok"), nil
	}

	msg, err := (DefaultLLM{}).Complete(context.Background(), LLMInput{
		Context: AgentContext{},
		Config:  Config{Model: model, StreamFn: streamFn},
		Events:  events,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.StopReason != "stop" {
		t.Fatalf("expected stop reason 'stop', got %s", msg.StopReason)
	}
}

func TestV1CompatCompleteWithRetryRetriesTransient(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	events := NewEventStream()
	drain(events)
	defer events.Close()

	var attempts int32
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			return nil, fmt.Errorf("HTTP 503 service unavailable")
		}
		return assistantStream(model, "recovered"), nil
	}

	msg, err := (DefaultLLM{}).Complete(context.Background(), LLMInput{
		Context: AgentContext{},
		Config:  Config{Model: model, StreamFn: streamFn, MaxRetryDelayMs: 10},
		Events:  events,
	})
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

func TestV1CompatCompleteWithRetryNoPermanentRetry(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	events := NewEventStream()
	drain(events)
	defer events.Close()

	var attempts int32
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, fmt.Errorf("HTTP 401 Unauthorized")
	}

	_, err := (DefaultLLM{}).Complete(context.Background(), LLMInput{
		Context: AgentContext{},
		Config:  Config{Model: model, StreamFn: streamFn},
		Events:  events,
	})
	if err == nil {
		t.Fatal("expected error for permanent failure")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("permanent errors should not retry, got %d attempts", atomic.LoadInt32(&attempts))
	}
}

func TestV1CompatPromptWithImagesPassesMessageToLLM(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	events := NewEventStream()
	drain(events)
	defer events.Close()

	var received []types.AgentMessage
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		received = llmCtx.Messages
		return assistantStream(model, "I see the image"), nil
	}

	content := []types.ContentBlock{
		&types.TextContent{Type: "text", Text: "What is this?"},
		&types.ImageContent{Type: "image", Data: "base64data", MimeType: "image/png"},
	}
	loop := NewLoop(DefaultLLM{}, DefaultTools{})
	_, err := loop.Run(context.Background(), LoopInput{
		Prompts: []AgentMessage{types.UserMessage{Role: RoleUser, Content: content, Timestamp: time.Now().UnixMilli()}},
		Context: AgentContext{},
		Config:  Config{Model: model, StreamFn: streamFn},
		Events:  events,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("expected one received message, got %d", len(received))
	}
	msg, ok := received[0].(types.UserMessage)
	if !ok {
		t.Fatalf("expected user message, got %T", received[0])
	}
	blocks, ok := msg.Content.([]types.ContentBlock)
	if !ok || len(blocks) != 2 {
		t.Fatalf("expected text + image content blocks, got %#v", msg.Content)
	}
}

func drain(events *EventStream) {
	go func() {
		for range events.Events() {
		}
	}()
}

func assistantText(text string) *types.AssistantMessage {
	return &types.AssistantMessage{
		Role:       RoleAssistant,
		StopReason: "stop",
		Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
		Timestamp:  time.Now().UnixMilli(),
	}
}

func assistantStream(model *types.Model, text string) types.EventStream {
	stream := types.NewEventStream()
	go func() {
		msg := &types.AssistantMessage{
			Role:       RoleAssistant,
			ProviderID: model.ProviderID,
			Model:      model.ID,
			StopReason: "stop",
			Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
			Timestamp:  time.Now().UnixMilli(),
		}
		stream.Push(types.StreamEvent{Type: types.EventDone, Reason: "stop", Message: msg})
		stream.Resolve(msg, nil)
		stream.Close()
	}()
	return stream
}
