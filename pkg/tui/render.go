package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/reflow/wrap"

	"github.com/openmodu/modu/pkg/types"
)

var uiANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// blockIndent is the left margin applied to every top-level scrollback
// glyph (>, ●, ⏺). Empty by default — bullets sit flush at column 0,
// matching the Claude Code layout. Continuation / hook padding
// (assistantPad, dotPad, hookPad) derives from this width, so adjusting
// it here shifts every block prefix and its continuation uniformly.
const blockIndent = ""

// dotPadW is the visual cell-width of "● " (● may be 2 cells in CJK terminals).
var dotPadW = lipgloss.Width("● ")

// hookStr is the raw connector string: blockIndent + 2 spaces + ⎿ + 1 space.
// The extra 2 spaces nest the hook glyph one step under the parent dot.
const hookStr = blockIndent + "  ⎿ "

// hookPadW is the visual width of hookStr.
var hookPadW = lipgloss.Width(hookStr)

// dotPad aligns continuation lines to the widest of the two prefixes.
var dotPad = strings.Repeat(" ", max(lipgloss.Width(blockIndent)+dotPadW, hookPadW))

// hookPad renders the ⎿ connector at fixed width.
var hookPad = uiDimText.Render(hookStr)

// assistantPad keeps assistant continuation lines aligned with the first
// content character after the leading "blockIndent ● ".
var assistantPad = strings.Repeat(" ", lipgloss.Width(blockIndent)+dotPadW)

// ─── View fragments ──────────────────────────────

func (m *uiModel) renderActivityLine() string {
	spinner := activitySpinner(time.Now())
	hint := "Enter follow-up • Shift+Enter or /s steer • esc interrupt"
	if !m.queryStartTime.IsZero() {
		elapsed := formatActivityDuration(time.Since(m.queryStartTime))
		if elapsed != "" {
			hint = fmt.Sprintf("%s • Enter follow-up • Shift+Enter or /s steer • esc interrupt", elapsed)
		}
	}
	return "  " + uiDimText.Render(spinner+" Working ("+hint+")")
}

func (m *uiModel) finishActivity(err error) string {
	if m.queryStartTime.IsZero() {
		m.clearActivity()
		return ""
	}
	elapsed := formatActivityDuration(time.Since(m.queryStartTime))
	if elapsed == "" {
		elapsed = "0s"
	}
	var summary string
	if err != nil {
		summary = "× Interrupted (" + elapsed + ")"
	} else {
		summary = "✓ Completed (" + elapsed + ")"
	}
	m.setTransientActivity("  " + uiDimText.Render(summary))
	return summary
}

func activitySpinner(now time.Time) string {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	idx := int(now.UnixMilli()/120) % len(frames)
	return frames[idx]
}

func formatActivityDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int(d.Round(time.Second).Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if seconds == 0 {
		return fmt.Sprintf("%dmin", minutes)
	}
	return fmt.Sprintf("%dmin %02ds", minutes, seconds)
}

func (m *uiModel) renderInputMeta() string {
	var parts []string
	if m.model != nil {
		modelText := m.model.Name
		if modelText == "" {
			modelText = m.model.ID
		}
		if m.model.ProviderID != "" && !strings.Contains(modelText, "(") {
			modelText += " (" + m.model.ProviderID + ")"
		}
		if strings.TrimSpace(modelText) != "" {
			parts = append(parts, modelText)
		}
	}
	if m.session != nil {
		cwd := m.session.RuntimeState().Cwd
		if cwd != "" {
			cwd = shortenUIPath(cwd)
			parts = append(parts, cwd)
		}
	}
	if m.tgUsername != "" {
		parts = append(parts, "@"+m.tgUsername)
	}
	return strings.Join(parts, "  ·  ")
}

// ─── Conversation rendering ───────────────────────

// renderUIUserBlock renders submitted user prompts with the ❯ glyph inside
// the uiUserPrompt background block (Padding(0,1) supplies the surrounding
// gutter). The glyph is part of the styled content so the highlight covers it
// uniformly; continuation lines use a two-space placeholder to keep the text
// column aligned under ❯.
func renderUIUserBlock(content string, width int) string {
	return renderUIUserBlockWithSource(content, "", width)
}

func renderUIUserBlockWithSource(content, source string, width int) string {
	return renderUIUserBlockWithQueueState(content, source, "", width)
}

func renderUIUserBlockWithQueueState(content, source, queueState string, width int) string {
	var b strings.Builder
	glyph := "❯ "
	style := uiUserPrompt
	switch strings.TrimSpace(source) {
	case "", "local":
	case "followup":
		glyph = "↳ "
	case "steer":
		glyph = "↯ "
		style = uiExternalUserPrompt
	default:
		glyph = "◆ "
		style = uiExternalUserPrompt
	}
	prefix := queueStatePrefix(queueState)
	glyphW := lipgloss.Width(glyph)
	leadW := glyphW + lipgloss.Width(prefix)
	// uiUserPrompt has no padding; only the two-cell ❯-glyph eats inline width.
	avail := max(8, width-leadW)
	first := true
	for _, rawLine := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if strings.TrimSpace(rawLine) == "" {
			b.WriteString("\n")
			continue
		}
		for _, seg := range wrapSegments(rawLine, avail) {
			lead := strings.Repeat(" ", leadW)
			if first {
				lead = glyph + prefix
				first = false
			}
			b.WriteString(blockIndent + style.Render(lead+seg) + "\n")
		}
	}
	return b.String()
}

func queueStatePrefix(queueState string) string {
	switch strings.TrimSpace(queueState) {
	case "queued":
		return "[queued] "
	case "running":
		return "[running] "
	case "done":
		return "[done] "
	case "failed":
		return "[failed] "
	case "interrupted":
		return "[interrupted] "
	default:
		return ""
	}
}

func renderUIAssistantBlock(content string, width int) string {
	var b strings.Builder
	writeWrappedStyledLines(&b, content, max(12, width-lipgloss.Width(assistantPad)), blockIndent+uiWhiteText.Render("●")+" ", assistantPad, lipgloss.NewStyle())
	return b.String()
}

func renderUIAssistantMarkdownBlock(content string, width int) string {
	var b strings.Builder
	writeWrappedStyledLines(&b, content, widthForPrefix(width), blockIndent+uiWhiteText.Render("●")+" ", assistantPad, lipgloss.NewStyle())
	return strings.TrimRight(b.String(), "\n")
}

// normalizeRenderedMarkdown collapses ANSI-padded blank lines that glamour
// emits between blocks into a single empty line, but preserves ANSI styling
// on lines that carry visible content. Stripping styling here would throw
// away glamour's heading/code/emphasis colors and reduce the agent's reply
// to plain text.
func normalizeRenderedMarkdown(content string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		visible := strings.TrimSpace(uiANSIPattern.ReplaceAllString(line, ""))
		if visible == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func renderUIThinking(content string, width int) string {
	var b strings.Builder
	b.WriteString(blockIndent + uiSecondaryText.Render("●") + " " + uiMutedText.Render("thinking") + "\n")
	writeWrappedStyledLines(&b, content, widthForPrefix(width), hookPad, dotPad, uiMutedText)
	return b.String()
}

func renderUITool(tool *uiToolState, expanded bool, width int) string {
	var b strings.Builder
	w := width

	dot := uiDimText.Render("⏺")
	if tool.Status == "done" {
		dot = uiSuccessText.Render("⏺")
	} else if tool.IsError || tool.Status == "error" {
		dot = uiErrorText.Render("⏺")
	}
	boldName := lipgloss.NewStyle().Bold(true)

	args := ""
	if tool.Input != "" {
		args = uiMutedText.Render("(" + truncateUI(tool.Input, 80) + ")")
	}
	b.WriteString(fmt.Sprintf("%s%s %s%s\n", blockIndent, dot, boldName.Render(tool.Name), args))

	if tool.Status == "running" {
		return b.String()
	}

	if tool.IsError && tool.Output != "" {
		errLines := strings.Split(tool.Output, "\n")
		for i, line := range errLines {
			pad := dotPad
			if i == 0 {
				pad = hookPad
			}
			if i >= 5 {
				b.WriteString(dotPad + uiErrorText.Render(fmt.Sprintf("... +%d more lines", len(errLines)-5)) + "\n")
				break
			}
			writeWrappedStyledLines(&b, line, widthForPrefix(w), pad, dotPad, uiErrorText)
		}
		return b.String()
	}

	if tool.Output == "" {
		return b.String()
	}

	b.WriteString(renderUIToolOutput(tool.Name, tool.Output, tool.FilePath, expanded, w))
	return b.String()
}

func renderUIToolOutput(toolName, output, filePath string, expanded bool, w int) string {
	var b strings.Builder
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	collapsedMax := 3
	expandedMax := 30
	rowWidth := widthForPrefix(w)

	maxRows := collapsedMax
	if expanded {
		maxRows = expandedMax
	}

	appendMoreHint := func(remaining int) {
		if remaining > 0 {
			b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines (ctrl+o to expand)", remaining)) + "\n")
		}
	}

	switch toolName {
	case "read":
		used, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if expanded {
			if remaining > 0 {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
			}
		} else if used > 0 {
			appendMoreHint(remaining)
		}

	case "bash":
		_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if !expanded {
			appendMoreHint(remaining)
		} else if remaining > 0 {
			b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
		}

	case "edit":
		if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
			b.WriteString(hookPad + uiSuccessText.Render("updated") + "\n")
		} else {
			// Edit-tool output layout (from pkg/coding_agent/tools/edit.go):
			//   line 0   : "Successfully edited <path> (N replacement(s))"
			//   line 1   : "" (blank separator)
			//   line 2-4 : "--- path", "+++ path", "@@ -a,b +c,d @@" (unidiff metadata)
			//   rest     : context (" "), removed ("-"), added ("+") lines
			//
			// Claude Code layout: hook shows a concise `Updated <path> (+N -M)`
			// summary; diff rows render lineno-first with the marker between
			// gutter and content; metadata + blank gap are dropped.
			rest := lines
			if strings.HasPrefix(lines[0], "Successfully edited") {
				rest = lines[1:] // strip the tool-generated success line; we synthesize our own summary
			}
			diffLines := make([]string, 0, len(rest))
			for _, line := range rest {
				if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "@@") {
					continue
				}
				if len(diffLines) == 0 && line == "" {
					continue // skip the blank separator that follows the summary
				}
				diffLines = append(diffLines, line)
			}

			// Pre-pass: count +/- and find max lineno digit width for gutter
			// alignment across context (' ') and ± rows.
			var addCount, removeCount, linenoWidth int
			for _, line := range diffLines {
				m, lineno, _ := parseEditDiffLine(line)
				if l := len(lineno); l > linenoWidth {
					linenoWidth = l
				}
				switch m {
				case '+':
					addCount++
				case '-':
					removeCount++
				}
			}

			b.WriteString(hookPad + uiSuccessText.Render(editSummary(addCount, removeCount)) + "\n")

			editMax := maxRows
			if !expanded && editMax < 10 {
				editMax = 10 // give the diff enough room to actually be visible
			}
			lexer := lexerForPath(filePath)
			usedRows := 0
			remaining := 0
			// Row width to fill with bg tint on ± lines so the highlight extends
			// to the column edge instead of stopping mid-row. lipgloss.Width
			// strips ANSI before measuring, matching what the terminal sees.
			tintWidth := lipgloss.Width(dotPad) + rowWidth
			for lineIdx := 0; lineIdx < len(diffLines) && usedRows < editMax; lineIdx++ {
				marker, _, _ := parseEditDiffLine(diffLines[lineIdx])
				firstPrefix, restPrefix, content := styleEditDiffRow(diffLines[lineIdx], lexer, linenoWidth)
				segments := wrapSegments(content, rowWidth)
				lineComplete := true
				for segIdx, seg := range segments {
					if usedRows >= editMax {
						remaining += len(segments) - segIdx
						lineComplete = false
						break
					}
					prefix := dotPad + restPrefix
					if segIdx == 0 {
						prefix = dotPad + firstPrefix
					}
					row := prefix + seg
					row = tintDiffRow(row, marker, tintWidth)
					b.WriteString(row + "\n")
					usedRows++
				}
				if !lineComplete {
					remaining += wrappedLineCount(diffLines[lineIdx+1:], rowWidth)
					break
				}
				if lineIdx == len(diffLines)-1 {
					break
				}
				remaining = wrappedLineCount(diffLines[lineIdx+1:], rowWidth)
			}
			if expanded {
				if remaining > 0 {
					b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
				}
			} else {
				appendMoreHint(remaining)
			}
		}

	case "write":
		if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
			b.WriteString(hookPad + uiSuccessText.Render("written") + "\n")
		} else {
			_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
			if expanded {
				if remaining > 0 {
					b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
				}
			} else {
				appendMoreHint(remaining)
			}
		}

	case "glob":
		_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if expanded {
			if remaining > 0 {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
			}
		} else {
			appendMoreHint(remaining)
		}

	case "grep":
		_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if expanded {
			if remaining > 0 {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
			}
		} else {
			appendMoreHint(remaining)
		}

	default:
		_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if expanded {
			if remaining > 0 {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
			}
		} else {
			appendMoreHint(remaining)
		}
	}
	return b.String()
}

func renderUISection(title, content string, width int) string {
	var b strings.Builder
	b.WriteString("  " + uiPrimaryText.Render(title) + "\n")
	contentWidth := max(12, width-lipgloss.Width("  "))
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if key, value, ok := splitSectionKeyValue(line); ok {
			b.WriteString("  " + uiDimText.Render(key+":") + " " + value + "\n")
			continue
		}
		writeWrappedStyledLines(&b, line, contentWidth, "  ", "  ", lipgloss.NewStyle())
	}
	return strings.TrimRight(b.String(), "\n")
}

func splitSectionKeyValue(line string) (string, string, bool) {
	if strings.TrimLeft(line, " \t") != line {
		return "", "", false
	}
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	value := strings.TrimSpace(line[idx+1:])
	if key == "" || len([]rune(key)) > 40 {
		return "", "", false
	}
	for _, r := range key {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case ' ', '-', '_', '.', '/':
			continue
		default:
			return "", "", false
		}
	}
	return key, value, true
}

func (m *uiModel) renderSingleBlock(block uiBlock) string {
	viewWidth := m.viewportContentWidth()
	contentWidth := max(20, viewWidth-max(dotPadW, hookPadW))
	noMargin := uint(0)
	style := glamourstyles.DarkStyleConfig
	style.Document = glamouransi.StyleBlock{
		StylePrimitive: style.Document.StylePrimitive,
		Margin:         &noMargin,
	}
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(contentWidth),
	)
	var s string
	switch block.Kind {
	case "user":
		s = renderUIUserBlockWithQueueState(block.Content, block.Source, block.QueueState, viewWidth)
	case "assistant":
		var ab strings.Builder
		if block.Thinking != "" {
			ab.WriteString(renderUIThinking(block.Thinking, viewWidth))
		}
		if strings.TrimSpace(block.Content) != "" {
			if renderer != nil {
				if md, err := renderer.Render(block.Content); err == nil {
					md = normalizeRenderedMarkdown(md)
					ab.WriteString(renderUIAssistantMarkdownBlock(md, viewWidth))
				} else {
					ab.WriteString(renderUIAssistantBlock(wrap.String(block.Content, contentWidth), viewWidth))
				}
			} else {
				ab.WriteString(renderUIAssistantBlock(wrap.String(block.Content, contentWidth), viewWidth))
			}
		}
		s = ab.String()
	case "tool":
		var tb strings.Builder
		for _, tool := range block.Tools {
			tb.WriteString(renderUITool(tool, m.transcriptMode, viewWidth))
		}
		s = tb.String()
	case "section":
		s = renderUISection(block.Title, block.Content, viewWidth)
	default:
		var db strings.Builder
		for _, line := range strings.Split(block.Content, "\n") {
			var lb strings.Builder
			writeWrappedStyledLines(&lb, line, max(12, viewWidth-lipgloss.Width("  ")), "  ", "  ", uiDimText)
			db.WriteString(lb.String())
		}
		s = db.String()
	}
	s = strings.TrimRight(s, "\n")
	return hardWrapRenderedText(s, viewWidth)
}

// ─── Exit rendering ───────────────────────────────

func (m *uiModel) renderExitSessionMeta() string {
	var parts []string
	if m.session != nil {
		stats := m.session.GetSessionStats()
		if stats.TotalTokens > 0 {
			parts = append(parts, fmt.Sprintf("~%d tokens", stats.TotalTokens))
		}
		if m.session.IsPlanMode() {
			parts = append(parts, "plan")
		}
		if m.session.ActiveWorktree() != "" {
			parts = append(parts, "worktree")
		}
	}
	if m.transcriptMode {
		parts = append(parts, "expanded")
	}
	if len(parts) == 0 {
		return ""
	}
	return "session: " + strings.Join(parts, " | ")
}

// ─── Block helpers ───────────────────────────────

func assistantMessageFromEvent(msg types.AgentMessage) (types.AssistantMessage, bool) {
	switch v := msg.(type) {
	case types.AssistantMessage:
		return v, true
	case *types.AssistantMessage:
		if v != nil {
			return *v, true
		}
	}
	return types.AssistantMessage{}, false
}

// ─── Tool input formatting ───────────────────────

func formatToolInput(toolName string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch toolName {
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			if idx := strings.Index(cmd, "\n"); idx > 0 {
				return cmd[:idx] + "..."
			}
			return cmd
		}
	case "read":
		// File tools use "path" (see pkg/coding_agent/tools/read.go). The TUI
		// previously read "file_path" here, which silently never matched and
		// fell through to the noisy key=value fallback.
		if fp, ok := args["path"].(string); ok {
			s := shortenUIPath(fp)
			if offset, ok := args["offset"].(float64); ok && offset > 0 {
				s += fmt.Sprintf(":%d", int(offset))
			}
			return s
		}
	case "write":
		if fp, ok := args["path"].(string); ok {
			return shortenUIPath(fp)
		}
	case "edit":
		// Claude Code style: tool header only shows the file path. The diff
		// summary line below carries the +/- counts, so embedding a quoted
		// old_text snippet here is redundant and frequently wraps to multi-row.
		if fp, ok := args["path"].(string); ok {
			return shortenUIPath(fp)
		}
	case "glob":
		if p, ok := args["pattern"].(string); ok {
			path, _ := args["path"].(string)
			if path != "" {
				return p + " in " + shortenUIPath(path)
			}
			return p
		}
	case "grep":
		if p, ok := args["pattern"].(string); ok {
			path, _ := args["path"].(string)
			if path != "" {
				return fmt.Sprintf("%q in %s", p, shortenUIPath(path))
			}
			return fmt.Sprintf("%q", p)
		}
	case "web_fetch":
		if u, ok := args["url"].(string); ok {
			return u
		}
	case "web_search":
		if q, ok := args["query"].(string); ok {
			return fmt.Sprintf("%q", q)
		}
	case "agent":
		if desc, ok := args["description"].(string); ok && desc != "" {
			return desc
		}
		if prompt, ok := args["prompt"].(string); ok {
			if len(prompt) > 60 {
				prompt = prompt[:60] + "..."
			}
			return prompt
		}
	}
	// Fallback: sort keys and render key=value
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, args[key]))
	}
	s := strings.Join(parts, " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func shortenUIPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// ─── Result text extraction ──────────────────────

func fullResultText(ev types.Event) string {
	if ev.Result == nil {
		return ""
	}
	// Primary path: AgentToolResult carries a typed content slice.
	if r, ok := ev.Result.(types.ToolResult); ok {
		return contentBlocksText(r.Content)
	}
	// Fallback paths for legacy or third-party tool implementations.
	if texts, ok := ev.Result.([]types.ContentBlock); ok {
		return contentBlocksText(texts)
	}
	switch v := ev.Result.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		raw, _ := json.Marshal(v)
		return strings.TrimSpace(string(raw))
	}
}

func contentBlocksText(blocks []types.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if t, ok := block.(*types.TextContent); ok && t != nil {
			if s := strings.TrimSpace(t.Text); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// ─── Utility ─────────────────────────────────────

func widthForPrefix(totalWidth int) int {
	return max(12, totalWidth-max(dotPadW, hookPadW))
}

func (m *uiModel) viewportContentWidth() int {
	return max(20, m.width-2)
}

func hardWrapRenderedText(text string, width int) string {
	width = max(8, width)
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, strings.Split(xansi.Hardwrap(line, width, true), "\n")...)
	}
	return strings.Join(out, "\n")
}

func writeWrappedStyledLines(b *strings.Builder, text string, width int, firstPrefix, restPrefix string, style lipgloss.Style) {
	width = max(8, width)
	first := true
	for _, rawLine := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(rawLine) == "" {
			b.WriteString("\n")
			continue
		}
		wrapped := wrap.String(rawLine, width)
		for _, part := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
			prefix := restPrefix
			if first {
				prefix = firstPrefix
				first = false
			}
			b.WriteString(prefix + style.Render(part) + "\n")
		}
	}
}

func wrapSegments(rawLine string, width int) []string {
	width = max(8, width)
	if strings.TrimSpace(rawLine) == "" {
		return []string{""}
	}
	wrapped := wrap.String(rawLine, width)
	parts := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}

func wrappedLineCount(lines []string, width int) int {
	total := 0
	for _, line := range lines {
		total += len(wrapSegments(line, width))
	}
	return total
}

func writeWrappedStyledRows(b *strings.Builder, lines []string, width int, firstPrefix, restPrefix string, style lipgloss.Style, maxRows int) (used int, remaining int) {
	maxRows = max(1, maxRows)
	first := true
	for i, line := range lines {
		segments := wrapSegments(line, width)
		for j, seg := range segments {
			if used >= maxRows {
				remaining += len(segments) - j
				remaining += wrappedLineCount(lines[i+1:], width)
				return used, remaining
			}
			prefix := restPrefix
			if first {
				prefix = firstPrefix
				first = false
			}
			b.WriteString(prefix + style.Render(seg) + "\n")
			used++
		}
	}
	return used, remaining
}

func writeSingleWrappedRowBudget(b *strings.Builder, line string, width int, firstPrefix, restPrefix string, style lipgloss.Style, budget int) (used int, complete bool, hidden int) {
	segments := wrapSegments(line, width)
	budget = max(0, budget)
	for i, seg := range segments {
		if used >= budget {
			return used, false, len(segments) - i
		}
		prefix := restPrefix
		if i == 0 {
			prefix = firstPrefix
		}
		b.WriteString(prefix + style.Render(seg) + "\n")
		used++
	}
	return used, true, 0
}

func truncateUI(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= maxLen {
		return s
	}
	if maxLen == 1 {
		return string(rs[:1])
	}
	return string(rs[:maxLen-1]) + "…"
}

func extractThinkText(raw string) (thinking string, visible string) {
	const openTag = "<think>"
	const closeTag = "</think>"

	var thinkParts []string
	var visibleParts strings.Builder
	rest := raw

	for {
		start := strings.Index(rest, openTag)
		if start < 0 {
			visibleParts.WriteString(rest)
			break
		}

		visibleParts.WriteString(rest[:start])
		rest = rest[start+len(openTag):]

		end := strings.Index(rest, closeTag)
		if end < 0 {
			break
		}

		chunk := strings.TrimSpace(rest[:end])
		if chunk != "" {
			thinkParts = append(thinkParts, chunk)
		}
		rest = rest[end+len(closeTag):]
	}

	return strings.Join(thinkParts, "\n"), visibleParts.String()
}
