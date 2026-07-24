package modutui

import (
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	osc52 "github.com/aymanbagabas/go-osc52/v2"
	"github.com/charmbracelet/x/ansi"
)

var writeLocalClipboard = clipboard.WriteAll

type clipboardCopyResultMsg struct {
	chars       int
	how         string
	needsOSC52  bool
	text        string
	osc52String string
}

func (m *Model) lineWidth(li int) int {
	if li < 0 || li >= len(m.lines) {
		return 0
	}
	return ansi.StringWidth(ansi.Strip(m.lines[li]))
}

func (m *Model) clampSelection() {
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

func (m *Model) highlightLine(li int) string {
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
	if g := m.gutterAt(li); from < g {
		from = g
	}
	if to <= from {
		return ln
	}
	return cellSlice(plain, 0, from) + selStyle.Render(cellSlice(plain, from, to)) + cellSlice(plain, to, 1<<30)
}

func (m *Model) onPress(x, y int) tea.Cmd {
	h := m.vpHeight()
	statusRow := h + m.approvalPanelHeight() + m.humanPromptPanelHeight() + m.slashPanelHeight() + m.todoPanelHeight() + 1
	if m.showJumpPanel() && y == statusRow {
		m.jumpToBottom()
		return nil
	}
	if y >= 0 && y < h {
		if idx, ok := m.headers[m.yOffset+y]; ok {
			setEntryExpanded(&m.entries[idx], !entryExpanded(m.entries[idx]))
			m.clearSelection()
			m.rebuild()
			return m.loadExpandedToolArtifactsCmd()
		}
		m.selecting = true
		m.follow = false
		m.dragCol = max(0, x)
		m.autoScrollTicks = 0
		c := m.cellAt(m.yOffset+y, x)
		m.selStart, m.selEnd = c, c
		m.status = ""
	}
	return nil
}

func (m *Model) onDrag(x, y int) tea.Cmd {
	h := m.vpHeight()
	yy := clamp(y, 0, h-1)
	m.dragCol = max(0, x)
	m.autoScrollTicks = 0
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

func (m Model) autoScrollTick() tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg { return autoScrollTickMsg{} })
}

func (m *Model) copySelection() tea.Cmd {
	if !m.hasSelection() {
		return nil
	}
	text := m.selectedText()
	if text == "" {
		return nil
	}
	return func() tea.Msg {
		localOK := writeLocalClipboard(text) == nil
		needsOSC52 := isRemoteSession() || !localOK
		how := "clipboard"
		if needsOSC52 {
			how = "OSC52"
			if localOK {
				how = "local+OSC52"
			}
		}
		return clipboardCopyResultMsg{
			chars:       len([]rune(text)),
			how:         how,
			needsOSC52:  needsOSC52,
			text:        text,
			osc52String: clipboardSequence(text),
		}
	}
}

func isRemoteSession() bool {
	if os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != "" {
		return true
	}
	// Inside tmux/screen, SSH_TTY/SSH_CONNECTION reflect whoever started the
	// multiplexer session, not whoever is currently attached — reattaching
	// over SSH to a session created locally leaves them empty. Since we can't
	// tell whether the real display is local or remote, assume remote and
	// always try OSC52 rather than silently writing to the clipboard of
	// whatever machine the multiplexer's original session ran on.
	return os.Getenv("TMUX") != "" || os.Getenv("STY") != "" || strings.HasPrefix(os.Getenv("TERM"), "screen")
}

func clipboardSequence(text string) string {
	seq := osc52.New(text)
	switch {
	case os.Getenv("TMUX") != "":
		seq = seq.Tmux()
	case strings.HasPrefix(os.Getenv("TERM"), "screen"):
		// TERM=screen-* is ambiguous: it's GNU screen's TERM, but also tmux's
		// default TERM — and over SSH the multiplexer usually sits on the
		// local side, where TERM is forwarded but TMUX is not, so we can't
		// tell which one it is. Emit both wrappings: the matching multiplexer
		// unwraps its own format and the other is dropped as an unknown DCS.
		return seq.Screen().String() + seq.Tmux().String()
	}
	return seq.String()
}

func (m *Model) cellAt(line, x int) cell {
	line = clamp(line, 0, max(0, len(m.lines)-1))
	w := 0
	if line < len(m.lines) {
		w = ansi.StringWidth(ansi.Strip(m.lines[line]))
	}
	return cell{line: line, col: clamp(x, 0, w)}
}

func (m *Model) hasSelection() bool { return m.selStart.line >= 0 && m.selEnd.line >= 0 }
func (m *Model) clearSelection() {
	m.selStart, m.selEnd, m.selecting = cell{line: -1}, cell{line: -1}, false
}

func (m *Model) selRange() (cell, cell) {
	if m.selEnd.before(m.selStart) {
		return m.selEnd, m.selStart
	}
	return m.selStart, m.selEnd
}

func (m *Model) selectedText() string {
	if !m.hasSelection() || len(m.lines) == 0 {
		return ""
	}
	lo, hi := m.selRange()
	lo.line = clamp(lo.line, 0, len(m.lines)-1)
	hi.line = clamp(hi.line, 0, len(m.lines)-1)
	lo.col = clamp(lo.col, 0, m.lineWidth(lo.line))
	hi.col = clamp(hi.col, 0, m.lineWidth(hi.line))
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
