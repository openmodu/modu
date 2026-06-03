package evals

import (
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestLastAssistantText(t *testing.T) {
	messages := []types.AgentMessage{
		types.UserMessage{Role: types.RoleUser, Content: "hello"},
		&types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "first"},
			},
		},
		types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "second"},
				&types.TextContent{Type: "text", Text: "line"},
			},
		},
	}

	if got := LastAssistantText(messages); got != "second\nline" {
		t.Fatalf("LastAssistantText() = %q", got)
	}
}

func TestToolCallHelpers(t *testing.T) {
	messages := []types.AgentMessage{
		types.UserMessage{Role: types.RoleUser, Content: "do it"},
		&types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "calling"},
				&types.ToolCallContent{Type: "tool_call", ID: "1", Name: "bash"},
			},
		},
		types.AssistantMessage{
			Role: types.RoleAssistant,
			Content: []types.ContentBlock{
				&types.ToolCallContent{Type: "tool_call", ID: "2", Name: "read"},
			},
		},
	}

	if got := ToolCallNames(messages); len(got) != 2 || got[0] != "bash" || got[1] != "read" {
		t.Fatalf("ToolCallNames() = %v", got)
	}
	if !ToolCalled(messages, "bash") || !ToolCalled(messages, "read") {
		t.Fatal("expected bash and read to be reported as called")
	}
	if ToolCalled(messages, "write") {
		t.Fatal("did not expect write to be reported as called")
	}
}
