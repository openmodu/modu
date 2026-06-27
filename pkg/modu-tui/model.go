package modutui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type streamTickMsg struct{}
type autoScrollTickMsg struct{}

type Model struct {
	width, height int

	messages    []Message
	lines       []string    // rendered transcript lines (the viewport's content)
	gutters     []int       // per-line leading decoration width to skip on select/copy
	headers     map[int]int // tool-header line index -> Message index
	yOffset     int         // top visible transcript line
	follow      bool        // stick to the bottom
	input       InputBlock
	streaming   bool
	streamRunes []rune
	streamIdx   int
	streamReply string

	selecting        bool
	selStart, selEnd cell
	dragCol          int
	autoScroll       int  // edge auto-scroll direction during drag: -1 up, +1 down, 0 none
	autoScrolling    bool // a tick loop is currently live
	status           string
	statusHint       string
	hooks            Hooks
	blockFactories   []MessageBlockFactory
	blockGap         int
}

func NewModel(options ...Options) Model {
	opts := Options{Width: 120, Height: 35, StatusHint: DefaultStatusHint, BlockGap: 1}
	if len(options) > 0 {
		if options[0].Width > 0 {
			opts.Width = options[0].Width
		}
		if options[0].Height > 0 {
			opts.Height = options[0].Height
		}
		opts.InitialMessages = append([]Message(nil), options[0].InitialMessages...)
		opts.StreamReply = options[0].StreamReply
		opts.Hooks = options[0].Hooks
		opts.BlockFactories = append([]MessageBlockFactory(nil), options[0].BlockFactories...)
		if options[0].BlockGap > 0 {
			opts.BlockGap = options[0].BlockGap
		}
		if options[0].StatusHint != "" {
			opts.StatusHint = options[0].StatusHint
		}
	}
	m := Model{
		width:          opts.Width,
		height:         opts.Height,
		follow:         true,
		selStart:       cell{line: -1},
		selEnd:         cell{line: -1},
		messages:       opts.InitialMessages,
		streamReply:    opts.StreamReply,
		statusHint:     opts.StatusHint,
		hooks:          opts.Hooks,
		blockFactories: opts.BlockFactories,
		blockGap:       opts.BlockGap,
	}
	m.rebuild()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Lines() []string {
	return append([]string(nil), m.lines...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.rebuild()

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
			if v := strings.TrimSpace(m.input.Value); v != "" && !m.streaming {
				m.messages = append(m.messages, Message{Role: RoleUser, Text: v})
				m.input.Reset()
				m.clearSelection()
				m.follow = true
				m.rebuild()
				if m.streamReply != "" {
					m.startStream()
					m.rebuild()
					return m, m.tick()
				}
			}
		case msg.Code == tea.KeyLeft, msg.String() == "ctrl+b":
			m.input.MoveLeft()
		case msg.Code == tea.KeyRight, msg.String() == "ctrl+f":
			m.input.MoveRight()
		case msg.Code == tea.KeyHome, msg.String() == "ctrl+a":
			m.input.MoveHome()
		case msg.Code == tea.KeyEnd, msg.String() == "ctrl+e":
			m.input.MoveEnd()
		case msg.Code == tea.KeyBackspace, msg.String() == "ctrl+h":
			m.input.Backspace()
		case msg.Code == tea.KeyDelete:
			m.input.DeleteForward()
		case msg.Text != "":
			m.input.Insert(msg.Text)
		}

	case tea.PasteMsg:
		m.input.Insert(msg.Content)

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scroll(-3)
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
			m.rebuild()
			if m.streaming {
				return m, m.tick()
			}
		}
	}

	return m, nil
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	_, caretX := m.input.Render(m.width)
	v.Cursor = tea.NewCursor(caretX, m.vpHeight()+1)
	return v
}

func (m *Model) vpHeight() int { return max(1, m.height-4) }
func (m *Model) maxOffset() int {
	return max(0, len(m.lines)-m.vpHeight())
}
func (m *Model) atBottom() bool { return m.yOffset >= m.maxOffset() }

func (m *Model) scroll(n int) {
	m.yOffset = clamp(m.yOffset+n, 0, m.maxOffset())
	m.follow = m.atBottom()
}

func (m *Model) jumpToBottom() {
	m.clearSelection()
	m.follow = true
	m.autoScroll = 0
	m.clampScroll()
}

func (m *Model) rebuild() {
	m.lines, m.gutters, m.headers = m.buildTranscript()
	m.clampSelection()
	m.clampScroll()
}

func (m *Model) gutterAt(li int) int {
	if li >= 0 && li < len(m.gutters) {
		return m.gutters[li]
	}
	return 0
}

func (m *Model) clampScroll() {
	if m.follow && !m.selecting {
		m.yOffset = m.maxOffset()
	} else {
		m.yOffset = clamp(m.yOffset, 0, m.maxOffset())
	}
}

func (m *Model) buildTranscript() ([]string, []int, map[int]int) {
	width := max(m.width, 1)
	contentWidth := max(1, width-2)
	ctx := RenderContext{ContentWidth: contentWidth, Markdown: markdownRenderer(contentWidth), Hooks: m.hooks}
	var lines []string
	var gutters []int
	headers := map[int]int{}
	addGap := func() {
		for range m.blockGap {
			lines = append(lines, "")
			gutters = append(gutters, 0)
		}
	}

	for idx, msg := range m.messages {
		if msg.Tool {
			headers[len(lines)] = idx
		}
		for _, line := range m.blockFromMessage(msg).Render(ctx).Lines {
			lines = append(lines, line.Text)
			gutters = append(gutters, line.Gutter)
		}
		if idx < len(m.messages)-1 {
			addGap()
		}
	}
	if m.streaming {
		if len(lines) > 0 {
			addGap()
		}
		block := TextBlock{
			Marker: botStyle.Render("● "),
			Text:   string(m.streamRunes[:m.streamIdx]),
		}.Render(ctx)
		for _, line := range block.Lines {
			lines = append(lines, dimStyle.Render(line.Text))
			gutters = append(gutters, line.Gutter)
		}
		lines, gutters = append(lines, "  "+dimStyle.Render("┄ streaming…")), append(gutters, 2)
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines, gutters = lines[:len(lines)-1], gutters[:len(gutters)-1]
	}
	return lines, gutters, headers
}

func (m *Model) blockFromMessage(msg Message) Block {
	for _, factory := range m.blockFactories {
		if block, ok := factory(msg); ok {
			return block
		}
	}
	return defaultBlockFromMessage(msg)
}

func (m *Model) render() string {
	h := m.vpHeight()
	window := make([]string, 0, h)
	for i := range h {
		li := m.yOffset + i
		if li < len(m.lines) {
			window = append(window, fitLine(m.highlightLine(li), m.width))
		} else {
			window = append(window, "")
		}
	}
	view := strings.Join(window, "\n")
	if !m.atBottom() {
		view = overlayJumpHint(view, m.width)
	}

	input, _ := m.input.Render(m.width)
	state := "○ idle"
	if m.streaming {
		state = "● streaming"
	}
	hint := m.status
	if hint == "" {
		hint = m.statusHint
	}
	status := fitLine(dimStyle.Render(fmt.Sprintf(" %s · %s ", state, hint)), m.width)

	return strings.Join([]string{
		view,
		ruleStyle.Render(strings.Repeat("─", max(0, m.width))),
		input,
		ruleStyle.Render(strings.Repeat("─", max(0, m.width))),
		status,
	}, "\n")
}

func (m *Model) startStream() {
	m.streaming = true
	m.streamRunes = []rune(m.streamReply)
	m.streamIdx = 0
}

func (m *Model) finishStream() {
	m.streaming = false
	m.messages = append(m.messages, Message{Role: RoleAssistant, Text: m.streamReply})
	m.streamRunes, m.streamIdx = nil, 0
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return streamTickMsg{} })
}
