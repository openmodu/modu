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

type pendingApproval struct {
	request ToolApprovalRequest
	respond chan<- ToolApprovalDecision
}

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
	busy        bool
	approval    *pendingApproval

	selecting        bool
	selStart, selEnd cell
	dragCol          int
	autoScroll       int  // edge auto-scroll direction during drag: -1 up, +1 down, 0 none
	autoScrolling    bool // a tick loop is currently live
	status           string
	statusHint       string
	infoCardLines    []string
	hooks            Hooks
	blockFactories   []MessageBlockFactory
	blockGap         int
	slashCommands    []SlashCommand
	slashMatches     []SlashCommand
	slashIndex       int
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
		opts.InfoCardLines = append([]string(nil), options[0].InfoCardLines...)
		opts.Hooks = options[0].Hooks
		opts.BlockFactories = append([]MessageBlockFactory(nil), options[0].BlockFactories...)
		opts.SlashCommands = append([]SlashCommand(nil), options[0].SlashCommands...)
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
		streamReply:    opts.StreamReply,
		statusHint:     opts.StatusHint,
		infoCardLines:  cleanInfoCardLines(opts.InfoCardLines),
		hooks:          opts.Hooks,
		blockFactories: opts.BlockFactories,
		blockGap:       opts.BlockGap,
		slashCommands:  normalizeSlashCommands(opts.SlashCommands),
	}
	for _, msg := range opts.InitialMessages {
		m.appendMessage(msg)
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
		if m.approval != nil {
			return m.handleApprovalKey(msg)
		}
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
			if v := strings.TrimSpace(m.input.Value); v != "" && !m.streaming && !m.busy {
				if len(m.slashMatches) > 0 {
					v = m.slashMatches[clamp(m.slashIndex, 0, len(m.slashMatches)-1)].Name
				}
				m.messages = append(m.messages, Message{Role: RoleUser, Text: v})
				m.input.Reset()
				m.clearSlashMatches()
				m.clearSelection()
				m.follow = true
				m.rebuild()
				if strings.HasPrefix(strings.TrimSpace(v), "/") && m.hooks.SlashCommand != nil {
					m.hooks.SlashCommand(v)
				} else if m.hooks.Submit != nil {
					m.hooks.Submit(v)
				}
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
			m.updateSlashMatches()
		case msg.Code == tea.KeyDelete:
			m.input.DeleteForward()
			m.updateSlashMatches()
		case msg.Code == tea.KeyTab:
			m.completeSlashMatch()
		case msg.Code == tea.KeyUp:
			if len(m.slashMatches) > 0 {
				m.slashIndex = (m.slashIndex - 1 + len(m.slashMatches)) % len(m.slashMatches)
			}
		case msg.Code == tea.KeyDown:
			if len(m.slashMatches) > 0 {
				m.slashIndex = (m.slashIndex + 1) % len(m.slashMatches)
			}
		case msg.Text != "":
			m.input.Insert(msg.Text)
			m.updateSlashMatches()
		}

	case tea.PasteMsg:
		m.input.Insert(msg.Content)
		m.updateSlashMatches()

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

	case AppendMessageMsg:
		m.appendMessage(msg.Message)
		m.follow = true
		m.rebuild()

	case SetStatusMsg:
		m.status = msg.Status

	case SetBusyMsg:
		m.busy = msg.Busy

	case ClearMessagesMsg:
		m.messages = nil
		m.clearSelection()
		m.follow = true
		m.rebuild()

	case RequestToolApprovalMsg:
		m.approval = &pendingApproval{request: msg.Request, respond: msg.Respond}
		m.clearSlashMatches()
		m.follow = true
		m.rebuild()

	case CancelToolApprovalMsg:
		if m.approval != nil && (msg.ID == "" || msg.ID == m.approval.request.ID) {
			m.approval = nil
			m.rebuild()
		}
	}

	return m, nil
}

func (m *Model) appendMessage(msg Message) {
	if msg.Tool && msg.ToolID != "" {
		for i := range m.messages {
			if m.messages[i].Tool && m.messages[i].ToolID == msg.ToolID {
				m.messages[i] = mergeToolMessage(m.messages[i], msg)
				return
			}
		}
	}
	m.messages = append(m.messages, msg)
}

func mergeToolMessage(base, update Message) Message {
	base.Tool = true
	if update.ToolName != "" {
		base.ToolName = update.ToolName
	}
	if update.Summary != "" && (!base.ToolDone || update.ToolDone) {
		base.Summary = update.Summary
	}
	if update.Detail != "" {
		base.Detail = update.Detail
	}
	if update.ToolInput != "" {
		base.ToolInput = update.ToolInput
	}
	if update.ToolOutput != "" {
		base.ToolOutput = update.ToolOutput
	}
	base.ToolError = base.ToolError || update.ToolError
	base.ToolDone = base.ToolDone || update.ToolDone
	base.Expanded = base.Expanded || update.Expanded
	return base
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	_, caretX := m.input.Render(m.width)
	if m.approval != nil {
		caretX = 0
	}
	v.Cursor = tea.NewCursor(caretX, m.vpHeight()+m.approvalPanelHeight()+m.slashPanelHeight()+m.jumpPanelHeight()+1)
	return v
}

func (m *Model) vpHeight() int {
	return max(1, m.height-4-m.approvalPanelHeight()-m.slashPanelHeight()-m.jumpPanelHeight())
}
func (m *Model) approvalPanelHeight() int {
	return len(m.approvalPanelLines())
}
func (m *Model) slashPanelHeight() int {
	return len(m.slashPanelLines())
}
func (m *Model) jumpPanelHeight() int {
	if m.showJumpPanel() {
		return 1
	}
	return 0
}
func (m *Model) showJumpPanel() bool {
	heightWithoutJump := max(1, m.height-4-m.approvalPanelHeight()-m.slashPanelHeight())
	return m.yOffset < max(0, len(m.lines)-heightWithoutJump)
}
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

	if len(m.infoCardLines) > 0 {
		for _, line := range (CardBlock{Lines: m.infoCardLines}).RenderWidth(width) {
			lines = append(lines, line)
			gutters = append(gutters, 0)
		}
		if len(m.messages) > 0 || m.streaming {
			addGap()
		}
	}

	for idx, msg := range m.messages {
		startLine := len(lines)
		rendered := m.blockFromMessage(msg).Render(ctx).Lines
		for offset, line := range rendered {
			if msg.Tool && (offset == 0 || msg.Expanded) {
				headers[startLine+offset] = idx
			}
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
			window = append(window, fitLine("", m.width))
		}
	}
	view := strings.Join(window, "\n")

	input, _ := m.input.Render(m.width)
	if m.approval != nil {
		input = fitLine(dimStyle.Render(" approval pending "), m.width)
	}
	state := "○ idle"
	if m.streaming {
		state = "● streaming"
	} else if m.approval != nil {
		state = "● approval"
	} else if m.busy {
		state = "● busy"
	}
	hint := m.status
	if hint == "" {
		hint = m.statusHint
	}
	status := fitLine(dimStyle.Render(fmt.Sprintf(" %s · %s ", state, hint)), m.width)

	parts := []string{view}
	if panel := m.approvalPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	if panel := m.slashPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	if panel := m.jumpPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	parts = append(parts,
		ruleStyle.Render(strings.Repeat("─", max(0, m.width))),
		input,
		ruleStyle.Render(strings.Repeat("─", max(0, m.width))),
		status,
	)
	return strings.Join(parts, "\n")
}

func (m *Model) jumpPanelLines() []string {
	if !m.showJumpPanel() {
		return nil
	}
	return []string{centeredLine(jumpHint(), m.width)}
}

func (m *Model) slashPanelLines() []string {
	if m.approval != nil || len(m.slashMatches) == 0 {
		return nil
	}
	return SlashCommandBlock{
		Commands: m.slashMatches,
		Selected: m.slashIndex,
		MaxRows:  8,
	}.RenderWidth(m.width)
}

func (m *Model) updateSlashMatches() {
	matches := matchSlashCommands(m.input.Value, m.slashCommands)
	if len(matches) == 0 {
		m.clearSlashMatches()
		return
	}
	if m.slashIndex >= len(matches) {
		m.slashIndex = len(matches) - 1
	}
	m.slashMatches = matches
}

func (m *Model) clearSlashMatches() {
	m.slashMatches = nil
	m.slashIndex = 0
}

func (m *Model) completeSlashMatch() bool {
	if len(m.slashMatches) == 0 {
		return false
	}
	chosen := m.slashMatches[clamp(m.slashIndex, 0, len(m.slashMatches)-1)]
	m.input.Value = chosen.Name + " "
	m.input.Cursor = m.input.Len()
	m.clearSlashMatches()
	return true
}

func normalizeSlashCommands(commands []SlashCommand) []SlashCommand {
	out := make([]SlashCommand, 0, len(commands))
	seen := map[string]struct{}{}
	for _, cmd := range commands {
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, SlashCommand{Name: name, Description: strings.TrimSpace(cmd.Description)})
	}
	return out
}

func cleanInfoCardLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (m *Model) approvalPanelLines() []string {
	if m.approval == nil {
		return nil
	}
	width := max(1, m.width)
	innerWidth := max(1, width-2)
	req := m.approval.request
	title := strings.TrimSpace(req.Summary)
	if title == "" {
		title = "Tool approval"
	}
	if req.ToolName != "" && !strings.Contains(strings.ToLower(title), strings.ToLower(req.ToolName)) {
		title += ": " + req.ToolName
	}

	var body []string
	body = append(body, botStyle.Render("Approval required")+" "+dimStyle.Render("for "+toolDisplayName(req.ToolName)))
	detail := strings.TrimSpace(req.Detail)
	if detail != "" {
		body = append(body, dimStyle.Render(toolDisplayName(req.ToolName)+" command:"))
		body = append(body, approvalDetailLines(detail, innerWidth-2, m.maxApprovalDetailLines())...)
	}
	body = append(body, "")
	body = append(body, ApprovalBlock{Request: req}.ActionsLine())

	for i, line := range body {
		if i == 0 && title != "" {
			body[i] = line + dimStyle.Render(" · "+title)
		}
	}
	return CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
}

func (m *Model) maxApprovalDetailLines() int {
	return max(1, min(4, m.height-7))
}

func limitedWrappedLines(text string, width int, limit int) []string {
	width = max(1, width)
	limit = max(1, limit)
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		wrapped := ansi.Wrap(raw, width, "")
		if wrapped == "" {
			wrapped = "\n"
		}
		for _, line := range strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n") {
			out = append(out, "  "+line)
		}
	}
	if len(out) > limit {
		out = out[:limit]
		out[len(out)-1] = ansi.Truncate(out[len(out)-1], width+2, "…")
	}
	return out
}

func approvalDetailLines(text string, width int, limit int) []string {
	lines := limitedWrappedLines(text, width, limit)
	for i, line := range lines {
		lines[i] = toolExpandedLine(width+2, strings.TrimPrefix(line, "  "))
	}
	return lines
}

func (m Model) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "y", "Y":
		return m.resolveApproval(ToolApprovalAllow), nil
	case "a", "A":
		return m.resolveApproval(ToolApprovalAllowAlways), nil
	case "n", "N":
		return m.resolveApproval(ToolApprovalDeny), nil
	case "d", "D":
		return m.resolveApproval(ToolApprovalDenyAlways), nil
	case "esc":
		return m.resolveApproval(ToolApprovalDeny), nil
	}
	return m, nil
}

func (m Model) resolveApproval(decision ToolApprovalDecision) Model {
	if m.approval == nil {
		return m
	}
	approval := m.approval
	if approval.respond != nil {
		go func() {
			approval.respond <- decision
		}()
	}
	if m.hooks.ToolApprovalDecision != nil {
		m.hooks.ToolApprovalDecision(ToolApprovalResult{Request: approval.request, Decision: decision})
	}
	m.status = "approval: " + string(decision)
	m.approval = nil
	m.rebuild()
	return m
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
