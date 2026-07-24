package tui

import (
	"strings"
	"testing"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

type toolNodePresenterStub struct {
	eventCalls  int
	callCalls   int
	resultCalls int
}

func (s *toolNodePresenterStub) EventNode(event types.Event, _ string) (modutui.ToolNode, bool) {
	s.eventCalls++
	return modutui.ToolNode{Call: modutui.ToolCall{ID: event.ToolCallID, Name: event.ToolName}}, true
}

func (s *toolNodePresenterStub) CallNode(call *types.ToolCallContent, _ string) modutui.ToolNode {
	s.callCalls++
	return modutui.ToolNode{Call: modutui.ToolCall{ID: call.ID, Name: call.Name}}
}

func (s *toolNodePresenterStub) ResultNode(result types.ToolResultMessage, _ string) modutui.ToolNode {
	s.resultCalls++
	return modutui.ToolNode{Call: modutui.ToolCall{ID: result.ToolCallID, Name: result.ToolName, Done: true}}
}

func TestEventPresenterSkipsSubmittedUserMessageEnd(t *testing.T) {
	presenter := NewEventPresenter(&toolNodePresenterStub{}, "compact")
	got := presenter.AgentEvent(types.Event{
		Type:    types.EventTypeMessageEnd,
		Message: types.UserMessage{Role: types.RoleUser, Content: "hello"},
	}, "")
	if len(got) != 0 {
		t.Fatalf("entries = %#v", got)
	}
}

func TestEventPresenterGroupsThinkingBeforeAssistantContent(t *testing.T) {
	tools := &toolNodePresenterStub{}
	presenter := NewEventPresenter(tools, "compact")
	got := presenter.AgentMessage(types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "answer"},
			&types.ThinkingContent{Type: "thinking", Thinking: "first"},
			&types.ToolCallContent{Type: "toolCall", ID: "call-1", Name: "read"},
			&types.ThinkingContent{Type: "thinking", Thinking: "second"},
		},
	}, "")

	if len(got) != 3 {
		t.Fatalf("entries = %#v", got)
	}
	thinking, ok := got[0].Nodes[0].(modutui.ThinkingNode)
	if !ok || !strings.Contains(thinking.Text, "first") || !strings.Contains(thinking.Text, "second") {
		t.Fatalf("thinking entry = %#v", got[0])
	}
	if text, ok := got[1].Nodes[0].(modutui.MarkdownNode); !ok || text.Text != "answer" {
		t.Fatalf("answer entry = %#v", got[1])
	}
	if tool, ok := got[2].Nodes[0].(modutui.ToolNode); !ok || tool.Call.ID != "call-1" {
		t.Fatalf("tool entry = %#v", got[2])
	}
	if tools.callCalls != 1 {
		t.Fatalf("tool calls = %d", tools.callCalls)
	}
}

func TestEventPresenterDelegatesToolLifecycle(t *testing.T) {
	tools := &toolNodePresenterStub{}
	presenter := NewEventPresenter(tools, "compact")

	started := presenter.AgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "bash",
	}, "")
	result := presenter.AgentMessage(types.ToolResultMessage{
		Role:       types.RoleToolResult,
		ToolCallID: "call-1",
		ToolName:   "bash",
	}, "")

	if len(started) != 1 || started[0].ID != "call-1" || len(result) != 1 || result[0].ID != "call-1" {
		t.Fatalf("started = %#v, result = %#v", started, result)
	}
	if tools.eventCalls != 1 || tools.resultCalls != 1 {
		t.Fatalf("event calls = %d, result calls = %d", tools.eventCalls, tools.resultCalls)
	}
}

func TestEventPresenterMapsSessionEvents(t *testing.T) {
	presenter := NewEventPresenter(nil, "------------- compact -------------")

	denied, ok := presenter.SessionEvent(coding_agent.SessionEvent{
		Type:     coding_agent.SessionEventPermissionDeny,
		ToolName: "bash",
		Reason:   "dangerous command",
	})
	if !ok {
		t.Fatal("permission event was not presented")
	}
	text := denied.Nodes[0].(modutui.MarkdownNode).Text
	if !strings.Contains(text, "bash") || !strings.Contains(text, "dangerous command") {
		t.Fatalf("permission entry = %#v", denied)
	}

	compact, ok := presenter.SessionEvent(coding_agent.SessionEvent{
		Type: coding_agent.SessionEventCompactionDone,
	})
	if !ok || !compact.Plain {
		t.Fatalf("compact entry = %#v, %v", compact, ok)
	}
	node, ok := compact.Nodes[0].(modutui.TextNode)
	if !ok || node.Text != "------------- compact -------------" {
		t.Fatalf("compact node = %#v", compact.Nodes)
	}
}
