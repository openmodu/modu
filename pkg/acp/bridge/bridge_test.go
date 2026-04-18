package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/openmodu/modu/pkg/acp/jsonrpc"
	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

func loadFixture(t *testing.T, name string) *jsonrpc.Message {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var m jsonrpc.Message
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
	return &m
}

func TestTranslate_AgentMessageChunk(t *testing.T) {
	msg := loadFixture(t, "agent_message_chunk.json")
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Type != agent.EventTypeMessageUpdate {
		t.Errorf("type = %s, want message_update", ev.Type)
	}
	if ev.StreamEvent == nil {
		t.Fatal("StreamEvent is nil")
	}
	if ev.StreamEvent.Type != types.EventTextDelta {
		t.Errorf("stream type = %s, want text_delta", ev.StreamEvent.Type)
	}
	if ev.StreamEvent.Delta != "Hello, world" {
		t.Errorf("delta = %q", ev.StreamEvent.Delta)
	}
}

func TestTranslate_AgentThoughtChunk(t *testing.T) {
	msg := loadFixture(t, "agent_thought_chunk.json")
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	ev := events[0]
	if ev.StreamEvent == nil || ev.StreamEvent.Type != types.EventThinkingDelta {
		t.Errorf("expected thinking_delta, got %+v", ev.StreamEvent)
	}
	if ev.StreamEvent.Delta != "Let me plan this..." {
		t.Errorf("delta = %q", ev.StreamEvent.Delta)
	}
}

func TestTranslate_ToolCall(t *testing.T) {
	msg := loadFixture(t, "tool_call.json")
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	ev := events[0]
	if ev.Type != agent.EventTypeToolExecutionStart {
		t.Errorf("type = %s", ev.Type)
	}
	if ev.ToolCallID != "tc-bash-42" {
		t.Errorf("id = %q", ev.ToolCallID)
	}
	if ev.ToolName != "Bash" {
		t.Errorf("toolName = %q (want Bash — flattened from _meta.claudeCode)", ev.ToolName)
	}
	args, ok := ev.Args.(map[string]any)
	if !ok {
		t.Fatalf("Args = %T, want map", ev.Args)
	}
	if args["command"] != "ls -la" {
		t.Errorf("command = %v", args["command"])
	}
}

func TestTranslate_ToolCallUpdateCompleted(t *testing.T) {
	msg := loadFixture(t, "tool_call_update_completed.json")
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	ev := events[0]
	if ev.Type != agent.EventTypeToolExecutionEnd {
		t.Errorf("type = %s", ev.Type)
	}
	if ev.IsError {
		t.Error("IsError should be false for completed")
	}
	if ev.ToolCallID != "tc-bash-42" {
		t.Errorf("id = %q", ev.ToolCallID)
	}
	if ev.Result == nil {
		t.Error("Result should carry tool output")
	}
}

func TestTranslate_ToolCallUpdateError(t *testing.T) {
	msg := loadFixture(t, "tool_call_update_error.json")
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	ev := events[0]
	if ev.Type != agent.EventTypeToolExecutionEnd {
		t.Errorf("type = %s", ev.Type)
	}
	if !ev.IsError {
		t.Error("IsError should be true")
	}
	if ev.Result != "command not found: wat" {
		t.Errorf("Result = %v", ev.Result)
	}
}

func TestTranslate_ToolCallUpdateRunning(t *testing.T) {
	msg := loadFixture(t, "tool_call_update_running.json")
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	ev := events[0]
	if ev.Type != agent.EventTypeToolExecutionUpdate {
		t.Errorf("type = %s (want tool_execution_update)", ev.Type)
	}
	if ev.Partial == nil {
		t.Error("Partial should carry in-flight content")
	}
}

func TestTranslate_AvailableCommandsUpdate(t *testing.T) {
	msg := loadFixture(t, "available_commands_update.json")
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if events == nil {
		t.Fatal("want empty non-nil slice to distinguish from unknown")
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0", len(events))
	}
}

func TestTranslate_UnknownUpdate(t *testing.T) {
	msg := loadFixture(t, "unknown_update.json")
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if events != nil {
		t.Errorf("want nil for unknown update, got %v", events)
	}
}

func TestTranslate_NilMessage(t *testing.T) {
	events, err := Translate(nil)
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if events != nil {
		t.Errorf("events = %v, want nil", events)
	}
}

func TestTranslate_NonSessionUpdate(t *testing.T) {
	msg := &jsonrpc.Message{Method: "session/something_else"}
	events, err := Translate(msg)
	if err != nil || events != nil {
		t.Errorf("want (nil,nil), got (%v,%v)", events, err)
	}
}

func TestTranslate_MalformedParams(t *testing.T) {
	msg := &jsonrpc.Message{
		Method: "session/update",
		Params: json.RawMessage(`{"update": "not an object"}`),
	}
	_, err := Translate(msg)
	if err == nil {
		t.Error("expected error for malformed params")
	}
}

func TestTranslate_ToolCallMissingID(t *testing.T) {
	// tool_call without toolCallId should be silently skipped.
	msg := &jsonrpc.Message{
		Method: "session/update",
		Params: json.RawMessage(`{"update":{"sessionUpdate":"tool_call","kind":"execute"}}`),
	}
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if events != nil {
		t.Errorf("want nil when toolCallId missing, got %v", events)
	}
}

func TestTranslate_EmptyTextChunkSkipped(t *testing.T) {
	// agent_message_chunk with empty content should produce no event.
	msg := &jsonrpc.Message{
		Method: "session/update",
		Params: json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":""}}}`),
	}
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if events != nil {
		t.Errorf("want nil for empty text, got %v", events)
	}
}

func TestTranslate_ClaudeCodeErrorFallback(t *testing.T) {
	// When update.error is empty but _meta.claudeCode.error is set, the
	// flattened claudeCode error should still mark the event as an error.
	msg := &jsonrpc.Message{
		Method: "session/update",
		Params: json.RawMessage(`{"update":{"sessionUpdate":"tool_call_update","toolCallId":"t1","_meta":{"claudeCode":{"error":"oops"}}}}`),
	}
	events, err := Translate(msg)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events", len(events))
	}
	ev := events[0]
	if !ev.IsError {
		t.Error("IsError should be true when claudeCode.error is set")
	}
	if ev.Result != "oops" {
		t.Errorf("Result = %v, want oops", ev.Result)
	}
}
