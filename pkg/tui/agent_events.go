package tui

import (
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func (m *uiModel) handleAgentEvent(ev agent.AgentEvent) {
	switch ev.Type {
	case agent.EventTypeAgentStart:
		m.queryActive = true
		m.statusMsg = "thinking"
		m.lastActivity = ""

	case agent.EventTypeMessageUpdate:
		if ev.StreamEvent == nil {
			break
		}
		switch ev.StreamEvent.Type {
		case types.EventThinkingDelta:
			block := m.currentAssistantBlock()
			block.Streaming = true
			block.Thinking += ev.StreamEvent.Delta
			if m.thinkingStart.IsZero() {
				m.thinkingStart = time.Now()
			}
		case types.EventTextDelta:
			block := m.currentAssistantBlock()
			block.Streaming = true
			block.RawText += ev.StreamEvent.Delta
			thinking, content := extractThinkText(block.RawText)
			if thinking != "" {
				block.Thinking = thinking
			}
			block.Content = content
		}
		return

	case agent.EventTypeToolExecutionStart:
		var args map[string]any
		if margs, ok := ev.Args.(map[string]any); ok {
			args = margs
		}
		block := m.currentToolBlock()
		var filePath string
		if args != nil {
			switch ev.ToolName {
			case "edit", "write", "read":
				if fp, ok := args["file_path"].(string); ok {
					filePath = fp
				}
			}
		}
		block.Tools = append(block.Tools, &uiToolState{
			ID:       ev.ToolCallID,
			Name:     ev.ToolName,
			Input:    formatToolInput(ev.ToolName, args),
			FilePath: filePath,
			Status:   "running",
		})

	case agent.EventTypeToolExecutionEnd:
		if ev.ToolCallID == "" {
			break
		}
		block := m.currentToolBlock()
		for _, tool := range block.Tools {
			if tool.ID == ev.ToolCallID {
				tool.Status = "done"
				tool.IsError = ev.IsError
				tool.Output = fullResultText(ev)
				if ev.IsError {
					tool.Status = "error"
				}
				break
			}
		}

	case agent.EventTypeMessageEnd:
		msg, ok := assistantMessageFromEvent(ev.Message)
		if !ok {
			break
		}
		block := m.currentAssistantBlock()
		for _, content := range msg.Content {
			switch c := content.(type) {
			case *types.ThinkingContent:
				if c != nil && strings.TrimSpace(c.Thinking) != "" {
					block.Thinking = c.Thinking
				}
			case *types.TextContent:
				if c != nil && strings.TrimSpace(c.Text) != "" {
					block.RawText = c.Text
					thinking, content := extractThinkText(c.Text)
					if thinking != "" {
						block.Thinking = thinking
					}
					block.Content = content
				}
			}
		}
		block.Streaming = false

	case agent.EventTypeAgentEnd:
		m.queryActive = false
		if m.statusMsg != "interrupted" {
			m.statusMsg = ""
		}
	}
}
