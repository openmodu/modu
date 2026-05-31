package tui

import (
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func queuePanelContent(ag *agent.Agent) string {
	steering, followUp := ag.QueuedMessages()
	if len(steering) == 0 && len(followUp) == 0 {
		return "No queued messages."
	}
	var b strings.Builder
	writeQueueMessages(&b, "Steer", steering)
	if len(steering) > 0 && len(followUp) > 0 {
		b.WriteString("\n")
	}
	writeQueueMessages(&b, "Follow-up", followUp)
	return strings.TrimRight(b.String(), "\n")
}

func writeQueueMessages(b *strings.Builder, title string, messages []types.AgentMessage) {
	if len(messages) == 0 {
		return
	}
	fmt.Fprintf(b, "%s (%d)\n", title, len(messages))
	for i, msg := range messages {
		fmt.Fprintf(b, "  %d. %s\n", i+1, queueMessagePreview(msg))
	}
}

func queueMessagePreview(msg types.AgentMessage) string {
	switch m := msg.(type) {
	case types.UserMessage:
		return truncateQueuePreview(contentPreview(m.Content))
	case *types.UserMessage:
		if m == nil {
			return "(nil)"
		}
		return truncateQueuePreview(contentPreview(m.Content))
	default:
		return truncateQueuePreview(fmt.Sprintf("%v", msg))
	}
}

func contentPreview(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []types.ContentBlock:
		var parts []string
		for _, block := range c {
			switch b := block.(type) {
			case *types.TextContent:
				if b != nil && strings.TrimSpace(b.Text) != "" {
					parts = append(parts, b.Text)
				}
			case *types.ImageContent:
				parts = append(parts, "[image]")
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	return fmt.Sprintf("%v", content)
}

func truncateQueuePreview(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return "(empty)"
	}
	const max = 120
	rs := []rune(text)
	if len(rs) <= max {
		return text
	}
	return string(rs[:max-3]) + "..."
}
