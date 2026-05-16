package openai

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

const (
	lmStudioTimeout = 3 * time.Minute
)

func newLMStudioProvider(t *testing.T) (providers.Provider, string) {
	t.Helper()
	baseURL := os.Getenv("LMSTUDIO_BASE_URL")
	model := os.Getenv("LMSTUDIO_MODEL")
	if baseURL == "" || model == "" {
		t.Skip("set LMSTUDIO_BASE_URL and LMSTUDIO_MODEL to run LM Studio integration tests")
	}
	return New("lmstudio",
		WithBaseURL(baseURL),
	), model
}

func TestLMStudio_Chat(t *testing.T) {
	p, model := newLMStudioProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), lmStudioTimeout)
	defer cancel()

	resp, err := p.Chat(ctx, &providers.ChatRequest{
		Model: model,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "Reply with the single word: pong"},
		},
	})
	if err != nil {
		t.Fatalf("Chat error: %v", err)
	}
	if resp.Message.Content == "" {
		t.Fatal("expected non-empty response content")
	}
	t.Logf("response: %s", resp.Message.Content)
	t.Logf("usage: prompt=%d completion=%d total=%d",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
}

func TestRequestWithoutReasoninglessAssistant(t *testing.T) {
	req := &providers.ChatRequest{
		Model: "test",
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "one"},
			{Role: providers.RoleAssistant, Content: "old answer"},
			{Role: providers.RoleUser, Content: "two"},
			{
				Role:             providers.RoleAssistant,
				Content:          "thinking answer",
				ReasoningContent: "reasoning",
			},
			{
				Role: providers.RoleAssistant,
				ToolCalls: []providers.ToolCall{{
					ID: "call-1", Type: "function",
					Function: providers.FuncCall{Name: "read", Arguments: "{}"},
				}},
			},
			{Role: providers.RoleTool, ToolCallID: "call-1", Content: "tool output"},
			{Role: providers.RoleUser, Content: "three"},
		},
	}

	got, changed := requestWithoutReasoninglessAssistant(req)
	if !changed {
		t.Fatal("expected request to change")
	}
	if len(got.Messages) != 4 {
		t.Fatalf("expected 4 messages after filtering, got %d: %#v", len(got.Messages), got.Messages)
	}
	if got.Messages[1].Role != providers.RoleUser || got.Messages[1].Content != "two" {
		t.Fatalf("expected user message preserved after old assistant, got %#v", got.Messages[1])
	}
	if got.Messages[2].ReasoningContent != "reasoning" {
		t.Fatalf("expected reasoning assistant preserved, got %#v", got.Messages[2])
	}
	if got.Messages[3].Content != "three" {
		t.Fatalf("expected trailing user preserved, got %#v", got.Messages[3])
	}
}

// TestLMStudio_Stream verifies streaming output for a thinking model.
func TestLMStudio_Stream(t *testing.T) {
	p, model := newLMStudioProvider(t)

	ctx, cancel := context.WithTimeout(context.Background(), lmStudioTimeout)
	defer cancel()

	stream, err := p.Stream(ctx, &providers.ChatRequest{
		Model: model,
		Messages: []providers.Message{
			{Role: providers.RoleUser, Content: "What is 17 * 23?"},
		},
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var textDeltas []string
	var sawThinkingStart, sawThinkingEnd, sawTextStart, sawTextEnd bool

	for event := range stream.Events() {
		switch event.Type {
		case types.EventThinkingStart:
			sawThinkingStart = true
		case types.EventThinkingEnd:
			sawThinkingEnd = true
		case types.EventTextStart:
			sawTextStart = true
		case types.EventTextDelta:
			textDeltas = append(textDeltas, event.Delta)
		case types.EventTextEnd:
			sawTextEnd = true
		case types.EventDone:
			t.Logf("[done] finish_reason=%s", event.Reason)
		case types.EventError:
			t.Fatalf("stream error: %v", event.Error)
		default:
			t.Logf("[other event] %s %+v", event.Type, event)
		}
	}

	resp, err := stream.Result()
	if err != nil {
		t.Fatalf("Result error: %v", err)
	}

	if sawThinkingStart || sawThinkingEnd {
		t.Log("Saw thinking events")
	} else {
		t.Log("No thinking events seen, provider likely streamed reasoning as plain text")
	}

	if !sawTextStart || !sawTextEnd {
		t.Error("expected text events")
	}

	finalContent := ""
	for _, b := range resp.Content {
		if tc, ok := b.(*types.TextContent); ok {
			finalContent += tc.Text
		}
	}

	if finalContent == "" {
		t.Fatal("expected non-empty final content")
	}

	full := strings.Join(textDeltas, "")
	if full != finalContent {
		t.Errorf("accumulated text deltas do not match final content\ndeltas: %q\nfinal:  %q", full, finalContent)
	}
	t.Logf("response: %s", finalContent)
	t.Logf("usage: prompt=%d completion=%d total=%d",
		resp.Usage.Input, resp.Usage.Output, resp.Usage.TotalTokens)
}
