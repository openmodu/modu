package openai

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/types"
)

const (
	lmStudioBaseURL = "http://192.168.5.149:1234/v1"
	lmStudioModel   = "qwen/qwen3.5-35b-a3b"

	lmStudioTimeout = 3 * time.Minute
)

func newLMStudioProvider() providers.Provider {
	return New("lmstudio",
		WithBaseURL(lmStudioBaseURL),
	)
}

func TestLMStudio_Chat(t *testing.T) {
	p := newLMStudioProvider()

	ctx, cancel := context.WithTimeout(context.Background(), lmStudioTimeout)
	defer cancel()

	resp, err := p.Chat(ctx, &providers.ChatRequest{
		Model: lmStudioModel,
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

// TestLMStudio_Stream verifies streaming output for a thinking model.
func TestLMStudio_Stream(t *testing.T) {
	p := newLMStudioProvider()

	ctx, cancel := context.WithTimeout(context.Background(), lmStudioTimeout)
	defer cancel()

	stream, err := p.Stream(ctx, &providers.ChatRequest{
		Model: lmStudioModel,
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
