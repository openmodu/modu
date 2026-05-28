package tui

import (
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func (m *uiModel) handleAgentEvent(ev agent.Event) {
	switch ev.Type {
	case agent.EventTypeAgentStart:
		m.queryActive = true
		m.setStatus("thinking")
		m.clearActivity()

	case agent.EventTypeMessageStart:
		if _, ok := assistantMessageFromEvent(ev.Message); ok {
			m.beginAssistantBlock()
		}

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
				// File tools (pkg/coding_agent/tools/{edit,write,read}.go) use
				// "path" as their key — NOT "file_path". A previous version of
				// this code used "file_path" and silently produced empty paths
				// in tool headers and edit-summary lines.
				if fp, ok := args["path"].(string); ok {
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
			m.setStatus("")
		}
	}
}
