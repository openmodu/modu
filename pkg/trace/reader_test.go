package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func writeTestTrace(t *testing.T) (string, *Recorder) {
	t.Helper()
	dir := t.TempDir()
	recorder, err := NewRecorder(Options{
		SessionID:   "test-session",
		Cwd:         dir,
		Provider:    "anthropic",
		ModelID:     "claude-opus-4-6",
		EventsFile:  filepath.Join(dir, "events.jsonl"),
		SummaryFile: filepath.Join(dir, "summary.json"),
	})
	if err != nil {
		t.Fatal(err)
	}

	events := []agent.AgentEvent{
		{Type: agent.EventTypeAgentStart},
		{Type: agent.EventTypeTurnStart},
		{Type: agent.EventTypeMessageEnd, Message: types.AssistantMessage{
			Role:    "assistant",
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "let me check"}},
			Usage:   types.AgentUsage{Input: 100, Output: 50, TotalTokens: 150},
		}},
		{Type: agent.EventTypeToolExecutionStart, ToolCallID: "t1", ToolName: "read", Args: map[string]any{"path": "/a.go"}},
		{Type: agent.EventTypeToolExecutionEnd, ToolCallID: "t1", ToolName: "read", Result: agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "file content"}},
		}},
		{Type: agent.EventTypeToolExecutionStart, ToolCallID: "t2", ToolName: "bash", Args: map[string]any{"cmd": "go test"}},
		{Type: agent.EventTypeToolExecutionEnd, ToolCallID: "t2", ToolName: "bash", IsError: true, Result: agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "FAIL"}},
		}},
		{Type: agent.EventTypeInterrupt, Interrupt: &agent.InterruptEvent{Reason: "approval", StepCount: 3}},
		{Type: agent.EventTypeTurnEnd},
		{Type: agent.EventTypeAgentEnd},
	}
	for _, e := range events {
		if err := recorder.RecordAgentEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	return dir, recorder
}

func TestReadEvents_NoFilter(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 10 {
		t.Fatalf("expected 10 events, got %d", len(events))
	}
	// Check seq ordering
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Fatalf("events not in seq order at index %d", i)
		}
	}
}

func TestReadEvents_FilterByType(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{
		Types: []string{"tool_execution_end"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 tool_execution_end events, got %d", len(events))
	}
	for _, e := range events {
		if e.Type != "tool_execution_end" {
			t.Fatalf("unexpected event type: %s", e.Type)
		}
	}
}

func TestReadEvents_FilterByToolName(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{
		ToolNames: []string{"bash"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 bash events (start+end), got %d", len(events))
	}
	for _, e := range events {
		if e.ToolName != "bash" {
			t.Fatalf("expected toolName=bash, got %s", e.ToolName)
		}
	}
}

func TestReadEvents_FilterErrorsOnly(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{
		ErrorsOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 error event, got %d", len(events))
	}
	if events[0].ToolName != "bash" || !events[0].IsError {
		t.Fatalf("unexpected error event: %+v", events[0])
	}
}

func TestReadEvents_FilterBySource(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{
		Source: "agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Source != "agent" {
			t.Fatalf("expected source=agent, got %s", e.Source)
		}
	}
}

func TestReadEvents_FilterLimit(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{
		Limit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
}

func TestReadEvents_FilterBySeq(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{
		MinSeq: 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Seq < 8 {
			t.Fatalf("expected seq >= 8, got %d", e.Seq)
		}
	}
}

func TestReadEvents_CombinedFilter(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{
		Types:     []string{"tool_execution_start", "tool_execution_end"},
		ToolNames: []string{"read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 read tool events, got %d", len(events))
	}
}

func TestReadEvents_MissingFile(t *testing.T) {
	_, err := ReadEvents("/nonexistent/path.jsonl", EventFilter{})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadSummary(t *testing.T) {
	dir, recorder := writeTestTrace(t)
	_ = recorder.Close()

	summary, err := ReadSummary(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.SessionID != "test-session" {
		t.Fatalf("expected session=test-session, got %s", summary.SessionID)
	}
	if summary.Model.Provider != "anthropic" {
		t.Fatalf("expected provider=anthropic, got %s", summary.Model.Provider)
	}
	if summary.Counts.Events != 10 {
		t.Fatalf("expected 10 events, got %d", summary.Counts.Events)
	}
	if summary.Counts.ToolCalls != 2 {
		t.Fatalf("expected 2 tool calls, got %d", summary.Counts.ToolCalls)
	}
	if summary.Tokens.TotalTokens != 150 {
		t.Fatalf("expected 150 tokens, got %d", summary.Tokens.TotalTokens)
	}
	if summary.DurationMs < 0 {
		t.Fatalf("expected non-negative duration, got %d", summary.DurationMs)
	}
}

func TestReadSummary_MissingFile(t *testing.T) {
	_, err := ReadSummary("/nonexistent/summary.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestTailEvents(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := TailEvents(filepath.Join(dir, "events.jsonl"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	// Should be the last 3 events
	if events[0].Type != "interrupt" {
		t.Fatalf("expected interrupt as 3rd from end, got %s", events[0].Type)
	}
	if events[2].Type != "agent_end" {
		t.Fatalf("expected agent_end as last, got %s", events[2].Type)
	}
}

func TestTailEvents_MoreThanAvailable(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := TailEvents(filepath.Join(dir, "events.jsonl"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 10 {
		t.Fatalf("expected all 10 events, got %d", len(events))
	}
}

func TestTailEvents_Zero(t *testing.T) {
	dir, _ := writeTestTrace(t)
	events, err := TailEvents(filepath.Join(dir, "events.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestComputeStats(t *testing.T) {
	dir, _ := writeTestTrace(t)

	events, err := ReadEvents(filepath.Join(dir, "events.jsonl"), EventFilter{})
	if err != nil {
		t.Fatal(err)
	}

	stats := ComputeStats(events)
	if stats.TotalEvents != 10 {
		t.Fatalf("expected 10 total events, got %d", stats.TotalEvents)
	}
	if stats.ByType["tool_execution_end"] != 2 {
		t.Fatalf("expected 2 tool_execution_end, got %d", stats.ByType["tool_execution_end"])
	}
	if stats.ByTool["read"] != 2 {
		t.Fatalf("expected 2 read events, got %d", stats.ByTool["read"])
	}
	if stats.ByTool["bash"] != 2 {
		t.Fatalf("expected 2 bash events, got %d", stats.ByTool["bash"])
	}
	if stats.ErrorCount != 1 {
		t.Fatalf("expected 1 error, got %d", stats.ErrorCount)
	}
	if stats.TimeSpanMs < 0 {
		t.Fatalf("expected non-negative time span, got %d", stats.TimeSpanMs)
	}
}

func TestFormatEvent(t *testing.T) {
	e := Event{
		Seq:        5,
		Time:       1713100800000, // 2024-04-15 00:00:00 UTC
		Type:       "tool_execution_end",
		Turn:       2,
		ToolName:   "bash",
		IsError:    true,
		DurationMs: 150,
		Preview:    "command failed",
	}
	s := FormatEvent(e)
	if !strings.Contains(s, "#5") {
		t.Fatalf("expected seq #5 in output: %s", s)
	}
	if !strings.Contains(s, "T2") {
		t.Fatalf("expected T2 in output: %s", s)
	}
	if !strings.Contains(s, "tool=bash") {
		t.Fatalf("expected tool=bash in output: %s", s)
	}
	if !strings.Contains(s, "ERROR") {
		t.Fatalf("expected ERROR in output: %s", s)
	}
	if !strings.Contains(s, "150ms") {
		t.Fatalf("expected 150ms in output: %s", s)
	}
	if !strings.Contains(s, "command failed") {
		t.Fatalf("expected preview in output: %s", s)
	}
}

func TestFormatEvent_LongPreview(t *testing.T) {
	e := Event{
		Seq:     1,
		Time:    1713100800000,
		Type:    "message_end",
		Preview: strings.Repeat("x", 200),
	}
	s := FormatEvent(e)
	if len(s) > 200 {
		// Preview should be truncated
		if !strings.HasSuffix(s, "...") {
			t.Fatalf("expected truncated preview ending in ...: %s", s)
		}
	}
}

func TestFormatSummary(t *testing.T) {
	s := Summary{
		StartedAt:  1713100800000,
		DurationMs: 65000,
		SessionID:  "test-1",
		Model:      ModelRef{Provider: "anthropic", ID: "claude-opus-4-6"},
		Counts: Counts{
			Events: 10, Turns: 2, Messages: 3,
			ToolCalls: 4, Errors: 1, Interrupts: 1,
		},
	}
	s.Tokens.Input = 500
	s.Tokens.Output = 200
	s.Tokens.TotalTokens = 700
	s.Cost.Total = 0.0042
	s.Cost.Input = 0.001
	s.Cost.Output = 0.003

	out := FormatSummary(s)
	if !strings.Contains(out, "test-1") {
		t.Fatalf("expected session ID: %s", out)
	}
	if !strings.Contains(out, "1m5s") {
		t.Fatalf("expected duration 1m5s: %s", out)
	}
	if !strings.Contains(out, "$0.0042") {
		t.Fatalf("expected cost: %s", out)
	}
	if !strings.Contains(out, "Interrupts: 1") {
		t.Fatalf("expected interrupts: %s", out)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		ms   int64
		want string
	}{
		{500, "500ms"},
		{1500, "1.5s"},
		{65000, "1m5s"},
		{3600000, "60m0s"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.ms)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestReadEvents_MalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	content := `{"seq":1,"time":1000,"source":"agent","type":"agent_start","totals":{}}
not valid json
{"seq":2,"time":2000,"source":"agent","type":"agent_end","totals":{}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := ReadEvents(path, EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 valid events (skipping malformed), got %d", len(events))
	}
}

func TestReadEvents_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	events, err := ReadEvents(path, EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
}

func TestReadSummary_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.json")
	original := Summary{
		StartedAt:  1000,
		UpdatedAt:  2000,
		DurationMs: 1000,
		SessionID:  "s1",
		Cwd:        "/test",
		Model:      ModelRef{Provider: "p", ID: "m"},
	}
	original.Tokens.Input = 10
	original.Tokens.TotalTokens = 10
	original.Cost.Total = 0.001

	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := ReadSummary(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionID != original.SessionID {
		t.Fatalf("sessionID mismatch: %s vs %s", loaded.SessionID, original.SessionID)
	}
	if loaded.DurationMs != original.DurationMs {
		t.Fatalf("duration mismatch: %d vs %d", loaded.DurationMs, original.DurationMs)
	}
	if loaded.Cost.Total != original.Cost.Total {
		t.Fatalf("cost mismatch: %f vs %f", loaded.Cost.Total, original.Cost.Total)
	}
}
