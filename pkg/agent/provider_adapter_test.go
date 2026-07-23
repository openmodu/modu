package agent

import (
	"encoding/json"
	"testing"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

func TestBuildChatRequestEncodesImageBlocksAsOpenAIDataURLs(t *testing.T) {
	req := buildChatRequest(&types.Model{ID: "test-model"}, &types.LLMContext{
		Messages: []types.AgentMessage{
			types.UserMessage{Role: types.RoleUser, Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "inspect"},
				&types.ImageContent{Type: "image", MimeType: "image/png", Data: "cG5n"},
			}},
		},
	}, &types.SimpleStreamOptions{})

	raw, err := json.Marshal(req.Messages[0].Content)
	if err != nil {
		t.Fatal(err)
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].Type != "text" || parts[0].Text != "inspect" {
		t.Fatalf("parts = %#v", parts)
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL.URL != "data:image/png;base64,cG5n" {
		t.Fatalf("image part = %#v", parts[1])
	}
}

func TestBuildChatRequestDropsUnknownToolCalls(t *testing.T) {
	req := buildChatRequest(&types.Model{ID: "test-model"}, &types.LLMContext{
		Tools: []types.ToolDefinition{{Name: "read"}},
		Messages: []types.AgentMessage{
			types.UserMessage{Role: types.RoleUser, Content: "commit it"},
			types.AssistantMessage{Role: types.RoleAssistant, Content: []types.ContentBlock{
				&types.ToolCallContent{Type: "toolCall", ID: "confirm-1", Name: "confirm", Arguments: map[string]any{"message": "确认提交？"}},
			}},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "confirm-1",
				ToolName:   "confirm",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "Tool not found"}},
				IsError:    true,
			},
			types.UserMessage{Role: types.RoleUser, Content: "confirmed"},
		},
	}, &types.SimpleStreamOptions{})

	if len(req.Messages) != 2 {
		t.Fatalf("expected unknown tool call chain to be dropped, got %#v", req.Messages)
	}
	for _, msg := range req.Messages {
		if msg.Role == providers.RoleTool || len(msg.ToolCalls) > 0 {
			t.Fatalf("expected no unknown tool message in request, got %#v", req.Messages)
		}
	}
}

func TestBuildChatRequestDropsOrphanToolResults(t *testing.T) {
	req := buildChatRequest(&types.Model{ID: "test-model"}, &types.LLMContext{
		Tools: []types.ToolDefinition{{Name: "read"}},
		Messages: []types.AgentMessage{
			types.UserMessage{Role: types.RoleUser, Content: "one"},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "read-1",
				ToolName:   "read",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "file"}},
			},
		},
	}, &types.SimpleStreamOptions{})

	if len(req.Messages) != 1 {
		t.Fatalf("expected orphan tool result to be dropped, got %#v", req.Messages)
	}
	if req.Messages[0].Role != providers.RoleUser {
		t.Fatalf("expected user message only, got %#v", req.Messages)
	}
}

func TestBuildChatRequestKeepsOnlyCurrentTurnThinking(t *testing.T) {
	req := buildChatRequest(&types.Model{ID: "test-model"}, &types.LLMContext{
		Messages: []types.AgentMessage{
			types.UserMessage{Role: types.RoleUser, Content: "q1"},
			types.AssistantMessage{Role: types.RoleAssistant, Content: []types.ContentBlock{
				&types.ThinkingContent{Type: "thinking", Thinking: "old reasoning"},
				&types.TextContent{Type: "text", Text: "answer 1"},
			}},
			types.UserMessage{Role: types.RoleUser, Content: "q2"},
			types.AssistantMessage{Role: types.RoleAssistant, Content: []types.ContentBlock{
				&types.ThinkingContent{Type: "thinking", Thinking: "current reasoning"},
				&types.TextContent{Type: "text", Text: "answer 2"},
			}},
		},
	}, &types.SimpleStreamOptions{})

	var firstAssistant, lastAssistant *providers.Message
	for i := range req.Messages {
		if req.Messages[i].Role != providers.RoleAssistant {
			continue
		}
		if firstAssistant == nil {
			firstAssistant = &req.Messages[i]
		}
		lastAssistant = &req.Messages[i]
	}
	if firstAssistant == nil || lastAssistant == nil || firstAssistant == lastAssistant {
		t.Fatalf("expected two assistant messages, got %#v", req.Messages)
	}
	if firstAssistant.ReasoningContent != "" {
		t.Fatalf("expected historical thinking to be dropped, got %q", firstAssistant.ReasoningContent)
	}
	if lastAssistant.ReasoningContent != "current reasoning" {
		t.Fatalf("expected current-turn thinking to be kept, got %q", lastAssistant.ReasoningContent)
	}
}

func TestBuildChatRequestKeepsValidToolCallChain(t *testing.T) {
	req := buildChatRequest(&types.Model{ID: "test-model"}, &types.LLMContext{
		Tools: []types.ToolDefinition{{Name: "read"}},
		Messages: []types.AgentMessage{
			types.AssistantMessage{Role: types.RoleAssistant, Content: []types.ContentBlock{
				&types.ToolCallContent{Type: "toolCall", ID: "read-1", Name: "read", Arguments: map[string]any{"path": "README.md"}},
			}},
			types.ToolResultMessage{
				Role:       types.RoleToolResult,
				ToolCallID: "read-1",
				ToolName:   "read",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "content"}},
			},
		},
	}, &types.SimpleStreamOptions{})

	if len(req.Messages) != 2 {
		t.Fatalf("expected valid tool call chain, got %#v", req.Messages)
	}
	if len(req.Messages[0].ToolCalls) != 1 || req.Messages[1].Role != providers.RoleTool {
		t.Fatalf("expected assistant tool call plus tool result, got %#v", req.Messages)
	}
}
