package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleNDJSON is a synthetic but shape-accurate event stream. Event names
// match what pkg/agent actually emits (tool_execution_start/end, not
// tool_call_*). Includes turn/agent envelope events so we can assert they
// stay hidden from the decoded view.
const sampleNDJSON = `{"type":"session_start","sessionId":"abc123","model":"gpt-4o"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_start","message":{"role":"user","content":"please show files"}}
{"type":"message_end","message":{"role":"user","content":"please show files"}}
{"type":"message_update","streamEvent":{"Type":"text_delta","Delta":"hel"},"message":"hel"}
{"type":"tool_execution_start","toolName":"Read","toolCallId":"t1","args":{"path":"foo.txt"}}
{"type":"tool_execution_end","toolName":"Read","toolCallId":"t1","result":{"content":[{"type":"text","text":"file line 1\nfile line 2"}]},"isError":false}
{"type":"tool_execution_start","toolName":"Run","toolCallId":"t2","args":{"cmd":"git status"}}
{"type":"tool_execution_end","toolName":"Run","toolCallId":"t2","isError":true}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden chain of thought"},{"type":"text","text":"hello\nworld"}]}}
{"type":"turn_end"}
{"type":"agent_end"}
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
	mustContain := []string{
		"session start  model=gpt-4o session=abc123",
		"✎ user:",
		"    please show files",
		"tool call      Read",
		"tool result    Read  ok",
		"    file line 1",
		"    file line 2",
		"tool call      Run",
		"tool result    Run  ERROR",
		"✎ assistant:",
		"    hello",
		"    world",
		"session end",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- output ---\n%s", want, got)
		}
	}
	// Noise events must NOT leak into the decoded view.
	mustNotContain := []string{
		"message_update",
		"agent_start",
		"agent_end",
		"turn_start",
		"turn_end",
		"hidden chain of thought", // thinking blocks are intentionally dropped
	}
	for _, banned := range mustNotContain {
		if strings.Contains(got, banned) {
			t.Errorf("decoded view should hide %q but contains it\n--- output ---\n%s", banned, got)
		}
	}
}

// TestDecodeRealRunlogShape exercises the decoder against the exact event
// shape emitted by a real qwen-via-LM-Studio run (captured manually from a
// task that asked the agent to print the current date via bash). Acts as a
// regression guard that we still surface user prompt / tool exec / final
// assistant message when faced with the actual pkg/agent event names.
func TestDecodeRealRunlogShape(t *testing.T) {
	const realRunlog = `{"model":"qwen/qwen3.6-35b-a3b","sessionId":"7372c720","type":"session_start"}
{"type":"agent_start"}
{"type":"turn_start"}
{"message":{"role":"user","content":"请输出当前的日期和时间"},"type":"message_start"}
{"message":{"role":"user","content":"请输出当前的日期和时间"},"type":"message_end"}
{"streamEvent":{"Type":"thinking_delta","Delta":"checking"},"type":"message_update"}
{"args":{"command":"date '+%Y-%m-%d %H:%M:%S'"},"toolCallId":"537580642","toolName":"bash","type":"tool_execution_start"}
{"args":{"command":"date '+%Y-%m-%d %H:%M:%S'"},"result":{"content":[{"type":"text","text":"2026-05-23 16:16:06\n"}],"details":{"exitCode":0,"timedOut":false}},"toolCallId":"537580642","toolName":"bash","type":"tool_execution_end"}
{"message":{"role":"toolResult","toolCallId":"537580642","toolName":"bash","content":[{"type":"text","text":"2026-05-23 16:16:06\n"}],"isError":false},"type":"message_start"}
{"message":{"role":"toolResult","toolCallId":"537580642","toolName":"bash","content":[{"type":"text","text":"2026-05-23 16:16:06\n"}],"isError":false},"type":"message_end"}
{"message":{"role":"assistant","content":[{"type":"thinking","thinking":"the user wants current time"},{"type":"text","text":"当前日期和时间是：2026-05-23 16:16:06"}]},"type":"message_end"}
{"type":"turn_end"}
{"type":"agent_end"}
{"type":"session_end"}
`
	var out bytes.Buffer
	if err := decodeEventStream(strings.NewReader(realRunlog), &out); err != nil {
		t.Fatalf("decodeEventStream: %v", err)
	}
	got := out.String()

	// Real-shape essentials.
	for _, want := range []string{
		"model=qwen/qwen3.6-35b-a3b session=7372c720",
		"✎ user:",
		"    请输出当前的日期和时间",
		"tool call      bash",
		"command=date '+%Y-%m-%d %H:%M:%S'",
		"tool result    bash  ok",
		"    2026-05-23 16:16:06",
		"✎ assistant:",
		"    当前日期和时间是：2026-05-23 16:16:06",
		"session end",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("real-shape output missing %q\n--- output ---\n%s", want, got)
		}
	}
	// toolResult role would duplicate the tool_execution_end output; assert
	// we don't double-print "2026-05-23 16:16:06" line.
	lines := strings.Split(got, "\n")
	hits := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "2026-05-23 16:16:06" {
			hits++
		}
	}
	if hits != 1 {
		t.Errorf("expected the date to appear exactly once in the decoded view, got %d times\n%s", hits, got)
	}

	// Whole transcript should fit in well under 20 lines — that's the whole
	// point of this exercise. Real input had ~100 NDJSON lines.
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty > 15 {
		t.Errorf("decoded view too verbose: %d non-empty lines\n%s", nonEmpty, got)
	}
}

// TestDecodeSkipsToolCallOnlyAssistantTurn guards against the bug where an
// assistant turn whose content is just `thinking + "\n\n" filler text +
// toolCall` was rendering as an empty "✎ assistant:" block. The tool call
// is already shown via tool_execution_start, so the whole turn should
// produce no output.
func TestDecodeSkipsToolCallOnlyAssistantTurn(t *testing.T) {
	const ndjson = `{"type":"session_start","sessionId":"x","model":"m"}
{"message":{"role":"assistant","content":[{"type":"thinking","thinking":"reason"},{"type":"text","text":"\n\n"},{"type":"toolCall","id":"1","name":"bash","arguments":{"cmd":"ls"}}]},"type":"message_end"}
{"type":"session_end"}
`
	var out bytes.Buffer
	if err := decodeEventStream(strings.NewReader(ndjson), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Contains(out.String(), "assistant:") {
		t.Errorf("expected no 'assistant:' line for a tool-call-only turn, got:\n%s", out.String())
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
