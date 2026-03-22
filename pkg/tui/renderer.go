package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
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
//	● text response              ← dim grey bullet, streamed inline
//	✧ Thinking…                  ← header text
//	  ⎿  content                 ← text streamed with ⎿ indent
//	● ToolName("arg")           ← green bullet, while tool is running
//	● ToolName("arg") → result  ← green bullet, after tool completes
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

	// Ctrl+R expand state.
	// expandIdx is the index into toolHistory currently displayed (-1 = none).
	// Each Ctrl+R press cycles one step older; wraps around to most recent.
	// expandShown is true in Screen mode when the expanded view is drawn and
	// should be erased before the next expand.
	expandIdx        int
	expandShown      bool
	expandMarkLines   int
	expandMarkPending string

	// Thinking content streaming state.
	// thinkNeedIndent is true when the next non-newline character needs a
	// leading indent ("  ") to align with the vertical bar prefix.
	// thinkPendingNL defers writing \n until the next non-newline content
	// arrives so trailing newlines are silently discarded at block end.
	thinkNeedIndent bool
	thinkPendingNL  bool

	// Inline-mode sticky-prompt state.
	// When the user is typing (or waiting during streaming), promptText holds
	// the text to keep painted at the bottom of the terminal.  write() erases
	// it before any AI output and repaints it after each complete line.
	promptMu    sync.Mutex
	promptText  string // empty = no active prompt
	promptShown bool   // true when promptText is currently drawn on screen
}

// NewRenderer creates a plain-mode Renderer writing to out.
func NewRenderer(out io.Writer) *Renderer {
	r := &Renderer{out: out, noColor: shouldDisableColor(out)}
	r.md = newMDWriter(r.noColor, func(text string) { r.write(text) })
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
		return
	}
	// In inline mode stdin is kept in raw mode during ReadLine and RunScrollLoop.
	// Raw mode disables the tty's ONLCR flag, so a bare \n only moves the cursor
	// down without resetting to column 1.  Always emit \r\n to be safe.
	if strings.Contains(text, "\n") {
		text = strings.ReplaceAll(text, "\r\n", "\n") // normalise existing CRLF
		text = strings.ReplaceAll(text, "\n", "\r\n") // then expand all LF→CRLF
	}
	r.promptMu.Lock()
	defer r.promptMu.Unlock()
	// Erase the prompt before writing AI content.
	if r.promptShown {
		fmt.Fprint(r.out, "\r\033[2K")
		r.promptShown = false
	}
	fmt.Fprint(r.out, text)
	// Repaint the prompt after each complete line.
	if r.promptText != "" && strings.HasSuffix(text, "\r\n") {
		fmt.Fprintf(r.out, "\r\033[2K%s", r.promptText)
		r.promptShown = true
	}
}

func (r *Renderer) writeln(text string) { r.write(text + "\n") }

// SetActivePrompt registers the prompt text that should stay painted at the
// bottom of the terminal during AI streaming.  Call with empty string to
// erase the prompt and deactivate.  Thread-safe.
func (r *Renderer) SetActivePrompt(text string) {
	r.promptMu.Lock()
	defer r.promptMu.Unlock()
	r.promptText = text
	if text == "" {
		if r.promptShown {
			fmt.Fprint(r.out, "\r\033[2K")
			r.promptShown = false
		}
		return
	}
	// Erase current line and draw the new prompt text.
	fmt.Fprintf(r.out, "\r\033[2K%s", text)
	r.promptShown = true
}

// bullet returns the styled ● marker for text responses.
func (r *Renderer) bullet() string { return styled(r.noColor, ansiDim, "●") }

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
		// New tool: reset expand state so next Ctrl+R starts from this tool.
		r.expandIdx = -1
		r.expandShown = false

		callLine := r.toolCallLine(event.ToolName, event.Args)
		if event.Parallel {
			// Parallel tools: write call line only — no collapsible placeholder,
			// because multiple tools run concurrently and share the Screen slot.
			// Result lines will be appended by each tool as it finishes.
			r.write("\n" + callLine + "\n")
		} else {
			// Serial tool: Line 1 = call, Line 2 = replaceable ⎿ placeholder.
			resultPlaceholder := r.toolResultLine(false, false, "")
			if r.screen != nil {
				r.write("\n" + callLine + "\n")
				r.screen.WriteToolHeader(resultPlaceholder)
			} else {
				r.write("\n" + callLine + "\n")
			}
		}

	case agent.EventTypeToolExecutionEnd:
		full := fullResultText(event)
		r.toolHistory = append(r.toolHistory, toolRecord{
			name:    event.ToolName,
			args:    event.Args,
			result:  full,
			isError: event.IsError,
		})

		resultLine := r.toolResultLine(true, event.IsError, toolResult(event))
		if event.Parallel {
			// Parallel: always append the result line (no cursor-up replacement).
			r.write(resultLine + "\n")
			r.toolLines = 0
		} else if r.screen != nil {
			// Serial + screen: replace the ⎿ placeholder in-place.
			r.screen.CollapseToolHeader(resultLine)
		} else {
			r.write(resultLine + "\n")
			r.toolLines = 0
		}

		// Show a preview of the output inline (Claude Code style).
		if !event.IsError && full != "" {
			const previewMax = 10
			lines := strings.Split(strings.TrimRight(full, "\n"), "\n")
			shown := lines
			extra := 0
			// Edit diffs always show in full; other tools truncate.
			if event.ToolName != "edit" && len(lines) > previewMax {
				shown = lines[:previewMax]
				extra = len(lines) - previewMax
			}

			// Per-tool display: syntax highlight for read, diff colours for edit.
			var highlighted []string
			switch event.ToolName {
			case "read":
				var filePath string
				if m, ok := event.Args.(map[string]any); ok {
					filePath, _ = m["path"].(string)
				}
				highlighted = buildHighlightedPreview(shown, filePath, r.noColor)
			case "edit":
				highlighted = buildDiffPreview(shown, r.noColor)
			}

			for i, l := range shown {
				if highlighted != nil {
					r.writeln(highlighted[i])
				} else {
					// Strip "N  " line number prefix for read tool output.
					if event.ToolName == "read" {
						if idx := strings.Index(l, "  "); idx > 0 && idx < 10 {
							l = l[idx+2:]
						}
					}
					r.writeln(styled(r.noColor, ansiDim, "     "+l))
				}
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
				// We don't have a full tag yet.
				// Find if there's a partial "</think>" tag at the end of the buffer.
				guard := 0
				for i := 1; i < len("</think>") && i <= len(r.textBuf); i++ {
					if strings.HasPrefix("</think>", r.textBuf[len(r.textBuf)-i:]) {
						guard = i
						break
					}
				}

				if len(r.textBuf) > guard {
					cut := len(r.textBuf) - guard
					for cut > 0 && cut < len(r.textBuf) && !utf8.RuneStart(r.textBuf[cut]) {
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
				// We don't have a full tag yet.
				// Find if there's a partial "<think>" tag at the end of the buffer.
				// We only need to guard the characters if the buffer *ends* with a prefix of "<think>".
				guard := 0
				for i := 1; i < len("<think>") && i <= len(r.textBuf); i++ {
					if strings.HasPrefix("<think>", r.textBuf[len(r.textBuf)-i:]) {
						guard = i
						break
					}
				}
				
				if len(r.textBuf) > guard {
					cut := len(r.textBuf) - guard
					for cut > 0 && cut < len(r.textBuf) && !utf8.RuneStart(r.textBuf[cut]) {
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
			// Process any remaining text in the buffer as thinking content immediately
			continue
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
//	● ToolName(arg)               ← line 1: permanent
//	  ⎿  summary (ctrl+r)         ← line 2: replaced in-place on completion

// toolCallLine returns line 1: "● ToolName(arg)".
func (r *Renderer) toolCallLine(name string, args any) string {
	bullet := styled(r.noColor, ansiBrightGreen, "●")
	nameStr := styled(r.noColor, ansiBold, name)
	arg := primaryArg(name, args)
	argStr := styled(r.noColor, ansiDim, "("+arg+")")
	return fmt.Sprintf("%s %s%s", bullet, nameStr, argStr)
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

// ExpandLastTool shows the full args+result of a tool call.  Called on Ctrl+R.
//
// Each press cycles one step older through the tool history (most-recent first,
// wrapping back around).  In Screen mode the previous expand is erased before
// drawing the next one.  In inline mode expands are append-only (content cannot
// be deleted from the scroll buffer).
func (r *Renderer) ExpandLastTool() {
	n := len(r.toolHistory)
	if n == 0 {
		r.writeln(styled(r.noColor, ansiDim, "(no tool calls yet)"))
		return
	}

	// Screen mode: erase any currently-visible expand before showing the next.
	if r.screen != nil && r.expandShown {
		r.screen.TrimToMark(r.expandMarkLines, r.expandMarkPending)
		r.expandShown = false
	}

	// Advance index: first press → most recent; each subsequent press → one older.
	if r.expandIdx < 0 {
		r.expandIdx = n - 1
	} else {
		r.expandIdx = (r.expandIdx - 1 + n) % n
	}

	if r.screen != nil {
		r.expandMarkLines, r.expandMarkPending = r.screen.ContentMark()
		r.expandShown = true
	}

	rec := r.toolHistory[r.expandIdx]
	pos := r.expandIdx + 1 // 1-based position
	w := termWidth()

	// Header: "─── bash (2/5) ─────"
	header := fmt.Sprintf(" %s (%d/%d) ", rec.name, pos, n)
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

	// Footer with navigation hint.
	var hint string
	if n == 1 {
		hint = "ctrl+r to close"
	} else {
		next := (r.expandIdx - 1 + n) % n
		hint = fmt.Sprintf("ctrl+r → %s (%d/%d)", r.toolHistory[next].name, next+1, n)
	}
	hintStyled := styled(r.noColor, ansiBrightBlack, hint)
	dashCount := w - visibleLen(hintStyled) - 1
	if dashCount < 1 {
		dashCount = 1
	}
	r.writeln(styled(r.noColor, ansiBrightBlack, strings.Repeat("─", dashCount)) + " " + hintStyled)
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
	return strings.Trim(extractResultText(event), "\n")
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

// ClearLine erases the current terminal line (\r\033[2K). Call this before
// writing external content when a raw readline prompt may be on screen,
// to prevent output from appearing after the ❯ prompt on the same line.
func (r *Renderer) ClearLine() {
	r.promptMu.Lock()
	defer r.promptMu.Unlock()
	fmt.Fprint(r.out, "\r\033[2K")
	r.promptShown = false
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
func (r *Renderer) PrintSeparator() {
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
