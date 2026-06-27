// Command tuipoc is a standalone proof-of-concept of the "full-screen TUI
// viewport" architecture (alt-screen + scrollable transcript + fixed input +
// mouse), as an alternative to modu's current native-scrollback incremental
// commit model. It does NOT touch any modu code.
//
// What it demonstrates:
//   - the input composer stays pinned to the bottom while the transcript above
//     scrolls independently (its own viewport offset, not terminal scrollback);
//   - mouse wheel scrolls the transcript; leaving the bottom stops auto-follow,
//     returning to the bottom resumes it;
//   - a streaming assistant reply (including a markdown table) renders into the
//     viewport with zero flicker — while streaming it shows raw text, then snaps
//     to a glamour-rendered box on finalize (the clean "don't draw a growing
//     box" pattern, for free, because there is no scrollback commit to fight).
//
// Run:  go run ./cmd/tuipoc
// Keys: Enter = send · PgUp/PgDn or wheel = scroll · Ctrl+C = quit
package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// cannedReply is streamed in to simulate an assistant response. It contains a
// markdown table so you can watch a table land in the viewport.
const cannedReply = `这是流式渲染进 **viewport** 的回复 —— 注意底部输入框始终钉在底部不动。

最近几个提交(表格在 viewport 内渲染,不进终端原生 scrollback):

| # | Hash | Message |
|---|---------|--------------------------------------------------|
| 1 | 331d879 | fix(tui): render streaming markdown tables as placeholders |
| 2 | 6d1739d | fix(tui): let markdown tables use full available width |
| 3 | c76b15b | feat(tui): add notify block kind for workflow completion |
| 4 | 7adf6f8 | feat(workflow): migrate Lua workflow engine to JavaScript |
| 5 | aebe2e0 | fix(tui): avoid committing streaming table on trailing newline |

向上滚轮可离开底部查看历史;回到底部会自动跟随后续输出。`

type role int

const (
	roleUser role = iota
	roleAssistant
)

type message struct {
	role role
	text string
}

type streamTickMsg struct{}

// autoScrollMsg drives edge auto-scroll: while the user drags a selection with
// the pointer parked at the top/bottom edge, no further motion events arrive, so
// a ticker keeps scrolling and extending the selection past the visible window.
type autoScrollMsg struct{}

type model struct {
	width, height int

	vp    viewport.Model
	input textarea.Model

	messages []message

	// streaming state
	streaming   bool
	streamRunes []rune
	streamIdx   int

	follow bool // keep the viewport pinned to the bottom
	ready  bool

	// drag-selection state. The transcript is a TUI viewport (terminal native
	// selection is swallowed once mouse reporting is on), so we implement our own
	// line selection and copy to the system clipboard.
	displayLines     []string // the exact lines currently shown in the viewport
	selecting        bool     // left button is held and dragging
	selStart, selEnd cell     // selection endpoints (line index + cell column); line<0 = none
	dragCol          int      // last pointer column, used to extend selection during edge scroll
	dragDir          int      // edge auto-scroll direction while dragging: -1 up, +1 down, 0 none
	autoScrolling    bool     // an auto-scroll ticker is in flight
	status           string   // transient status (e.g. "copied N chars")
}

// cell is a position in the rendered transcript: a display-line index plus a
// terminal-cell column (wide CJK glyphs occupy 2 columns).
type cell struct{ line, col int }

func (c cell) before(o cell) bool {
	return c.line < o.line || (c.line == o.line && c.col < o.col)
}

var (
	youStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	botStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	ruleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	selStyle    = lipgloss.NewStyle().Background(lipgloss.Color("240")).Foreground(lipgloss.Color("231"))
)

func newModel() model {
	ta := textarea.New()
	ta.Placeholder = "输入消息,Enter 发送…"
	ta.Prompt = "❯ "
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(2)
	ta.Focus()

	return model{
		input:    ta,
		follow:   true,
		selStart: cell{line: -1},
		selEnd:   cell{line: -1},
		messages: []message{
			{role: roleAssistant, text: "POC: 全屏 viewport 架构。底部输入框固定,上方 transcript 独立滚动(自维护 offset,不依赖终端 scrollback)。Enter 发送会模拟一段含表格的流式回复。"},
		},
	}
}

func (m model) Init() tea.Cmd { return textarea.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		m.refresh()
		m.ready = true

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			if v := strings.TrimSpace(m.input.Value()); v != "" && !m.streaming {
				m.messages = append(m.messages, message{role: roleUser, text: v})
				m.input.Reset()
				m.clearSelection()
				m.startStream()
				m.follow = true
				m.refresh()
				return m, tea.Batch(append(cmds, m.tick())...)
			}
		}

	case tea.MouseMsg:
		// Left-button drag = our own selection. Wheel is left to viewport.Update
		// (forwarded below).
		cmds = append(cmds, m.handleMouse(msg))

	case autoScrollMsg:
		if m.selecting && m.dragDir != 0 {
			if m.dragDir < 0 {
				m.vp.ScrollUp(1)
				m.selEnd = m.cellAt(m.vp.YOffset, m.dragCol)
			} else {
				m.vp.ScrollDown(1)
				m.selEnd = m.cellAt(m.vp.YOffset+m.vp.Height-1, m.dragCol)
			}
			m.refresh()
			cmds = append(cmds, m.autoScrollTick())
		} else {
			m.autoScrolling = false
		}

	case streamTickMsg:
		if m.streaming {
			m.streamIdx += 4 // advance a few runes per tick
			if m.streamIdx >= len(m.streamRunes) {
				m.streamIdx = len(m.streamRunes)
				m.finishStream()
			}
			m.refresh()
			if m.streaming {
				cmds = append(cmds, m.tick())
			}
		}
	}

	// Let the focused components handle the rest (input editing, viewport keys).
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)
	if !m.selecting {
		m.follow = m.vp.AtBottom()
	}

	return m, tea.Batch(cmds...)
}

// handleMouse implements left-button drag selection over the transcript. Each
// drag updates the highlighted line range; dragging to the top/bottom edge
// starts edge auto-scroll so the selection can extend past the visible window;
// releasing copies the selection to the clipboard.
func (m *model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Button != tea.MouseButtonLeft {
		return nil
	}
	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Y >= 0 && msg.Y < m.vp.Height {
			m.selecting = true
			m.follow = false
			m.dragDir = 0
			m.dragCol = max(0, msg.X)
			start := m.cellAt(m.vp.YOffset+msg.Y, msg.X)
			m.selStart, m.selEnd = start, start
			m.status = ""
			m.refresh()
		}
	case tea.MouseActionMotion:
		if m.selecting {
			y := max(0, min(msg.Y, m.vp.Height-1))
			m.dragCol = max(0, msg.X)
			m.selEnd = m.cellAt(m.vp.YOffset+y, msg.X)
			// Park at an edge → auto-scroll in that direction.
			switch {
			case msg.Y <= 0:
				m.dragDir = -1
			case msg.Y >= m.vp.Height-1:
				m.dragDir = 1
			default:
				m.dragDir = 0
			}
			m.refresh()
			if m.dragDir != 0 && !m.autoScrolling {
				m.autoScrolling = true
				return m.autoScrollTick()
			}
		}
	case tea.MouseActionRelease:
		if m.selecting {
			m.selecting = false
			m.dragDir = 0
			m.copySelection()
			m.refresh()
		}
	}
	return nil
}

func (m model) autoScrollTick() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return autoScrollMsg{} })
}

// cellAt builds a selection point from a (possibly out-of-range) line index and
// a screen column, clamping the line to the transcript and the column to that
// line's display width.
func (m *model) cellAt(line, x int) cell {
	line = m.clampLine(line)
	w := 0
	if line < len(m.displayLines) {
		w = ansi.StringWidth(ansi.Strip(m.displayLines[line]))
	}
	return cell{line: line, col: max(0, min(x, w))}
}

func (m *model) clampLine(n int) int {
	if len(m.displayLines) == 0 {
		return 0
	}
	return max(0, min(n, len(m.displayLines)-1))
}

func (m *model) hasSelection() bool { return m.selStart.line >= 0 && m.selEnd.line >= 0 }

func (m *model) clearSelection() {
	m.selStart, m.selEnd, m.selecting = cell{line: -1}, cell{line: -1}, false
}

// selRange returns the selection endpoints in document order (lo before hi).
func (m *model) selRange() (cell, cell) {
	if m.selEnd.before(m.selStart) {
		return m.selEnd, m.selStart
	}
	return m.selStart, m.selEnd
}

// copySelection writes the selected text (ANSI stripped, cell-accurate) to the
// system clipboard. atotto/clipboard uses pbcopy on macOS; over SSH you would
// instead emit an OSC 52 sequence so the *local* terminal does the copy.
func (m *model) copySelection() {
	if !m.hasSelection() || len(m.displayLines) == 0 {
		return
	}
	text := m.selectedText()
	if text == "" {
		return
	}
	if err := clipboard.WriteAll(text); err != nil {
		m.status = "clipboard error: " + err.Error()
		return
	}
	m.status = fmt.Sprintf("✓ copied %d chars", len([]rune(text)))
}

// selectedText is the plain (ANSI-stripped) text of the current character-level
// selection: from the start column on the first line, through whole middle
// lines, to the end column on the last line.
func (m *model) selectedText() string {
	if !m.hasSelection() || len(m.displayLines) == 0 {
		return ""
	}
	lo, hi := m.selRange()
	if lo.line == hi.line {
		return cellSlice(ansi.Strip(m.displayLines[lo.line]), lo.col, hi.col)
	}
	var parts []string
	parts = append(parts, cellSlice(ansi.Strip(m.displayLines[lo.line]), lo.col, 1<<30))
	for i := lo.line + 1; i < hi.line; i++ {
		parts = append(parts, ansi.Strip(m.displayLines[i]))
	}
	parts = append(parts, cellSlice(ansi.Strip(m.displayLines[hi.line]), 0, hi.col))
	return strings.Join(parts, "\n")
}

func (m model) View() string {
	if !m.ready {
		return "loading…"
	}
	state := map[bool]string{true: "● streaming", false: "○ idle"}[m.streaming]
	hint := m.status
	if hint == "" {
		hint = "拖拽选择→复制 · Enter 发送 · 滚轮/PgUp 滚动 · Ctrl+C 退出"
	}
	status := statusStyle.Render(fmt.Sprintf(" %s  ·  %s ", state, hint))
	return lipgloss.JoinVertical(
		lipgloss.Left,
		m.vp.View(),
		ruleStyle.Render(strings.Repeat("─", m.width)),
		m.input.View(),
		status,
	)
}

// layout sizes the viewport to fill everything above the fixed input chrome.
func (m *model) layout() {
	inputH := lipgloss.Height(m.input.View())
	chrome := inputH + 2 // rule + status line
	vpH := max(m.height-chrome, 1)
	if m.vp.Width == 0 && m.vp.Height == 0 {
		m.vp = viewport.New(m.width, vpH)
	} else {
		m.vp.Width = m.width
		m.vp.Height = vpH
	}
	m.input.SetWidth(m.width)
}

// refresh re-renders the transcript into the viewport, following the bottom
// while streaming or when the user is already at the bottom. The selected line
// range (if any) is highlighted before the content is handed to the viewport.
func (m *model) refresh() {
	m.layout()
	m.displayLines = strings.Split(m.renderTranscript(), "\n")
	m.vp.SetContent(m.viewportContent())
	if m.follow && !m.selecting {
		m.vp.GotoBottom()
	}
}

// viewportContent returns the display lines with the active selection painted as
// a reverse-video highlight over the selected CELL range of each line (a
// character-level selection, like a terminal's). Highlighted lines are rebuilt
// from plain text, so they lose markdown color over the whole line — acceptable
// for a selection, and the unselected lines keep their styling.
func (m *model) viewportContent() string {
	if !m.hasSelection() {
		return strings.Join(m.displayLines, "\n")
	}
	lo, hi := m.selRange()
	out := make([]string, len(m.displayLines))
	for i, ln := range m.displayLines {
		if i < lo.line || i > hi.line {
			out[i] = ln
			continue
		}
		plain := ansi.Strip(ln)
		from, to := 0, ansi.StringWidth(plain)
		if i == lo.line {
			from = lo.col
		}
		if i == hi.line {
			to = hi.col
		}
		if to <= from { // empty selection on this line — leave as-is
			out[i] = ln
			continue
		}
		out[i] = cellSlice(plain, 0, from) +
			selStyle.Render(cellSlice(plain, from, to)) +
			cellSlice(plain, to, 1<<30)
	}
	return strings.Join(out, "\n")
}

// cellSlice returns the substring of plain (no ANSI) whose terminal cells fall
// in [from, to). A rune is included when its starting cell is within range;
// wide (CJK) runes count as 2 cells.
func cellSlice(plain string, from, to int) string {
	if to <= from {
		return ""
	}
	var b strings.Builder
	cellPos := 0
	for _, r := range plain {
		w := ansi.StringWidth(string(r))
		if cellPos >= to {
			break
		}
		if cellPos >= from {
			b.WriteRune(r)
		}
		cellPos += w
	}
	return b.String()
}

func (m *model) renderTranscript() string {
	width := max(m.vp.Width, 10)
	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(width-2))

	var b strings.Builder
	render := func(mm message, streaming bool) {
		switch mm.role {
		case roleUser:
			b.WriteString(youStyle.Render("you") + "\n")
			b.WriteString(mm.text + "\n\n")
		case roleAssistant:
			b.WriteString(botStyle.Render("assistant") + "\n")
			if streaming {
				// While streaming, show raw text (fast, stable). It snaps to the
				// glamour-rendered box only once finalized — no growing box.
				b.WriteString(dimStyle.Render(mm.text) + "\n")
				b.WriteString(dimStyle.Render("┄ streaming…") + "\n\n")
			} else if r != nil {
				if out, err := r.Render(mm.text); err == nil {
					b.WriteString(strings.TrimRight(out, "\n") + "\n\n")
				} else {
					b.WriteString(mm.text + "\n\n")
				}
			}
		}
	}

	for _, mm := range m.messages {
		render(mm, false)
	}
	if m.streaming {
		render(message{role: roleAssistant, text: string(m.streamRunes[:m.streamIdx])}, true)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *model) startStream() {
	m.streaming = true
	m.streamRunes = []rune(cannedReply)
	m.streamIdx = 0
}

func (m *model) finishStream() {
	m.streaming = false
	m.messages = append(m.messages, message{role: roleAssistant, text: cannedReply})
	m.streamRunes = nil
	m.streamIdx = 0
}

func (m model) tick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return streamTickMsg{} })
}

func main() {
	p := tea.NewProgram(
		newModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Println("error:", err)
	}
}
