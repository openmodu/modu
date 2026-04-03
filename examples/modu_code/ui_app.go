package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"
)

type uiAgentEventMsg struct{ event agent.AgentEvent }
type uiSessionEventMsg struct{ event coding_agent.SessionEvent }
type uiPromptDoneMsg struct{ err error }
type uiApprovalRequestMsg struct{ req tui.ApprovalRequest }
type uiApprovalCancelMsg struct{ toolCallID string }
type uiExternalInfoMsg struct{ text string }
type uiExternalUserMsg struct{ text string }
type uiClearScreenMsg struct{}
type uiQuitMsg struct{}

type uiBlock struct {
	Kind      string
	Title     string
	Content   string
	Thinking  string
	Tools     []*uiToolState
	Timestamp time.Time
}

type uiToolState struct {
	ID      string
	Name    string
	Input   string
	Status  string
	Output  string
	IsError bool
}

type uiSlashPrinter struct {
	lines []string
	clear bool
}

func (p *uiSlashPrinter) PrintInfo(msg string) {
	p.lines = append(p.lines, msg)
}

func (p *uiSlashPrinter) PrintError(err error) {
	if err == nil {
		return
	}
	p.lines = append(p.lines, "error: "+err.Error())
}

func (p *uiSlashPrinter) PrintSection(title string, lines []string) {
	p.lines = append(p.lines, title)
	p.lines = append(p.lines, lines...)
}

func (p *uiSlashPrinter) ClearScreen() {
	p.clear = true
}

type uiBridgePrinter struct {
	program *tea.Program
}

func (p *uiBridgePrinter) PrintInfo(msg string) {
	if p.program != nil {
		p.program.Send(uiExternalInfoMsg{text: msg})
	}
}

func (p *uiBridgePrinter) PrintError(err error) {
	if err == nil {
		return
	}
	if p.program != nil {
		p.program.Send(uiExternalInfoMsg{text: "error: " + err.Error()})
	}
}

func (p *uiBridgePrinter) PrintUser(msg string) {
	if p.program != nil {
		p.program.Send(uiExternalUserMsg{text: msg})
	}
}

func (p *uiBridgePrinter) ClearLine() {}

type uiModel struct {
	session        *coding_agent.CodingSession
	model          *types.Model
	mailboxRuntime *moduCodeMailboxRuntime
	histFile       string
	approvalCh     chan tui.ApprovalRequest
	promptMu       *sync.Mutex
	ctx            context.Context
	tgUsername     string

	width    int
	height   int
	ready    bool
	viewport viewport.Model
	input    textarea.Model
	spinner  spinner.Model

	blocks             []uiBlock
	history            []string
	historyIdx         int
	historyDraft       string
	queryActive        bool
	userScrolled       bool
	errMsg             string
	statusMsg          string
	pendingPerm        *tui.ApprovalRequest
	sessionStart       time.Time
	approvalCmdStarted bool
}

func newUIModel(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, mailboxRuntime *moduCodeMailboxRuntime, histFile string, approvalCh chan tui.ApprovalRequest, promptMu *sync.Mutex, tgUsername string) *uiModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter to send, Shift+Enter for newline)"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 8
	ta.SetHeight(1)
	ta.Prompt = "> "
	ta.Focus()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(uiDim)
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(uiPrimary).Bold(true)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(uiDim)

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(uiPrimary)

	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)

	return &uiModel{
		session:        session,
		model:          model,
		mailboxRuntime: mailboxRuntime,
		histFile:       histFile,
		approvalCh:     approvalCh,
		promptMu:       promptMu,
		ctx:            ctx,
		tgUsername:     tgUsername,
		viewport:       vp,
		input:          ta,
		spinner:        sp,
		historyIdx:     -1,
		sessionStart:   time.Now(),
	}
}

func (m *uiModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		headerHeight := 1
		statusHeight := 1
		inputHeight := lipgloss.Height(m.renderInputArea())
		prevContent := m.viewport.View()
		prevOffset := m.viewport.YOffset
		prevScrolled := m.userScrolled
		m.width = msg.Width
		m.height = msg.Height
		m.viewport = viewport.New(msg.Width, max(4, msg.Height-headerHeight-statusHeight-inputHeight-2))
		m.input.SetWidth(max(20, msg.Width-4))
		m.ready = true
		if prevContent != "" {
			m.viewport.SetContent(prevContent)
			if prevScrolled {
				m.viewport.SetYOffset(prevOffset)
			} else {
				m.viewport.GotoBottom()
			}
		}
		m.refreshViewport()
		if !m.approvalCmdStarted && m.approvalCh != nil {
			m.approvalCmdStarted = true
			return m, m.waitApprovalCmd()
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		if m.pendingPerm != nil {
			switch strings.ToLower(msg.String()) {
			case "y":
				m.resolveApproval("allow")
			case "a":
				m.resolveApproval("allow_always")
			case "n", "esc":
				m.resolveApproval("deny")
			case "d":
				m.resolveApproval("deny_always")
			}
			return m, nil
		}
		if handled := m.handleViewportKey(msg.String()); handled {
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c":
			if m.queryActive {
				m.session.Abort()
				m.statusMsg = "interrupted"
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+l":
			m.blocks = nil
			m.errMsg = ""
			m.statusMsg = "cleared viewport"
			m.refreshViewport()
			return m, nil
		case "enter":
			if strings.TrimSpace(m.input.Value()) == "" {
				return m, nil
			}
			if strings.Count(m.input.Value(), "\n")+1 > 1 && !msg.Alt {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			line := strings.TrimSpace(m.input.Value())
			m.captureHistory(line)
			m.input.Reset()
			m.input.SetHeight(1)
			return m, m.submitLineCmd(line)
		case "up":
			if strings.Count(m.input.Value(), "\n")+1 == 1 {
				if strings.TrimSpace(m.input.Value()) == "" && m.handleViewportKey("up") {
					return m, nil
				}
				m.navigateHistory(-1)
				return m, nil
			}
		case "down":
			if strings.Count(m.input.Value(), "\n")+1 == 1 {
				if strings.TrimSpace(m.input.Value()) == "" && m.handleViewportKey("down") {
					return m, nil
				}
				m.navigateHistory(1)
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.syncInputHeight()
		return m, cmd

	case uiPromptDoneMsg:
		m.queryActive = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.appendBlock(uiBlock{Kind: "system", Content: "error: " + msg.err.Error(), Timestamp: time.Now()})
		} else {
			m.errMsg = ""
		}
		m.statusMsg = ""
		_ = m.session.SaveMessages()
		_ = saveHistoryFile(m.histFile, m.history)
		m.refreshViewport()
		return m, nil

	case uiAgentEventMsg:
		m.handleAgentEvent(msg.event)
		return m, nil

	case uiSessionEventMsg:
		switch msg.event.Type {
		case coding_agent.SessionEventCompactionStart:
			m.statusMsg = "compacting context"
		case coding_agent.SessionEventCompactionDone:
			m.appendBlock(uiBlock{Kind: "system", Content: "context compacted", Timestamp: time.Now()})
			m.statusMsg = ""
		}
		m.refreshViewport()
		return m, nil

	case uiApprovalRequestMsg:
		m.pendingPerm = &msg.req
		m.statusMsg = "approval required"
		m.refreshViewport()
		cmds := []tea.Cmd{m.waitApprovalCmd()}
		if msg.req.Cancel != nil {
			cmds = append(cmds, m.waitApprovalCancelCmd(msg.req.ToolCallID, msg.req.Cancel))
		}
		return m, tea.Batch(cmds...)

	case uiApprovalCancelMsg:
		if m.pendingPerm != nil && m.pendingPerm.ToolCallID == msg.toolCallID {
			m.pendingPerm = nil
			m.statusMsg = "approval dismissed"
			m.refreshViewport()
		}
		return m, nil

	case uiExternalInfoMsg:
		m.appendBlock(uiBlock{Kind: "system", Content: msg.text, Timestamp: time.Now()})
		return m, nil

	case uiExternalUserMsg:
		m.appendBlock(uiBlock{Kind: "user", Content: msg.text, Timestamp: time.Now()})
		return m, nil

	case uiClearScreenMsg:
		m.blocks = nil
		m.refreshViewport()
		return m, nil

	case uiQuitMsg:
		return m, tea.Quit
	}

	return m, nil
}

func (m *uiModel) View() string {
	if !m.ready {
		return "loading modu_code..."
	}
	var parts []string
	parts = append(parts, m.renderHeader())
	parts = append(parts, m.viewport.View())
	if m.pendingPerm != nil {
		parts = append(parts, m.renderPermissionPrompt())
	} else if m.queryActive {
		parts = append(parts, m.renderActivityLine())
	} else {
		parts = append(parts, m.renderInputArea())
	}
	parts = append(parts, m.renderStatusBar())
	return strings.Join(parts, "\n")
}

func (m *uiModel) renderHeader() string {
	left := uiPrimaryText.Render(" ● modu_code")
	model := uiMutedText.Render(" " + m.model.Name)
	right := uiDimText.Render(" " + formatUIDuration(time.Since(m.sessionStart)))
	if m.tgUsername != "" {
		model += uiMutedText.Render("  @" + m.tgUsername)
	}
	gap := max(1, m.width-lipgloss.Width(left+model)-lipgloss.Width(right))
	return lipgloss.NewStyle().
		Background(lipgloss.Color("#1a1b26")).
		Foreground(uiMuted).
		Width(m.width).
		Render(left + model + strings.Repeat(" ", gap) + right)
}

func (m *uiModel) renderStatusBar() string {
	parts := []string{
		uiMutedText.Render(fmt.Sprintf("tokens %d", m.session.GetSessionStats().TotalTokens)),
		uiMutedText.Render(fmt.Sprintf("tools %d", len(m.session.GetActiveToolNames()))),
	}
	if m.session.IsPlanMode() {
		parts = append(parts, uiSecondaryText.Render("plan"))
	}
	if m.session.ActiveWorktree() != "" {
		parts = append(parts, uiWarningText.Render("worktree"))
	}
	if m.statusMsg != "" {
		parts = append(parts, uiPrimaryText.Render(m.statusMsg))
	}
	if m.errMsg != "" {
		parts = append(parts, uiErrorText.Render(m.errMsg))
	}
	left := strings.Join(parts, uiDimText.Render(" │ "))
	right := uiDimText.Render(fmt.Sprintf(" %d%% ", int(m.viewport.ScrollPercent()*100)))
	if m.viewport.AtBottom() {
		right = uiDimText.Render(" end ")
	}
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-1)
	return lipgloss.NewStyle().Background(lipgloss.Color("#1a1b26")).Width(m.width).PaddingLeft(1).
		Render(left + strings.Repeat(" ", gap) + right)
}

func (m *uiModel) renderActivityLine() string {
	label := "Thinking…"
	if tool := m.latestRunningTool(); tool != nil {
		label = "running " + tool.Name + "..."
	}
	return "  " + m.spinner.View() + " " + uiDimText.Render(label)
}

func (m *uiModel) renderInputArea() string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(uiDim).
		Padding(0, 1).
		Render(m.input.View())
	hint := uiDimText.Render("enter send  shift+enter newline  /help commands  ctrl+c abort")
	if m.errMsg != "" {
		return uiErrorText.Render("  ✗ "+m.errMsg) + "\n" + box + "\n" + hint
	}
	return box + "\n" + hint
}

func (m *uiModel) renderPermissionPrompt() string {
	input := formatApprovalArgs(m.pendingPerm.Args)
	body := strings.Join([]string{
		fmt.Sprintf("%s wants to execute:", uiPrimaryText.Bold(true).Render(m.pendingPerm.ToolName)),
		"",
		uiDimText.Render(input),
		"",
		uiSuccessText.Bold(true).Render("[Y]es") + "  " +
			uiErrorText.Render("[N]o") + "  " +
			uiWarningText.Bold(true).Render("[A]lways allow") + "  " +
			uiMutedText.Render("[D]eny always"),
	}, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(uiWarning).
		Padding(0, 1).
		Render(body)
}

func (m *uiModel) refreshViewport() {
	if !m.ready {
		return
	}
	offset := m.viewport.YOffset
	keepOffset := m.userScrolled
	m.viewport.SetContent(m.renderConversation())
	if keepOffset {
		m.viewport.SetYOffset(offset)
		if m.viewport.AtBottom() {
			m.userScrolled = false
		}
		return
	}
	m.viewport.GotoBottom()
}

func (m *uiModel) handleViewportKey(key string) bool {
	if !m.ready {
		return false
	}
	switch key {
	case "pgup":
		m.viewport.HalfViewUp()
	case "pgdown":
		m.viewport.HalfViewDown()
	case "home":
		m.viewport.GotoTop()
	case "end":
		m.viewport.GotoBottom()
	case "ctrl+u":
		m.viewport.HalfViewUp()
	case "ctrl+d":
		m.viewport.HalfViewDown()
	case "up":
		m.viewport.LineUp(1)
	case "down":
		m.viewport.LineDown(1)
	default:
		return false
	}
	m.userScrolled = !m.viewport.AtBottom()
	return true
}

func (m *uiModel) renderConversation() string {
	if len(m.blocks) == 0 {
		return m.renderWelcome()
	}
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(max(40, m.width-8)),
	)
	var out strings.Builder
	for idx, block := range m.blocks {
		if idx > 0 {
			out.WriteString("\n")
		}
		switch block.Kind {
		case "user":
			out.WriteString(renderUIUserBlock(block.Content, m.width))
		case "assistant":
			if block.Thinking != "" {
				out.WriteString(renderUIThinking(block.Thinking))
			}
			if strings.TrimSpace(block.Content) != "" {
				content := block.Content
				if renderer != nil {
					if rendered, err := renderer.Render(content); err == nil {
						content = strings.TrimSpace(rendered)
					}
				}
				for _, line := range strings.Split(content, "\n") {
					out.WriteString("  " + line + "\n")
				}
			}
		case "tool":
			for _, tool := range block.Tools {
				out.WriteString(renderUITool(tool, m.width))
			}
		case "section":
			out.WriteString(renderUISection(block.Title, block.Content, m.width))
		default:
			for _, line := range strings.Split(block.Content, "\n") {
				out.WriteString(uiDimText.Render("  " + line))
				out.WriteString("\n")
			}
		}
	}
	return strings.TrimRight(out.String(), "\n")
}

func renderUIUserBlock(content string, width int) string {
	var b strings.Builder
	sepWidth := min(max(18, width-8), 52)
	b.WriteString(uiDimText.Render("  " + strings.Repeat("─", sepWidth)))
	b.WriteString("\n")
	for idx, line := range strings.Split(content, "\n") {
		prefix := "    "
		if idx == 0 {
			prefix = "  " + uiSecondaryText.Bold(true).Render(">") + " "
		}
		b.WriteString(prefix + line + "\n")
	}
	return b.String()
}

func renderUIThinking(content string) string {
	var b strings.Builder
	b.WriteString("  " + uiSecondaryText.Render("✧ ") + uiDimText.Render("Thinking…") + "\n")
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(uiThinkText.Render("  ⎿  " + line))
		b.WriteString("\n")
	}
	return b.String()
}

func renderUITool(tool *uiToolState, width int) string {
	var b strings.Builder
	dot := uiPrimaryText.Render("⏺")
	nameStyle := uiPrimaryText.Bold(true)
	if tool.Status == "done" {
		dot = uiSuccessText.Render("✓")
		nameStyle = uiSuccessText.Bold(true)
	} else if tool.IsError {
		dot = uiErrorText.Render("✗")
		nameStyle = uiErrorText
	}
	b.WriteString(fmt.Sprintf("  %s %s%s\n", dot, nameStyle.Render(tool.Name), uiMutedText.Render("("+truncateUI(tool.Input, 72)+")")))
	switch {
	case tool.Status == "running":
		b.WriteString("    " + uiDimText.Render("Running...") + "\n")
	case tool.Output != "":
		lines := strings.Split(strings.TrimSpace(tool.Output), "\n")
		maxLines := 6
		if len(lines) < maxLines {
			maxLines = len(lines)
		}
		for _, line := range lines[:maxLines] {
			b.WriteString("    " + uiDimText.Render(truncateUI(line, width-8)) + "\n")
		}
		if len(lines) > maxLines {
			b.WriteString("    " + uiDimText.Render(fmt.Sprintf("... +%d lines", len(lines)-maxLines)) + "\n")
		}
	default:
		b.WriteString("    " + uiDimText.Render("done") + "\n")
	}
	return b.String()
}

func renderUISection(title, content string, width int) string {
	boxWidth := min(max(28, width-4), 96)
	border := lipgloss.RoundedBorder()
	style := lipgloss.NewStyle().Border(border).BorderForeground(uiDim).Padding(0, 1).Width(boxWidth - 2)
	return style.Render(uiPrimaryText.Render(title) + "\n" + content)
}

func (m *uiModel) renderWelcome() string {
	cwd := m.session.RuntimeState().Cwd
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + cwd[len(home):]
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(uiDim).
		Padding(1, 2)
	lines := []string{
		uiPrimaryText.Bold(true).Render("modu_code"),
		uiMutedText.Render("coding_agent interactive console"),
		"",
		uiMutedText.Render("cwd    " + cwd),
		uiMutedText.Render("model  " + m.model.Name),
		uiMutedText.Render("help   /help"),
	}
	return "\n" + box.Render(strings.Join(lines, "\n")) + "\n"
}

func (m *uiModel) latestRunningTool() *uiToolState {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		block := m.blocks[i]
		for j := len(block.Tools) - 1; j >= 0; j-- {
			if block.Tools[j].Status == "running" {
				return block.Tools[j]
			}
		}
	}
	return nil
}

func (m *uiModel) appendBlock(block uiBlock) {
	m.blocks = append(m.blocks, block)
	m.refreshViewport()
}

func (m *uiModel) currentAssistantBlock() *uiBlock {
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].Kind != "assistant" {
		m.blocks = append(m.blocks, uiBlock{Kind: "assistant", Timestamp: time.Now()})
	}
	return &m.blocks[len(m.blocks)-1]
}

func (m *uiModel) currentToolBlock() *uiBlock {
	if len(m.blocks) == 0 || m.blocks[len(m.blocks)-1].Kind != "tool" {
		m.blocks = append(m.blocks, uiBlock{Kind: "tool", Timestamp: time.Now()})
	}
	return &m.blocks[len(m.blocks)-1]
}

func (m *uiModel) handleAgentEvent(ev agent.AgentEvent) {
	switch ev.Type {
	case agent.EventTypeAgentStart:
		m.queryActive = true
		m.statusMsg = "thinking"
	case agent.EventTypeMessageUpdate:
		if ev.StreamEvent == nil {
			break
		}
		switch ev.StreamEvent.Type {
		case types.EventThinkingDelta:
			block := m.currentAssistantBlock()
			block.Thinking += ev.StreamEvent.Delta
		case types.EventTextDelta:
			block := m.currentAssistantBlock()
			block.Content += ev.StreamEvent.Delta
		}
	case agent.EventTypeToolExecutionStart:
		var args map[string]any
		if margs, ok := ev.Args.(map[string]any); ok {
			args = margs
		}
		block := m.currentToolBlock()
		block.Tools = append(block.Tools, &uiToolState{
			ID:     ev.ToolCallID,
			Name:   ev.ToolName,
			Input:  formatApprovalArgs(args),
			Status: "running",
		})
	case agent.EventTypeToolExecutionEnd:
		block := m.currentToolBlock()
		for _, tool := range block.Tools {
			if tool.ID == ev.ToolCallID || tool.Name == ev.ToolName {
				tool.Status = "done"
				tool.IsError = ev.IsError
				tool.Output = fullResultText(ev)
				if ev.IsError {
					tool.Status = "error"
				}
				break
			}
		}
	case agent.EventTypeAgentEnd:
		m.queryActive = false
		m.statusMsg = ""
	}
	m.refreshViewport()
}

func fullResultText(ev agent.AgentEvent) string {
	if ev.Result == nil {
		return ""
	}
	if texts, ok := ev.Result.([]types.ContentBlock); ok {
		var parts []string
		for _, block := range texts {
			if t, ok := block.(*types.TextContent); ok && t != nil {
				parts = append(parts, t.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	switch v := ev.Result.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		raw, _ := json.Marshal(v)
		return strings.TrimSpace(string(raw))
	}
}

func (m *uiModel) submitLineCmd(line string) tea.Cmd {
	if strings.HasPrefix(line, "/") {
		return func() tea.Msg {
			printer := &uiSlashPrinter{}
			handled, exit := handleSlash(m.ctx, line, m.session, printer, m.model, m.mailboxRuntime)
			if !handled {
				return uiExternalInfoMsg{text: "unknown command: " + line}
			}
			if printer.clear {
				return uiClearScreenMsg{}
			}
			if exit {
				return uiQuitMsg{}
			}
			text := strings.TrimSpace(strings.Join(printer.lines, "\n"))
			return uiExternalInfoMsg{text: text}
		}
	}
	m.appendBlock(uiBlock{Kind: "user", Content: line, Timestamp: time.Now()})
	m.queryActive = true
	m.statusMsg = "thinking"
	return func() tea.Msg {
		if !m.promptMu.TryLock() {
			return uiPromptDoneMsg{err: fmt.Errorf("session is busy")}
		}
		defer m.promptMu.Unlock()
		err := m.session.Prompt(m.ctx, line)
		return uiPromptDoneMsg{err: err}
	}
}

func (m *uiModel) syncInputHeight() {
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > 8 {
		lines = 8
	}
	m.input.SetHeight(lines)
}

func (m *uiModel) captureHistory(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	for idx, item := range m.history {
		if item == line {
			m.history = append(m.history[:idx], m.history[idx+1:]...)
			break
		}
	}
	m.history = append(m.history, line)
	if len(m.history) > 200 {
		m.history = m.history[len(m.history)-200:]
	}
	m.historyIdx = -1
	m.historyDraft = ""
}

func (m *uiModel) navigateHistory(direction int) {
	if len(m.history) == 0 {
		return
	}
	if direction < 0 {
		if m.historyIdx == -1 {
			m.historyDraft = m.input.Value()
			m.historyIdx = len(m.history) - 1
		} else if m.historyIdx > 0 {
			m.historyIdx--
		}
	} else {
		if m.historyIdx == -1 {
			return
		}
		if m.historyIdx < len(m.history)-1 {
			m.historyIdx++
		} else {
			m.historyIdx = -1
			m.input.SetValue(m.historyDraft)
			m.input.CursorEnd()
			return
		}
	}
	if m.historyIdx >= 0 {
		m.input.SetValue(m.history[m.historyIdx])
		m.input.CursorEnd()
	}
}

func (m *uiModel) resolveApproval(decision string) {
	if m.pendingPerm == nil {
		return
	}
	m.pendingPerm.Response <- decision
	m.pendingPerm = nil
	m.statusMsg = ""
	m.refreshViewport()
}

func (m *uiModel) waitApprovalCmd() tea.Cmd {
	if m.approvalCh == nil {
		return nil
	}
	return func() tea.Msg {
		req := <-m.approvalCh
		return uiApprovalRequestMsg{req: req}
	}
}

func (m *uiModel) waitApprovalCancelCmd(toolCallID string, cancel <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-cancel
		return uiApprovalCancelMsg{toolCallID: toolCallID}
	}
}

func loadHistoryFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	items := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out = append(out, item)
		}
	}
	return out, nil
}

func saveHistoryFile(path string, history []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(history, "\n")+"\n"), 0o600)
}

func formatApprovalArgs(args map[string]any) string {
	if args == nil {
		return ""
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, args[key]))
	}
	return strings.Join(parts, " ")
}

func truncateUI(s string, max int) string {
	if max <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	if max == 1 {
		return string(rs[:1])
	}
	return string(rs[:max-1]) + "…"
}

func formatUIDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func runInteractiveUI(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, mailboxRuntime *moduCodeMailboxRuntime, noApprove bool) error {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
		// Surface the restore in the initial viewport without sending a Tea
		// message before the program has started.
		// The session state is already restored internally by this point.
		// Keep this lightweight; full transcript hydration can come later.
		_ = n
	}
	histFile := session.InputHistoryFile()
	var approvalCh chan tui.ApprovalRequest
	if !noApprove {
		approvalCh = make(chan tui.ApprovalRequest)
	}
	var promptMu sync.Mutex

	ui := newUIModel(ctx, session, model, mailboxRuntime, histFile, approvalCh, &promptMu, "")
	if count := len(session.GetMessages()); count > 0 {
		ui.blocks = append(ui.blocks, uiBlock{
			Kind:      "system",
			Content:   fmt.Sprintf("restored previous session — %d messages", count),
			Timestamp: time.Now(),
		})
	}
	if history, err := loadHistoryFile(histFile); err == nil {
		ui.history = history
	}

	program := tea.NewProgram(ui)

	if approvalCh != nil {
		session.SetToolApprovalCallback(func(toolName, toolCallID string, args map[string]any) (agent.ToolApprovalDecision, error) {
			respCh := make(chan string, 1)
			approvalCh <- tui.ApprovalRequest{
				ToolName:   toolName,
				ToolCallID: toolCallID,
				Args:       args,
				Response:   respCh,
			}
			return agent.ToolApprovalDecision(<-respCh), nil
		})
	}

	unsub := session.Subscribe(func(ev agent.AgentEvent) {
		program.Send(uiAgentEventMsg{event: ev})
	})
	defer unsub()

	unsubSession := session.SubscribeSession(func(ev coding_agent.SessionEvent) {
		program.Send(uiSessionEventMsg{event: ev})
	})
	defer unsubSession()

	printer := &uiBridgePrinter{program: program}

	token := os.Getenv("MOMS_TG_TOKEN")
	if tgCfg, err := loadTelegramConfig(); err == nil && tgCfg.Token != "" {
		token = tgCfg.Token
	}
	if token != "" {
		attachDir := os.TempDir() + "/modu_code_tg"
		if username, err := startTelegramBackground(ctx, token, attachDir, session, printer, &promptMu, approvalCh); err == nil {
			ui.tgUsername = username
		}
	}

	_, err := program.Run()
	return err
}
