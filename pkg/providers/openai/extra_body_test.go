package openai

import (
	"encoding/json"
	"testing"

	"github.com/openmodu/modu/pkg/providers"
)

func TestBuildBodyMergesExtraBody(t *testing.T) {
	p := New("deepseek",
		WithBaseURL("https://api.deepseek.com/v1"),
		WithExtraBody(map[string]any{
			"thinking": map[string]any{"type": "disabled"},
			// A structural key must not be able to corrupt the request.
			"messages": "hijacked",
		}),
	).(*openAIProvider)

	raw, err := p.buildBody(&providers.ChatRequest{
		Model:    "deepseek-v4-flash",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	}, false)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	thinking, ok := body["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("extra body thinking not merged: %v", body["thinking"])
	}
	// messages must remain the real messages, not the extra-body override.
	if _, hijacked := body["messages"].(string); hijacked {
		t.Fatalf("extra body clobbered structural key 'messages': %v", body["messages"])
	}
}

func TestBuildBodyWithoutExtraBodyIsUnchanged(t *testing.T) {
	p := New("x", WithBaseURL("http://localhost")).(*openAIProvider)
	raw, err := p.buildBody(&providers.ChatRequest{
		Model:    "m",
		Messages: []providers.Message{{Role: providers.RoleUser, Content: "hi"}},
	}, false)
	if err != nil {
		t.Fatalf("buildBody: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := body["thinking"]; ok {
		t.Fatalf("unexpected thinking field without extra body")
	}
}
