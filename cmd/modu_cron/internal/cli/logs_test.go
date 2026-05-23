package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleNDJSON = `{"type":"session_start","sessionId":"abc123","model":"gpt-4o"}
{"type":"message_update","streamEvent":{"Type":"delta","Delta":"hel"},"message":"hel"}
{"type":"message_update","streamEvent":{"Type":"delta","Delta":"lo"},"message":"lo"}
{"type":"tool_call_start","toolName":"Read","toolCallId":"t1","args":{"path":"foo.txt"}}
{"type":"tool_call_end","toolName":"Read","toolCallId":"t1","result":{"text":"contents"},"isError":false}
{"type":"tool_call_start","toolName":"Run","toolCallId":"t2","args":{"cmd":"git status"}}
{"type":"tool_call_end","toolName":"Run","toolCallId":"t2","isError":true}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello\nworld"}]}}
{"type":"session_end"}
`

func writeLog(t *testing.T, root, taskID, name, body string) {
	t.Helper()
	dir := filepath.Join(root, taskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeEventStreamHumanReadable(t *testing.T) {
	var out bytes.Buffer
	if err := decodeEventStream(strings.NewReader(sampleNDJSON), &out); err != nil {
		t.Fatalf("decodeEventStream: %v", err)
	}
	got := out.String()
	checks := []string{
		"session start  model=gpt-4o session=abc123",
		"tool call      Read",
		"tool result    Read  ok",
		"tool call      Run",
		"tool result    Run  ERROR",
		"assistant:",
		"    hello",
		"    world",
		"session end",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, got)
		}
	}
	if strings.Contains(got, "message_update") {
		t.Errorf("message_update noise should be hidden from decoded view\n%s", got)
	}
}

func TestLogsTaskWithNoRunsListIsHelpful(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var out bytes.Buffer
	if err := Logs("nope", LogsOptions{}, &out); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if !strings.Contains(out.String(), "no logs for task") {
		t.Errorf("expected friendly message, got: %s", out.String())
	}
}

func TestLogsTailErrorsWhenEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := Logs("nope", LogsOptions{Tail: true}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no log files yet") {
		t.Errorf("expected 'no log files yet', got: %v", err)
	}
}

func TestLogsListAndTailRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logRoot := filepath.Join(home, ".modu_cron", "logs")
	writeLog(t, logRoot, "demo", "2026-05-23T10-00-00.000000000Z.log", sampleNDJSON)
	writeLog(t, logRoot, "demo", "2026-05-23T10-05-00.000000000Z.log", sampleNDJSON)

	var listOut bytes.Buffer
	if err := Logs("demo", LogsOptions{}, &listOut); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut.String(), "Task demo — 2 run(s)") {
		t.Errorf("list output: %s", listOut.String())
	}

	var tailOut bytes.Buffer
	if err := Logs("demo", LogsOptions{Tail: true}, &tailOut); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(tailOut.String(), "session start") {
		t.Errorf("decoded tail missing session start: %s", tailOut.String())
	}
}

func TestLogsJSONReturnsRaw(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logRoot := filepath.Join(home, ".modu_cron", "logs")
	writeLog(t, logRoot, "demo", "single.log", sampleNDJSON)

	var out bytes.Buffer
	if err := Logs("demo", LogsOptions{File: "single.log", JSON: true}, &out); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if out.String() != sampleNDJSON {
		t.Errorf("--json output should match raw exactly")
	}
}

func TestLogsFileNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	err := Logs("demo", LogsOptions{File: "missing.log"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}
