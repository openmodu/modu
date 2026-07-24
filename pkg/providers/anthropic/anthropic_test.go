package anthropic

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

func TestReadSSEParsesUsageWithCache(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":900,"cache_creation_input_tokens":30,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}

`
	p := &anthropicProvider{}
	stream := types.NewEventStream()
	go p.readSSE(io.NopCloser(strings.NewReader(sse)), "claude-test", stream)
	go func() {
		for range stream.Events() {
		}
	}()

	msg, err := stream.Result()
	if err != nil {
		t.Fatalf("Result error: %v", err)
	}
	if msg.Usage.Input != 100 {
		t.Errorf("Input = %d, want 100", msg.Usage.Input)
	}
	if msg.Usage.CacheRead != 900 {
		t.Errorf("CacheRead = %d, want 900", msg.Usage.CacheRead)
	}
	if msg.Usage.CacheWrite != 30 {
		t.Errorf("CacheWrite = %d, want 30", msg.Usage.CacheWrite)
	}
	if msg.Usage.Output != 50 {
		t.Errorf("Output = %d, want 50 (final message_delta count)", msg.Usage.Output)
	}
	if msg.Usage.TotalTokens != 1080 {
		t.Errorf("TotalTokens = %d, want 1080", msg.Usage.TotalTokens)
	}
}

func TestBuildBodyConvertsMultimodalContentToAnthropicBlocks(t *testing.T) {
	provider := &anthropicProvider{}
	raw, err := provider.buildBody(&providers.ChatRequest{
		Messages: []providers.Message{{
			Role: providers.RoleUser,
			Content: []any{
				map[string]any{"type": "text", "text": "inspect"},
				map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": "data:image/png;base64,cG5n",
					},
				},
			},
		}},
	}, "claude-test", false)
	if err != nil {
		t.Fatal(err)
	}

	var body struct {
		Messages []struct {
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 1 || len(body.Messages[0].Content) != 2 {
		t.Fatalf("body = %s", raw)
	}
	if got := body.Messages[0].Content[0]; got.Type != "text" || got.Text != "inspect" {
		t.Fatalf("text block = %#v", got)
	}
	if got := body.Messages[0].Content[1]; got.Type != "image" || got.Source.Type != "base64" || got.Source.MediaType != "image/png" || got.Source.Data != "cG5n" {
		t.Fatalf("image block = %#v", got)
	}
}
