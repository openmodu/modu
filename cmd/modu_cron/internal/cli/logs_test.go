package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleNDJSON is the slim format that runner.summaryWriter produces.
// Every line is one of the seven event types the decoder knows about
// (run_start / session_start / user / assistant / tool_call / tool_result
// / run_end).
const sampleNDJSON = `{"type":"run_start","task_id":"demo","prompt":"please show files","started_at":"2026-05-23T10:00:00Z"}
{"type":"session_start","session_id":"abc123","model":"gpt-4o"}
{"type":"user","text":"please show files"}
{"type":"tool_call","name":"Read","args":{"path":"foo.txt"}}
{"type":"tool_result","name":"Read","ok":true,"snippet":"file line 1\nfile line 2"}
{"type":"tool_call","name":"Run","args":{"cmd":"git status"}}
{"type":"tool_result","name":"Run","ok":false}
{"type":"assistant","text":"hello\nworld"}
{"type":"run_end","status":"ok","duration_ms":1234,"ended_at":"2026-05-23T10:00:01Z"}
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
	mustContain := []string{
		"run start      task=demo",
		"✎ prompt:",
		"    please show files",
		"session         model=gpt-4o id=abc123",
		"✎ user:",
		"tool call      Read",
		"tool result    Read  ok",
		"    file line 1",
		"    file line 2",
		"tool call      Run",
		"tool result    Run  ERROR",
		"✎ assistant:",
		"    hello",
		"    world",
		"run end        status=ok duration=1234ms",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

func TestDecodeRunEndErrorShowsError(t *testing.T) {
	const ndjson = `{"type":"run_start","task_id":"demo","prompt":"do thing","started_at":"2026-05-23T10:00:00Z"}
{"type":"run_end","status":"error","duration_ms":42,"ended_at":"2026-05-23T10:00:00Z","error":"create session: missing api key"}
`
	var out bytes.Buffer
	if err := decodeEventStream(strings.NewReader(ndjson), &out); err != nil {
		t.Fatalf("decodeEventStream: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"run end        status=error duration=42ms",
		"  error:",
		"    create session: missing api key",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

func TestDecodeUnknownEventStillSurfaces(t *testing.T) {
	const ndjson = `{"type":"future_event","payload":"x"}
`
	var out bytes.Buffer
	if err := decodeEventStream(strings.NewReader(ndjson), &out); err != nil {
		t.Fatalf("decodeEventStream: %v", err)
	}
	if !strings.Contains(out.String(), "· future_event") {
		t.Errorf("expected unknown-event marker, got: %s", out.String())
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
	if !strings.Contains(listOut.String(), "| FILE") || !strings.Contains(listOut.String(), "| SIZE") {
		t.Errorf("list output should render a table: %s", listOut.String())
	}

	var tailOut bytes.Buffer
	if err := Logs("demo", LogsOptions{Tail: true}, &tailOut); err != nil {
		t.Fatalf("tail: %v", err)
	}
	if !strings.Contains(tailOut.String(), "run start") {
		t.Errorf("decoded tail missing run start: %s", tailOut.String())
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
