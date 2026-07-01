package modutui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type streamTickMsg struct{}
type autoScrollTickMsg struct{}
type statusExpireMsg struct {
	status string
}

const maxInputHistory = 100

// bottomFixedRowsBase counts the always-present rows below the viewport: a gap,
// the agent status line, the input top rule, the input bottom rule, and the
// footer. The input area itself adds inputRows() on top (1..maxInputRows).
const bottomFixedRowsBase = 5
const maxInputRows = 5
const minViewportRows = 1
const maxAutoScrollTicksWithoutDrag = 80

type pendingApproval struct {
	request ToolApprovalRequest
	respond chan<- ToolApprovalDecision
}

type pendingHumanPrompt struct {
	request  HumanPromptRequest
	respond  chan<- string
	selected int
}

type pendingHumanText struct {
	request HumanTextRequest
	respond chan<- string
	input   InputBlock
}

type Model struct {
	width, height int

	messages      []Message
	lines         []string    // rendered transcript lines (the viewport's content)
	gutters       []int       // per-line leading decoration width to skip on select/copy
	headers       map[int]int // tool-header line index -> Message index
	yOffset       int         // top visible transcript line
	panel         *Panel
	panelLines    []string
	panelRowLines []int
	panelOffset   int
	panelSelected int
	follow        bool // stick to the bottom
	unseen        int  // messages appended while the user is away from bottom
	input         InputBlock
	inputHistory  []string
	historyIdx    int
	historyHold   string
	imeTail       string
	imeActive     bool
	streaming     bool
	streamRunes   []rune
	streamIdx     int
	streamReply   string
	busy          bool
	approval      *pendingApproval
	humanPrompt   *pendingHumanPrompt
	humanText     *pendingHumanText
	todos         []TodoItem
	todosCurrent  bool

	selecting             bool
	selStart, selEnd      cell
	dragCol               int
	autoScroll            int  // edge auto-scroll direction during drag: -1 up, +1 down, 0 none
	autoScrolling         bool // a tick loop is currently live
	autoScrollTicks       int
	status                string
	statusExpiresAt       time.Time
	statusExpiresText     string
	statusHint            string
	footer                string
	infoCardLines         []string
	disableMouse          bool
	arrowKeysScroll       bool
	hooks                 Hooks
	blockFactories        []MessageBlockFactory
	blockGap              int
	slashCommands         []SlashCommand
	slashCommandsProvider func() []SlashCommand
	slashMatches          []SlashCommand
	slashIndex            int
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
		opts.InputHistory = append([]string(nil), options[0].InputHistory...)
		opts.Todos = append([]TodoItem(nil), options[0].Todos...)
		opts.StreamReply = options[0].StreamReply
		opts.Footer = options[0].Footer
		opts.InfoCardLines = append([]string(nil), options[0].InfoCardLines...)
		opts.DisableMouse = options[0].DisableMouse
		opts.ArrowKeysScroll = options[0].ArrowKeysScroll
		opts.Hooks = options[0].Hooks
		opts.BlockFactories = append([]MessageBlockFactory(nil), options[0].BlockFactories...)
		opts.SlashCommands = append([]SlashCommand(nil), options[0].SlashCommands...)
		opts.SlashCommandsProvider = options[0].SlashCommandsProvider
		if options[0].BlockGap > 0 {
			opts.BlockGap = options[0].BlockGap
		}
		if options[0].StatusHint != "" {
			opts.StatusHint = options[0].StatusHint
		}
	}
	m := Model{
		width:                 opts.Width,
		height:                opts.Height,
		follow:                true,
		selStart:              cell{line: -1},
		selEnd:                cell{line: -1},
		streamReply:           opts.StreamReply,
		statusHint:            opts.StatusHint,
		footer:                opts.Footer,
		infoCardLines:         cleanInfoCardLines(opts.InfoCardLines),
		todos:                 normalizeTodos(opts.Todos),
		disableMouse:          opts.DisableMouse,
		arrowKeysScroll:       opts.ArrowKeysScroll,
		hooks:                 opts.Hooks,
		blockFactories:        opts.BlockFactories,
		blockGap:              opts.BlockGap,
		slashCommands:         normalizeSlashCommands(opts.SlashCommands),
		slashCommandsProvider: opts.SlashCommandsProvider,
		inputHistory:          normalizeInputHistory(opts.InputHistory),
	}
	m.historyIdx = len(m.inputHistory)
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

func (m *Model) submitInput(steer bool) tea.Cmd {
	v := strings.TrimSpace(m.input.ExpandedValue())
	if v == "" {
		return nil
	}
	if len(m.slashMatches) > 0 && !steer {
		v = m.slashMatches[clamp(m.slashIndex, 0, len(m.slashMatches)-1)].Name
	}

	trimmed := strings.TrimSpace(v)
	kind := SubmitKindPrompt
	if m.streaming || m.busy {
		if steer {
			kind = SubmitKindSteer
		} else {
			kind = SubmitKindFollowUp
		}
	}

	m.messages = append(m.messages, Message{Role: RoleUser, Text: v})
	m.appendInputHistory(v)
	m.input.Reset()
	m.historyIdx = len(m.inputHistory)
	m.historyHold = ""
	m.clearSlashMatches()
	m.clearSelection()
	m.follow = true
	m.unseen = 0
	m.rebuild()
	if strings.HasPrefix(trimmed, "/") && m.hooks.SlashCommand != nil {
		m.hooks.SlashCommand(v)
		return nil
	}
	if m.hooks.SubmitMessage != nil {
		m.hooks.SubmitMessage(SubmitEvent{Text: v, Kind: kind})
	} else if m.hooks.Submit != nil {
		m.hooks.Submit(v)
	}
	if kind == SubmitKindPrompt && m.streamReply != "" {
		m.startStream()
		m.rebuild()
		return m.tick()
	}
	return nil
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
		if m.humanPrompt != nil {
			return m.handleHumanPromptKey(msg)
		}
		if m.humanText != nil {
			return m.handleHumanTextKey(msg)
		}
		if m.panel != nil {
			return m.handlePanelKey(msg)
		}
		switch {
		case isCtrlCKey(msg):
			if strings.TrimSpace(m.input.ExpandedValue()) != "" {
				m.resetIMEState()
				m.input.Reset()
				m.clearHistorySelection()
				m.clearSlashMatches()
				m.rebuild()
				return m, nil
			}
			return m, tea.Quit
		case isEscKey(msg):
			m.resetIMEState()
			if len(m.slashMatches) > 0 {
				m.clearSlashMatches()
			} else if m.streaming || m.busy {
				m.status = "interrupting"
				if m.hooks.Interrupt != nil {
					m.hooks.Interrupt()
				}
			}
		case msg.String() == "ctrl+end":
			m.resetIMEState()
			m.jumpToBottom()
		case msg.Code == tea.KeyPgUp:
			m.resetIMEState()
			m.scroll(-max(1, m.vpHeight()-1))
		case msg.Code == tea.KeyPgDown:
			m.resetIMEState()
			m.scroll(max(1, m.vpHeight()-1))
		case msg.Code == tea.KeyEnter && msg.Mod.Contains(tea.ModAlt):
			m.resetIMEState()
			m.input.InsertNewline()
			m.clearHistorySelection()
			m.updateSlashMatches()
		case msg.String() == "shift+enter":
			m.resetIMEState()
			if cmd := m.submitInput(true); cmd != nil {
				return m, cmd
			}
		case msg.Code == tea.KeyEnter:
			m.resetIMEState()
			if cmd := m.submitInput(false); cmd != nil {
				return m, cmd
			}
		case msg.Code == tea.KeyLeft, msg.String() == "ctrl+b":
			m.resetIMEState()
			m.input.MoveLeft()
		case msg.Code == tea.KeyRight, msg.String() == "ctrl+f":
			m.resetIMEState()
			m.input.MoveRight()
		case msg.Code == tea.KeyHome, msg.String() == "ctrl+a":
			m.resetIMEState()
			m.input.MoveHome()
		case msg.Code == tea.KeyEnd, msg.String() == "ctrl+e":
			m.resetIMEState()
			m.input.MoveEnd()
		case msg.Code == tea.KeyBackspace, msg.String() == "ctrl+h":
			m.resetIMEState()
			m.input.Backspace()
			m.clearHistorySelection()
			m.updateSlashMatches()
		case isCtrlWKey(msg):
			m.resetIMEState()
			m.input.DeleteWordBackward()
			m.clearHistorySelection()
			m.updateSlashMatches()
		case msg.Code == tea.KeyDelete:
			m.resetIMEState()
			m.input.DeleteForward()
			m.clearHistorySelection()
			m.updateSlashMatches()
		case msg.Code == tea.KeyTab:
			m.resetIMEState()
			m.completeSlashMatch()
		case msg.Code == tea.KeyUp:
			m.resetIMEState()
			if len(m.slashMatches) > 0 {
				m.slashIndex = (m.slashIndex - 1 + len(m.slashMatches)) % len(m.slashMatches)
			} else if m.shouldArrowKeyScroll() {
				m.scroll(-3)
			} else {
				m.navigateInputHistory(-1)
			}
		case msg.Code == tea.KeyDown:
			m.resetIMEState()
			if len(m.slashMatches) > 0 {
				m.slashIndex = (m.slashIndex + 1) % len(m.slashMatches)
			} else if m.shouldArrowKeyScroll() {
				m.scroll(3)
			} else {
				m.navigateInputHistory(1)
			}
		case msg.Text != "":
			m.insertKeyText(msg.Text)
			m.clearHistorySelection()
			m.updateSlashMatches()
		}

	case tea.PasteMsg:
		if m.humanText != nil {
			m.humanText.input.InsertPaste(msg.Content)
			m.rebuild()
			return m, nil
		}
		m.resetIMEState()
		m.input.InsertPaste(msg.Content)
		m.clearHistorySelection()
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
			m.autoScrollTicks = 0
			return m, m.copySelection()
		}

	case autoScrollTickMsg:
		if m.selecting && m.autoScroll != 0 {
			m.autoScrollTicks++
			if m.autoScrollTicks > maxAutoScrollTicksWithoutDrag {
				m.autoScroll = 0
				m.autoScrolling = false
				m.clearSelection()
				return m, nil
			}
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
		wasAtBottom := m.atBottom()
		added := m.appendMessage(msg.Message)
		m.rebuild()
		if wasAtBottom || m.follow {
			m.follow = true
			m.unseen = 0
			m.clampScroll()
		} else {
			m.follow = false
			if added {
				m.unseen++
			}
			m.clampScroll()
		}

	case SetStatusMsg:
		m.status = msg.Status
		m.statusExpiresAt = time.Time{}
		m.statusExpiresText = ""
		if msg.Status != "" && msg.TransientFor > 0 {
			m.statusExpiresAt = time.Now().Add(msg.TransientFor)
			m.statusExpiresText = msg.Status
			return m, tea.Tick(msg.TransientFor, func(time.Time) tea.Msg {
				return statusExpireMsg{status: msg.Status}
			})
		}

	case statusExpireMsg:
		if m.status == msg.status && m.status == m.statusExpiresText && !m.statusExpiresAt.IsZero() && !time.Now().Before(m.statusExpiresAt) {
			m.status = ""
			m.statusExpiresAt = time.Time{}
			m.statusExpiresText = ""
		}

	case SetFooterMsg:
		m.footer = msg.Footer

	case SetBusyMsg:
		if msg.Busy != m.busy {
			m.todosCurrent = false
		}
		m.busy = msg.Busy
		m.clampScroll()

	case SetTodosMsg:
		m.todos = normalizeTodos(msg.Todos)
		m.todosCurrent = (m.busy || m.streaming) && hasOutstandingTodos(m.todos)
		m.clampScroll()

	case ClearMessagesMsg:
		m.messages = nil
		m.clearSelection()
		m.follow = true
		m.unseen = 0
		m.rebuild()

	case SetPanelMsg:
		panel := normalizePanel(msg.Panel)
		m.panel = &panel
		m.panelOffset = 0
		m.panelSelected = clamp(panel.Selected, 0, max(0, len(panel.Rows)-1))
		m.clearSlashMatches()
		m.clearSelection()
		m.rebuild()
		m.ensurePanelSelectionVisible()
		m.rebuild()

	case RefreshPanelMsg:
		panel := normalizePanel(msg.Panel)
		if m.panel == nil || m.panel.ID != panel.ID {
			m.panel = &panel
			m.panelOffset = 0
			m.panelSelected = clamp(panel.Selected, 0, max(0, len(panel.Rows)-1))
		} else {
			selected := m.panelSelected
			offset := m.panelOffset
			m.panel = &panel
			m.panelSelected = clamp(selected, 0, max(0, len(panel.Rows)-1))
			m.panelOffset = offset
		}
		m.clearSlashMatches()
		m.clearSelection()
		m.rebuild()
		m.ensurePanelSelectionVisible()
		m.rebuild()

	case ClearPanelMsg:
		if m.panel != nil && (msg.ID == "" || msg.ID == m.panel.ID) {
			m.panel = nil
			m.panelLines = nil
			m.panelRowLines = nil
			m.panelOffset = 0
			m.panelSelected = 0
			m.rebuild()
		}

	case RequestToolApprovalMsg:
		m.approval = &pendingApproval{request: msg.Request, respond: msg.Respond}
		m.panel = nil
		m.panelLines = nil
		m.panelRowLines = nil
		m.panelOffset = 0
		m.panelSelected = 0
		m.humanPrompt = nil
		m.humanText = nil
		m.clearSlashMatches()
		m.follow = true
		m.unseen = 0
		m.rebuild()

	case CancelToolApprovalMsg:
		if m.approval != nil && (msg.ID == "" || msg.ID == m.approval.request.ID) {
			m.approval = nil
			m.rebuild()
		}

	case RequestHumanPromptMsg:
		req := normalizeHumanPromptRequest(msg.Request)
		selected := req.DefaultIndex
		if selected < 0 && len(req.Options) > 0 {
			selected = 0
		}
		m.humanPrompt = &pendingHumanPrompt{request: req, respond: msg.Respond, selected: selected}
		m.approval = nil
		m.panel = nil
		m.panelLines = nil
		m.panelRowLines = nil
		m.panelOffset = 0
		m.panelSelected = 0
		m.humanText = nil
		m.clearSlashMatches()
		m.follow = true
		m.unseen = 0
		m.rebuild()

	case CancelHumanPromptMsg:
		if m.humanPrompt != nil && (msg.ID == "" || msg.ID == m.humanPrompt.request.ID) {
			m.humanPrompt = nil
			m.rebuild()
		}

	case RequestHumanTextMsg:
		req := normalizeHumanTextRequest(msg.Request)
		input := InputBlock{}
		if req.Default != "" {
			input.Insert(req.Default)
		}
		m.humanText = &pendingHumanText{request: req, respond: msg.Respond, input: input}
		m.approval = nil
		m.humanPrompt = nil
		m.panel = nil
		m.panelLines = nil
		m.panelRowLines = nil
		m.panelOffset = 0
		m.panelSelected = 0
		m.clearSlashMatches()
		m.follow = true
		m.unseen = 0
		m.rebuild()

	case CancelHumanTextMsg:
		if m.humanText != nil && (msg.ID == "" || msg.ID == m.humanText.request.ID) {
			m.humanText = nil
			m.rebuild()
		}
	}

	return m, nil
}

func (m *Model) insertKeyText(text string) {
	text = normalizeInputText(text)
	if text == "" {
		return
	}
	if m.coalesceIMEText(text) {
		return
	}

	m.input.Insert(text)
	if isASCIICompositionText(text) {
		m.imeTail = text
		m.imeActive = utf8.RuneCountInString(text) > 1
		return
	}
	m.resetIMEState()
}

func (m *Model) coalesceIMEText(text string) bool {
	if m.imeTail == "" || m.input.Cursor != m.input.Len() || !strings.HasSuffix(m.input.Value, m.imeTail) {
		m.resetIMEState()
		return false
	}

	tail := m.imeTail
	switch {
	case isASCIICompositionText(tail) && isASCIICompositionText(text) && text != tail && hasPrefixEither(text, tail):
		m.input.ReplaceBeforeCursor(utf8.RuneCountInString(tail), text)
	case isASCIICompositionText(tail) && containsHan(text) && (m.imeActive || utf8.RuneCountInString(tail) > 1):
		m.input.ReplaceBeforeCursor(utf8.RuneCountInString(tail), text)
	case m.imeActive && containsHan(tail) && containsHan(text):
		if strings.HasPrefix(tail, text) {
			return true
		}
		m.input.ReplaceBeforeCursor(utf8.RuneCountInString(tail), text)
	default:
		m.resetIMEState()
		return false
	}

	m.imeTail = text
	m.imeActive = true
	return true
}

func (m *Model) resetIMEState() {
	m.imeTail = ""
	m.imeActive = false
}

func hasPrefixEither(a, b string) bool {
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func isASCIICompositionText(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r == '\'' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		return false
	}
	return true
}

func containsHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func (m *Model) appendMessage(msg Message) bool {
	if msg.Tool && msg.ToolID != "" {
		for i := range m.messages {
			if m.messages[i].Tool && m.messages[i].ToolID == msg.ToolID {
				m.messages[i] = mergeToolMessage(m.messages[i], msg)
				return false
			}
		}
	}
	m.messages = append(m.messages, msg)
	return true
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
	if update.ToolCode != "" {
		base.ToolCode = update.ToolCode
	}
	if update.ToolLanguage != "" {
		base.ToolLanguage = update.ToolLanguage
	}
	base.ToolError = base.ToolError || update.ToolError
	base.ToolDone = base.ToolDone || update.ToolDone
	base.ToolNoCollapse = base.ToolNoCollapse || update.ToolNoCollapse
	base.Expanded = base.Expanded || update.Expanded
	return base
}

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	if m.disableMouse {
		v.MouseMode = tea.MouseModeNone
	} else {
		v.MouseMode = tea.MouseModeCellMotion
	}
	_, cursorRow, caretX := m.input.Render(m.inputRenderWidth(), m.inputRows())
	if m.hasBlockingPrompt() {
		caretX, cursorRow = 0, 0
	}
	v.Cursor = tea.NewCursor(caretX, m.vpHeight()+m.approvalPanelHeight()+m.humanPromptPanelHeight()+m.slashPanelHeight()+m.todoPanelHeight()+3+cursorRow)
	return v
}

// inputRows is the number of rows the input area currently occupies: the visual
// wrapped line count clamped to [1, maxInputRows]. Blocking prompt modes use a
// single placeholder line.
func (m *Model) inputRows() int {
	if m.hasBlockingPrompt() || m.panel != nil {
		return 1
	}
	return clamp(m.input.VisualLineCount(m.inputRenderWidth()), 1, maxInputRows)
}

func (m *Model) bottomFixedRows() int {
	return bottomFixedRowsBase + m.inputRows()
}

func (m *Model) vpHeight() int {
	return max(m.minViewportRows(), m.height-m.bottomFixedRows()-m.approvalPanelHeight()-m.humanPromptPanelHeight()-m.slashPanelHeight()-m.todoPanelHeight())
}
func (m *Model) approvalPanelHeight() int {
	return len(m.approvalPanelLines())
}
func (m *Model) humanPromptPanelHeight() int {
	return len(m.humanPromptPanelLines())
}
func (m *Model) slashPanelHeight() int {
	return len(m.slashPanelLines())
}
func (m *Model) todoPanelHeight() int {
	return len(m.todoPanelLines())
}
func (m *Model) showJumpPanel() bool {
	if m.panel != nil {
		return false
	}
	if m.vpHeight() <= m.minViewportRows() {
		return false
	}
	return !m.atBottom()
}

func (m *Model) minViewportRows() int {
	if m.height <= m.bottomFixedRows() {
		return 0
	}
	return minViewportRows
}

func (m *Model) hasBlockingPrompt() bool {
	return m.approval != nil || m.humanPrompt != nil || m.humanText != nil
}
func (m *Model) maxOffset() int {
	if m.panel != nil {
		return max(0, len(m.panelLines)-m.vpHeight())
	}
	return max(0, len(m.lines)-m.vpHeight())
}
func (m *Model) viewOffset() int {
	if m.panel != nil {
		return m.panelOffset
	}
	return m.yOffset
}
func (m *Model) atBottom() bool { return m.viewOffset() >= m.maxOffset() }

func (m *Model) scroll(n int) {
	if m.panel != nil {
		m.panelOffset = clamp(m.panelOffset+n, 0, m.maxOffset())
		return
	}
	before := m.yOffset
	m.yOffset = clamp(m.yOffset+n, 0, m.maxOffset())
	if m.yOffset < before {
		m.follow = false
	} else {
		m.follow = m.atBottom()
	}
	if m.follow {
		m.unseen = 0
	}
}

func (m *Model) jumpToBottom() {
	m.clearSelection()
	m.follow = true
	m.autoScroll = 0
	m.unseen = 0
	m.clampScroll()
}

func (m *Model) rebuild() {
	m.lines, m.gutters, m.headers = m.buildTranscript()
	m.panelLines = m.buildPanelLines()
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
	if m.panel != nil {
		m.panelOffset = clamp(m.panelOffset, 0, m.maxOffset())
		return
	}
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
			if (msg.Tool || msg.Thinking) && !msg.ToolNoCollapse && (offset == 0 || msg.Expanded) {
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
			Marker: streamingAssistantMarkerStyle.Render("● "),
			Text:   string(m.streamRunes[:m.streamIdx]),
		}.Render(ctx)
		for _, line := range block.Lines {
			lines = append(lines, line.Text)
			gutters = append(gutters, line.Gutter)
		}
		lines, gutters = append(lines, "  "+dimStyle.Render("┄ streaming…")), append(gutters, 2)
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines, gutters = lines[:len(lines)-1], gutters[:len(gutters)-1]
	}
	return lines, gutters, headers
}

func (m *Model) buildPanelLines() []string {
	if m.panel == nil {
		m.panelRowLines = nil
		return nil
	}
	m.panelRowLines = nil
	width := max(1, m.width)
	innerWidth := max(1, width-4)
	panel := *m.panel
	var body []string
	title := strings.TrimSpace(panel.Title)
	if title == "" {
		title = "Panel"
	}
	body = append(body, botStyle.Render(title))
	if subtitle := strings.TrimSpace(panel.Subtitle); subtitle != "" {
		body = append(body, dimStyle.Render(ansi.Truncate(subtitle, innerWidth, "…")))
	}
	if len(panel.Lines) > 0 {
		body = append(body, "")
	}
	for _, raw := range panel.Lines {
		if strings.TrimSpace(raw) == "" {
			body = append(body, "")
			continue
		}
		wrapped := ansi.Wrap(raw, innerWidth, "")
		for _, line := range strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n") {
			body = append(body, line)
		}
	}
	if len(panel.Rows) > 0 {
		if len(panel.Lines) > 0 {
			body = append(body, "")
		}
		for i, row := range panel.Rows {
			m.panelRowLines = append(m.panelRowLines, len(body)+1)
			body = append(body, panelRowLine(row, i, m.panelSelected, innerWidth))
		}
	}
	footer := strings.TrimSpace(panel.Footer)
	if footer == "" {
		if len(panel.Rows) > 0 {
			footer = "[↑/↓] select  [enter] open  [esc/q] close"
		} else {
			footer = "[esc/q] close  [↑/↓] scroll"
		}
	}
	body = append(body, "", dimStyle.Render(footer))
	return CardBlock{Lines: body}.RenderWidth(width)
}

func panelRowLine(row PanelRow, idx, selected, width int) string {
	label := strings.TrimSpace(row.Label)
	if label == "" {
		label = strings.TrimSpace(row.Value)
	}
	if label == "" {
		label = fmt.Sprintf("item %d", idx+1)
	}
	prefix := "  "
	if idx == selected {
		prefix = "› "
	}
	line := prefix + label
	if detail := strings.TrimSpace(row.Detail); detail != "" {
		space := max(1, width-ansi.StringWidth(ansi.Strip(line))-ansi.StringWidth(detail))
		line += strings.Repeat(" ", space) + detail
	}
	line = ansi.Truncate(line, max(1, width), "…")
	if idx == selected {
		return selStyle.Render(fitLine(line, width))
	}
	return line
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
	sourceLines := m.lines
	offset := m.yOffset
	if m.panel != nil {
		sourceLines = m.panelLines
		offset = m.panelOffset
	}
	window := make([]string, 0, h)
	for i := range h {
		li := offset + i
		if li < len(sourceLines) {
			line := sourceLines[li]
			if m.panel == nil {
				line = m.highlightLine(li)
			}
			window = append(window, fitLine(line, m.width))
		} else {
			window = append(window, fitLine("", m.width))
		}
	}
	view := strings.Join(window, "\n")

	inputLines, _, _ := m.input.Render(m.inputRenderWidth(), m.inputRows())
	if m.approval != nil {
		inputLines = []string{fitLine(dimStyle.Render(" approval pending "), m.inputRenderWidth())}
	} else if m.panel != nil {
		inputLines = []string{fitLine(dimStyle.Render(" panel open "), m.inputRenderWidth())}
	} else if m.humanPrompt != nil || m.humanText != nil {
		inputLines = []string{fitLine(dimStyle.Render(" human input pending "), m.inputRenderWidth())}
	}
	for i := range inputLines {
		inputLines[i] = clearToEndOfLine(inputLines[i])
	}
	state := "○ idle"
	if m.streaming {
		state = "● streaming"
	} else if m.approval != nil {
		state = "● approval"
	} else if m.panel != nil {
		state = "● panel"
	} else if m.humanPrompt != nil || m.humanText != nil {
		state = "● input"
	} else if m.busy {
		state = "● running"
	}
	inner := agentStatusText(state, m.status)
	if m.panel != nil {
		inner = agentStatusText(state, panelStatusText(m.panel, m.panelOffset, m.maxOffset()))
	} else if (m.busy || m.streaming) && !m.hasBlockingPrompt() && !m.showJumpPanel() && runStatusAllowsHint(m.status) {
		inner += "  ·  " + steerFollowupHint
	}
	status := m.statusLine(inner)
	footer := fitLine(dimStyle.Render(" "+m.footer+" "), m.width)

	parts := []string{view}
	if panel := m.approvalPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	if panel := m.humanPromptPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	if panel := m.slashPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	if panel := m.todoPanelLines(); len(panel) > 0 {
		parts = append(parts, panel...)
	}
	parts = append(parts,
		fitLine("", m.width),
		status,
		m.inputTopRuleLine(),
	)
	parts = append(parts, inputLines...)
	parts = append(parts,
		ruleStyle.Render(strings.Repeat("─", max(0, m.width))),
		footer,
	)
	return strings.Join(parts, "\n")
}

// steerFollowupHint is shown in the agent status line while a task is running
// so the operator knows how a typed message will be delivered.
const steerFollowupHint = "Enter follow-up · ⇧Enter steer"

// runStatusAllowsHint reports whether the running-state hint should be appended,
// i.e. only during the plain running state, not while a transient status
// (interrupting / steering / queued / completed) is being shown.
func runStatusAllowsHint(status string) bool {
	switch strings.TrimSpace(status) {
	case "", "running", "idle":
		return true
	default:
		return false
	}
}

func agentStatusText(state, status string) string {
	status = strings.TrimSpace(status)
	switch {
	case status == "", status == "idle", status == "running":
		return state
	case strings.HasPrefix(status, "✓"):
		return status
	default:
		return state + " · " + status
	}
}

func panelStatusText(panel *Panel, offset, maxOffset int) string {
	if panel == nil {
		return ""
	}
	title := strings.TrimSpace(panel.Title)
	if title == "" {
		title = "panel"
	}
	if maxOffset > 0 {
		return fmt.Sprintf("%s · %d/%d · esc closes", title, offset+1, maxOffset+1)
	}
	return title + " · esc closes"
}

func (m *Model) statusLine(inner string) string {
	if m.showJumpPanel() {
		jump := m.jumpHint()
		jumpWidth := ansi.StringWidth(jump)
		if jumpWidth >= m.width {
			return fitLine(jump, m.width)
		}
		jumpStart := max(0, (m.width-jumpWidth)/2)
		left := ansi.Truncate(" "+inner+" ", jumpStart, "…")
		return fitLine(fitLine(dimStyle.Render(left), jumpStart)+jump, m.width)
	}
	return fitLine(dimStyle.Render(" "+inner+" "), m.width)
}

func (m *Model) inputRenderWidth() int {
	if m.width <= 1 {
		return max(1, m.width)
	}
	return m.width - 1
}

func clearToEndOfLine(line string) string {
	return line + "\x1b[K"
}

func (m *Model) inputTopRuleLine() string {
	width := max(0, m.width)
	hint := m.inputHistoryHint()
	if hint == "" || width <= 0 {
		return ruleStyle.Render(strings.Repeat("─", width))
	}
	label := " " + hint + " "
	labelWidth := ansi.StringWidth(label)
	if labelWidth >= width {
		return dimStyle.Render(ansi.Truncate(label, width, ""))
	}
	leftWidth := min(3, width-labelWidth)
	rightWidth := max(0, width-labelWidth-leftWidth)
	return ruleStyle.Render(strings.Repeat("─", leftWidth)) +
		dimStyle.Render(label) +
		ruleStyle.Render(strings.Repeat("─", rightWidth))
}

func (m *Model) slashPanelLines() []string {
	if m.hasBlockingPrompt() || len(m.slashMatches) == 0 {
		return nil
	}
	available := m.height - m.bottomFixedRows() - m.minViewportRows() - m.approvalPanelHeight() - m.humanPromptPanelHeight()
	if available < 3 {
		return nil
	}
	commands := m.slashMatches
	selected := clamp(m.slashIndex, 0, len(commands)-1)
	maxRows := max(1, available-2)
	if len(commands) > maxRows {
		if available == 3 {
			commands = []SlashCommand{commands[selected]}
			selected = 0
		} else {
			maxRows = max(1, available-3)
		}
	}
	return SlashCommandBlock{
		Commands: commands,
		Selected: selected,
		MaxRows:  min(8, maxRows),
	}.RenderWidth(m.width)
}

func (m *Model) todoPanelLines() []string {
	if m.hasBlockingPrompt() || (!m.busy && !m.streaming) || !m.todosCurrent {
		return nil
	}
	budget := m.height - m.bottomFixedRows() - m.minViewportRows() - m.approvalPanelHeight() - m.humanPromptPanelHeight() - m.slashPanelHeight()
	if budget < 3 {
		return nil
	}
	maxRows := max(1, min(todoBlockMaxRows, budget-2))
	return (TodoBlock{Items: m.todos, MaxRows: maxRows}).RenderWidth(m.width)
}

func (m *Model) updateSlashMatches() {
	if m.slashCommandsProvider != nil {
		m.slashCommands = normalizeSlashCommands(m.slashCommandsProvider())
	}
	matches := matchSlashCommands(m.input.Value, m.slashCommands)
	if len(matches) == 0 {
		m.clearSlashMatches()
		m.clampScroll()
		return
	}
	if m.slashIndex >= len(matches) {
		m.slashIndex = len(matches) - 1
	}
	m.slashMatches = matches
	m.clampScroll()
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

func (m *Model) shouldArrowKeyScroll() bool {
	return m.arrowKeysScroll && strings.TrimSpace(m.input.ExpandedValue()) == "" && len(m.inputHistory) == 0
}

func (m *Model) appendInputHistory(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	m.inputHistory = append(m.inputHistory, line)
	m.inputHistory = trimInputHistory(m.inputHistory)
	m.historyIdx = len(m.inputHistory)
	m.historyHold = ""
	if m.hooks.InputHistoryChanged != nil {
		m.hooks.InputHistoryChanged(append([]string(nil), m.inputHistory...))
	}
}

func (m *Model) navigateInputHistory(delta int) {
	if len(m.inputHistory) == 0 {
		return
	}
	if m.historyIdx == len(m.inputHistory) {
		m.historyHold = m.input.ExpandedValue()
	}
	next := clamp(m.historyIdx+delta, 0, len(m.inputHistory))
	m.historyIdx = next
	if m.historyIdx == len(m.inputHistory) {
		m.input.Value = m.historyHold
	} else {
		m.input.Value = m.inputHistory[m.historyIdx]
		m.input.Pastes = nil
	}
	m.input.Cursor = m.input.Len()
	m.updateSlashMatches()
}

func (m *Model) clearHistorySelection() {
	m.historyIdx = len(m.inputHistory)
	m.historyHold = ""
}

func (m *Model) inputHistoryHint() string {
	if len(m.inputHistory) == 0 || m.historyIdx < 0 || m.historyIdx >= len(m.inputHistory) {
		return ""
	}
	return fmt.Sprintf("History %d/%d", m.historyIdx+1, len(m.inputHistory))
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

func normalizeInputHistory(history []string) []string {
	out := make([]string, 0, min(len(history), maxInputHistory))
	for _, item := range history {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return trimInputHistory(out)
}

func trimInputHistory(history []string) []string {
	if len(history) <= maxInputHistory {
		return history
	}
	return append([]string(nil), history[len(history)-maxInputHistory:]...)
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
	lines := CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
	budget := m.approvalPanelBudget()
	if budget <= 0 {
		return nil
	}
	if len(lines) <= budget {
		return lines
	}
	return compactApprovalPanelLines(req, width, budget)
}

func (m *Model) humanPromptPanelLines() []string {
	if m.humanText != nil {
		return m.humanTextPanelLines()
	}
	if m.humanPrompt == nil {
		return nil
	}
	width := max(1, m.width)
	innerWidth := max(1, width-4)
	req := m.humanPrompt.request
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Input required"
	}
	var body []string
	body = append(body, botStyle.Render("Human input required")+" "+dimStyle.Render(title))
	if text := strings.TrimSpace(req.Body); text != "" {
		body = append(body, limitedWrappedLines(text, innerWidth, m.maxApprovalDetailLines())...)
	}
	if len(req.Options) > 0 {
		body = append(body, "")
		body = append(body, humanPromptOptionLines(req, m.humanPrompt.selected, innerWidth)...)
		body = append(body, "")
		body = append(body, humanPromptActionsLine(req))
	}
	lines := CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
	budget := m.humanPromptPanelBudget()
	if budget <= 0 {
		return nil
	}
	if len(lines) <= budget {
		return lines
	}
	if budget < 3 {
		return nil
	}
	bodyCap := max(1, budget-2)
	compact := []string{botStyle.Render("Human input required") + " " + dimStyle.Render(ansi.Truncate(title, max(1, innerWidth), "…"))}
	if len(req.Options) > 0 && bodyCap >= 2 {
		options := humanPromptOptionLines(req, m.humanPrompt.selected, innerWidth)
		if len(options) > 0 {
			compact = append(compact, options[0])
		}
		compact = append(compact, humanPromptActionsLine(req))
	}
	for len(compact) > bodyCap {
		compact = compact[:len(compact)-1]
	}
	return CardBlock{Lines: compact, BorderStyle: approvalBorderStyle}.RenderWidth(width)
}

func (m *Model) humanPromptPanelBudget() int {
	return m.height - m.bottomFixedRows() - m.minViewportRows() - m.approvalPanelHeight()
}

func (m *Model) humanTextPanelLines() []string {
	if m.humanText == nil {
		return nil
	}
	width := max(1, m.width)
	innerWidth := max(1, width-4)
	req := m.humanText.request
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Input required"
	}
	value := m.humanText.input.ExpandedValue()
	display := value
	if req.Secret && display != "" {
		display = strings.Repeat("*", len([]rune(display)))
	}
	if strings.TrimSpace(display) == "" {
		display = dimStyle.Render(fallbackString(req.Placeholder, "type value"))
	}
	var body []string
	body = append(body, botStyle.Render("Human input required")+" "+dimStyle.Render(title))
	if text := strings.TrimSpace(req.Body); text != "" {
		body = append(body, limitedWrappedLines(text, innerWidth, m.maxApprovalDetailLines())...)
	}
	body = append(body, "")
	body = append(body, fitLine("  "+display, innerWidth))
	body = append(body, "")
	body = append(body, dimStyle.Render("[enter] save  [esc] cancel"))
	lines := CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
	budget := m.humanPromptPanelBudget()
	if budget <= 0 {
		return nil
	}
	if len(lines) <= budget {
		return lines
	}
	if budget < 3 {
		return nil
	}
	compact := []string{
		botStyle.Render("Human input required") + " " + dimStyle.Render(ansi.Truncate(title, max(1, innerWidth), "…")),
		fitLine("  "+display, innerWidth),
		dimStyle.Render("[enter] save  [esc] cancel"),
	}
	for len(compact) > max(1, budget-2) {
		compact = compact[:len(compact)-1]
	}
	return CardBlock{Lines: compact, BorderStyle: approvalBorderStyle}.RenderWidth(width)
}

func humanPromptActionsLine(req HumanPromptRequest) string {
	if len(req.Options) == 0 {
		return dimStyle.Render("[enter] continue  [esc] cancel")
	}
	return dimStyle.Render("[↑/↓] select  [enter] choose  [1-9] quick  [esc] cancel")
}

func humanPromptOptionLines(req HumanPromptRequest, selected int, width int) []string {
	if len(req.Options) == 0 {
		return nil
	}
	lines := make([]string, 0, len(req.Options))
	for i, option := range req.Options {
		if i >= 9 {
			break
		}
		label := strings.TrimSpace(option.Label)
		if label == "" {
			label = strings.TrimSpace(option.Value)
		}
		if label == "" {
			label = fmt.Sprintf("Option %d", i+1)
		}
		prefix := fmt.Sprintf("  %d. ", i+1)
		if i == selected {
			prefix = "› " + strconv.Itoa(i+1) + ". "
		}
		line := ansi.Truncate(prefix+label, max(1, width), "…")
		if i == selected {
			line = selStyle.Render(line)
		} else {
			line = dimStyle.Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

func normalizeHumanPromptRequest(req HumanPromptRequest) HumanPromptRequest {
	if req.DefaultIndex < 0 || req.DefaultIndex >= len(req.Options) {
		req.DefaultIndex = -1
	}
	for i := range req.Options {
		req.Options[i].Label = strings.TrimSpace(req.Options[i].Label)
		req.Options[i].Value = strings.TrimSpace(req.Options[i].Value)
		if req.Options[i].Value == "" {
			req.Options[i].Value = req.Options[i].Label
		}
		if req.Options[i].Label == "" {
			req.Options[i].Label = req.Options[i].Value
		}
	}
	return req
}

func (m *Model) maxApprovalDetailLines() int {
	return max(1, min(4, m.height-7))
}

func (m *Model) approvalPanelBudget() int {
	return m.height - m.bottomFixedRows() - m.minViewportRows()
}

func compactApprovalPanelLines(req ToolApprovalRequest, width int, budget int) []string {
	if budget < 3 {
		return nil
	}
	bodyCap := max(1, budget-2)
	body := []string{
		botStyle.Render("Approval required") + " " + dimStyle.Render("for "+toolDisplayName(req.ToolName)),
	}
	actions := ApprovalBlock{Request: req}.ActionsLine()
	detail := strings.TrimSpace(req.Detail)
	if bodyCap >= 3 && detail != "" {
		body = append(body, dimStyle.Render(ansi.Truncate(detail, max(1, width-4), "…")))
	}
	if bodyCap >= 4 {
		body = append(body, "")
	}
	if bodyCap >= 2 {
		body = append(body, actions)
	}
	for len(body) > bodyCap {
		body = body[:len(body)-1]
	}
	return CardBlock{Lines: body, BorderStyle: approvalBorderStyle}.RenderWidth(width)
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

func normalizeHumanTextRequest(req HumanTextRequest) HumanTextRequest {
	req.ID = strings.TrimSpace(req.ID)
	req.Title = strings.TrimSpace(req.Title)
	req.Body = strings.TrimSpace(req.Body)
	req.Placeholder = strings.TrimSpace(req.Placeholder)
	req.Default = normalizeInputText(req.Default)
	return req
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func normalizePanel(panel Panel) Panel {
	panel.ID = strings.TrimSpace(panel.ID)
	panel.Title = strings.TrimSpace(panel.Title)
	panel.Subtitle = strings.TrimSpace(panel.Subtitle)
	panel.Footer = strings.TrimSpace(panel.Footer)
	panel.Lines = append([]string(nil), panel.Lines...)
	panel.Rows = append([]PanelRow(nil), panel.Rows...)
	panel.Shortcuts = append([]PanelShortcut(nil), panel.Shortcuts...)
	for i := range panel.Rows {
		panel.Rows[i].Label = strings.TrimSpace(panel.Rows[i].Label)
		panel.Rows[i].Detail = strings.TrimSpace(panel.Rows[i].Detail)
		panel.Rows[i].Value = strings.TrimSpace(panel.Rows[i].Value)
		panel.Rows[i].Command = strings.TrimSpace(panel.Rows[i].Command)
	}
	for i := range panel.Shortcuts {
		panel.Shortcuts[i].Key = strings.TrimSpace(panel.Shortcuts[i].Key)
		panel.Shortcuts[i].Label = strings.TrimSpace(panel.Shortcuts[i].Label)
		panel.Shortcuts[i].Command = strings.TrimSpace(panel.Shortcuts[i].Command)
	}
	panel.Selected = clamp(panel.Selected, 0, max(0, len(panel.Rows)-1))
	return panel
}

func (m Model) handlePanelKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlCKey(msg) || isEscKey(msg) || strings.EqualFold(strings.TrimSpace(msg.Text), "q") {
		panelID := ""
		if m.panel != nil {
			panelID = m.panel.ID
		}
		m.closePanel()
		m.rebuild()
		if panelID != "" && m.hooks.PanelClosed != nil {
			m.hooks.PanelClosed(panelID)
		}
		return m, nil
	}
	if m.panel != nil && len(m.panel.Shortcuts) > 0 {
		if shortcut, ok := panelShortcutForKey(m.panel.Shortcuts, msg); ok {
			action := PanelAction{
				PanelID: m.panel.ID,
				Index:   -1,
				Row: PanelRow{
					Label:   shortcut.Label,
					Command: shortcut.Command,
				},
				Command: strings.TrimSpace(shortcut.Command),
			}
			m.closePanel()
			m.rebuild()
			if m.hooks.PanelAction != nil {
				m.hooks.PanelAction(action)
			}
			return m, nil
		}
	}
	if m.panel != nil && len(m.panel.Rows) > 0 {
		switch {
		case msg.Code == tea.KeyUp || strings.TrimSpace(msg.Text) == "k":
			m.panelSelected = (m.panelSelected - 1 + len(m.panel.Rows)) % len(m.panel.Rows)
			m.ensurePanelSelectionVisible()
			m.rebuild()
			return m, nil
		case msg.Code == tea.KeyDown || strings.TrimSpace(msg.Text) == "j":
			m.panelSelected = (m.panelSelected + 1) % len(m.panel.Rows)
			m.ensurePanelSelectionVisible()
			m.rebuild()
			return m, nil
		case msg.Code == tea.KeyEnter:
			row := m.panel.Rows[clamp(m.panelSelected, 0, len(m.panel.Rows)-1)]
			action := PanelAction{
				PanelID: m.panel.ID,
				Index:   m.panelSelected,
				Row:     row,
				Command: strings.TrimSpace(row.Command),
			}
			m.closePanel()
			m.rebuild()
			if m.hooks.PanelAction != nil {
				m.hooks.PanelAction(action)
			}
			return m, nil
		}
	}
	switch {
	case msg.Code == tea.KeyUp || strings.TrimSpace(msg.Text) == "k":
		m.scroll(-1)
	case msg.Code == tea.KeyDown || strings.TrimSpace(msg.Text) == "j":
		m.scroll(1)
	case msg.Code == tea.KeyPgUp:
		m.scroll(-max(1, m.vpHeight()-1))
	case msg.Code == tea.KeyPgDown:
		m.scroll(max(1, m.vpHeight()-1))
	case msg.Code == tea.KeyHome:
		m.panelOffset = 0
	case msg.Code == tea.KeyEnd || msg.String() == "ctrl+end":
		m.panelOffset = m.maxOffset()
	}
	m.rebuild()
	return m, nil
}

func panelShortcutForKey(shortcuts []PanelShortcut, msg tea.KeyPressMsg) (PanelShortcut, bool) {
	text := strings.TrimSpace(msg.Text)
	if text == "" || msg.Mod != 0 {
		return PanelShortcut{}, false
	}
	for _, shortcut := range shortcuts {
		if strings.EqualFold(strings.TrimSpace(shortcut.Key), text) && strings.TrimSpace(shortcut.Command) != "" {
			return shortcut, true
		}
	}
	return PanelShortcut{}, false
}

func (m *Model) closePanel() {
	m.panel = nil
	m.panelLines = nil
	m.panelRowLines = nil
	m.panelOffset = 0
	m.panelSelected = 0
}

func (m *Model) ensurePanelSelectionVisible() {
	if m.panel == nil || len(m.panelRowLines) == 0 || m.panelSelected < 0 || m.panelSelected >= len(m.panelRowLines) {
		return
	}
	line := m.panelRowLines[m.panelSelected]
	if line < m.panelOffset {
		m.panelOffset = line
		return
	}
	bottom := m.panelOffset + max(1, m.vpHeight()) - 1
	if line > bottom {
		m.panelOffset = line - max(1, m.vpHeight()) + 1
	}
	m.panelOffset = clamp(m.panelOffset, 0, m.maxOffset())
}

func (m Model) handleApprovalKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlCKey(msg) {
		return m, tea.Quit
	}
	if isEscKey(msg) {
		return m.resolveApproval(ToolApprovalDeny), nil
	}
	switch msg.String() {
	case "y", "Y":
		return m.resolveApproval(ToolApprovalAllow), nil
	case "a", "A":
		return m.resolveApproval(ToolApprovalAllowAlways), nil
	case "n", "N":
		return m.resolveApproval(ToolApprovalDeny), nil
	case "d", "D":
		return m.resolveApproval(ToolApprovalDenyAlways), nil
	}
	return m, nil
}

func (m Model) handleHumanPromptKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlCKey(msg) {
		return m, tea.Quit
	}
	if isEscKey(msg) {
		return m.resolveHumanPrompt(m.defaultHumanPromptValue()), nil
	}
	if m.humanPrompt != nil && len(m.humanPrompt.request.Options) > 0 {
		switch msg.String() {
		case "up":
			return m.moveHumanPromptSelection(-1), nil
		case "down":
			return m.moveHumanPromptSelection(1), nil
		}
		switch strings.TrimSpace(msg.Text) {
		case "k":
			return m.moveHumanPromptSelection(-1), nil
		case "j":
			return m.moveHumanPromptSelection(1), nil
		}
	}
	if msg.Code == tea.KeyEnter {
		return m.resolveHumanPrompt(m.selectedHumanPromptValue()), nil
	}
	if idx, ok := humanPromptOptionIndex(msg); ok && m.humanPrompt != nil {
		options := m.humanPrompt.request.Options
		if idx >= 0 && idx < len(options) {
			return m.resolveHumanPrompt(options[idx].Value), nil
		}
	}
	return m, nil
}

func (m Model) moveHumanPromptSelection(delta int) Model {
	if m.humanPrompt == nil || len(m.humanPrompt.request.Options) == 0 {
		return m
	}
	n := min(len(m.humanPrompt.request.Options), 9)
	if n <= 0 {
		return m
	}
	selected := m.humanPrompt.selected
	if selected < 0 || selected >= n {
		selected = 0
	}
	m.humanPrompt.selected = (selected + delta + n) % n
	m.rebuild()
	return m
}

func (m Model) handleHumanTextKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.humanText == nil {
		return m, nil
	}
	if isCtrlCKey(msg) {
		return m, tea.Quit
	}
	if isEscKey(msg) {
		return m.resolveHumanText(""), nil
	}
	switch {
	case msg.Code == tea.KeyEnter:
		value := m.humanText.input.ExpandedValue()
		if m.humanText.request.Required && strings.TrimSpace(value) == "" {
			m.status = "input required"
			m.rebuild()
			return m, nil
		}
		return m.resolveHumanText(value), nil
	case msg.Code == tea.KeyLeft, msg.String() == "ctrl+b":
		m.humanText.input.MoveLeft()
	case msg.Code == tea.KeyRight, msg.String() == "ctrl+f":
		m.humanText.input.MoveRight()
	case msg.Code == tea.KeyHome, msg.String() == "ctrl+a":
		m.humanText.input.MoveHome()
	case msg.Code == tea.KeyEnd, msg.String() == "ctrl+e":
		m.humanText.input.MoveEnd()
	case msg.Code == tea.KeyBackspace, msg.String() == "ctrl+h":
		m.humanText.input.Backspace()
	case isCtrlWKey(msg):
		m.humanText.input.DeleteWordBackward()
	case msg.Code == tea.KeyDelete:
		m.humanText.input.DeleteForward()
	case msg.Text != "":
		m.humanText.input.Insert(msg.Text)
	}
	m.rebuild()
	return m, nil
}

func (m Model) resolveHumanText(value string) Model {
	if m.humanText == nil {
		return m
	}
	prompt := m.humanText
	if prompt.respond != nil {
		select {
		case prompt.respond <- value:
		default:
		}
	}
	m.humanText = nil
	if strings.TrimSpace(value) == "" {
		m.status = "input cancelled"
	} else {
		m.status = "input received"
	}
	m.rebuild()
	return m
}

func humanPromptOptionIndex(msg tea.KeyPressMsg) (int, bool) {
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		text = strings.TrimSpace(msg.String())
	}
	if len(text) != 1 || text[0] < '1' || text[0] > '9' {
		return 0, false
	}
	return int(text[0] - '1'), true
}

func (m Model) defaultHumanPromptValue() string {
	if m.humanPrompt == nil {
		return ""
	}
	req := m.humanPrompt.request
	if req.DefaultIndex >= 0 && req.DefaultIndex < len(req.Options) {
		return req.Options[req.DefaultIndex].Value
	}
	return ""
}

func (m Model) selectedHumanPromptValue() string {
	if m.humanPrompt == nil {
		return ""
	}
	req := m.humanPrompt.request
	idx := m.humanPrompt.selected
	if idx >= 0 && idx < len(req.Options) {
		return req.Options[idx].Value
	}
	return m.defaultHumanPromptValue()
}

func isCtrlCKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.String() == "ctrl+c" || key.Text == "\x03" || (key.Code == 'c' && key.Mod.Contains(tea.ModCtrl))
}

func isCtrlWKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.String() == "ctrl+w" || key.Text == "\x17" || (key.Code == 'w' && key.Mod.Contains(tea.ModCtrl))
}

func isEscKey(msg tea.KeyPressMsg) bool {
	key := msg.Key()
	return msg.String() == "esc" || key.Text == "\x1b" || key.Code == tea.KeyEsc || key.Code == tea.KeyEscape || (key.Code == '[' && key.Mod.Contains(tea.ModCtrl))
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

func (m Model) resolveHumanPrompt(value string) Model {
	if m.humanPrompt == nil {
		return m
	}
	prompt := m.humanPrompt
	if prompt.respond != nil {
		go func() {
			prompt.respond <- value
		}()
	}
	if value == "" {
		m.status = "input cancelled"
	} else {
		m.status = "input received"
	}
	m.humanPrompt = nil
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
	m.todosCurrent = false
	m.messages = append(m.messages, Message{Role: RoleAssistant, Text: m.streamReply})
	m.streamRunes, m.streamIdx = nil, 0
}

func (m Model) tick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return streamTickMsg{} })
}
