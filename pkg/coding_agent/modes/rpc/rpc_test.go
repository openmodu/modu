package rpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRpcCommandParsing(t *testing.T) {
	input := `{"id":"1","command":"get_state"}`
	var cmd RpcCommand
	if err := json.Unmarshal([]byte(input), &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.ID != "1" {
		t.Fatalf("expected id '1', got %s", cmd.ID)
	}
	if cmd.Command != RpcCmdGetState {
		t.Fatalf("expected command 'get_state', got %s", cmd.Command)
	}
}

func TestRpcCommandWithData(t *testing.T) {
	input := `{"id":"2","command":"prompt","data":{"message":"hello"}}`
	var cmd RpcCommand
	if err := json.Unmarshal([]byte(input), &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.Command != RpcCmdPrompt {
		t.Fatalf("expected 'prompt', got %s", cmd.Command)
	}
	var data PromptData
	if err := json.Unmarshal(cmd.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Message != "hello" {
		t.Fatalf("expected 'hello', got %s", data.Message)
	}
}

func TestRpcResponseSerialization(t *testing.T) {
	resp := RpcResponse{
		ID:      "1",
		Type:    "response",
		Command: RpcCmdGetState,
		Success: true,
		Data: RpcSessionState{
			Model:         "test-model",
			ThinkingLevel: "medium",
			SessionID:     "abc",
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var parsed RpcResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.ID != "1" || !parsed.Success {
		t.Fatal("response fields mismatch")
	}
}

func TestRpcAllCommandTypes(t *testing.T) {
	commands := []RpcCommandType{
		RpcCmdPrompt, RpcCmdSteer, RpcCmdFollowUp, RpcCmdAbort,
		RpcCmdGetState, RpcCmdSetModel, RpcCmdCycleModel,
		RpcCmdSetThinkingLevel, RpcCmdCycleThinking, RpcCmdCompact,
		RpcCmdSetAutoCompaction, RpcCmdSetAutoRetry, RpcCmdAbortRetry,
		RpcCmdGetMessages, RpcCmdNewSession, RpcCmdGetCommands,
	}
	for _, cmd := range commands {
		if cmd == "" {
			t.Fatal("empty command type")
		}
	}
}

func TestRpcEventSerialization(t *testing.T) {
	evt := RpcEvent{
		Type: "agent_event",
		Data: map[string]string{"key": "value"},
	}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"agent_event"`) {
		t.Fatal("missing event type in JSON")
	}
}

func TestRpcModeWriteResponse(t *testing.T) {
	var buf bytes.Buffer
	mode := &RpcMode{
		output: &buf,
	}
	mode.writeResponse(RpcResponse{
		ID:      "test",
		Type:    "response",
		Command: RpcCmdGetState,
		Success: true,
	})
	output := buf.String()
	if !strings.Contains(output, `"success":true`) {
		t.Fatalf("expected success in output, got: %s", output)
	}
}

func TestRpcModeWriteEvent(t *testing.T) {
	var buf bytes.Buffer
	mode := &RpcMode{
		output: &buf,
	}
	mode.writeEvent("test_event", map[string]string{"foo": "bar"})
	output := buf.String()
	if !strings.Contains(output, `"type":"test_event"`) {
		t.Fatalf("expected event type in output, got: %s", output)
	}
}
