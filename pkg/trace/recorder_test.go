package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func newTestRecorder(t *testing.T) (*Recorder, string) {
	t.Helper()
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
	return recorder, dir
}

func TestRecorderWritesEventsAndAggregatesTokens(t *testing.T) {
	recorder, dir := newTestRecorder(t)

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

func TestRecorderAllEventTypes(t *testing.T) {
	recorder, dir := newTestRecorder(t)

	events := []agent.AgentEvent{
		{Type: agent.EventTypeAgentStart},
		{Type: agent.EventTypeTurnStart},
		{Type: agent.EventTypeMessageStart, Message: types.UserMessage{Role: "user", Content: "hello"}},
		{Type: agent.EventTypeMessageUpdate},
		{Type: agent.EventTypeMessageEnd, Message: types.UserMessage{Role: "user", Content: "hello"}},
		{Type: agent.EventTypeMessageEnd, Message: types.AssistantMessage{
			Role:    "assistant",
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "hi"}},
			Usage:   types.AgentUsage{Input: 10, Output: 5, TotalTokens: 15},
		}},
		{Type: agent.EventTypeToolExecutionStart, ToolCallID: "t1", ToolName: "bash", Args: map[string]any{"cmd": "ls"}},
		{Type: agent.EventTypeToolExecutionUpdate, ToolCallID: "t1", ToolName: "bash"},
		{Type: agent.EventTypeToolExecutionEnd, ToolCallID: "t1", ToolName: "bash", Result: agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "output"}},
		}},
		{Type: agent.EventTypeInterrupt, Interrupt: &agent.InterruptEvent{Reason: "max_steps", StepCount: 5}},
		{Type: agent.EventTypeTurnEnd},
		{Type: agent.EventTypeAgentEnd},
	}

	for _, e := range events {
		if err := recorder.RecordAgentEvent(e); err != nil {
			t.Fatalf("RecordAgentEvent(%s): %v", e.Type, err)
		}
	}

	summary := recorder.Summary()
	if summary.Counts.Turns != 1 {
		t.Fatalf("expected 1 turn, got %d", summary.Counts.Turns)
	}
	if summary.Counts.Messages != 2 {
		t.Fatalf("expected 2 messages (user+assistant), got %d", summary.Counts.Messages)
	}
	if summary.Counts.AssistantMessages != 1 {
		t.Fatalf("expected 1 assistant message, got %d", summary.Counts.AssistantMessages)
	}
	if summary.Counts.ToolCalls != 1 {
		t.Fatalf("expected 1 tool call, got %d", summary.Counts.ToolCalls)
	}
	if summary.Counts.Interrupts != 1 {
		t.Fatalf("expected 1 interrupt, got %d", summary.Counts.Interrupts)
	}
	if summary.Tokens.TotalTokens != 15 {
		t.Fatalf("expected 15 total tokens, got %d", summary.Tokens.TotalTokens)
	}

	// Verify all events were written
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 12 {
		t.Fatalf("expected 12 event lines, got %d", len(lines))
	}
}

func TestRecorderToolDuration(t *testing.T) {
	recorder, dir := newTestRecorder(t)

	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionStart,
		ToolCallID: "t1",
		ToolName:   "bash",
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionEnd,
		ToolCallID: "t1",
		ToolName:   "bash",
		Result: agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Read events file and check duration on tool_execution_end
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Type == "tool_execution_end" {
			if e.DurationMs < 0 {
				t.Fatalf("expected non-negative duration, got %d", e.DurationMs)
			}
			// Duration should be >= 0 (both events happen nearly instantly in tests)
			return
		}
	}
	t.Fatal("tool_execution_end event not found")
}

func TestRecorderCostTracking(t *testing.T) {
	recorder, _ := newTestRecorder(t)

	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type: agent.EventTypeMessageEnd,
		Message: types.AssistantMessage{
			Role:    "assistant",
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}},
			Usage: types.AgentUsage{
				Input: 100, Output: 50, TotalTokens: 150,
				Cost: struct {
					Input      float64 `json:"input"`
					Output     float64 `json:"output"`
					CacheRead  float64 `json:"cacheRead"`
					CacheWrite float64 `json:"cacheWrite"`
					Total      float64 `json:"total"`
				}{
					Input: 0.001, Output: 0.003, Total: 0.004,
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	summary := recorder.Summary()
	if summary.Cost.Total != 0.004 {
		t.Fatalf("expected cost total 0.004, got %f", summary.Cost.Total)
	}
	if summary.Cost.Input != 0.001 {
		t.Fatalf("expected cost input 0.001, got %f", summary.Cost.Input)
	}
	if summary.Cost.Output != 0.003 {
		t.Fatalf("expected cost output 0.003, got %f", summary.Cost.Output)
	}
}

func TestRecorderClose(t *testing.T) {
	recorder, dir := newTestRecorder(t)

	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	// Read summary and check durationMs is set
	data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.DurationMs < 0 {
		t.Fatalf("expected non-negative durationMs, got %d", summary.DurationMs)
	}

	// Double close should be safe
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderConcurrency(t *testing.T) {
	recorder, _ := newTestRecorder(t)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart})
			_ = recorder.RecordSessionEvent("ping", map[string]any{"x": 1})
		}()
	}
	wg.Wait()

	summary := recorder.Summary()
	if summary.Counts.Events != 40 {
		t.Fatalf("expected 40 events, got %d", summary.Counts.Events)
	}
}

func TestRecorderNoFiles(t *testing.T) {
	// Recorder with empty file paths should work without writing to disk
	recorder, err := NewRecorder(Options{
		SessionID: "s1",
		Cwd:       "/tmp",
		Provider:  "test",
		ModelID:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnStart}); err != nil {
		t.Fatal(err)
	}

	summary := recorder.Summary()
	if summary.Counts.Events != 2 {
		t.Fatalf("expected 2 events, got %d", summary.Counts.Events)
	}
}

func TestRecorderErrorCounting(t *testing.T) {
	recorder, _ := newTestRecorder(t)

	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeAgentStart}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{Type: agent.EventTypeTurnStart}); err != nil {
		t.Fatal(err)
	}
	// Tool error
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionStart,
		ToolCallID: "t1",
		ToolName:   "bash",
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionEnd,
		ToolCallID: "t1",
		ToolName:   "bash",
		IsError:    true,
		Result: agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "command failed"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Agent end with error
	if err := recorder.RecordAgentEvent(agent.AgentEvent{
		Type:    agent.EventTypeAgentEnd,
		IsError: true,
	}); err != nil {
		t.Fatal(err)
	}

	summary := recorder.Summary()
	if summary.Counts.Errors != 2 {
		t.Fatalf("expected 2 errors, got %d", summary.Counts.Errors)
	}
}

func TestRecorderSessionEventUpdatesModel(t *testing.T) {
	recorder, _ := newTestRecorder(t)

	if err := recorder.RecordSessionEvent("model_change", map[string]any{
		"provider":  "anthropic",
		"modelId":   "claude-opus-4-6",
		"sessionId": "new-session",
		"cwd":       "/new/dir",
	}); err != nil {
		t.Fatal(err)
	}

	summary := recorder.Summary()
	if summary.Model.Provider != "anthropic" {
		t.Fatalf("expected provider=anthropic, got %s", summary.Model.Provider)
	}
	if summary.Model.ID != "claude-opus-4-6" {
		t.Fatalf("expected model=claude-opus-4-6, got %s", summary.Model.ID)
	}
	if summary.SessionID != "new-session" {
		t.Fatalf("expected sessionId=new-session, got %s", summary.SessionID)
	}
	if summary.Cwd != "/new/dir" {
		t.Fatalf("expected cwd=/new/dir, got %s", summary.Cwd)
	}
}
