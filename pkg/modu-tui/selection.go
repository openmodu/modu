package modutui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

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
	if m.showJumpPanel() && y == h+m.approvalPanelHeight()+m.slashPanelHeight() {
		m.jumpToBottom()
		return nil
	}
	if y >= 0 && y < h {
		if idx, ok := m.headers[m.yOffset+y]; ok {
			m.messages[idx].Expanded = !m.messages[idx].Expanded
			m.clearSelection()
			m.rebuild()
			return nil
		}
		m.selecting = true
		m.follow = false
		m.dragCol = max(0, x)
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
	how := "OSC52"
	if err := clipboard.WriteAll(text); err == nil {
		how = "local+OSC52"
	}
	m.status = fmt.Sprintf("✓ copied %d chars (%s)", len([]rune(text)), how)
	return tea.SetClipboard(text)
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
