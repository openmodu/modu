package runtime

import (
	"reflect"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestMessageRoundTrip(t *testing.T) {
	original := []types.AgentMessage{
		types.UserMessage{Role: types.RoleUser, Content: "hello", Timestamp: 1},
		types.UserMessage{Role: types.RoleUser, Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "look"},
			&types.ImageContent{Type: "image", Data: "deadbeef", MimeType: "image/png"},
		}, Timestamp: 2},
		types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.ContentBlock{
				&types.ThinkingContent{Type: "thinking", Thinking: "hmm", ThinkingSignature: "sig"},
				&types.TextContent{Type: "text", Text: "answer"},
				&types.ToolCallContent{Type: "toolCall", ID: "t1", Name: "echo", Arguments: map[string]any{"value": "x"}},
			},
			ProviderID: "openai", Model: "mock", StopReason: "toolUse", Timestamp: 3,
		},
		types.ToolResultMessage{
			Role: types.RoleToolResult, ToolCallID: "t1", ToolName: "echo",
			Content:   []types.ContentBlock{&types.TextContent{Type: "text", Text: "echoed: x"}},
			Details:   map[string]any{"ok": true},
			IsError:   false,
			Timestamp: 4,
		},
	}

	envs, err := marshalMessages(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	restored, err := unmarshalMessages(envs)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(restored) != len(original) {
		t.Fatalf("expected %d messages, got %d", len(original), len(restored))
	}
	for i := range original {
		// Details survives as map[string]any either way; DeepEqual handles the
		// pointer content blocks by following them.
		if !reflect.DeepEqual(original[i], restored[i]) {
			t.Fatalf("message %d mismatch:\n have %#v\n want %#v", i, restored[i], original[i])
		}
	}
}

func TestMarshalRejectsUnknownMessage(t *testing.T) {
	if _, err := marshalMessage(struct{ X int }{X: 1}); err == nil {
		t.Fatal("expected error for unknown message type")
	}
}
