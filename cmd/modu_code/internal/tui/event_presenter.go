package tui

import (
	"fmt"
	"strings"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

// ToolNodePresenter owns tool-specific summaries, previews, artifacts, and
// code/diff presentation. EventPresenter only decides where tool nodes belong
// in the agent event stream.
type ToolNodePresenter interface {
	EventNode(types.Event, string) (modutui.ToolNode, bool)
	CallNode(*types.ToolCallContent, string) modutui.ToolNode
	ResultNode(types.ToolResultMessage, string) modutui.ToolNode
}

type EventPresenter struct {
	tools          ToolNodePresenter
	compactDivider string
}

func NewEventPresenter(tools ToolNodePresenter, compactDivider string) EventPresenter {
	return EventPresenter{
		tools:          tools,
		compactDivider: strings.TrimSpace(compactDivider),
	}
}

func (p EventPresenter) AgentEvent(event types.Event, cwd string) []modutui.Entry {
	switch event.Type {
	case types.EventTypeMessageEnd:
		if isUserAgentMessage(event.Message) {
			return nil
		}
		return p.AgentMessage(event.Message, cwd)
	case types.EventTypeToolExecutionStart, types.EventTypeToolExecutionEnd:
		if p.tools == nil {
			return nil
		}
		node, ok := p.tools.EventNode(event, cwd)
		if !ok {
			return nil
		}
		return []modutui.Entry{toolEntry(node)}
	default:
		return nil
	}
}

func (p EventPresenter) AgentMessages(messages []types.AgentMessage, cwd string) []modutui.Entry {
	out := make([]modutui.Entry, 0, len(messages))
	for _, message := range messages {
		out = append(out, p.AgentMessage(message, cwd)...)
	}
	return out
}

func (p EventPresenter) AgentMessage(message types.AgentMessage, cwd string) []modutui.Entry {
	switch message := message.(type) {
	case types.UserMessage:
		return userEntries(message.Content)
	case *types.UserMessage:
		if message == nil {
			return nil
		}
		return userEntries(message.Content)
	case types.AssistantMessage:
		return p.assistantEntries(message, cwd)
	case *types.AssistantMessage:
		if message == nil {
			return nil
		}
		return p.assistantEntries(*message, cwd)
	case types.ToolResultMessage:
		if p.tools == nil {
			return nil
		}
		return []modutui.Entry{toolEntry(p.tools.ResultNode(message, cwd))}
	case *types.ToolResultMessage:
		if message == nil || p.tools == nil {
			return nil
		}
		return []modutui.Entry{toolEntry(p.tools.ResultNode(*message, cwd))}
	default:
		return nil
	}
}

func (p EventPresenter) SessionEvent(event coding_agent.SessionEvent) (modutui.Entry, bool) {
	switch event.Type {
	case coding_agent.SessionEventModelChange:
		return infoEntry("model: " + event.Provider + "/" + event.ModelID), true
	case coding_agent.SessionEventCompactionDone:
		return p.ContextCompactEntry(), true
	case coding_agent.SessionEventThinkingChange:
		return infoEntry("thinking: " + event.Level), true
	case coding_agent.SessionEventCwdChanged:
		return infoEntry("cwd: " + event.NewCwd), true
	case coding_agent.SessionEventWorktreeCreate:
		return infoEntry("worktree: " + event.Path), true
	case coding_agent.SessionEventWorktreeRemove:
		return infoEntry("worktree removed: " + event.Path), true
	case coding_agent.SessionEventSubagentStart:
		return infoEntry("subagent start: " + event.SubagentName + "\n" + event.SubagentTask), true
	case coding_agent.SessionEventSubagentStop:
		text := "subagent stop: " + event.SubagentName
		if event.ErrorMessage != "" {
			text += "\nerror: " + event.ErrorMessage
		}
		if event.SubagentResult != "" {
			text += "\n" + event.SubagentResult
		}
		return infoEntry(text), true
	case coding_agent.SessionEventPermissionReq:
		return infoEntry("permission requested: " + event.ToolName), true
	case coding_agent.SessionEventPermissionDeny:
		text := "permission denied: " + event.ToolName
		if event.Reason != "" {
			text += "\n" + event.Reason
		}
		return infoEntry(text), true
	case coding_agent.SessionEventExtensionNotify:
		text := event.Message
		if event.ExtensionName != "" {
			text = event.ExtensionName + ": " + text
		}
		return infoEntry(text), true
	default:
		return modutui.Entry{}, false
	}
}

func (p EventPresenter) ContextCompactEntry() modutui.Entry {
	return modutui.Entry{
		Role:  modutui.RoleAssistant,
		Nodes: []modutui.Node{modutui.TextNode{Text: p.compactDivider}},
		Plain: true,
	}
}

func (p EventPresenter) assistantEntries(message types.AssistantMessage, cwd string) []modutui.Entry {
	var thinking []string
	var out []modutui.Entry
	for _, block := range message.Content {
		switch block := block.(type) {
		case *types.TextContent:
			if block != nil && strings.TrimSpace(block.Text) != "" {
				out = append(out, markdownEntry(modutui.RoleAssistant, block.Text))
			}
		case *types.ThinkingContent:
			if block != nil && strings.TrimSpace(block.Thinking) != "" {
				thinking = append(thinking, strings.TrimSpace(block.Thinking))
			}
		case *types.ToolCallContent:
			if block != nil && p.tools != nil {
				out = append(out, toolEntry(p.tools.CallNode(block, cwd)))
			}
		}
	}
	if len(thinking) > 0 {
		out = append([]modutui.Entry{{
			Role: modutui.RoleAssistant,
			Nodes: []modutui.Node{modutui.ThinkingNode{
				Text: strings.Join(thinking, "\n\n"),
			}},
		}}, out...)
	}
	if len(out) == 0 && message.ErrorMessage != "" {
		out = append(out, markdownEntry(modutui.RoleAssistant, "error: "+message.ErrorMessage))
	}
	return out
}

func userEntries(content any) []modutui.Entry {
	return []modutui.Entry{markdownEntry(modutui.RoleUser, contentText(content))}
}

func markdownEntry(role modutui.Role, text string) modutui.Entry {
	return modutui.Entry{
		Role:  role,
		Nodes: []modutui.Node{modutui.MarkdownNode{Text: text}},
	}
}

func infoEntry(text string) modutui.Entry {
	return markdownEntry(modutui.RoleAssistant, strings.TrimSpace(text))
}

func toolEntry(node modutui.ToolNode) modutui.Entry {
	return modutui.Entry{
		ID:    node.Call.ID,
		Role:  modutui.RoleAssistant,
		Nodes: []modutui.Node{node},
	}
}

func isUserAgentMessage(message types.AgentMessage) bool {
	switch message.(type) {
	case types.UserMessage, *types.UserMessage:
		return true
	default:
		return false
	}
}

func contentText(content any) string {
	switch content := content.(type) {
	case string:
		return content
	case []types.ContentBlock:
		return contentBlocksText(content)
	default:
		return fmt.Sprint(content)
	}
}

func contentBlocksText(blocks []types.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	imageIndex := 0
	for _, block := range blocks {
		switch block := block.(type) {
		case *types.TextContent:
			if block != nil && block.Text != "" {
				parts = append(parts, block.Text)
			}
		case *types.ThinkingContent:
			if block != nil && block.Thinking != "" {
				parts = append(parts, block.Thinking)
			}
		case *types.ImageContent:
			if block != nil {
				imageIndex++
				parts = append(parts, fmt.Sprintf("[Image #%d]", imageIndex))
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
