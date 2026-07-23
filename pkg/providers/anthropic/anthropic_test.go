package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/openmodu/modu/pkg/providers"
)

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
