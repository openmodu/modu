package rpc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// --- Legacy protocol compat ---

func TestRpcCommandParsing(t *testing.T) {
	input := `{"id":"1","command":"get_state"}`
	var cmd RpcCommand
	if err := json.Unmarshal([]byte(input), &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.ID != "1" {
		t.Fatalf("expected id '1', got %s", cmd.ID)
	}
	if cmd.CommandType() != RpcCmdGetState {
		t.Fatalf("expected command 'get_state', got %s", cmd.CommandType())
	}
}

func TestRpcCommandWithData(t *testing.T) {
	input := `{"id":"2","command":"prompt","data":{"message":"hello"}}`
	var cmd RpcCommand
	if err := json.Unmarshal([]byte(input), &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.CommandType() != RpcCmdPrompt {
		t.Fatalf("expected 'prompt', got %s", cmd.CommandType())
	}
	var data PromptData
	if err := json.Unmarshal(cmd.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Message != "hello" {
		t.Fatalf("expected 'hello', got %s", data.Message)
	}
}

// --- New protocol format (type field) ---

func TestRpcCommandTypeField(t *testing.T) {
	input := `{"id":"1","type":"get_state"}`
	var cmd RpcCommand
	if err := json.Unmarshal([]byte(input), &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.CommandType() != RpcCmdGetState {
		t.Fatalf("expected 'get_state', got %s", cmd.CommandType())
	}
}

func TestRpcCommandTypeFieldPriority(t *testing.T) {
	// type field should take priority over command field
	input := `{"id":"1","type":"get_state","command":"prompt"}`
	var cmd RpcCommand
	if err := json.Unmarshal([]byte(input), &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.CommandType() != RpcCmdGetState {
		t.Fatalf("expected type field to win, got %s", cmd.CommandType())
	}
}

func TestRpcCommandFlatMessage(t *testing.T) {
	input := `{"id":"1","type":"prompt","message":"hello world"}`
	var cmd RpcCommand
	if err := json.Unmarshal([]byte(input), &cmd); err != nil {
		t.Fatal(err)
	}
	if cmd.CommandType() != RpcCmdPrompt {
		t.Fatalf("expected 'prompt', got %s", cmd.CommandType())
	}
	if cmd.Message != "hello world" {
		t.Fatalf("expected 'hello world', got %s", cmd.Message)
	}
}

func TestResolveMessage_FlatField(t *testing.T) {
	cmd := RpcCommand{Message: "flat msg"}
	msg, err := resolveMessage(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "flat msg" {
		t.Fatalf("expected 'flat msg', got %s", msg)
	}
}

func TestResolveMessage_DataField(t *testing.T) {
	raw, _ := json.Marshal(PromptData{Message: "data msg"})
	cmd := RpcCommand{Data: raw}
	msg, err := resolveMessage(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "data msg" {
		t.Fatalf("expected 'data msg', got %s", msg)
	}
}

func TestResolveMessage_PreferFlat(t *testing.T) {
	raw, _ := json.Marshal(PromptData{Message: "data msg"})
	cmd := RpcCommand{Message: "flat msg", Data: raw}
	msg, err := resolveMessage(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if msg != "flat msg" {
		t.Fatalf("expected flat field to win, got %s", msg)
	}
}

func TestResolveMessage_Missing(t *testing.T) {
	cmd := RpcCommand{}
	_, err := resolveMessage(cmd)
	if err == nil {
		t.Fatal("expected error for missing message")
	}
}

// --- Response serialization ---

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

// --- All command types ---

func TestRpcAllCommandTypes(t *testing.T) {
	commands := []RpcCommandType{
		RpcCmdPrompt, RpcCmdSteer, RpcCmdFollowUp, RpcCmdAbort,
		RpcCmdGetState, RpcCmdSetModel, RpcCmdCycleModel,
		RpcCmdSetThinkingLevel, RpcCmdCycleThinking, RpcCmdCompact,
		RpcCmdSetAutoCompaction, RpcCmdSetAutoRetry, RpcCmdAbortRetry,
		RpcCmdGetMessages, RpcCmdNewSession, RpcCmdGetCommands,
		// New commands
		RpcCmdGetAvailableModels, RpcCmdSetSteeringMode, RpcCmdSetFollowUpMode,
		RpcCmdBash, RpcCmdAbortBash, RpcCmdGetSessionStats,
		RpcCmdExportHTML, RpcCmdSwitchSession, RpcCmdFork,
		RpcCmdGetForkMessages, RpcCmdGetLastAssistantText, RpcCmdSetSessionName,
	}
	if len(commands) != 28 {
		t.Fatalf("expected 28 commands, got %d", len(commands))
	}
	for _, cmd := range commands {
		if cmd == "" {
			t.Fatal("empty command type")
		}
	}
}

// --- Event serialization ---

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

// --- New data types serialization ---

func TestBashDataSerialization(t *testing.T) {
	data := BashData{Command: "ls -la", TimeoutMs: 5000}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var parsed BashData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Command != "ls -la" || parsed.TimeoutMs != 5000 {
		t.Fatalf("mismatch: %+v", parsed)
	}
}

func TestForkDataSerialization(t *testing.T) {
	data := ForkData{EntryID: "entry-123"}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var parsed ForkData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.EntryID != "entry-123" {
		t.Fatalf("expected 'entry-123', got %s", parsed.EntryID)
	}
}

func TestSetModeDataSerialization(t *testing.T) {
	data := SetModeData{Mode: "all"}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var parsed SetModeData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Mode != "all" {
		t.Fatalf("expected 'all', got %s", parsed.Mode)
	}
}

func TestSetSessionNameDataSerialization(t *testing.T) {
	data := SetSessionNameData{Name: "my-session"}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"name":"my-session"`) {
		t.Fatalf("missing name in JSON: %s", string(raw))
	}
}

func TestExportHTMLDataSerialization(t *testing.T) {
	data := ExportHTMLData{Path: "/tmp/export.html"}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"/tmp/export.html"`) {
		t.Fatalf("missing path in JSON: %s", string(raw))
	}
}

func TestSwitchSessionDataSerialization(t *testing.T) {
	data := SwitchSessionData{SessionFile: "/path/to/session.jsonl"}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var parsed SwitchSessionData
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.SessionFile != "/path/to/session.jsonl" {
		t.Fatalf("mismatch: %+v", parsed)
	}
}

func TestRpcSlashCommandSerialization(t *testing.T) {
	cmd := RpcSlashCommand{Name: "bash", Description: "Execute shell commands", Source: "tool"}
	raw, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"name":"bash"`) {
		t.Fatalf("missing name in JSON: %s", string(raw))
	}
	if !strings.Contains(string(raw), `"source":"tool"`) {
		t.Fatalf("missing source in JSON: %s", string(raw))
	}
}

// --- RpcSessionState new fields ---

func TestRpcSessionStateNewFields(t *testing.T) {
	state := RpcSessionState{
		Model:               "test",
		Provider:            "ollama",
		ThinkingLevel:       "medium",
		IsStreaming:         false,
		SessionID:           "abc",
		AutoCompaction:      true,
		AutoRetry:           false,
		MessageCount:        5,
		IsCompacting:        true,
		SteeringMode:        "one-at-a-time",
		FollowUpMode:        "all",
		SessionFile:         "/tmp/session.jsonl",
		SessionName:         "my-session",
		PendingMessageCount: 3,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var parsed RpcSessionState
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.IsCompacting {
		t.Fatal("expected isCompacting=true")
	}
	if parsed.SteeringMode != "one-at-a-time" {
		t.Fatalf("expected steeringMode='one-at-a-time', got %s", parsed.SteeringMode)
	}
	if parsed.FollowUpMode != "all" {
		t.Fatalf("expected followUpMode='all', got %s", parsed.FollowUpMode)
	}
	if parsed.SessionFile != "/tmp/session.jsonl" {
		t.Fatalf("expected sessionFile, got %s", parsed.SessionFile)
	}
	if parsed.SessionName != "my-session" {
		t.Fatalf("expected sessionName='my-session', got %s", parsed.SessionName)
	}
	if parsed.PendingMessageCount != 3 {
		t.Fatalf("expected pendingMessageCount=3, got %d", parsed.PendingMessageCount)
	}
}

// --- RpcMode write tests ---

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

// --- New command round-trip tests via JSON ---

func TestNewCommandsRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		cmd   RpcCommandType
	}{
		{"bash", `{"id":"1","type":"bash","data":{"command":"ls"}}`, RpcCmdBash},
		{"abort_bash", `{"id":"2","type":"abort_bash"}`, RpcCmdAbortBash},
		{"get_session_stats", `{"id":"3","type":"get_session_stats"}`, RpcCmdGetSessionStats},
		{"get_available_models", `{"id":"4","type":"get_available_models"}`, RpcCmdGetAvailableModels},
		{"set_steering_mode", `{"id":"5","type":"set_steering_mode","data":{"mode":"all"}}`, RpcCmdSetSteeringMode},
		{"set_follow_up_mode", `{"id":"6","type":"set_follow_up_mode","data":{"mode":"one-at-a-time"}}`, RpcCmdSetFollowUpMode},
		{"export_html", `{"id":"7","type":"export_html","data":{"path":"/tmp/out.html"}}`, RpcCmdExportHTML},
		{"switch_session", `{"id":"8","type":"switch_session","data":{"sessionFile":"/tmp/s.jsonl"}}`, RpcCmdSwitchSession},
		{"fork", `{"id":"9","type":"fork","data":{"entryId":"e1"}}`, RpcCmdFork},
		{"get_fork_messages", `{"id":"10","type":"get_fork_messages"}`, RpcCmdGetForkMessages},
		{"get_last_assistant_text", `{"id":"11","type":"get_last_assistant_text"}`, RpcCmdGetLastAssistantText},
		{"set_session_name", `{"id":"12","type":"set_session_name","data":{"name":"test"}}`, RpcCmdSetSessionName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cmd RpcCommand
			if err := json.Unmarshal([]byte(tt.input), &cmd); err != nil {
				t.Fatalf("failed to parse: %v", err)
			}
			if cmd.CommandType() != tt.cmd {
				t.Fatalf("expected %s, got %s", tt.cmd, cmd.CommandType())
			}
		})
	}
}
