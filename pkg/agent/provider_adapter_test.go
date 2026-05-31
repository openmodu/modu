package agent

import (
	"testing"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

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
