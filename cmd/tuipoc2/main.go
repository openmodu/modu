// Command tuipoc2 is the v2 version of the full-screen TUI viewport POC, built on
// charm.land/bubbletea/v2 with NO bubbles (there is no v2 bubbles). The viewport
// and input are hand-rolled, the same way modu's real TUI (pkg/tui) does it.
//
// Why v2 matters here vs the v1 POC (cmd/tuipoc):
//   - alt-screen + mouse are DECLARATIVE View fields (View.AltScreen / .MouseMode),
//     not program options;
//   - clipboard is native: tea.SetClipboard(text) emits a correct OSC 52 — no
//     hand-rolled sequence, no syncWriter, and (the v1 pain) no "]11;rgb…" /
//     "[<…M" garbage leaking into the input over SSH, because v2's input parser
//     buffers split report sequences properly;
//   - mouse arrives as typed messages (MouseWheelMsg / MouseClickMsg / …).
//
// Run:  go run ./cmd/tuipoc2
// Keys:
//   - Enter = send · wheel/PgUp/PgDn = scroll · drag = select+copy
//   - click ▸ = fold · ctrl+End = jump to bottom · Ctrl+C = quit
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const cannedReply = `这是流式渲染进 **viewport** 的回复(v2 版)—— 底部输入框始终钉在底部。

最近几个提交(表格在 viewport 内渲染,不进终端原生 scrollback):

| # | Hash | Message |
|---|---------|--------------------------------------------------|
| 1 | 331d879 | fix(tui): render streaming markdown tables as placeholders |
| 2 | 6d1739d | fix(tui): let markdown tables use full available width |
| 3 | c76b15b | feat(tui): add notify block kind |
| 4 | 7adf6f8 | feat(workflow): migrate Lua engine to JavaScript |

剪贴板走 v2 原生 tea.SetClipboard(OSC 52),SSH 下也不会把鼠标/查询序列漏进输入框。`

type role int

const (
	roleUser role = iota
	roleAssistant
)

type message struct {
	role     role
	text     string
	tool     bool
	summary  string
	detail   string
	expanded bool
}

type streamTickMsg struct{}
type autoScrollTickMsg struct{}

// cell is a transcript position: display-line index + terminal-cell column.
type cell struct{ line, col int }

func (c cell) before(o cell) bool {
	return c.line < o.line || (c.line == o.line && c.col < o.col)
}

var (
	youStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	botStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	ruleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	selStyle  = lipgloss.NewStyle().Background(lipgloss.Color("240")).Foreground(lipgloss.Color("231"))
	jumpStyle = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231")).Padding(0, 1)
)

type model struct {
	width, height int

	messages    []message
	lines       []string    // rendered transcript lines (the viewport's content)
	gutters     []int       // per-line leading decoration width to skip on select/copy
	headers     map[int]int // tool-header line index -> message index
	yOffset     int         // top visible transcript line
	follow      bool        // stick to the bottom
	input       string      // input box value
	cursor      int         // caret position as a rune index into input
	streaming   bool
	streamRunes []rune
	streamIdx   int

	selecting        bool
	selStart, selEnd cell
	dragCol          int
	autoScroll       int  // edge auto-scroll direction during drag: -1 up, +1 down, 0 none
	autoScrolling    bool // a tick loop is currently live
	status           string
}

func newModel() model {
	m := model{
		width:    120,
		height:   35,
		follow:   true,
		selStart: cell{line: -1},
		selEnd:   cell{line: -1},
		messages: []message{
			{role: roleAssistant, text: "POC v2: 全屏 viewport 架构,跑在 bubbletea v2(charm.land)。自研滚动区 + 输入框,不依赖 bubbles。Enter 发送会模拟含表格的流式回复。"},
			{tool: true, summary: "Ran 1 shell command", detail: "$ go test ./cmd/tuipoc2/\nok  github.com/openmodu/modu/cmd/tuipoc2  0.4s\n\n点这一行可展开/折叠。"},
		},
	}
	m.rebuild() // populate lines for the first frame, before any WindowSizeMsg
	return m
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuild() // width affects wrapping → content must re-render

	case tea.KeyPressMsg:
		switch {
		case msg.String() == "ctrl+c":
			return m, tea.Quit
		case msg.String() == "ctrl+end":
			m.jumpToBottom()
		case msg.Code == tea.KeyPgUp:
			m.scroll(-max(1, m.vpHeight()-1))
		case msg.Code == tea.KeyPgDown:
			m.scroll(max(1, m.vpHeight()-1))
		case msg.Code == tea.KeyEnter:
			if v := strings.TrimSpace(m.input); v != "" && !m.streaming {
				m.messages = append(m.messages, message{role: roleUser, text: v})
				m.input, m.cursor = "", 0
				m.clearSelection()
				m.startStream()
				m.follow = true
				m.rebuild()
				return m, m.tick()
			}
		case msg.Code == tea.KeyLeft, msg.String() == "ctrl+b":
			m.cursor = max(0, m.cursor-1)
		case msg.Code == tea.KeyRight, msg.String() == "ctrl+f":
			m.cursor = min(m.inputLen(), m.cursor+1)
		case msg.Code == tea.KeyHome, msg.String() == "ctrl+a":
			m.cursor = 0
		case msg.Code == tea.KeyEnd, msg.String() == "ctrl+e":
			m.cursor = m.inputLen()
		case msg.Code == tea.KeyBackspace, msg.String() == "ctrl+h":
			m.backspace() // delete the rune BEFORE the caret (Terminal.app: DEL or BS)
		case msg.Code == tea.KeyDelete:
			m.deleteForward() // delete the rune AT the caret
		case msg.Text != "":
			m.insertInput(msg.Text)
		}
		// input edits don't touch the transcript — no rebuild

	case tea.PasteMsg:
		// Bracketed paste — also how some terminals deliver IME-committed text.
		m.insertInput(msg.Content)

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll(-3) // pure scroll: offset only, no re-render
		case tea.MouseWheelDown:
			m.scroll(3)
		}

	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			return m, m.onPress(msg.X, msg.Y)
		}

	case tea.MouseMotionMsg:
		if m.selecting {
			return m, m.onDrag(msg.X, msg.Y)
		}

	case tea.MouseReleaseMsg:
		if m.selecting {
			m.selecting = false
			m.autoScroll = 0
			return m, m.copySelection()
		}

	case autoScrollTickMsg:
		if m.selecting && m.autoScroll != 0 {
			m.scroll(m.autoScroll)
			edge := m.yOffset
			if m.autoScroll > 0 {
				edge = m.yOffset + m.vpHeight() - 1
			}
			m.selEnd = m.cellAt(edge, m.dragCol)
			return m, m.autoScrollTick()
		}
		m.autoScrolling = false

	case streamTickMsg:
		if m.streaming {
			m.streamIdx += 4
			if m.streamIdx >= len(m.streamRunes) {
				m.streamIdx = len(m.streamRunes)
				m.finishStream()
			}
			m.rebuild() // streamed text grew → re-render
			if m.streaming {
				return m, m.tick()
			}
		}
	}

	return m, nil
}

func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	// A real hardware cursor at the caret: it lets the terminal's IME anchor its
	// composition window (without it CJK can't be entered), and it shows where in
	// the line edits land so the middle of the text can be modified.
	caret := clamp(m.cursor, 0, m.inputLen())
	_, caretX := m.inputLine(caret)
	v.Cursor = tea.NewCursor(caretX, m.vpHeight()+1)
	return v
}

// ─── geometry ───────────────────────────────────────

func (m *model) vpHeight() int { return max(1, m.height-3) } // input + rule + status
func (m *model) maxOffset() int {
	return max(0, len(m.lines)-m.vpHeight())
}
func (m *model) atBottom() bool { return m.yOffset >= m.maxOffset() }

func (m *model) scroll(n int) {
	m.yOffset = clamp(m.yOffset+n, 0, m.maxOffset())
	m.follow = m.atBottom()
}

func (m *model) jumpToBottom() {
	m.clearSelection()
	m.follow = true
	m.autoScroll = 0
	m.clampScroll()
}

// rebuild re-renders the transcript (runs glamour — expensive) and re-pins scroll.
// Call ONLY when message content changes (send, tool toggle, stream, resize) —
// never on a pure scroll, or markdown re-renders every frame and the scroll janks.
func (m *model) rebuild() {
	m.lines, m.gutters, m.headers = m.buildTranscript()
	m.clampSelection()
	m.clampScroll()
}

// gutterAt is the leading non-selectable decoration width of a display line.
func (m *model) gutterAt(li int) int {
	if li >= 0 && li < len(m.gutters) {
		return m.gutters[li]
	}
	return 0
}

// clampScroll re-pins the viewport offset against the cached lines without
// re-rendering content. Cheap — safe to call on every scroll tick.
func (m *model) clampScroll() {
	if m.follow && !m.selecting {
		m.yOffset = m.maxOffset()
	} else {
		m.yOffset = clamp(m.yOffset, 0, m.maxOffset())
	}
}

func (m *model) lineWidth(li int) int {
	if li < 0 || li >= len(m.lines) {
		return 0
	}
	return ansi.StringWidth(ansi.Strip(m.lines[li]))
}

func (m *model) clampSelection() {
	if !m.hasSelection() {
		return
	}
	if len(m.lines) == 0 {
		m.clearSelection()
		return
	}
	clampCell := func(c cell) cell {
		c.line = clamp(c.line, 0, len(m.lines)-1)
		c.col = clamp(c.col, 0, m.lineWidth(c.line))
		return c
	}
	m.selStart = clampCell(m.selStart)
	m.selEnd = clampCell(m.selEnd)
}

// ─── transcript ─────────────────────────────────────

// buildTranscript renders the messages into display lines plus, for each line, a
// gutter width: the count of leading cells (the "● " / "❯ " marker, or the hanging
// indent under it) that are decoration, NOT content. Selection and copy skip the
// gutter, so the bullet/prompt is never highlighted or copied, and every wrapped
// paragraph line stays aligned under the first line's text.
func (m *model) buildTranscript() ([]string, []int, map[int]int) {
	width := max(m.width, 1)
	contentWidth := max(1, width-2)
	r := markdownRenderer(contentWidth)
	var lines []string
	var gutters []int
	headers := map[int]int{}
	add := func(s string) {
		for l := range strings.SplitSeq(s, "\n") {
			lines, gutters = append(lines, l), append(gutters, 0)
		}
	}
	// addBody emits a marker line ("● "/"❯ ") followed by 2-space hanging-indent
	// continuation lines, all with a gutter of 2 so content aligns and the marker
	// is excluded from selection.
	addBody := func(marker, body string, styleLine func(string) string) {
		first := true
		for _, raw := range strings.Split(body, "\n") {
			wrapped := ansi.Wrap(raw, contentWidth, "")
			if wrapped == "" {
				wrapped = "\n"
			}
			for _, bl := range strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n") {
				prefix := "  "
				if first {
					prefix = marker
					first = false
				}
				lines = append(lines, prefix+styleLine(bl))
				gutters = append(gutters, 2)
			}
		}
	}
	identity := func(s string) string { return s }

	for idx, mm := range m.messages {
		switch {
		case mm.tool:
			arrow := "▸"
			if mm.expanded {
				arrow = "▾"
			}
			headers[len(lines)] = idx
			add(dimStyle.Render(arrow + " " + mm.summary))
			if mm.expanded {
				for dl := range strings.SplitSeq(strings.TrimRight(mm.detail, "\n"), "\n") {
					add(dimStyle.Render("    " + dl))
				}
			}
			add("")
		case mm.role == roleUser:
			addBody(youStyle.Render("❯ "), mm.text, identity)
			add("")
		case mm.role == roleAssistant:
			body := mm.text
			if out, err := r.Render(mm.text); err == nil {
				body = strings.Trim(out, "\n") // glamour adds a leading blank line
			}
			addBody(botStyle.Render("● "), body, identity)
			add("")
		}
	}
	if m.streaming {
		addBody(botStyle.Render("● "), string(m.streamRunes[:m.streamIdx]), func(s string) string { return dimStyle.Render(s) })
		lines, gutters = append(lines, "  "+dimStyle.Render("┄ streaming…")), append(gutters, 2)
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines, gutters = lines[:len(lines)-1], gutters[:len(gutters)-1]
	}
	return lines, gutters, headers
}

// render composes the full screen: viewport window + rule + input + status.
func (m *model) render() string {
	h := m.vpHeight()
	window := make([]string, 0, h)
	for i := range h {
		li := m.yOffset + i
		if li < len(m.lines) {
			window = append(window, m.fitLine(m.highlightLine(li)))
		} else {
			window = append(window, "")
		}
	}
	view := strings.Join(window, "\n")
	if !m.atBottom() {
		view = overlayJumpHint(view, m.width)
	}

	input, _ := m.inputLine(clamp(m.cursor, 0, m.inputLen()))
	state := "○ idle"
	if m.streaming {
		state = "● streaming"
	}
	hint := m.status
	if hint == "" {
		hint = "拖拽选择→复制 · 点 ▸ 折叠 · Enter 发送 · 滚轮滚动 · ctrl+End 到底 · Ctrl+C 退出"
	}
	status := m.fitLine(dimStyle.Render(fmt.Sprintf(" %s · %s ", state, hint)))

	return strings.Join([]string{
		view,
		ruleStyle.Render(strings.Repeat("─", max(0, m.width))),
		input,
		status,
	}, "\n")
}

func (m *model) fitLine(s string) string {
	if m.width <= 0 {
		return ""
	}
	return ansi.Truncate(s, m.width, "")
}

func (m *model) inputLine(caret int) (string, int) {
	prefix := youStyle.Render("❯ ")
	prefixWidth := lipgloss.Width(prefix)
	contentWidth := max(1, m.width-prefixWidth-1)
	runes := []rune(m.input)
	caret = clamp(caret, 0, len(runes))
	before := string(runes[:caret])
	after := string(runes[caret:])
	beforeWidth := ansi.StringWidth(before)
	totalWidth := beforeWidth + ansi.StringWidth(after)

	visible := m.input
	cursorX := prefixWidth + beforeWidth
	if totalWidth > contentWidth {
		if beforeWidth >= contentWidth {
			visible = ansi.TruncateLeft(before, contentWidth, "")
			cursorX = prefixWidth + ansi.StringWidth(visible)
		} else {
			visible = before + ansi.Truncate(after, contentWidth-beforeWidth, "")
			cursorX = prefixWidth + beforeWidth
		}
	}
	cursorX = clamp(cursorX, 0, max(0, m.width-1))
	return m.fitLine(prefix + visible), cursorX
}

func (m *model) highlightLine(li int) string {
	ln := m.lines[li]
	if !m.hasSelection() {
		return ln
	}
	lo, hi := m.selRange()
	if li < lo.line || li > hi.line {
		return ln
	}
	plain := ansi.Strip(ln)
	from, to := 0, ansi.StringWidth(plain)
	if li == lo.line {
		from = lo.col
	}
	if li == hi.line {
		to = hi.col
	}
	if g := m.gutterAt(li); from < g { // never highlight the bullet/prompt gutter
		from = g
	}
	if to <= from {
		return ln
	}
	return cellSlice(plain, 0, from) + selStyle.Render(cellSlice(plain, from, to)) + cellSlice(plain, to, 1<<30)
}

// ─── input editing (cursor-aware, rune-based) ───────

func (m *model) inputLen() int { return len([]rune(m.input)) }

// insertInput inserts s at the caret and advances the caret past it.
func (m *model) insertInput(s string) {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	r, ins := []rune(m.input), []rune(s)
	out := make([]rune, 0, len(r)+len(ins))
	out = append(out, r[:m.cursor]...)
	out = append(out, ins...)
	out = append(out, r[m.cursor:]...)
	m.input, m.cursor = string(out), m.cursor+len(ins)
}

// backspace deletes the rune before the caret.
func (m *model) backspace() {
	if m.cursor == 0 {
		return
	}
	r := []rune(m.input)
	m.input = string(append(r[:m.cursor-1], r[m.cursor:]...))
	m.cursor--
}

// deleteForward deletes the rune at the caret.
func (m *model) deleteForward() {
	r := []rune(m.input)
	if m.cursor >= len(r) {
		return
	}
	m.input = string(append(r[:m.cursor], r[m.cursor+1:]...))
}

// ─── selection + tool toggle (mouse) ────────────────

func (m *model) onPress(x, y int) tea.Cmd {
	h := m.vpHeight()
	if !m.atBottom() && y == h-1 {
		if left := jumpHintLeft(m.width); x >= left && x < left+jumpHintWidth() {
			m.jumpToBottom()
			return nil
		}
	}
	if y >= 0 && y < h {
		if idx, ok := m.headers[m.yOffset+y]; ok {
			m.messages[idx].expanded = !m.messages[idx].expanded
			m.clearSelection()
			m.rebuild() // fold/unfold changes line count
			return nil
		}
		m.selecting = true
		m.follow = false
		m.dragCol = max(0, x)
		c := m.cellAt(m.yOffset+y, x)
		m.selStart, m.selEnd = c, c
		m.status = "" // selection highlight is applied at render time — no rebuild
	}
	return nil
}

// onDrag extends the selection to the cursor. When the cursor is parked at the
// top/bottom edge, it arms a tick loop so the viewport keeps scrolling even while
// the mouse is still — the edge auto-scroll that pure motion events can't sustain.
func (m *model) onDrag(x, y int) tea.Cmd {
	h := m.vpHeight()
	yy := clamp(y, 0, h-1)
	m.dragCol = max(0, x)
	m.selEnd = m.cellAt(m.yOffset+yy, x)
	switch {
	case y <= 0:
		m.autoScroll = -1
	case y >= h-1:
		m.autoScroll = 1
	default:
		m.autoScroll = 0
	}
	if m.autoScroll != 0 && !m.autoScrolling {
		m.autoScrolling = true
		return m.autoScrollTick()
	}
	return nil
}

func (m model) autoScrollTick() tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg { return autoScrollTickMsg{} })
}

func (m *model) copySelection() tea.Cmd {
	if !m.hasSelection() {
		return nil
	}
	text := m.selectedText()
	if text == "" {
		return nil
	}
	// Local clipboard (pbcopy/xclip/clip) — works in terminals that ignore OSC52,
	// notably macOS Terminal.app. tea.SetClipboard then emits OSC52 to also cover
	// SSH/remote sessions where there's no local pasteboard.
	how := "OSC52"
	if err := clipboard.WriteAll(text); err == nil {
		how = "local+OSC52"
	}
	m.status = fmt.Sprintf("✓ copied %d chars (%s)", len([]rune(text)), how)
	return tea.SetClipboard(text)
}

func (m *model) cellAt(line, x int) cell {
	line = clamp(line, 0, max(0, len(m.lines)-1))
	w := 0
	if line < len(m.lines) {
		w = ansi.StringWidth(ansi.Strip(m.lines[line]))
	}
	return cell{line: line, col: clamp(x, 0, w)}
}

func (m *model) hasSelection() bool { return m.selStart.line >= 0 && m.selEnd.line >= 0 }
func (m *model) clearSelection() {
	m.selStart, m.selEnd, m.selecting = cell{line: -1}, cell{line: -1}, false
}

func (m *model) selRange() (cell, cell) {
	if m.selEnd.before(m.selStart) {
		return m.selEnd, m.selStart
	}
	return m.selStart, m.selEnd
}

func (m *model) selectedText() string {
	if !m.hasSelection() || len(m.lines) == 0 {
		return ""
	}
	lo, hi := m.selRange()
	lo.line = clamp(lo.line, 0, len(m.lines)-1)
	hi.line = clamp(hi.line, 0, len(m.lines)-1)
	lo.col = clamp(lo.col, 0, m.lineWidth(lo.line))
	hi.col = clamp(hi.col, 0, m.lineWidth(hi.line))
	// start always clamped past the gutter, so the bullet/prompt/indent is dropped
	start := func(li, col int) int { return max(col, m.gutterAt(li)) }
	if lo.line == hi.line {
		return cellSlice(ansi.Strip(m.lines[lo.line]), start(lo.line, lo.col), hi.col)
	}
	var parts []string
	parts = append(parts, cellSlice(ansi.Strip(m.lines[lo.line]), start(lo.line, lo.col), 1<<30))
	for i := lo.line + 1; i < hi.line; i++ {
		parts = append(parts, cellSlice(ansi.Strip(m.lines[i]), m.gutterAt(i), 1<<30))
	}
	parts = append(parts, cellSlice(ansi.Strip(m.lines[hi.line]), m.gutterAt(hi.line), hi.col))
	return strings.Join(parts, "\n")
}

// ─── streaming ──────────────────────────────────────

func (m *model) startStream() {
	m.streaming = true
	m.streamRunes = []rune(cannedReply)
	m.streamIdx = 0
}
func (m *model) finishStream() {
	m.streaming = false
	m.messages = append(m.messages, message{role: roleAssistant, text: cannedReply})
	m.streamRunes, m.streamIdx = nil, 0
}
func (m model) tick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return streamTickMsg{} })
}

// ─── helpers ────────────────────────────────────────

func jumpHint() string           { return jumpStyle.Render("Jump to bottom (ctrl+End) ↓") }
func jumpHintWidth() int         { return lipgloss.Width(jumpHint()) }
func jumpHintLeft(width int) int { return max(0, (width-jumpHintWidth())/2) }

func overlayJumpHint(view string, width int) string {
	if width <= 0 {
		return view
	}
	pill := jumpHint()
	pw := lipgloss.Width(pill)
	left := jumpHintLeft(width)
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		return view
	}
	last := lines[len(lines)-1]
	leftPart := ansi.Truncate(last, left, "")
	if pad := left - lipgloss.Width(leftPart); pad > 0 {
		leftPart += strings.Repeat(" ", pad)
	}
	right := ansi.Truncate(ansi.TruncateLeft(last, left+pw, ""), max(0, width-left-pw), "")
	lines[len(lines)-1] = ansi.Truncate(leftPart+pill+right, width, "")
	return strings.Join(lines, "\n")
}

func cellSlice(plain string, from, to int) string {
	if to <= from {
		return ""
	}
	var b strings.Builder
	pos := 0
	for _, r := range plain {
		w := ansi.StringWidth(string(r))
		if pos >= to {
			break
		}
		if pos >= from {
			b.WriteRune(r)
		}
		pos += w
	}
	return b.String()
}

// markdownRenderer builds a glamour renderer with the document Margin zeroed, so
// finalized markdown sits flush against the left edge — matching the raw streamed
// text (which is flush) instead of getting glamour's default 2-cell indent.
func markdownRenderer(width int) *glamour.TermRenderer {
	style := glamourstyles.DarkStyleConfig
	if glamourStyle() == "light" {
		style = glamourstyles.LightStyleConfig
	}
	noMargin := uint(0)
	style.Document = glamouransi.StyleBlock{
		StylePrimitive: style.Document.StylePrimitive,
		Margin:         &noMargin,
	}
	r, _ := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	return r
}

// glamourStyle picks dark/light WITHOUT querying the terminal (no OSC leak).
func glamourStyle() string {
	if s := os.Getenv("TUIPOC_STYLE"); s == "light" || s == "dark" {
		return s
	}
	if fgbg := os.Getenv("COLORFGBG"); fgbg != "" {
		parts := strings.Split(fgbg, ";")
		if last := parts[len(parts)-1]; last == "7" || last == "15" {
			return "light"
		}
	}
	return "dark"
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

func main() {
	final, err := tea.NewProgram(newModel(), tea.WithWindowSize(120, 35)).Run()
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// Run() has returned, so the alt-screen is already torn down and the normal
	// buffer restored. Printing the transcript now lands it in the main screen, so
	// the conversation survives in scrollback — like Claude Code does on exit.
	if m, ok := final.(model); ok && len(m.lines) > 0 {
		fmt.Println(strings.Join(m.lines, "\n"))
	}
}
