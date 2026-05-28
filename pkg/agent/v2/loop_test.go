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
	emitEvent(input.Events, Event{Type: EventTypeMessageStart, Message: *msg})
	emitEvent(input.Events, Event{Type: EventTypeMessageEnd, Message: *msg})
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
		Options: LLMOptions{
			Model:    model,
			StreamFn: streamFn,
		},
		Events: events,
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
		Options: LLMOptions{
			Model:           model,
			StreamFn:        streamFn,
			MaxRetryDelayMs: 10,
		},
		Events: events,
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
		Options: LLMOptions{
			Model:    model,
			StreamFn: streamFn,
		},
		Events: events,
	})
	if err == nil {
		t.Fatal("expected error for permanent failure")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("permanent errors should not retry, got %d attempts", atomic.LoadInt32(&attempts))
	}
}

func TestV2DefaultLLMUsesStreamDefaultWhenStreamFnMissing(t *testing.T) {
	events := NewEventStream()
	drain(events)
	defer events.Close()

	_, err := (DefaultLLM{}).Complete(context.Background(), LLMInput{
		Context: AgentContext{},
		Options: LLMOptions{
			Model: &types.Model{ID: "mock", ProviderID: "missing-provider"},
		},
		Events: events,
	})
	if err == nil {
		t.Fatal("expected missing provider error")
	}
	if got := err.Error(); got != `no provider registered for "missing-provider"` {
		t.Fatalf("expected StreamDefault provider lookup error, got %q", got)
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

func TestV1CompatDefaultConvertToLLMFiltersProviderMessages(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	events := NewEventStream()
	drain(events)
	defer events.Close()

	var received []types.AgentMessage
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		received = llmCtx.Messages
		return assistantStream(model, "ok"), nil
	}

	loop := NewLoop(DefaultLLM{}, DefaultTools{})
	_, err := loop.Run(context.Background(), LoopInput{
		Prompts: []AgentMessage{types.UserMessage{Role: RoleUser, Content: "hello", Timestamp: time.Now().UnixMilli()}},
		Context: AgentContext{Messages: []AgentMessage{struct{ Note string }{Note: "internal"}}},
		Config:  Config{Model: model, StreamFn: streamFn},
		Events:  events,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("expected only provider-compatible message, got %d", len(received))
	}
	if roleOf(received[0]) != RoleUser {
		t.Fatalf("expected user message, got role %q", roleOf(received[0]))
	}
}

func TestV1CompatToolArgumentsValidateAgainstSchema(t *testing.T) {
	tool := &schemaTool{}
	events := NewEventStream()
	drain(events)
	defer events.Close()

	output, err := (DefaultTools{}).Execute(context.Background(), ToolInput{
		Tools: []Tool{tool},
		Calls: []types.ToolCallContent{
			{Type: "toolCall", ID: "tool-1", Name: "schema_echo", Arguments: map[string]any{}},
		},
		Events: events,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.executed {
		t.Fatal("tool should not execute when arguments fail schema validation")
	}
	if len(output.Results) != 1 {
		t.Fatalf("expected one tool result, got %d", len(output.Results))
	}
	if !output.Results[0].IsError {
		t.Fatal("expected schema validation failure to produce an error tool result")
	}
}

func TestV1CompatAgentClearMessages(t *testing.T) {
	agent := NewAgent(Config{})
	agent.AppendMessage(types.UserMessage{Role: RoleUser, Content: "hello", Timestamp: time.Now().UnixMilli()})
	if len(agent.GetState().Messages) != 1 {
		t.Fatal("expected 1 message")
	}
	agent.ClearMessages()
	if len(agent.GetState().Messages) != 0 {
		t.Fatal("expected 0 messages after ClearMessages")
	}
}

func TestV1CompatAgentModeGetters(t *testing.T) {
	agent := NewAgent(Config{
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

func TestV1CompatAgentQueuedMessageCounts(t *testing.T) {
	agent := NewAgent(Config{})
	agent.Steer(types.UserMessage{Role: RoleUser, Content: "change direction"})
	agent.FollowUp(types.UserMessage{Role: RoleUser, Content: "after this"})
	agent.FollowUp(types.UserMessage{Role: RoleUser, Content: "then this"})

	steering, followUp := agent.QueuedMessageCounts()
	if steering != 1 || followUp != 2 {
		t.Fatalf("expected steering=1 followUp=2, got steering=%d followUp=%d", steering, followUp)
	}
	if got := agent.QueuedMessageCount(); got != 3 {
		t.Fatalf("expected total queued count 3, got %d", got)
	}
}

func TestV1CompatAgentQueuedMessagesAndDropLast(t *testing.T) {
	agent := NewAgent(Config{})
	agent.Steer(types.UserMessage{Role: RoleUser, Content: "change direction"})
	agent.FollowUp(types.UserMessage{Role: RoleUser, Content: "after this"})

	steering, followUp := agent.QueuedMessages()
	if len(steering) != 1 || len(followUp) != 1 {
		t.Fatalf("expected one steering and one follow-up, got %d and %d", len(steering), len(followUp))
	}
	steering[0] = types.UserMessage{Role: RoleUser, Content: "mutated"}
	steering, _ = agent.QueuedMessages()
	if got := steering[0].(types.UserMessage).Content; got != "change direction" {
		t.Fatalf("expected queued message copy, got %q", got)
	}

	kind, ok := agent.DropLastQueuedMessage()
	if !ok || kind != "follow-up" {
		t.Fatalf("expected to drop follow-up, got kind=%q ok=%v", kind, ok)
	}
	steeringCount, followUpCount := agent.QueuedMessageCounts()
	if steeringCount != 1 || followUpCount != 0 {
		t.Fatalf("expected steering=1 followUp=0, got steering=%d followUp=%d", steeringCount, followUpCount)
	}
	kind, ok = agent.DropLastQueuedMessage()
	if !ok || kind != "steer" {
		t.Fatalf("expected to drop steer, got kind=%q ok=%v", kind, ok)
	}
	if _, ok = agent.DropLastQueuedMessage(); ok {
		t.Fatal("expected empty queue drop to fail")
	}
}

func TestV1CompatAgentSessionIDGetterSetter(t *testing.T) {
	agent := NewAgent(Config{SessionID: "initial"})
	if agent.GetSessionID() != "initial" {
		t.Fatal("expected session ID 'initial'")
	}
	agent.SetSessionID("updated")
	if agent.GetSessionID() != "updated" {
		t.Fatal("expected session ID 'updated'")
	}
}

func TestV1CompatAgentThinkingBudgetsGetterSetter(t *testing.T) {
	agent := NewAgent(Config{})
	if agent.GetThinkingBudgets() != nil {
		t.Fatal("expected nil thinking budgets by default")
	}
	budgets := &types.ThinkingBudgets{Minimal: 100, Low: 500, Medium: 2000, High: 8000}
	agent.SetThinkingBudgets(budgets)
	if agent.GetThinkingBudgets() != budgets {
		t.Fatal("expected thinking budgets to be set")
	}
}

func TestV1CompatAgentMaxRetryDelayMsGetterSetter(t *testing.T) {
	agent := NewAgent(Config{MaxRetryDelayMs: 5000})
	if agent.GetMaxRetryDelayMs() != 5000 {
		t.Fatalf("expected 5000, got %d", agent.GetMaxRetryDelayMs())
	}
	agent.SetMaxRetryDelayMs(10000)
	if agent.GetMaxRetryDelayMs() != 10000 {
		t.Fatalf("expected 10000, got %d", agent.GetMaxRetryDelayMs())
	}
}

func TestV1CompatAgentStateSettersAndReset(t *testing.T) {
	model := &types.Model{ID: "mock", ProviderID: "openai"}
	tool := &fakeTool{}
	agent := NewAgent(Config{})
	agent.SetSystemPrompt("system")
	agent.SetModel(model)
	agent.SetThinkingLevel(ThinkingLevelHigh)
	agent.SetTools([]Tool{tool})
	agent.ReplaceMessages([]AgentMessage{types.UserMessage{Role: RoleUser, Content: "hello"}})
	agent.Steer(types.UserMessage{Role: RoleUser, Content: "steer"})
	agent.FollowUp(types.UserMessage{Role: RoleUser, Content: "follow"})

	state := agent.GetState()
	if state.SystemPrompt != "system" || state.Model != model || state.ThinkingLevel != ThinkingLevelHigh {
		t.Fatalf("state setters did not apply: %#v", state)
	}
	if len(state.Tools) != 1 || len(state.Messages) != 1 {
		t.Fatalf("expected tools and messages to be set, got tools=%d messages=%d", len(state.Tools), len(state.Messages))
	}
	agent.Reset()
	state = agent.GetState()
	if len(state.Messages) != 0 || state.IsStreaming || state.StreamMessage != nil || state.Error != "" {
		t.Fatalf("expected reset state, got %#v", state)
	}
	if agent.HasQueuedMessages() {
		t.Fatal("expected reset to clear queues")
	}
}

func TestV1CompatAgentContinueConsumesQueuedSteering(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	var received [][]types.AgentMessage
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		received = append(received, append([]types.AgentMessage{}, llmCtx.Messages...))
		return assistantStream(model, "ok"), nil
	}

	agent := NewAgent(Config{
		InitialState: &State{
			Model:            model,
			Messages:         []AgentMessage{assistantText("previous")},
			PendingToolCalls: map[string]struct{}{},
		},
		StreamFn: streamFn,
	})
	agent.Steer(types.UserMessage{Role: RoleUser, Content: "new direction"})

	err := agent.Continue(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.HasQueuedMessages() {
		t.Fatal("expected queued steering message to be consumed")
	}
	if len(received) != 1 {
		t.Fatalf("expected one LLM call, got %d", len(received))
	}
	last := received[0][len(received[0])-1]
	if roleOf(last) != RoleUser {
		t.Fatalf("expected queued steering user message to reach LLM, got role %q", roleOf(last))
	}
}

func TestV1CompatAgentPromptWithImages(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	var received []types.AgentMessage
	streamFn := func(ctx context.Context, _ *types.Model, llmCtx *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		received = llmCtx.Messages
		return assistantStream(model, "I see the image"), nil
	}

	agent := NewAgent(Config{
		InitialState: &State{
			Model:            model,
			ThinkingLevel:    ThinkingLevelOff,
			Tools:            []Tool{},
			Messages:         []AgentMessage{},
			PendingToolCalls: map[string]struct{}{},
		},
		StreamFn: streamFn,
	})

	err := agent.PromptWithImages(context.Background(), "What is this?", []types.ImageContent{
		{Type: "image", Data: "base64data", MimeType: "image/png"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(received) == 0 {
		t.Fatal("expected at least one received message")
	}
	state := agent.GetState()
	if len(state.Messages) < 2 {
		t.Fatalf("expected at least 2 messages (user + assistant), got %d", len(state.Messages))
	}
}

func TestV1CompatAgentErrorMessageAppendedOnFailure(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		return nil, fmt.Errorf("HTTP 401 Unauthorized")
	}

	agent := NewAgent(Config{
		InitialState: &State{
			Model:            model,
			ThinkingLevel:    ThinkingLevelOff,
			Tools:            []Tool{},
			Messages:         []AgentMessage{},
			PendingToolCalls: map[string]struct{}{},
		},
		StreamFn: streamFn,
	})

	err := agent.Prompt(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected prompt error")
	}
	state := agent.GetState()
	if state.Error == "" {
		t.Fatal("expected state error to be set")
	}
	if len(state.Messages) < 2 {
		t.Fatalf("expected user + error assistant messages, got %d", len(state.Messages))
	}
	last, ok := state.Messages[len(state.Messages)-1].(types.AssistantMessage)
	if !ok || last.ErrorMessage == "" {
		t.Fatalf("expected last message to be assistant error, got %T", state.Messages[len(state.Messages)-1])
	}
	if state.IsStreaming {
		t.Fatal("expected IsStreaming to be false after error")
	}
}

func TestV1CompatAgentInterruptsForToolApprovalAndResumes(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	tool := &fakeTool{}
	callIndex := 0
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			var msg *types.AssistantMessage
			if callIndex == 0 {
				msg = &types.AssistantMessage{
					Role:       RoleAssistant,
					StopReason: "toolUse",
					Content: []types.ContentBlock{
						&types.ToolCallContent{Type: "toolCall", ID: "tool-1", Name: "echo", Arguments: map[string]any{"value": "hello"}},
					},
					Timestamp: time.Now().UnixMilli(),
				}
			} else {
				msg = assistantText("done")
			}
			callIndex++
			stream.Push(types.StreamEvent{Type: types.EventDone, Reason: msg.StopReason, Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}
	agent := NewAgent(Config{
		InitialState: &State{
			Model:            model,
			Tools:            []Tool{tool},
			Messages:         []AgentMessage{},
			PendingToolCalls: map[string]struct{}{},
		},
		StreamFn:         streamFn,
		EnableInterrupts: true,
	})

	interrupted := make(chan *InterruptEvent, 1)
	agent.Subscribe(func(event Event) {
		if event.Type == EventTypeInterrupt {
			interrupted <- event.Interrupt
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- agent.Prompt(context.Background(), "go")
	}()

	interrupt := <-interrupted
	if interrupt == nil || interrupt.Reason != InterruptReasonToolApproval {
		t.Fatalf("expected tool approval interrupt, got %#v", interrupt)
	}
	if agent.GetStatus() != SessionStatusPaused {
		t.Fatalf("expected paused status, got %s", agent.GetStatus())
	}
	if !agent.Resume(ResumeDecision{Allow: true}) {
		t.Fatal("expected resume to succeed")
	}
	if err := <-done; err != nil {
		t.Fatalf("unexpected prompt error: %v", err)
	}
	if len(tool.executed) != 1 || tool.executed[0] != "hello" {
		t.Fatalf("expected approved tool to execute, got %#v", tool.executed)
	}
	if agent.GetStatus() != SessionStatusCompleted {
		t.Fatalf("expected completed status, got %s", agent.GetStatus())
	}
}

func TestV1CompatAgentInterruptsAtMaxStepsAndResumes(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	callIndex := 0
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			msg := assistantText(fmt.Sprintf("step-%d", callIndex))
			callIndex++
			stream.Push(types.StreamEvent{Type: types.EventDone, Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	}
	agent := NewAgent(Config{
		InitialState: &State{
			Model:            model,
			Messages:         []AgentMessage{},
			PendingToolCalls: map[string]struct{}{},
		},
		StreamFn:         streamFn,
		EnableInterrupts: true,
		MaxSteps:         1,
	})
	agent.FollowUp(types.UserMessage{Role: RoleUser, Content: "next"})

	interrupted := make(chan *InterruptEvent, 1)
	agent.Subscribe(func(event Event) {
		if event.Type == EventTypeInterrupt {
			interrupted <- event.Interrupt
		}
	})
	done := make(chan error, 1)
	go func() {
		done <- agent.Prompt(context.Background(), "start")
	}()

	interrupt := <-interrupted
	if interrupt == nil || interrupt.Reason != InterruptReasonMaxSteps {
		t.Fatalf("expected max steps interrupt, got %#v", interrupt)
	}
	if !agent.Resume(ResumeDecision{Allow: true}) {
		t.Fatal("expected resume to succeed")
	}
	if err := <-done; err != nil {
		t.Fatalf("unexpected prompt error: %v", err)
	}
	if callIndex != 2 {
		t.Fatalf("expected two LLM calls after max-step resume, got %d", callIndex)
	}
}

func TestV1CompatAgentAbortCancelsPrompt(t *testing.T) {
	model := &types.Model{ID: "mock", Api: "openai-responses", ProviderID: "openai"}
	started := make(chan struct{})
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	agent := NewAgent(Config{
		InitialState: &State{
			Model:            model,
			Messages:         []AgentMessage{},
			PendingToolCalls: map[string]struct{}{},
		},
		StreamFn: streamFn,
	})

	done := make(chan error, 1)
	go func() {
		done <- agent.Prompt(context.Background(), "hello")
	}()
	<-started
	agent.Abort()

	err := <-done
	if err == nil {
		t.Fatal("expected abort to return an error")
	}
	state := agent.GetState()
	if state.IsStreaming {
		t.Fatal("expected aborted agent to stop streaming")
	}
	if state.Error == "" {
		t.Fatal("expected abort to record state error")
	}
	last, ok := state.Messages[len(state.Messages)-1].(types.AssistantMessage)
	if !ok || last.StopReason != "aborted" {
		t.Fatalf("expected aborted assistant error message, got %#v", state.Messages[len(state.Messages)-1])
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

type schemaTool struct {
	executed bool
}

func (t *schemaTool) Name() string        { return "schema_echo" }
func (t *schemaTool) Label() string       { return "Schema Echo" }
func (t *schemaTool) Description() string { return "Echo tool with schema" }
func (t *schemaTool) Parameters() any {
	return map[string]any{
		"type":     "object",
		"required": []any{"value"},
		"properties": map[string]any{
			"value": map[string]any{"type": "string"},
		},
	}
}
func (t *schemaTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate ToolUpdateCallback) (ToolResult, error) {
	t.executed = true
	return ToolResult{Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}}}, nil
}
