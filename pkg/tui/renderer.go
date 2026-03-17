package tui

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/types"
)

// toolRecord stores full tool call data for Ctrl+R expansion.
type toolRecord struct {
	name    string
	args    any
	result  string // full, untruncated result text
	isError bool
}

// Renderer renders agent events to the terminal in a Claude Code-inspired style.
//
// Visual language:
//
//	● text response              ← green bullet, streamed inline
//	✧ Thinking…                  ← header text
//	  ⎿  content                 ← text streamed with ⎿ indent
//	⏺ ToolName("arg")           ← while tool is running
//	⏺ ToolName("arg") → result  ← after tool completes (replaced in-place)
//
// Two output modes:
//   - Plain (screen == nil): writes directly to out; uses cursor-up to
//     replace the tool "running" line with the completed line.
//   - Screen: writes to the scroll-region viewport managed by Screen.
type Renderer struct {
	out     io.Writer
	screen  *Screen
	noColor bool

	// per-turn state
	hadText   bool
	hadTool   bool
	inThink   bool
	turnStart time.Time

	// plain-mode: lines printed into the scroll region since the last
	// tool header (used to cursor-up back to that line on completion).
	toolLines int

	// tool history for Ctrl+R expansion (all calls this session).
	toolHistory []toolRecord

	// <think>...</think> tag filtering for models that embed thinking in text stream.
	inTextThink bool   // currently inside a <think> block in text
	textBuf     string // partial text buffer for tag detection

	// Markdown renderer for AI text responses.
	md *mdWriter

	// Expand/collapse state for Ctrl+R tool output toggle.
	toolExpanded      bool
	expandMarkLines   int
	expandMarkPending string

	// Thinking content streaming state.
	// thinkNeedIndent is true when the next non-newline character needs a
	// leading indent ("  ") to align with the vertical bar prefix.
	// thinkPendingNL defers writing \n until the next non-newline content
	// arrives so trailing newlines are silently discarded at block end.
	thinkNeedIndent bool
	thinkPendingNL  bool
}

// NewRenderer creates a plain-mode Renderer writing to out.
func NewRenderer(out io.Writer) *Renderer {
	r := &Renderer{out: out, noColor: shouldDisableColor(out)}
	r.md = newMDWriter(r.noColor, func(text string) { fmt.Fprint(r.out, text) })
	return r
}

// NewRendererWithScreen creates a Renderer backed by a viewport Screen.
func NewRendererWithScreen(s *Screen) *Renderer {
	r := &Renderer{screen: s, noColor: s.noColor}
	r.md = newMDWriter(r.noColor, func(text string) { s.Write(text) })
	return r
}

// SetNoColor overrides automatic color detection.
func (r *Renderer) SetNoColor(v bool) { r.noColor = v }

// ── output helpers ──────────────────────────────────────────────────────────

func (r *Renderer) write(text string) {
	if r.screen != nil {
		r.screen.Write(text)
	} else {
		fmt.Fprint(r.out, text)
	}
}

func (r *Renderer) writeln(text string) { r.write(text + "\n") }

// bullet returns the styled ● marker.
func (r *Renderer) bullet() string { return styled(r.noColor, ansiBrightGreen, "●") }

// ── event handler ────────────────────────────────────────────────────────────

func (r *Renderer) HandleEvent(event agent.AgentEvent) {
	switch event.Type {

	case agent.EventTypeAgentStart:
		r.hadText = false
		r.hadTool = false
		r.inThink = false
		r.inTextThink = false
		r.thinkNeedIndent = false
		r.thinkPendingNL = false
		r.textBuf = ""
		r.toolLines = 0
		r.turnStart = time.Now()
		r.md.Reset()

	case agent.EventTypeMessageUpdate:
		if event.StreamEvent == nil {
			return
		}
		switch event.StreamEvent.Type {

		case types.EventThinkingStart:
			r.flushTextBuf()
			r.inThink = true
			r.thinkNeedIndent = true
			header := styled(r.noColor, ansiOrange, "✧ ") + styled(r.noColor, ansiDim, "Thinking…")
			r.write("\n" + header + "\n")
			r.toolLines++

		case types.EventThinkingDelta:
			r.writeThinkContent(event.StreamEvent.Delta)

		case types.EventThinkingEnd:
			r.inThink = false
			r.writeThinkClose()

		case types.EventTextDelta:
			r.processTextDelta(event.StreamEvent.Delta)
		}

	case agent.EventTypeToolExecutionStart:
		r.flushTextBuf()
		r.hadTool = true
		r.toolLines = 0
		r.toolExpanded = false // new tool call invalidates any previous expansion

		// Line 1: ⏺ ToolName(arg)  — written as regular (permanent) content.
		// Line 2:   ⎿  …           — written as the replaceable tool header.
		callLine := r.toolCallLine(event.ToolName, event.Args)
		resultPlaceholder := r.toolResultLine(false, false, "")
		if r.screen != nil {
			r.write("\n" + callLine + "\n")
			r.screen.WriteToolHeader(resultPlaceholder)
		} else {
			fmt.Fprintf(r.out, "\n%s\n%s\n", callLine, resultPlaceholder)
		}

	case agent.EventTypeToolExecutionEnd:
		full := fullResultText(event)
		r.toolHistory = append(r.toolHistory, toolRecord{
			name:    event.ToolName,
			args:    event.Args,
			result:  full,
			isError: event.IsError,
		})

		// Replace the ⎿ placeholder with the result summary.
		resultLine := r.toolResultLine(true, event.IsError, toolResult(event))
		if r.screen != nil {
			r.screen.CollapseToolHeader(resultLine)
		} else {
			upLines := r.toolLines + 1
			fmt.Fprintf(r.out, "\033[%dA", upLines)
			fmt.Fprint(r.out, ansiEraseLine)
			fmt.Fprintln(r.out, resultLine)
			r.toolLines = 0
		}

		// Show a preview of the output inline (Claude Code style).
		if !event.IsError && full != "" {
			const previewMax = 10
			lines := strings.Split(strings.TrimRight(full, "\n"), "\n")
			shown := lines
			extra := 0
			if len(lines) > previewMax {
				shown = lines[:previewMax]
				extra = len(lines) - previewMax
			}
			for _, l := range shown {
				r.writeln(styled(r.noColor, ansiDim, "     "+l))
			}
			if extra > 0 {
				r.writeln(styled(r.noColor, ansiBrightBlack,
					fmt.Sprintf("     … (%d more lines, ctrl+r to expand)", extra)))
			}
		}

	case agent.EventTypeAgentEnd:
		r.flushTextBuf()
		// Elapsed time footer, like Claude Code's "✻ Churned for Xs".
		elapsed := time.Since(r.turnStart)
		footer := formatElapsed(elapsed)
		r.writeln("\n" + styled(r.noColor, ansiDim, "✻ "+footer))
	}
}

// processTextDelta handles streaming text, collapsing <think>...</think> blocks
// (used by models like Qwen and DeepSeek that embed reasoning in the text stream).
func (r *Renderer) processTextDelta(delta string) {
	r.textBuf += delta
	for {
		if r.inTextThink {
			// Stream think content with dim styling, buffer only tail for tag detection.
			idx := strings.Index(r.textBuf, "</think>")
			if idx < 0 {
				// Flush all but last 8 bytes (guards against partial </think> tag).
				// Snap the cut to a valid UTF-8 rune boundary to avoid splitting
				// multi-byte characters (e.g. 3-byte CJK chars).
				if len(r.textBuf) > 8 {
					cut := len(r.textBuf) - 8
					for cut > 0 && !utf8.RuneStart(r.textBuf[cut]) {
						cut--
					}
					if cut > 0 {
						r.writeThinkContent(r.textBuf[:cut])
						r.textBuf = r.textBuf[cut:]
					}
				}
				return
			}
			// Flush content up to the closing tag.
			if idx > 0 {
				r.writeThinkContent(r.textBuf[:idx])
			}
			r.inTextThink = false
			r.writeThinkClose()
			// Skip leading newlines that models emit after </think>.
			r.textBuf = strings.TrimLeft(r.textBuf[idx+len("</think>"):], "\n")
		} else {
			// Look for opening tag.
			idx := strings.Index(r.textBuf, "<think>")
			if idx < 0 {
				// Flush all but the last 6 bytes (guards against partial "<think>" tags).
				// Snap cut to valid UTF-8 rune boundary.
				const guard = len("<think>") - 1
				if len(r.textBuf) > guard {
					cut := len(r.textBuf) - guard
					for cut > 0 && !utf8.RuneStart(r.textBuf[cut]) {
						cut--
					}
					if cut > 0 {
						r.emitText(r.textBuf[:cut])
						r.textBuf = r.textBuf[cut:]
					}
				}
				return
			}
			// Emit any text before the <think> tag.
			if idx > 0 {
				r.emitText(r.textBuf[:idx])
			}
			// Show thinking header; content will stream below it.
			header := styled(r.noColor, ansiOrange, "✧ ") + styled(r.noColor, ansiDim, "Thinking…")
			r.write("\n" + header + "\n")
			r.toolLines++
			r.thinkNeedIndent = true
			r.inTextThink = true
			r.textBuf = r.textBuf[idx+len("<think>"):]
		}
	}
}

// emitText writes a text chunk, routing through the Markdown renderer.
func (r *Renderer) emitText(text string) {
	if strings.TrimSpace(text) == "" && !r.hadText {
		// Do not emit a bullet for pure whitespace if we haven't started text yet.
		// We still feed it to MD so spacing is preserved if it's mid-stream.
		r.md.Feed(text)
		return
	}
	if !r.hadText {
		r.write("\n" + r.bullet() + " ")
		r.hadText = true
		r.toolLines++
	}
	r.md.Feed(text)
}

// writeThinkContent writes a chunk of thinking content with dim/italic styling
// and "  ⎿  " indentation at the start of each line.
//
// Newlines are deferred (lazy): a \n is only written when the next
// non-newline character arrives.  This ensures trailing newlines in the
// model's thinking output are silently discarded, so the closing ⎿  ──
// line appears immediately after the last line of content with no blank gap.
func (r *Renderer) writeThinkContent(text string) {
	if text == "" {
		return
	}
	var out strings.Builder
	for _, ch := range text {
		if ch == '\n' {
			// Only queue a newline when not already at line-start; this
			// collapses consecutive trailing newlines into nothing.
			if !r.thinkNeedIndent {
				r.thinkPendingNL = true
				r.thinkNeedIndent = true
			}
			continue
		}
		// Flush the deferred newline + indent before real content.
		if r.thinkPendingNL {
			out.WriteByte('\n')
			r.thinkPendingNL = false
		}
		if r.thinkNeedIndent {
			out.WriteString("  ⎿  ")
			r.thinkNeedIndent = false
		}
		out.WriteRune(ch)
	}
	if out.Len() > 0 {
		r.write(styled(r.noColor, ansiDim+ansiItalic, out.String()))
	}
}

// writeThinkClose writes a final newline if needed for a thinking block.
// Discards any pending trailing newline and positions the line correctly
// whether the last content ended with \n or not.
func (r *Renderer) writeThinkClose() {
	r.thinkPendingNL = false
	// If thinkNeedIndent is true we're already at a fresh line (or no content
	// was written at all); skip the leading \n to avoid a blank gap.
	prefix := "\n"
	if r.thinkNeedIndent {
		prefix = ""
	}
	r.thinkNeedIndent = false
	r.write(styled(r.noColor, ansiDim, prefix))
}

// flushTextBuf flushes any text held in the partial-tag detection buffer and
// the Markdown line buffer.  Must be called at turn end or before a tool call.
func (r *Renderer) flushTextBuf() {
	if r.inTextThink {
		r.inTextThink = false
	}
	if r.textBuf != "" {
		r.emitText(r.textBuf)
		r.textBuf = ""
	}
	r.md.Flush()
}

// ── tool line formatting ─────────────────────────────────────────────────────
//
// Two-line format, mirroring Claude Code:
//
//	⏺ ToolName(arg)               ← line 1: permanent
//	  ⎿  summary (ctrl+r)         ← line 2: replaced in-place on completion

// toolCallLine returns line 1: "⏺ ToolName(arg)".
func (r *Renderer) toolCallLine(name string, args any) string {
	bullet := styled(r.noColor, ansiBrightGreen, "⏺")
	nameStr := styled(r.noColor, ansiBold, name)
	arg := primaryArg(name, args)
	argStr := styled(r.noColor, ansiDim, "("+arg+")")
	return fmt.Sprintf("%s  %s%s", bullet, nameStr, argStr)
}

// toolResultLine returns line 2: "  ⎿  summary" or "  ⎿  …" while running.
func (r *Renderer) toolResultLine(done, isError bool, result string) string {
	prefix := styled(r.noColor, ansiDim, "  ⎿  ")
	if !done {
		return prefix + styled(r.noColor, ansiDim, "…")
	}
	hint := styled(r.noColor, ansiBrightBlack, " (ctrl+r to expand)")
	if isError {
		return prefix + styled(r.noColor, ansiRed, result) + hint
	}
	if result == "" {
		return prefix + styled(r.noColor, ansiDim, "done") + hint
	}
	return prefix + styled(r.noColor, ansiDim, result) + hint
}

// ExpandLastTool toggles the full args+result of the most recent tool call.
// First press expands; second press collapses back.  Called on Ctrl+R.
func (r *Renderer) ExpandLastTool() {
	if len(r.toolHistory) == 0 {
		r.writeln(styled(r.noColor, ansiDim, "(no tool calls yet)"))
		return
	}

	// ── collapse ─────────────────────────────────────────────────────────────
	if r.toolExpanded {
		if r.screen != nil {
			r.screen.TrimToMark(r.expandMarkLines, r.expandMarkPending)
		}
		r.toolExpanded = false
		return
	}

	// ── expand ────────────────────────────────────────────────────────────────
	// Record the current buffer position so we can trim back on collapse.
	if r.screen != nil {
		r.expandMarkLines, r.expandMarkPending = r.screen.ContentMark()
	}
	r.toolExpanded = true

	rec := r.toolHistory[len(r.toolHistory)-1]
	w := termWidth()

	// Header.
	header := fmt.Sprintf(" %s ", rec.name)
	side := (w - len([]rune(header))) / 2
	if side < 1 {
		side = 1
	}
	bar := strings.Repeat("─", side) + header + strings.Repeat("─", w-side-len([]rune(header)))
	r.writeln("\n" + styled(r.noColor, ansiBold+ansiBrightGreen, bar))

	// Args.
	if m, ok := rec.args.(map[string]any); ok && len(m) > 0 {
		r.writeln(styled(r.noColor, ansiDim, "Args:"))
		for k, v := range m {
			val := fmt.Sprintf("%v", v)
			r.writeln(styled(r.noColor, ansiDim, "  "+k+": ") + truncateLines(val, 20))
		}
	}

	// Result.
	if rec.isError {
		r.writeln(styled(r.noColor, ansiRed, "Error:"))
	} else {
		r.writeln(styled(r.noColor, ansiDim, "Output:"))
	}
	if rec.result != "" {
		r.write(rec.result)
		if !strings.HasSuffix(rec.result, "\n") {
			r.write("\n")
		}
	}

	r.writeln(styled(r.noColor, ansiBrightBlack, strings.Repeat("─", w)))
}

// primaryArg returns the most informative single argument for display.
// For known tools it picks the key arg; for unknown tools it uses the first value.
func primaryArg(toolName string, args any) string {
	m, ok := args.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}

	// Ordered preference list per tool.
	prefs := map[string][]string{
		"read":  {"file_path", "path"},
		"write": {"file_path", "path"},
		"edit":  {"file_path", "path"},
		"bash":  {"command", "cmd"},
		"grep":  {"pattern", "query"},
		"find":  {"pattern", "path"},
		"ls":    {"path", "dir"},
	}

	if keys, ok := prefs[strings.ToLower(toolName)]; ok {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				return truncate(fmt.Sprintf("%v", v), 60)
			}
		}
	}

	// Fall back to first value in map iteration.
	for _, v := range m {
		return truncate(fmt.Sprintf("%v", v), 60)
	}
	return ""
}

// toolResult extracts a short human-readable result summary.
func toolResult(event agent.AgentEvent) string {
	if event.IsError {
		return "✗ " + truncate(errorText(event), 60)
	}

	text := strings.TrimSpace(extractResultText(event))
	if text == "" {
		return ""
	}
	lines := strings.Count(text, "\n") + 1
	if lines > 1 {
		return fmt.Sprintf("%d lines", lines)
	}
	return truncate(text, 60)
}

// fullResultText extracts the complete, untruncated result text.
func fullResultText(event agent.AgentEvent) string {
	if event.IsError {
		return errorText(event)
	}
	return strings.TrimSpace(extractResultText(event))
}

// extractResultText pulls plain text content from a tool result.
func extractResultText(event agent.AgentEvent) string {
	res, ok := event.Result.(agent.AgentToolResult)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, b := range res.Content {
		if tc, ok := b.(*types.TextContent); ok && tc != nil {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// errorText extracts the error description from a failed tool result.
func errorText(event agent.AgentEvent) string {
	res, ok := event.Result.(agent.AgentToolResult)
	if !ok {
		return "error"
	}
	for _, b := range res.Content {
		if tc, ok := b.(*types.TextContent); ok && tc != nil && tc.Text != "" {
			return tc.Text
		}
	}
	return "error"
}

// truncate shortens s to max runes, appending … if cut.
func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", "↵")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// truncateLines keeps at most maxLines lines of s, appending a count hint.
func truncateLines(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n") +
		fmt.Sprintf("\n… (%d more lines)", len(lines)-maxLines)
}

// formatElapsed converts a duration to a human-readable string.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.0fms", float64(d.Milliseconds()))
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", m, s)
}

// ── public print helpers ─────────────────────────────────────────────────────

// PrintUser renders the user's prompt line.
func (r *Renderer) PrintUser(msg string) {
	prompt := styled(r.noColor, ansiBold+ansiBlue, "❯")
	r.writeln(fmt.Sprintf("\n%s %s", prompt, msg))
}

// PrintInfo renders a dim informational line.
func (r *Renderer) PrintInfo(msg string) {
	r.writeln(styled(r.noColor, ansiDim, msg))
}

// PrintError renders a red error line.
func (r *Renderer) PrintError(err error) {
	r.writeln(styled(r.noColor, ansiRed, "error: "+err.Error()))
}

// PrintBanner renders the startup banner.
func (r *Renderer) PrintBanner(model, cwd string) {
	w := termWidth()
	bar := styled(r.noColor, ansiBrightGreen, strings.Repeat("─", w))
	r.writeln(bar)
	r.writeln(styled(r.noColor, ansiBold, "  modu code"))
	r.writeln(styled(r.noColor, ansiDim, fmt.Sprintf("  model: %s", model)))
	r.writeln(styled(r.noColor, ansiDim, fmt.Sprintf("  cwd:   %s", cwd)))
	r.writeln(bar)
	r.writeln("")
	r.writeln(styled(r.noColor, ansiDim, "Type your message and press Enter. /help for commands, Ctrl+C to abort."))
	r.writeln("")
}

// PrintSeparator renders a turn separator.
// In Screen mode the static separator line at the bottom of the viewport
// already provides visual separation, so we just add a blank line to the
// scroll buffer (useful when scrolling back through history).
// In plain mode a full horizontal rule is drawn.
func (r *Renderer) PrintSeparator() {
	if r.screen != nil {
		r.writeln("")
		return
	}
	w := termWidth()
	r.writeln(styled(r.noColor, ansiBrightBlack, strings.Repeat("─", w)))
}

// PrintUsage renders a dim token usage hint.
func (r *Renderer) PrintUsage(totalTokens int) {
	if totalTokens <= 0 {
		return
	}
	r.writeln(styled(r.noColor, ansiDim, fmt.Sprintf("  tokens: %d", totalTokens)))
}
