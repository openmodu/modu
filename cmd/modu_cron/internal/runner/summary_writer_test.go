package runner

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// decodeLines parses the slim NDJSON output back into a slice of maps so
// tests can assert on individual fields without coupling to JSON key order.
func decodeLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode: %v\n--- raw ---\n%s", err, raw)
		}
		out = append(out, m)
	}
	return out
}

func TestSummaryWriterFullStream(t *testing.T) {
	// The full coding_agent event stream, in the shape pkg/agent emits.
	// Covers every transformation case: session_start field rename,
	// envelope drop, tool_execution_* → tool_call/tool_result, message_end
	// per-role handling, and the tool-result-only message dropping.
	const input = `{"type":"session_start","sessionId":"abc","model":"gpt-4o"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_start","message":{"role":"user","content":"do thing"}}
{"type":"message_end","message":{"role":"user","content":"do thing"}}
{"type":"message_update","streamEvent":{"Type":"text_delta","Delta":"hel"},"message":"hel"}
{"type":"interrupt"}
{"type":"tool_execution_start","toolName":"bash","toolCallId":"t1","args":{"cmd":"ls"}}
{"type":"tool_execution_end","toolName":"bash","toolCallId":"t1","result":{"content":[{"type":"text","text":"a\nb\n"}]},"isError":false}
{"type":"message_end","message":{"role":"toolResult","toolCallId":"t1","content":[{"type":"text","text":"a\nb\n"}]}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"reasoning"},{"type":"text","text":"done"}]}}
{"type":"tool_execution_end","toolName":"bash","toolCallId":"t2","isError":true}
{"type":"turn_end"}
{"type":"agent_end"}
{"type":"session_end"}
`
	var buf bytes.Buffer
	sw := newSummaryWriter(&buf)
	if _, err := sw.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}

	lines := decodeLines(t, buf.String())
	wantTypes := []string{
		"session_start",
		"user",
		"tool_call",
		"tool_result",
		"assistant",
		"tool_result",
	}
	if len(lines) != len(wantTypes) {
		t.Fatalf("got %d lines, want %d\n--- raw ---\n%s", len(lines), len(wantTypes), buf.String())
	}
	for i, want := range wantTypes {
		if got, _ := lines[i]["type"].(string); got != want {
			t.Errorf("line %d: type=%q, want %q", i, got, want)
		}
	}

	// Per-line field assertions.
	if lines[0]["session_id"] != "abc" || lines[0]["model"] != "gpt-4o" {
		t.Errorf("session_start fields wrong: %+v", lines[0])
	}
	if lines[1]["text"] != "do thing" {
		t.Errorf("user text wrong: %+v", lines[1])
	}
	if lines[2]["name"] != "bash" {
		t.Errorf("tool_call name wrong: %+v", lines[2])
	}
	args, _ := lines[2]["args"].(map[string]any)
	if args["cmd"] != "ls" {
		t.Errorf("tool_call args wrong: %+v", lines[2])
	}
	if lines[3]["ok"] != true || lines[3]["snippet"] != "a\nb" {
		t.Errorf("tool_result ok/snippet wrong: %+v", lines[3])
	}
	if lines[4]["text"] != "done" {
		t.Errorf("assistant text wrong: %+v", lines[4])
	}
	if lines[5]["ok"] != false {
		t.Errorf("error tool_result should have ok=false: %+v", lines[5])
	}
	if _, ok := lines[5]["snippet"]; ok {
		t.Errorf("error tool_result with no body should omit snippet: %+v", lines[5])
	}
}

func TestSummaryWriterDropsEmptyAssistantTurn(t *testing.T) {
	// Assistant turn whose only "text" is whitespace before a tool call —
	// LM Studio does this. Should produce no output line; the tool call is
	// already covered by tool_execution_start in the real stream.
	const input = `{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"x"},{"type":"text","text":"\n\n"},{"type":"toolCall","id":"1","name":"bash","arguments":{"cmd":"ls"}}]}}
`
	var buf bytes.Buffer
	sw := newSummaryWriter(&buf)
	if _, err := sw.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output, got: %q", buf.String())
	}
}

func TestSummaryWriterPartialWrites(t *testing.T) {
	// A real json.Encoder always flushes complete objects with trailing
	// \n, but defensive coverage: feeding the writer one byte at a time
	// must produce the same output as one big write.
	const input = `{"type":"session_start","sessionId":"x","model":"m"}
{"type":"message_end","message":{"role":"user","content":"hi"}}
`
	var wholeBuf bytes.Buffer
	sw1 := newSummaryWriter(&wholeBuf)
	_, _ = sw1.Write([]byte(input))

	var pieceBuf bytes.Buffer
	sw2 := newSummaryWriter(&pieceBuf)
	for i := range len(input) {
		_, _ = sw2.Write([]byte{input[i]})
	}

	if wholeBuf.String() != pieceBuf.String() {
		t.Errorf("byte-by-byte writes diverged from one-shot:\n--- whole ---\n%s--- piece ---\n%s",
			wholeBuf.String(), pieceBuf.String())
	}
}

func TestSummaryWriterMalformedLineDropped(t *testing.T) {
	const input = `not valid json
{"type":"session_start","sessionId":"x","model":"m"}
`
	var buf bytes.Buffer
	sw := newSummaryWriter(&buf)
	if _, err := sw.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}
	lines := decodeLines(t, buf.String())
	if len(lines) != 1 || lines[0]["type"] != "session_start" {
		t.Errorf("malformed line should be dropped, healthy one kept: %+v", lines)
	}
}

func TestToolSnippetTruncation(t *testing.T) {
	long := strings.Repeat("line\n", toolSnippetMaxLines+3)
	result := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": long}},
	}
	got := toolSnippet(result)
	if !strings.Contains(got, "+3 more lines") {
		t.Errorf("expected truncation tail, got: %q", got)
	}
	// Body lines (before the "+N more" tail) must equal the cap.
	body, _, _ := strings.Cut(got, "\n... ")
	if n := len(strings.Split(body, "\n")); n != toolSnippetMaxLines {
		t.Errorf("expected %d body lines in snippet, got %d: %q", toolSnippetMaxLines, n, got)
	}
}
