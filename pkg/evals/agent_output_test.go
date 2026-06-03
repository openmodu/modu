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
