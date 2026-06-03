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

// ToolCalls returns every tool call across all assistant messages, in order.
// Use it to assert real tool usage instead of trusting the model's prose.
func ToolCalls(messages []types.AgentMessage) []types.ToolCallContent {
	var calls []types.ToolCallContent
	for _, m := range messages {
		var content []types.ContentBlock
		switch msg := m.(type) {
		case types.AssistantMessage:
			content = msg.Content
		case *types.AssistantMessage:
			if msg != nil {
				content = msg.Content
			}
		}
		for _, block := range content {
			if tc, ok := block.(*types.ToolCallContent); ok && tc != nil {
				calls = append(calls, *tc)
			}
		}
	}
	return calls
}

// ToolCalled reports whether a tool with the given name was called at least once.
func ToolCalled(messages []types.AgentMessage, name string) bool {
	for _, call := range ToolCalls(messages) {
		if call.Name == name {
			return true
		}
	}
	return false
}

// ToolCallNames returns the names of all tool calls, in order (duplicates kept).
func ToolCallNames(messages []types.AgentMessage) []string {
	calls := ToolCalls(messages)
	names := make([]string, len(calls))
	for i, call := range calls {
		names[i] = call.Name
	}
	return names
}
