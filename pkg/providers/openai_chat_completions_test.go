package providers

import (
	"context"
	"strings"
	"testing"
	"time"
)

const (
	lmStudioBaseURL = "http://192.168.5.149:1234/v1"
	lmStudioModel   = "qwen/qwen3.5-35b-a3b"

	lmStudioTimeout = 3 * time.Minute
)

func newLMStudioProvider() Provider {
	return NewOpenAIChatCompletionsProvider("lmstudio",
		WithBaseURL(lmStudioBaseURL),
	)
}

func TestLMStudio_Chat(t *testing.T) {
	p := newLMStudioProvider()

	ctx, cancel := context.WithTimeout(context.Background(), lmStudioTimeout)
	defer cancel()

	resp, err := p.Chat(ctx, &ChatRequest{
		Model: lmStudioModel,
		Messages: []Message{
			{Role: RoleUser, Content: "Reply with the single word: pong"},
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

// TestLMStudio_Stream verifies streaming output for a thinking model.
// qwen3.5-35b-a3b always produces a reasoning_content block before the final
// response, so both thinking and text events are expected.
func TestLMStudio_Stream(t *testing.T) {
	p := newLMStudioProvider()

	ctx, cancel := context.WithTimeout(context.Background(), lmStudioTimeout)
	defer cancel()

	stream, err := p.Stream(ctx, &ChatRequest{
		Model: lmStudioModel,
		Messages: []Message{
			{Role: RoleUser, Content: "What is 17 * 23?"},
		},
	})
	if err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	var textDeltas []string
	var sawThinkingStart, sawThinkingEnd, sawTextStart, sawTextEnd bool

	for event := range stream.Events() {
		switch event.Type {
		case EventThinkingStart:
			sawThinkingStart = true
		case EventThinkingEnd:
			sawThinkingEnd = true
			t.Logf("[thinking] %s", event.Content)
		case EventTextStart:
			sawTextStart = true
		case EventTextDelta:
			textDeltas = append(textDeltas, event.Delta)
		case EventTextEnd:
			sawTextEnd = true
		case EventDone:
			t.Logf("[done] finish_reason=%s", event.Reason)
		case EventError:
			t.Fatalf("stream error: %v", event.Err)
		}
	}

	resp, err := stream.Result()
	if err != nil {
		t.Fatalf("Result error: %v", err)
	}

	if !sawThinkingStart || !sawThinkingEnd {
		t.Error("expected thinking events from a thinking model")
	}
	if !sawTextStart || !sawTextEnd {
		t.Error("expected text events")
	}
	if resp.Message.Content == "" {
		t.Fatal("expected non-empty final content")
	}

	full := strings.Join(textDeltas, "")
	if full != resp.Message.Content {
		t.Errorf("accumulated text deltas do not match final content\ndeltas: %q\nfinal:  %q", full, resp.Message.Content)
	}
	t.Logf("response: %s", resp.Message.Content)
	t.Logf("usage: prompt=%d completion=%d total=%d",
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
}
