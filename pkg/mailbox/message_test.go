package mailbox

import (
	"encoding/json"
	"testing"
)

func TestNewTaskAssignMessage(t *testing.T) {
	raw, err := NewTaskAssignMessage("orchestrator", "task-1", "research the topic")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	if msg.Type != MessageTypeTaskAssign {
		t.Errorf("expected type task_assign, got %s", msg.Type)
	}
	if msg.From != "orchestrator" {
		t.Errorf("expected from=orchestrator, got %s", msg.From)
	}
	if msg.TaskID != "task-1" {
		t.Errorf("expected task_id=task-1, got %s", msg.TaskID)
	}

	payload, err := ParseTaskAssignPayload(msg)
	if err != nil {
		t.Fatalf("ParseTaskAssignPayload failed: %v", err)
	}
	if payload.Description != "research the topic" {
		t.Errorf("unexpected description: %s", payload.Description)
	}
}

func TestNewTaskResultMessage(t *testing.T) {
	raw, err := NewTaskResultMessage("worker-1", "task-1", "42", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msg, err := ParseMessage(raw)
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	if msg.Type != MessageTypeTaskResult {
		t.Errorf("expected type task_result, got %s", msg.Type)
	}

	payload, err := ParseTaskResultPayload(msg)
	if err != nil {
		t.Fatalf("ParseTaskResultPayload failed: %v", err)
	}
	if payload.Result != "42" || payload.Error != "" {
		t.Errorf("unexpected payload: %+v", payload)
	}
}

func TestNewTaskResultMessageWithError(t *testing.T) {
	raw, err := NewTaskResultMessage("worker-1", "task-2", "", "timeout")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	msg, _ := ParseMessage(raw)
	payload, _ := ParseTaskResultPayload(msg)
	if payload.Error != "timeout" {
		t.Errorf("expected error=timeout, got %s", payload.Error)
	}
}

func TestParseMessageInvalidJSON(t *testing.T) {
	_, err := ParseMessage("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestMessageRoundTrip(t *testing.T) {
	original := Message{
		Type:   MessageTypeInfo,
		From:   "agent-a",
		TaskID: "task-99",
		Payload: json.RawMessage(`{"key":"value"}`),
	}
	b, _ := json.Marshal(original)
	parsed, err := ParseMessage(string(b))
	if err != nil {
		t.Fatalf("ParseMessage failed: %v", err)
	}
	if parsed.Type != original.Type || parsed.From != original.From || parsed.TaskID != original.TaskID {
		t.Errorf("round-trip mismatch: %+v", parsed)
	}
}
