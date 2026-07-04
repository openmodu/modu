package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// toolSnippetMaxLines caps how many lines of a tool's textual result are
// kept in the slimmed-down log. Five matches the old --tail decoder so the
// human view doesn't get noticeably noisier or thinner after the switch.
const toolSnippetMaxLines = 5

// summaryWriter wraps the per-run log file and rewrites the full
// coding_agent NDJSON event stream into a slim NDJSON form: one line per
// meaningful step (session_start / user / assistant / tool_call /
// tool_result), envelopes and per-token deltas dropped.
//
// Each Write appends to an internal line buffer; complete lines are
// JSON-decoded, transformed via mapEvent, then re-encoded. Partial lines
// (writes split mid-object by the encoder's buffered output) stay buffered
// for the next call.
//
// The writer is NOT safe for concurrent use — callers wrap a single
// io.Writer that's already serialized (runlog.Run uses a mutex).
type summaryWriter struct {
	w   io.Writer
	buf bytes.Buffer
}

func newSummaryWriter(w io.Writer) *summaryWriter {
	return &summaryWriter{w: w}
}

// Write always reports it consumed all of p — failures to write a single
// transformed line are silently dropped so a malformed event never aborts
// the surrounding RunPrint call. We tolerate junk because the alternative
// (returning short writes / errors) would propagate up and cancel an
// otherwise healthy run.
func (sw *summaryWriter) Write(p []byte) (int, error) {
	sw.buf.Write(p)
	data := sw.buf.Bytes()
	consumed := 0
	for {
		i := bytes.IndexByte(data[consumed:], '\n')
		if i < 0 {
			break
		}
		line := data[consumed : consumed+i]
		sw.handleLine(line)
		consumed += i + 1
	}
	if consumed > 0 {
		remaining := append([]byte(nil), data[consumed:]...)
		sw.buf.Reset()
		sw.buf.Write(remaining)
	}
	return len(p), nil
}

func (sw *summaryWriter) handleLine(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	var ev map[string]any
	if err := json.Unmarshal(line, &ev); err != nil {
		return
	}
	out := mapEvent(ev)
	if out == nil {
		return
	}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	_, _ = sw.w.Write(b)
	_, _ = sw.w.Write([]byte("\n"))
}

// mapEvent turns one raw coding_agent event into the slim form, or returns
// nil to drop the event. Returning nil for unrecognized types means new
// upstream events default to invisible — acceptable here because the slim
// log is meant to be a curated subset, not a passthrough.
func mapEvent(ev map[string]any) map[string]any {
	kind, _ := ev["type"].(string)
	switch kind {
	case "session_start":
		return map[string]any{
			"type":       "session_start",
			"session_id": ev["sessionId"],
			"model":      ev["model"],
		}
	case "tool_execution_start":
		return map[string]any{
			"type": "tool_call",
			"name": ev["toolName"],
			"args": ev["args"],
		}
	case "tool_execution_end":
		ok := true
		if isErr, _ := ev["isError"].(bool); isErr {
			ok = false
		}
		out := map[string]any{
			"type": "tool_result",
			"name": ev["toolName"],
			"ok":   ok,
		}
		if snip := toolSnippet(ev["result"]); snip != "" {
			out["snippet"] = snip
		}
		return out
	case "message_end":
		return mapMessage(ev["message"])
	}
	return nil
}

func mapMessage(message any) map[string]any {
	msg, ok := message.(map[string]any)
	if !ok {
		return nil
	}
	role, _ := msg["role"].(string)
	switch role {
	case "user":
		text := strings.TrimSpace(flattenText(msg["content"]))
		if text == "" {
			return nil
		}
		return map[string]any{"type": "user", "text": text}
	case "assistant":
		// Assistant turns whose only "text" is whitespace filler before a
		// tool call (LM Studio does this) have nothing useful to surface;
		// the tool call itself shows up as a separate tool_call line.
		text := strings.TrimSpace(flattenText(msg["content"]))
		if text == "" {
			return nil
		}
		return map[string]any{"type": "assistant", "text": text}
	}
	// toolResult role is already covered by tool_result; dropping here
	// keeps the slim stream from duplicating output.
	return nil
}

// flattenText concatenates text-bearing blocks. content is either a string
// (raw user prompts) or an array of blocks with shape
// {"type":"text","text":"..."} — thinking / toolCall blocks are skipped on
// purpose.
func flattenText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, item := range c {
			blk, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := blk["type"].(string); t != "text" {
				continue
			}
			if s, ok := blk["text"].(string); ok {
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return ""
}

// toolSnippet pulls the textual portion of a tool result and trims it to
// the first toolSnippetMaxLines lines with a "+N more" tail. Empty input
// yields empty output so the caller can skip the field entirely.
func toolSnippet(result any) string {
	m, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	text := flattenText(m["content"])
	if text == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= toolSnippetMaxLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:toolSnippetMaxLines], "\n") +
		fmt.Sprintf("\n... (+%d more lines)", len(lines)-toolSnippetMaxLines)
}
