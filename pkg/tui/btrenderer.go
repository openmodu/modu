package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// ── lipgloss styles ───────────────────────────────────────────────────────────

var (
	// Agent output
	bsTextBullet     = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	bsTextBulletDone = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	bsToolBullet = lipgloss.NewStyle().Foreground(lipgloss.Color("#4CAF50"))
	bsToolName   = lipgloss.NewStyle().Foreground(lipgloss.Color("#88CC88")).Bold(true)
	bsToolArg    = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	bsToolRes    = lipgloss.NewStyle().Foreground(lipgloss.Color("#777777"))
	bsToolResErr = lipgloss.NewStyle().Foreground(lipgloss.Color("#CC5555"))
	bsToolResBar = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	bsThinkHdr   = lipgloss.NewStyle().Foreground(lipgloss.Color("#CC8833"))
	bsThinkBody  = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Italic(true)
	bsDim        = lipgloss.NewStyle().Foreground(lipgloss.Color("#555555"))
	bsPreview    = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	bsElapsed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

	// Print helpers
	bsInfo        = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	bsError       = lipgloss.NewStyle().Foreground(lipgloss.Color("#CC4444"))
	bsUserPrompt  = lipgloss.NewStyle().Foreground(lipgloss.Color("#4488EE")).Bold(true)
	bsSep         = lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	bsTokens      = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	bsBannerTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF88")).Bold(true)
	bsBannerMeta  = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	bsBannerBar   = lipgloss.NewStyle().Foreground(lipgloss.Color("#007744"))

	// Markdown inline
	bsMdBold  = lipgloss.NewStyle().Bold(true)
	bsMdItal  = lipgloss.NewStyle().Italic(true)
	bsMdCode  = lipgloss.NewStyle().Foreground(lipgloss.Color("#55CCAA"))
	bsMdH1    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#AAFFCC"))
	bsMdH2    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#CCFFEE"))
	bsMdH3    = lipgloss.NewStyle().Bold(true)
	bsMdBul   = lipgloss.NewStyle().Foreground(lipgloss.Color("#55AA55"))
	bsMdCBar  = lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	bsMdCLine = lipgloss.NewStyle().Foreground(lipgloss.Color("#88AACC"))
	bsMdBq    = lipgloss.NewStyle().Foreground(lipgloss.Color("#777777")).Italic(true)
	bsMdHRule = lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))

	// Expand
	bsExpandHdr = lipgloss.NewStyle().Foreground(lipgloss.Color("#44AA66")).Bold(true)
	bsExpandKey = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	bsExpandOut = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
)

// ── btMDWriter ─────────────────────────────────────────────────────────────────

// btMDWriter is a streaming Markdown renderer that emits lipgloss-styled text.
// It mirrors the line-buffering behaviour of mdWriter but uses lipgloss styles.
type btMDWriter struct {
	out       func(string)
	lineBuf   string
	inCode    bool
	codeLang  string
	width     int
	indent    string // prefix added to every rendered line
	firstLine bool   // if true, skip indent once (first line already follows the bullet)
}

func newBTMDWriter(width int, out func(string)) *btMDWriter {
	if width <= 4 {
		width = 80
	}
	return &btMDWriter{out: out, width: width}
}

func (m *btMDWriter) SetWidth(w int) {
	if w > 4 {
		m.width = w
	}
}

func (m *btMDWriter) Feed(text string) {
	m.lineBuf += text
	for {
		idx := strings.IndexByte(m.lineBuf, '\n')
		if idx < 0 {
			break
		}
		m.renderLine(m.lineBuf[:idx])
		m.lineBuf = m.lineBuf[idx+1:]
	}
}

func (m *btMDWriter) Flush() {
	if m.lineBuf == "" {
		return
	}
	m.renderLine(m.lineBuf)
	m.lineBuf = ""
}

func (m *btMDWriter) Reset() {
	m.lineBuf = ""
	m.inCode = false
	m.codeLang = ""
	m.indent = ""
	m.firstLine = false
}

func (m *btMDWriter) codeBarWidth() int {
	w := m.width - 4
	if w < 4 {
		w = 4
	}
	return w
}

func (m *btMDWriter) pfx() string {
	if m.firstLine {
		m.firstLine = false
		return ""
	}
	return m.indent
}

func (m *btMDWriter) renderLine(line string) {
	w := m.codeBarWidth()

	p := m.pfx()

	// Fenced code block
	if strings.HasPrefix(line, "```") {
		if m.inCode {
			m.inCode = false
			m.codeLang = ""
			m.out(p + bsMdCBar.Render("┌"+strings.Repeat("─", w)) + "\n")
		} else {
			m.inCode = true
			m.codeLang = strings.TrimSpace(strings.TrimPrefix(line, "```"))
			if m.codeLang != "" {
				label := " " + m.codeLang + " "
				dashes := w - len([]rune(label))
				if dashes < 0 {
					dashes = 0
				}
				m.out(p + bsMdCBar.Render("┌"+label+strings.Repeat("─", dashes)) + "\n")
			} else {
				m.out(p + bsMdCBar.Render("┌"+strings.Repeat("─", w)) + "\n")
			}
		}
		return
	}
	if m.inCode {
		m.out(p + bsMdCBar.Render("│") + " " + bsMdCLine.Render(line) + "\n")
		return
	}

	// Horizontal rule
	if isHorizontalRule(line) {
		ruleW := m.width - len([]rune(p))
		if ruleW < 4 {
			ruleW = 4
		}
		m.out(p + bsMdHRule.Render(strings.Repeat("─", ruleW)) + "\n")
		return
	}

	// ATX headings
	if strings.HasPrefix(line, "#") {
		level := 0
		for _, r := range line {
			if r == '#' {
				level++
			} else {
				break
			}
		}
		if level <= 6 && len(line) > level && line[level] == ' ' {
			content := m.renderInline(strings.TrimSpace(line[level+1:]))
			switch level {
			case 1:
				m.out(p + bsMdH1.Render(content) + "\n")
			case 2:
				m.out(p + bsMdH2.Render(content) + "\n")
			case 3:
				m.out(p + bsMdH3.Render(content) + "\n")
			default:
				m.out(p + bsDim.Render(content) + "\n")
			}
			return
		}
	}

	// Blockquote
	if strings.HasPrefix(line, "> ") {
		m.out(p + bsMdBq.Render("│ "+m.renderInline(line[2:])) + "\n")
		return
	}

	// Unordered list (top-level and one level of indent)
	if len(line) >= 2 && (line[0] == '-' || line[0] == '*' || line[0] == '+') && line[1] == ' ' {
		m.out(p + bsMdBul.Render("•") + " " + m.renderInline(line[2:]) + "\n")
		return
	}
	if len(line) >= 4 && line[:2] == "  " && (line[2] == '-' || line[2] == '*' || line[2] == '+') && line[3] == ' ' {
		m.out(p + "  " + bsMdBul.Render("•") + " " + m.renderInline(line[4:]) + "\n")
		return
	}

	// Ordered list
	if num, rest, ok := parseOrderedList(line); ok {
		m.out(p + bsMdBul.Render(fmt.Sprintf("%d.", num)) + " " + m.renderInline(rest) + "\n")
		return
	}

	// Normal paragraph
	m.out(p + m.renderInline(line) + "\n")
}

func (m *btMDWriter) renderInline(s string) string {
	if s == "" {
		return s
	}
	var out strings.Builder
	i := 0
	n := len(s)
	for i < n {
		// Inline code: `...`
		if s[i] == '`' {
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				out.WriteString(bsMdCode.Render("`" + s[i+1:i+1+j] + "`"))
				i = i + 1 + j + 1
				continue
			}
		}
		// Bold: **...**
		if i+1 < n && s[i] == '*' && s[i+1] == '*' {
			if j := strings.Index(s[i+2:], "**"); j >= 0 {
				out.WriteString(bsMdBold.Render(s[i+2 : i+2+j]))
				i = i + 2 + j + 2
				continue
			}
		}
		if i+1 < n && s[i] == '_' && s[i+1] == '_' {
			if j := strings.Index(s[i+2:], "__"); j >= 0 {
				out.WriteString(bsMdBold.Render(s[i+2 : i+2+j]))
				i = i + 2 + j + 2
				continue
			}
		}
		// Italic: *...*
		if s[i] == '*' && (i+1 >= n || s[i+1] != '*') {
			if j := strings.IndexByte(s[i+1:], '*'); j >= 0 && s[i+1+j] != '*' {
				text := s[i+1 : i+1+j]
				if text != "" && text[0] != ' ' {
					out.WriteString(bsMdItal.Render(text))
					i = i + 1 + j + 1
					continue
				}
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// ── tool record ───────────────────────────────────────────────────────────────

type btToolRecord struct {
	name    string
	args    any
	result  string
	isError bool
}

// ── BTRenderer ────────────────────────────────────────────────────────────────

// BTRenderer renders agent events to lipgloss-styled strings via an output
// callback.  It is a full bubbletea-native replacement for the ANSI Renderer.
//
// The callback is called from whichever goroutine drives agent events, so it
// must be goroutine-safe (use a buffered channel or tea.Program.Send).
type BTRenderer struct {
	width    int
	onOutput func(string)

	// per-turn state
	hadText         bool
	hadTool         bool
	inThink         bool
	inTextThink     bool
	textBuf         string
	thinkNeedIndent bool
	thinkPendingNL  bool
	turnStart       time.Time
	pendingApproval bool // true between approval-shown and tool-execution-start

	// tool history (Ctrl+R)
	toolHistory []btToolRecord
	expandIdx   int // -1 = none expanded

	// markdown renderer
	md *btMDWriter
}

// NewBTRenderer creates a BTRenderer.  width is the initial terminal column
// count; call SetWidth when the window resizes.  onOutput is called with each
// rendered text chunk (may be called from any goroutine).
func NewBTRenderer(width int, onOutput func(string)) *BTRenderer {
	if width <= 0 {
		width = 80
	}
	r := &BTRenderer{width: width, onOutput: onOutput, expandIdx: -1}
	r.md = newBTMDWriter(width, func(text string) { r.emit(text) })
	return r
}

// MarkApprovalShown tells the renderer that an approval prompt for the next
// tool call has already been displayed, so EventTypeToolExecutionStart should
// not repeat the tool call line.
func (r *BTRenderer) MarkApprovalShown() { r.pendingApproval = true }

// SetWidth updates the column count used for separators and code blocks.
func (r *BTRenderer) SetWidth(w int) {
	if w > 0 {
		r.width = w
		r.md.SetWidth(w)
	}
}

func (r *BTRenderer) emit(text string) {
	if r.onOutput != nil {
		r.onOutput(text)
	}
}
func (r *BTRenderer) emitln(text string) { r.emit(text + "\n") }

// ── public print helpers ──────────────────────────────────────────────────────

// ClearLine is a no-op in bubbletea mode; bubbletea manages the layout.
func (r *BTRenderer) ClearLine() {}

// PrintUser renders the user's submitted message.
func (r *BTRenderer) PrintUser(msg string) {
	r.emitln("\n" + bsUserPrompt.Render("❯") + " " + msg)
}

// PrintInfo renders a dim informational line.
func (r *BTRenderer) PrintInfo(msg string) { r.emitln(bsInfo.Render(msg)) }

// PrintError renders a red error line.
func (r *BTRenderer) PrintError(err error) {
	r.emitln(bsError.Render("error: " + err.Error()))
}

// PrintBanner renders the startup banner into the viewport content channel.
func (r *BTRenderer) PrintBanner(model, cwd, tgUsername string) {
	bar := bsBannerBar.Render(strings.Repeat("─", r.width))
	r.emitln(bar)
	r.emitln(bsBannerTitle.Render("  modu code"))
	r.emitln(bsBannerMeta.Render("  model: " + model))
	r.emitln(bsBannerMeta.Render("  cwd:   " + cwd))
	if tgUsername != "" {
		r.emitln(bsBannerMeta.Render("  telegram: @" + tgUsername))
	}
	r.emitln(bar)
	r.emitln(bsInfo.Render("  Type your message and press Enter. /help for commands."))
}

// PrintSeparator renders a turn separator line.
func (r *BTRenderer) PrintSeparator() {
	r.emitln(bsSep.Render(strings.Repeat("─", r.width)))
}

// PrintUsage renders a dim token-count hint.
func (r *BTRenderer) PrintUsage(totalTokens int) {
	if totalTokens <= 0 {
		return
	}
	r.emitln(bsTokens.Render(fmt.Sprintf("  tokens: %d", totalTokens)))
}

// ── event handler ─────────────────────────────────────────────────────────────

// HandleEvent processes an AgentEvent and emits styled output via onOutput.
func (r *BTRenderer) HandleEvent(ev agent.AgentEvent) {
	switch ev.Type {

	case agent.EventTypeAgentStart:
		r.hadText = false
		r.hadTool = false
		r.inThink = false
		r.inTextThink = false
		r.thinkNeedIndent = false
		r.thinkPendingNL = false
		r.textBuf = ""
		r.expandIdx = -1
		r.pendingApproval = false
		r.turnStart = time.Now()
		r.md.Reset()

	case agent.EventTypeMessageUpdate:
		if ev.StreamEvent == nil {
			return
		}
		switch ev.StreamEvent.Type {
		case types.EventThinkingStart:
			r.btFlushTextBuf()
			r.inThink = true
			r.thinkNeedIndent = true
			r.emit("\n" + bsThinkHdr.Render("✧") + " " + bsDim.Render("Thinking…") + "\n")
		case types.EventThinkingDelta:
			r.btWriteThinkContent(ev.StreamEvent.Delta)
		case types.EventThinkingEnd:
			r.inThink = false
			r.btWriteThinkClose()
		case types.EventTextDelta:
			r.btProcessTextDelta(ev.StreamEvent.Delta)
		}

	case agent.EventTypeMessageEnd:
		// Flush any pending text so it appears before tool approval prompts.
		r.btFlushTextBuf()

	case agent.EventTypeToolExecutionStart:
		r.btFlushTextBuf()
		r.hadTool = true
		r.hadText = false
		if !r.pendingApproval {
			r.emit("\n" + r.btToolCallLine(ev.ToolName, ev.Args) + "\n")
		}
		r.pendingApproval = false

	case agent.EventTypeToolExecutionEnd:
		full := fullResultText(ev)
		r.toolHistory = append(r.toolHistory, btToolRecord{
			name:    ev.ToolName,
			args:    ev.Args,
			result:  full,
			isError: ev.IsError,
		})
		r.emitln(r.btToolResultLine(ev.IsError, toolResult(ev)))

		// Preview lines (same logic as old Renderer)
		if !ev.IsError && full != "" {
			r.btEmitPreview(ev.ToolName, ev.Args, full)
		}

	case agent.EventTypeAgentEnd:
		r.btFlushTextBuf()
		elapsed := time.Since(r.turnStart)
		r.emitln("\n" + bsElapsed.Render("  ✻ "+formatElapsed(elapsed)))
	}
}

// ExpandLastTool emits the full content of the most recent (or next older) tool call.
func (r *BTRenderer) ExpandLastTool() {
	n := len(r.toolHistory)
	if n == 0 {
		r.emitln(bsDim.Render("(no tool calls yet)"))
		return
	}
	if r.expandIdx < 0 {
		r.expandIdx = n - 1
	} else {
		r.expandIdx = (r.expandIdx - 1 + n) % n
	}
	rec := r.toolHistory[r.expandIdx]
	pos := r.expandIdx + 1

	w := r.width
	header := fmt.Sprintf(" %s (%d/%d) ", rec.name, pos, n)
	side := (w - len([]rune(header))) / 2
	if side < 1 {
		side = 1
	}
	bar := strings.Repeat("─", side) + header +
		strings.Repeat("─", w-side-len([]rune(header)))
	r.emitln("\n" + bsExpandHdr.Render(bar))

	if m, ok := rec.args.(map[string]any); ok && len(m) > 0 {
		r.emitln(bsExpandKey.Render("Args:"))
		for k, v := range m {
			r.emitln(bsExpandKey.Render("  "+k+": ") + truncateLines(fmt.Sprintf("%v", v), 20))
		}
	}
	if rec.isError {
		r.emitln(bsError.Render("Error:"))
	} else {
		r.emitln(bsExpandKey.Render("Output:"))
	}
	if rec.result != "" {
		r.emit(rec.result)
		if !strings.HasSuffix(rec.result, "\n") {
			r.emit("\n")
		}
	}

	var hint string
	if n == 1 {
		hint = "ctrl+r to close"
	} else {
		next := (r.expandIdx - 1 + n) % n
		hint = fmt.Sprintf("ctrl+r → %s (%d/%d)", r.toolHistory[next].name, next+1, n)
	}
	dashCount := w - len([]rune(hint)) - 1
	if dashCount < 1 {
		dashCount = 1
	}
	r.emitln(bsDim.Render(strings.Repeat("─", dashCount)) + " " + bsDim.Render(hint))
}

// ── internal: text / thinking ─────────────────────────────────────────────────

func (r *BTRenderer) btProcessTextDelta(delta string) {
	r.textBuf += delta
	for {
		if r.inTextThink {
			idx := strings.Index(r.textBuf, "</think>")
			if idx < 0 {
				guard := 0
				for i := 1; i < len("</think>") && i <= len(r.textBuf); i++ {
					if strings.HasPrefix("</think>", r.textBuf[len(r.textBuf)-i:]) {
						guard = i
						break
					}
				}
				if len(r.textBuf) > guard {
					cut := len(r.textBuf) - guard
					for cut > 0 && cut < len(r.textBuf) && !isRuneStart(r.textBuf[cut]) {
						cut--
					}
					if cut > 0 {
						r.btWriteThinkContent(r.textBuf[:cut])
						r.textBuf = r.textBuf[cut:]
					}
				}
				return
			}
			if idx > 0 {
				r.btWriteThinkContent(r.textBuf[:idx])
			}
			r.inTextThink = false
			r.btWriteThinkClose()
			r.textBuf = strings.TrimLeft(r.textBuf[idx+len("</think>"):], "\n")
		} else {
			idx := strings.Index(r.textBuf, "<think>")
			if idx < 0 {
				guard := 0
				for i := 1; i < len("<think>") && i <= len(r.textBuf); i++ {
					if strings.HasPrefix("<think>", r.textBuf[len(r.textBuf)-i:]) {
						guard = i
						break
					}
				}
				if len(r.textBuf) > guard {
					cut := len(r.textBuf) - guard
					for cut > 0 && cut < len(r.textBuf) && !isRuneStart(r.textBuf[cut]) {
						cut--
					}
					if cut > 0 {
						r.btEmitText(r.textBuf[:cut])
						r.textBuf = r.textBuf[cut:]
					}
				}
				return
			}
			if idx > 0 {
				r.btEmitText(r.textBuf[:idx])
			}
			r.emit("\n" + bsThinkHdr.Render("✧") + " " + bsDim.Render("Thinking…") + "\n")
			r.thinkNeedIndent = true
			r.inTextThink = true
			r.textBuf = r.textBuf[idx+len("<think>"):]
			continue
		}
	}
}

func (r *BTRenderer) btEmitText(text string) {
	if strings.TrimSpace(text) == "" && !r.hadText {
		r.md.Feed(text)
		return
	}
	if !r.hadText {
		r.emit("\n" + bsTextBulletDone.Render("●") + " ")
		r.md.indent = "  "
		r.md.firstLine = true
		r.hadText = true
	}
	r.md.Feed(text)
}

func (r *BTRenderer) btWriteThinkContent(text string) {
	if text == "" {
		return
	}
	var out strings.Builder
	for _, ch := range text {
		if ch == '\n' {
			if !r.thinkNeedIndent {
				r.thinkPendingNL = true
				r.thinkNeedIndent = true
			}
			continue
		}
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
		r.emit(bsThinkBody.Render(out.String()))
	}
}

func (r *BTRenderer) btWriteThinkClose() {
	r.thinkPendingNL = false
	prefix := "\n"
	if r.thinkNeedIndent {
		prefix = ""
	}
	r.thinkNeedIndent = false
	if prefix != "" {
		r.emit(bsDim.Render(prefix))
	}
}

func (r *BTRenderer) btFlushTextBuf() {
	if r.inTextThink {
		r.inTextThink = false
	}
	if r.textBuf != "" {
		r.btEmitText(strings.TrimRight(r.textBuf, "\n\r"))
		r.textBuf = ""
	}
	r.md.Flush()
}

// ── internal: tool display ────────────────────────────────────────────────────

func (r *BTRenderer) btToolCallLine(name string, args any) string {
	arg := primaryArg(name, args)
	return bsToolBullet.Render("⏺") + " " + bsToolName.Render(name) +
		bsToolArg.Render("("+arg+")")
}

func (r *BTRenderer) btToolResultLine(isError bool, result string) string {
	prefix := "  " + bsToolResBar.Render("⎿") + "  "
	if isError {
		return prefix + bsToolResErr.Render(result)
	}
	if result == "" {
		return prefix + bsDim.Render("done")
	}
	return prefix + bsToolRes.Render(result)
}

func (r *BTRenderer) btEmitPreview(toolName string, args any, full string) {
	const previewMax = 10
	lines := strings.Split(strings.TrimRight(full, "\n"), "\n")
	shown := lines
	extra := 0
	if toolName != "edit" && len(lines) > previewMax {
		shown = lines[:previewMax]
		extra = len(lines) - previewMax
	}

	var highlighted []string
	switch toolName {
	case "read":
		var filePath string
		if m, ok := args.(map[string]any); ok {
			filePath, _ = m["file_path"].(string)
			if filePath == "" {
				filePath, _ = m["path"].(string)
			}
		}
		highlighted = buildHighlightedPreview(shown, filePath, false)
	case "edit":
		highlighted = buildDiffPreview(shown, false)
	}

	for i, l := range shown {
		if highlighted != nil {
			r.emitln(highlighted[i])
		} else {
			if toolName == "read" {
				if idx := strings.Index(l, "  "); idx > 0 && idx < 10 {
					l = l[idx+2:]
				}
			}
			r.emitln("     " + bsPreview.Render(l))
		}
	}
	if extra > 0 {
		r.emitln(bsDim.Render(fmt.Sprintf("     … (%d more lines)", extra)))
	}
}

// isRuneStart reports whether b is the first byte of a UTF-8 sequence.
func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// ── approval block ────────────────────────────────────────────────────────────

var (
	bsApprToolBullet = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Bold(true)
	bsApprToolName   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	bsApprArg        = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	bsApprBorder     = lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))
	bsApprKeyAllow   = lipgloss.NewStyle().Foreground(lipgloss.Color("#44BB77")).Bold(true)
	bsApprKeyDeny    = lipgloss.NewStyle().Foreground(lipgloss.Color("#CC4444")).Bold(true)
	bsApprKeyLabel   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
)

// FormatApproval returns a styled multi-line string for a tool approval prompt.
// width is the terminal width (used to size the box).
func FormatApproval(toolName string, args map[string]any, width int) string {
	if width < 20 {
		width = 80
	}
	arg := primaryArg(toolName, args)

	// Line 1: tool name + arg
	bullet := bsApprToolBullet.Render("⏺")
	name := bsApprToolName.Render(toolName)
	var line1 string
	if arg != "" {
		line1 = bullet + " " + name + " " + bsApprArg.Render(truncate(arg, max0(width-lipgloss.Width(toolName)-6)))
	} else {
		line1 = bullet + " " + name
	}

	// Line 2: key hints
	y := bsApprKeyAllow.Render("[y]") + bsApprKeyLabel.Render(" allow  ")
	a := bsApprKeyAllow.Render("[a]") + bsApprKeyLabel.Render(" always  ")
	n := bsApprKeyDeny.Render("[n]") + bsApprKeyLabel.Render(" deny  ")
	d := bsApprKeyDeny.Render("[d]") + bsApprKeyLabel.Render(" always deny  ")
	e := bsApprKeyDeny.Render("[esc]") + bsApprKeyLabel.Render(" deny")
	line2 := "  " + y + a + n + d + e

	return line1 + "\n" + line2 + "\n"
}

// FormatApprovalResult returns a short styled line confirming the decision.
func FormatApprovalResult(decision string) string {
	switch decision {
	case "allow":
		return bsApprKeyAllow.Render("  ✓ Allowed") + "\n"
	case "allow_always":
		return bsApprKeyAllow.Render("  ✓ Always allowed") + "\n"
	case "deny":
		return bsApprKeyDeny.Render("  ✗ Denied") + "\n"
	case "deny_always":
		return bsApprKeyDeny.Render("  ✗ Always denied") + "\n"
	}
	return ""
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
