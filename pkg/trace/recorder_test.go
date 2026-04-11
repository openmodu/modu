package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func TestRecorderWritesEventsAndAggregatesTokens(t *testing.T) {
	dir := t.TempDir()
	recorder, err := NewRecorder(Options{
		SessionID:   "session-1",
		Cwd:         dir,
		Provider:    "openai",
		ModelID:     "gpt-5.4",
		EventsFile:  filepath.Join(dir, "events.jsonl"),
		SummaryFile: filepath.Join(dir, "summary.json"),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := recorder.RecordSessionEvent("session_start", map[string]any{"source": "test"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageEnd,
		Message: types.AssistantMessage{
			Role: "assistant",
			Content: []types.ContentBlock{
				&types.ThinkingContent{Type: "thinking", Thinking: "inspect repo"},
				&types.TextContent{Type: "text", Text: "call tool next"},
			},
			Usage: types.AgentUsage{Input: 11, Output: 7, TotalTokens: 18},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionStart,
		ToolCallID: "tool-1",
		ToolName:   "read",
		Args:       map[string]any{"path": "/tmp/demo.txt"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionEnd,
		ToolCallID: "tool-1",
		ToolName:   "read",
		Result: agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "file content"}},
			Details: map[string]any{"path": "/tmp/demo.txt"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	summary := recorder.Summary()
	if summary.Counts.Events != 6 {
		t.Fatalf("expected 6 events, got %#v", summary.Counts)
	}
	if summary.Counts.Turns != 1 || summary.Counts.Messages != 1 || summary.Counts.ToolCalls != 1 {
		t.Fatalf("unexpected summary counts: %#v", summary.Counts)
	}
	if summary.Tokens.TotalTokens != 18 || summary.Tokens.Input != 11 || summary.Tokens.Output != 7 {
		t.Fatalf("unexpected token totals: %#v", summary.Tokens)
	}
	if summary.LastEvent == nil || summary.LastEvent.ToolName != "read" {
		t.Fatalf("unexpected last event: %#v", summary.LastEvent)
	}

	eventsData, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(eventsData), `"type":"tool_execution_end"`) {
		t.Fatalf("expected tool execution event in events file, got %q", string(eventsData))
	}
	if !strings.Contains(string(eventsData), `"preview":"thinking: inspect repo`) {
		t.Fatalf("expected assistant preview in events file, got %q", string(eventsData))
	}

	summaryData, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(summaryData), `"totalTokens": 18`) {
		t.Fatalf("expected totalTokens in summary file, got %q", string(summaryData))
	}
}
