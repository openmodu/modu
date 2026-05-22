package tui

import (
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func (r *goTUIRoot) runQueueCommand(args string) {
	if r.session == nil || r.session.GetAgent() == nil {
		r.model.setTransientStatus("session is not available")
		r.bump()
		return
	}
	ag := r.session.GetAgent()
	fields := strings.Fields(args)
	if len(fields) == 0 {
		r.appendSystemSection("Queue", queuePanelContent(ag))
		return
	}
	switch fields[0] {
	case "clear":
		r.runQueueClearCommand(ag, fields[1:])
	case "drop":
		if len(fields) > 1 {
			r.model.setTransientStatus("usage: /queue drop")
			r.bump()
			return
		}
		kind, ok := ag.DropLastQueuedMessage()
		if !ok {
			r.model.setTransientStatus("queue is empty")
			r.bump()
			return
		}
		r.model.setTransientStatus("dropped " + kind)
		r.bump()
	default:
		r.model.setTransientStatus("usage: /queue [clear [steer|followup]|drop]")
		r.bump()
	}
}

func (r *goTUIRoot) runQueueClearCommand(ag *agent.Agent, fields []string) {
	if len(fields) == 0 {
		ag.ClearAllQueues()
		r.model.setTransientStatus("queue cleared")
		r.bump()
		return
	}
	if len(fields) > 1 {
		r.model.setTransientStatus("usage: /queue clear [steer|followup]")
		r.bump()
		return
	}
	switch fields[0] {
	case "steer", "steering":
		ag.ClearSteeringQueue()
		r.model.setTransientStatus("steer queue cleared")
	case "followup", "follow-up", "followups":
		ag.ClearFollowUpQueue()
		r.model.setTransientStatus("follow-up queue cleared")
	default:
		r.model.setTransientStatus("usage: /queue clear [steer|followup]")
	}
	r.bump()
}

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

func writeQueueMessages(b *strings.Builder, title string, messages []agent.AgentMessage) {
	if len(messages) == 0 {
		return
	}
	fmt.Fprintf(b, "%s (%d)\n", title, len(messages))
	for i, msg := range messages {
		fmt.Fprintf(b, "  %d. %s\n", i+1, queueMessagePreview(msg))
	}
}

func queueMessagePreview(msg agent.AgentMessage) string {
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
