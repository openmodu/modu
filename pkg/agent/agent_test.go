package agent

import (
	"context"
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
