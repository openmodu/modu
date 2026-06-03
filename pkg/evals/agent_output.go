package evals

import (
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

// LastAssistantText returns the text blocks from the latest assistant message.
func LastAssistantText(messages []types.AgentMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		switch msg := messages[i].(type) {
		case types.AssistantMessage:
			return assistantText(msg)
		case *types.AssistantMessage:
			if msg != nil {
				return assistantText(*msg)
			}
		}
	}
	return ""
}

func assistantText(msg types.AssistantMessage) string {
	var parts []string
	for _, block := range msg.Content {
		if text, ok := block.(*types.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}
