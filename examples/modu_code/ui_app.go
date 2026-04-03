package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"
)

// ─── Tea messages ────────────────────────────────

type uiAgentEventMsg struct{ event agent.AgentEvent }
type uiSessionEventMsg struct{ event coding_agent.SessionEvent }
type uiPromptDoneMsg struct{ err error }
type uiApprovalRequestMsg struct{ req tui.ApprovalRequest }
type uiApprovalCancelMsg struct{ toolCallID string }
type uiExternalInfoMsg struct{ text string }
type uiExternalUserMsg struct{ text string }
type uiShellResultMsg struct {
	cmd    string
	output string
	err    error
}
type uiClearScreenMsg struct{}
type uiQuitMsg struct{}

// ─── UI states ───────────────────────────────────

type uiState int

const (
	uiStateInit uiState = iota
	uiStateInput
	uiStateQuerying
	uiStatePermission
)

// ─── Display blocks ──────────────────────────────

type uiBlock struct {
	Kind      string
	Title     string
	Content   string
	RawText   string
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

// ─── Slash autocomplete ──────────────────────────

type slashCommandDef struct {
	Name        string
	Description string
}

var uiSlashCommands = []slashCommandDef{
	{"/help", "show help"},
	{"/quit", "exit"},
	{"/clear", "clear screen and session"},
	{"/model", "show current model"},
	{"/compact", "compact context"},
	{"/tokens", "show token usage"},
	{"/tools", "list active tools"},
	{"/agents", "list subagents"},
	{"/todos", "show todo list"},
	{"/tasks", "show background tasks"},
	{"/hints", "show harness hints"},
	{"/runtime", "show runtime paths"},
	{"/dashboard", "runtime summary"},
	{"/state", "runtime state snapshot"},
	{"/config", "show effective config"},
	{"/config-template", "default config template"},
	{"/logs", "harness JSONL log paths"},
	{"/artifacts", "harness artifact paths"},
	{"/bridge", "harness event bridge dirs"},
	{"/actions", "harness action statuses"},
	{"/plan", "toggle plan mode"},
	{"/worktree", "toggle worktree mode"},
	{"/skills", "list skills"},
	{"/telegram", "Telegram bot config"},
}

func matchSlashCommands(prefix string) []slashCommandDef {
	var out []slashCommandDef
	for _, cmd := range uiSlashCommands {
		if strings.HasPrefix(cmd.Name, prefix) {
			out = append(out, cmd)
		}
	}
	return out
}

// ─── Printer implementations ─────────────────────

type uiSlashPrinter struct {
	lines []string
	clear bool
}

func (p *uiSlashPrinter) PrintInfo(msg string) { p.lines = append(p.lines, msg) }
func (p *uiSlashPrinter) PrintError(err error) {
	if err != nil {
		p.lines = append(p.lines, "error: "+err.Error())
	}
}
func (p *uiSlashPrinter) PrintSection(title string, lines []string) {
	p.lines = append(p.lines, title)
	p.lines = append(p.lines, lines...)
}
func (p *uiSlashPrinter) ClearScreen() { p.clear = true }

type uiBridgePrinter struct {
	program *tea.Program
}

func (p *uiBridgePrinter) PrintInfo(msg string) {
	if p.program != nil {
		p.program.Send(uiExternalInfoMsg{text: msg})
	}
}
func (p *uiBridgePrinter) PrintError(err error) {
	if err == nil || p.program == nil {
		return
	}
	p.program.Send(uiExternalInfoMsg{text: "error: " + err.Error()})
}
func (p *uiBridgePrinter) PrintUser(msg string) {
	if p.program != nil {
		p.program.Send(uiExternalUserMsg{text: msg})
	}
}
func (p *uiBridgePrinter) ClearLine() {}

// ─── Model ───────────────────────────────────────

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
	state    uiState
	viewport viewport.Model
	input    *uiInputModel
	spinner  spinner.Model

	blocks             []uiBlock
	queryActive        bool
	userScrolled       bool
	errMsg             string
	statusMsg          string
	pendingPerm        *tui.ApprovalRequest
	sessionStart       time.Time
	approvalCmdStarted bool

	// Query tracking
	spinnerVerb    string
	queryStartTime time.Time
	thinkingStart  time.Time

	// Toggle modes
	transcriptMode bool

	// Slash autocomplete
	slashMatches []slashCommandDef
	showSlash    bool
}

func newUIModel(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, mailboxRuntime *moduCodeMailboxRuntime, histFile string, approvalCh chan tui.ApprovalRequest, promptMu *sync.Mutex, tgUsername string) *uiModel {
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
		input:          newUIInputModel(),
		spinner:        sp,
		state:          uiStateInit,
		sessionStart:   time.Now(),
		spinnerVerb:    randomSpinnerVerb(),
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
		if inputHeight > 0 {
			inputHeight--
		}
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
		if m.state == uiStateInit {
			m.state = uiStateInput
			m.input.Focus()
		}
		if !m.approvalCmdStarted && m.approvalCh != nil {
			m.approvalCmdStarted = true
			return m, m.waitApprovalCmd()
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if msg.Button == tea.MouseButtonWheelUp {
			m.userScrolled = true
		}
		if msg.Button == tea.MouseButtonWheelDown && m.viewport.AtBottom() {
			m.userScrolled = false
		}
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)

	case uiPromptDoneMsg:
		m.queryActive = false
		m.state = uiStateInput
		m.input.Focus()
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.appendBlock(uiBlock{Kind: "system", Content: "error: " + msg.err.Error(), Timestamp: time.Now()})
		} else {
			m.errMsg = ""
		}
		m.statusMsg = ""
		m.thinkingStart = time.Time{}
		_ = m.session.SaveMessages()
		_ = saveHistoryFile(m.histFile, m.input.History())
		m.refreshViewport()
		fmt.Print("\a") // bell notification
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
		m.state = uiStatePermission
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
			m.state = uiStateQuerying
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

	case uiShellResultMsg:
		out := strings.TrimRight(msg.output, "\n")
		if msg.err != nil {
			out += "\n" + msg.err.Error()
		}
		m.appendBlock(uiBlock{Kind: "system", Content: out, Timestamp: time.Now()})
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

// ─── Key handling ────────────────────────────────

func (m *uiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.state == uiStateQuerying || m.state == uiStatePermission {
			m.session.Abort()
			m.queryActive = false
			m.pendingPerm = nil
			m.state = uiStateInput
			m.input.Focus()
			m.statusMsg = "interrupted"
			m.refreshViewport()
			return m, nil
		}
		return m, tea.Quit
	case "ctrl+d":
		if m.state == uiStateInput && strings.TrimSpace(m.input.Value()) == "" {
			return m, tea.Quit
		}
	case "ctrl+l":
		m.blocks = nil
		m.errMsg = ""
		m.statusMsg = "cleared"
		m.refreshViewport()
		return m, nil
	case "ctrl+o":
		m.transcriptMode = !m.transcriptMode
		m.refreshViewport()
		return m, nil
	}

	switch m.state {
	case uiStatePermission:
		return m.handlePermissionKey(msg)
	case uiStateQuerying:
		return m.handleQueryingKey(msg)
	default:
		return m.handleInputKey(msg)
	}
}

func (m *uiModel) handleQueryingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "pgup", "ctrl+b":
		m.viewport.HalfViewUp()
		m.userScrolled = true
		return m, nil
	case "pgdown", "ctrl+f":
		m.viewport.HalfViewDown()
		if m.viewport.AtBottom() {
			m.userScrolled = false
		}
		return m, nil
	case "home":
		m.viewport.GotoTop()
		m.userScrolled = true
		return m, nil
	case "end":
		m.viewport.GotoBottom()
		m.userScrolled = false
		return m, nil
	case "enter":
		if strings.TrimSpace(m.input.RawValue()) != "" {
			m.statusMsg = "busy: press ctrl+c to interrupt"
		}
		return m, nil
	}

	submitted, cmd := m.input.Update(msg)
	if submitted {
		return m, nil
	}
	m.updateSlashAutocomplete()
	return m, cmd
}

func (m *uiModel) handleScrollKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.viewport.LineUp(1)
		m.userScrolled = true
	case "down", "j":
		m.viewport.LineDown(1)
		if m.viewport.AtBottom() {
			m.userScrolled = false
		}
	case "pgup", "ctrl+b":
		m.viewport.HalfViewUp()
		m.userScrolled = true
	case "pgdown", "ctrl+f":
		m.viewport.HalfViewDown()
		if m.viewport.AtBottom() {
			m.userScrolled = false
		}
	case "home", "g":
		m.viewport.GotoTop()
		m.userScrolled = true
	case "end", "G":
		m.viewport.GotoBottom()
		m.userScrolled = false
	}
	return m, nil
}

func (m *uiModel) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "pgup":
		m.viewport.HalfViewUp()
		m.userScrolled = true
		return m, nil
	case "pgdown":
		m.viewport.HalfViewDown()
		if m.viewport.AtBottom() {
			m.userScrolled = false
		}
		return m, nil
	case "tab":
		if m.showSlash && len(m.slashMatches) > 0 {
			m.input.ta.Reset()
			m.input.ta.InsertString(m.slashMatches[0].Name + " ")
			m.showSlash = false
			m.slashMatches = nil
			return m, nil
		}
	case "esc":
		if m.showSlash {
			m.showSlash = false
			m.slashMatches = nil
			return m, nil
		}
		m.input.Reset()
		return m, nil
	case "enter":
		if strings.TrimSpace(m.input.Value()) == "" {
			return m, nil
		}
		line := strings.TrimSpace(m.input.Value())
		m.input.Reset()
		m.showSlash = false
		m.slashMatches = nil
		return m, m.submitLineCmd(line)
	}

	submitted, cmd := m.input.Update(msg)
	if submitted {
		line := m.input.Value()
		m.input.Reset()
		m.showSlash = false
		m.slashMatches = nil
		if line == "" {
			return m, nil
		}
		return m, m.submitLineCmd(line)
	}
	m.updateSlashAutocomplete()
	return m, cmd
}

func (m *uiModel) handlePermissionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingPerm == nil {
		return m, nil
	}
	switch strings.ToLower(msg.String()) {
	case "y", "enter":
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

func (m *uiModel) updateSlashAutocomplete() {
	val := m.input.Value()
	if strings.HasPrefix(val, "/") && !strings.Contains(val, " ") {
		m.slashMatches = matchSlashCommands(val)
		m.showSlash = len(m.slashMatches) > 0
	} else {
		m.showSlash = false
		m.slashMatches = nil
	}
}

// ─── Submit ──────────────────────────────────────

func (m *uiModel) submitLineCmd(line string) tea.Cmd {
	// Shell escape: ! <cmd>
	if strings.HasPrefix(line, "! ") {
		shellCmd := strings.TrimPrefix(line, "! ")
		m.appendBlock(uiBlock{Kind: "system", Content: "$ " + shellCmd, Timestamp: time.Now()})
		return func() tea.Msg {
			out, err := exec.Command("bash", "-c", shellCmd).CombinedOutput()
			return uiShellResultMsg{cmd: shellCmd, output: string(out), err: err}
		}
	}

	// Slash commands
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

	// Regular prompt
	m.appendBlock(uiBlock{Kind: "user", Content: line, Timestamp: time.Now()})
	m.queryActive = true
	m.state = uiStateQuerying
	m.input.Focus()
	m.statusMsg = "thinking"
	m.userScrolled = false
	m.spinnerVerb = randomSpinnerVerb()
	m.queryStartTime = time.Now()
	m.thinkingStart = time.Time{}
	return func() tea.Msg {
		if !m.promptMu.TryLock() {
			return uiPromptDoneMsg{err: fmt.Errorf("session is busy")}
		}
		defer m.promptMu.Unlock()
		err := m.session.Prompt(m.ctx, line)
		return uiPromptDoneMsg{err: err}
	}
}

// ─── Agent events ────────────────────────────────

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
			if m.thinkingStart.IsZero() {
				m.thinkingStart = time.Now()
			}
		case types.EventTextDelta:
			block := m.currentAssistantBlock()
			block.RawText += ev.StreamEvent.Delta
			thinking, content := extractThinkText(block.RawText)
			if thinking != "" {
				block.Thinking = thinking
			}
			block.Content = content
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
			Input:  formatToolInput(ev.ToolName, args),
			Status: "running",
		})
		m.spinnerVerb = uiToolVerb(ev.ToolName) + " " + ev.ToolName

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
		// Reset spinner verb after tool finishes
		m.spinnerVerb = randomSpinnerVerb()

	case agent.EventTypeMessageEnd:
		msg, ok := assistantMessageFromEvent(ev.Message)
		if !ok {
			break
		}
		block := m.currentAssistantBlock()
		for _, content := range msg.Content {
			switch c := content.(type) {
			case *types.ThinkingContent:
				if c != nil && strings.TrimSpace(c.Thinking) != "" {
					block.Thinking = c.Thinking
				}
			case *types.TextContent:
				if c != nil && strings.TrimSpace(c.Text) != "" {
					block.RawText = c.Text
					thinking, content := extractThinkText(c.Text)
					if thinking != "" {
						block.Thinking = thinking
					}
					block.Content = content
				}
			}
		}

	case agent.EventTypeAgentEnd:
		m.queryActive = false
		m.statusMsg = ""
	}
	m.refreshViewport()
}

// ─── View ────────────────────────────────────────

func (m *uiModel) View() string {
	if !m.ready {
		return "  " + m.spinner.View() + " loading modu_code..."
	}
	var parts []string
	parts = append(parts, m.renderHeader())
	if meta := m.renderSessionMeta(); meta != "" {
		parts = append(parts, meta)
	}
	parts = append(parts, m.viewport.View())
	switch m.state {
	case uiStatePermission:
		parts = append(parts, m.renderPermissionPrompt())
	case uiStateQuerying:
		parts = append(parts, m.renderActivityLine())
		if m.showSlash && len(m.slashMatches) > 0 {
			parts = append(parts, m.renderSlashSuggestions())
		}
		parts = append(parts, m.renderInputArea())
	case uiStateInit:
		parts = append(parts, "  "+m.spinner.View()+" Initializing...")
	default:
		if m.showSlash && len(m.slashMatches) > 0 {
			parts = append(parts, m.renderSlashSuggestions())
		}
		parts = append(parts, m.renderInputArea())
	}
	parts = append(parts, m.renderStatusBar())
	return strings.Join(parts, "\n")
}

func (m *uiModel) renderHeader() string {
	line := "  " + uiPrimaryText.Render("●") + " " + uiPrimaryText.Bold(true).Render("modu_code")
	line += uiMutedText.Render("  " + m.model.Name)
	if m.tgUsername != "" {
		line += uiMutedText.Render("  @" + m.tgUsername)
	}
	return lipgloss.NewStyle().Width(m.width).Render(line)
}

func (m *uiModel) renderSessionMeta() string {
	var parts []string
	if m.session != nil {
		stats := m.session.GetSessionStats()
		if stats.TotalTokens > 0 {
			parts = append(parts, uiMutedText.Render(fmt.Sprintf("~%d tokens", stats.TotalTokens)))
		}
		if m.session.IsPlanMode() {
			parts = append(parts, uiSecondaryText.Render("plan"))
		}
		if m.session.ActiveWorktree() != "" {
			parts = append(parts, uiWarningText.Render("worktree"))
		}
	}
	if m.transcriptMode {
		parts = append(parts, uiDimText.Render("expanded"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "    " + strings.Join(parts, uiDimText.Render(" · "))
}

func (m *uiModel) renderStatusBar() string {
	var parts []string
	if m.statusMsg != "" {
		parts = append(parts, uiPrimaryText.Render(m.statusMsg))
	}
	if m.errMsg != "" {
		parts = append(parts, uiErrorText.Render(m.errMsg))
	}
	left := strings.Join(parts, uiDimText.Render(" · "))
	right := ""
	if m.viewport.Height > 0 {
		right = uiDimText.Render(fmt.Sprintf(" %d%% ", int(m.viewport.ScrollPercent()*100)))
	}
	if right != "" && m.viewport.AtBottom() {
		right = ""
	}
	if len(parts) == 0 && right == "" {
		return ""
	}
	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-1)
	return lipgloss.NewStyle().Width(m.width).PaddingLeft(1).Render(left + strings.Repeat(" ", gap) + right)
}

func (m *uiModel) renderActivityLine() string {
	elapsed := ""
	if !m.queryStartTime.IsZero() {
		elapsed = " (" + formatUIDuration(time.Since(m.queryStartTime)) + ")"
	}
	label := m.spinnerVerb + "..."
	if label == "..." {
		label = "Thinking..."
	}
	return "  " + m.spinner.View() + " " + uiDimText.Render(label) + uiDimText.Render(elapsed)
}

func (m *uiModel) renderInputArea() string {
	box := lipgloss.NewStyle().PaddingLeft(1).Render(m.input.View())
	hint := "enter send  shift+enter newline  tab complete  ctrl+o expand  /help"
	if m.state == uiStateQuerying {
		hint = "draft while waiting  enter waits  ctrl+c interrupt  shift+enter newline"
	}
	hintText := uiDimText.Render(hint)
	areaStyle := lipgloss.NewStyle().MarginTop(1)
	if m.errMsg != "" {
		return areaStyle.Render(uiErrorText.Render("  ! "+m.errMsg) + "\n" + box + "\n  " + hintText)
	}
	return areaStyle.Render(box + "\n  " + hintText)
}

func (m *uiModel) renderPermissionPrompt() string {
	if m.pendingPerm == nil {
		return ""
	}
	input := formatToolInput(m.pendingPerm.ToolName, m.pendingPerm.Args)
	if len(input) > 400 {
		input = input[:400] + "..."
	}
	lines := []string{
		fmt.Sprintf("  %s %s", uiWarningText.Render("●"), uiPrimaryText.Bold(true).Render(m.pendingPerm.ToolName)),
		hookPad + uiDimText.Render(input),
		dotPad + uiSuccessText.Bold(true).Render("[Y]es") + "  " +
			uiErrorText.Render("[N]o") + "  " +
			uiWarningText.Bold(true).Render("[A]lways allow") + "  " +
			uiMutedText.Render("[D]eny always"),
	}
	return strings.Join(lines, "\n")
}

func (m *uiModel) renderSlashSuggestions() string {
	maxShow := 8
	if len(m.slashMatches) < maxShow {
		maxShow = len(m.slashMatches)
	}
	var inner strings.Builder
	for i := 0; i < maxShow; i++ {
		cmd := m.slashMatches[i]
		name := lipgloss.NewStyle().Bold(true).Foreground(uiSecondary).Render(cmd.Name)
		desc := uiDimText.Render("  " + cmd.Description)
		prefix := "  " + uiDimText.Render("·") + " "
		inner.WriteString(prefix + name + desc)
		if i < maxShow-1 {
			inner.WriteString("\n")
		}
	}
	if len(m.slashMatches) > maxShow {
		inner.WriteString(fmt.Sprintf("\n  %s", uiDimText.Render(fmt.Sprintf("+%d more", len(m.slashMatches)-maxShow))))
	}
	return inner.String()
}

// ─── Viewport ────────────────────────────────────

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

// dotPadW is the visual cell-width of the "  ● " prefix (accounts for
// ● being 2 cells in CJK terminals).
var dotPadW = lipgloss.Width("  ● ")

// dotPad is a pure-space indent that matches the dot-prefix width.
var dotPad = strings.Repeat(" ", dotPadW)

// hookPad uses "⎿" as a visual connector from the ● header to its body,
// padded to the same width as dotPad so content stays aligned.
var hookPad = "  " + uiDimText.Render("⎿") + strings.Repeat(" ", dotPadW-lipgloss.Width("  ⎿"))

// ─── Conversation rendering ───────────────────────

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
				out.WriteString(renderUIAssistantBlock(content))
			}
		case "tool":
			for _, tool := range block.Tools {
				out.WriteString(renderUITool(tool, m.transcriptMode, m.width))
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
	_ = width
	var b strings.Builder
	for idx, line := range strings.Split(content, "\n") {
		prefix := dotPad
		if idx == 0 {
			prefix = "  " + uiSecondaryText.Render("●") + " "
		}
		b.WriteString(prefix + line + "\n")
	}
	return b.String()
}

func renderUIThinking(content string) string {
	var b strings.Builder
	b.WriteString("  " + uiSecondaryText.Render("●") + " " + uiMutedText.Render("thinking") + "\n")
	first := true
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if first {
			b.WriteString(hookPad + uiMutedText.Render(line) + "\n")
			first = false
		} else {
			b.WriteString(dotPad + uiMutedText.Render(line) + "\n")
		}
	}
	return b.String()
}

func renderUIAssistantBlock(content string) string {
	var b strings.Builder
	b.WriteString("  " + uiPrimaryText.Render("●") + " " + uiMutedText.Render("assistant") + "\n")
	first := true
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if strings.TrimSpace(trimmed) == "" {
			b.WriteString("\n")
			continue
		}
		if first {
			b.WriteString(hookPad + trimmed + "\n")
			first = false
		} else {
			b.WriteString(dotPad + trimmed + "\n")
		}
	}
	return b.String()
}

func renderUITool(tool *uiToolState, expanded bool, width int) string {
	var b strings.Builder
	w := width

	dot := uiPrimaryText.Render("●")
	nameStyle := uiPrimaryText.Bold(true)
	if tool.Status == "done" {
		dot = uiSuccessText.Render("●")
		nameStyle = uiSuccessText.Bold(true)
	} else if tool.IsError || tool.Status == "error" {
		dot = uiErrorText.Render("●")
		nameStyle = uiErrorText
	}

	args := ""
	if tool.Input != "" {
		args = uiMutedText.Render("(" + truncateUI(tool.Input, 80) + ")")
	}
	b.WriteString(fmt.Sprintf("  %s %s%s\n", dot, nameStyle.Render(tool.Name), args))

	if tool.Status == "running" {
		b.WriteString(hookPad + uiDimText.Render("running") + "\n")
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
			b.WriteString(pad + uiErrorText.Render(truncateUI(line, w-8)) + "\n")
		}
		return b.String()
	}

	if tool.Output == "" {
		return b.String()
	}

	b.WriteString(renderUIToolOutput(tool.Name, tool.Output, expanded, w))
	return b.String()
}

func renderUIToolOutput(toolName, output string, expanded bool, w int) string {
	var b strings.Builder
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	total := len(lines)

	collapsedMax := 3
	expandedMax := 30

	// padAt returns hookPad for the first line (idx==0), dotPad otherwise.
	padAt := func(idx int) string {
		if idx == 0 {
			return hookPad
		}
		return dotPad
	}

	switch toolName {
	case "read":
		if !expanded {
			b.WriteString(hookPad + uiDimText.Render(fmt.Sprintf("%d lines", total)) + "\n")
		} else {
			show := min(total, expandedMax)
			for i := 0; i < show; i++ {
				b.WriteString(padAt(i) + uiDimText.Render(truncateUI(lines[i], w-8)) + "\n")
			}
			if total > show {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d lines", total-show)) + "\n")
			}
		}

	case "bash":
		show := collapsedMax
		if expanded {
			show = expandedMax
		}
		if total <= show {
			for i, line := range lines {
				b.WriteString(padAt(i) + uiDimText.Render(truncateUI(line, w-8)) + "\n")
			}
		} else {
			for i := 0; i < show; i++ {
				b.WriteString(padAt(i) + uiDimText.Render(truncateUI(lines[i], w-8)) + "\n")
			}
			b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d lines (ctrl+o to expand)", total-show)) + "\n")
		}

	case "edit":
		if !expanded {
			b.WriteString(hookPad + uiSuccessText.Render("updated") + "\n")
		} else {
			show := min(total, expandedMax)
			for i := 0; i < show; i++ {
				pad := padAt(i)
				line := lines[i]
				if strings.HasPrefix(line, "+") {
					b.WriteString(pad + uiSuccessText.Render(truncateUI(line, w-8)) + "\n")
				} else if strings.HasPrefix(line, "-") {
					b.WriteString(pad + uiErrorText.Render(truncateUI(line, w-8)) + "\n")
				} else {
					b.WriteString(pad + uiDimText.Render(truncateUI(line, w-8)) + "\n")
				}
			}
			if total > show {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d lines", total-show)) + "\n")
			}
		}

	case "write":
		if !expanded {
			b.WriteString(hookPad + uiSuccessText.Render("written") + "\n")
		} else {
			show := min(total, expandedMax)
			for i := 0; i < show; i++ {
				b.WriteString(padAt(i) + uiDimText.Render(truncateUI(lines[i], w-8)) + "\n")
			}
			if total > show {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d lines", total-show)) + "\n")
			}
		}

	case "glob":
		show := 8
		if expanded {
			show = 30
		}
		if total <= show {
			for i, line := range lines {
				b.WriteString(padAt(i) + uiDimText.Render(truncateUI(line, w-8)) + "\n")
			}
		} else {
			for i := 0; i < show; i++ {
				b.WriteString(padAt(i) + uiDimText.Render(truncateUI(lines[i], w-8)) + "\n")
			}
			b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d files", total-show)) + "\n")
		}

	case "grep":
		show := 5
		if expanded {
			show = 30
		}
		if total <= show {
			for i, line := range lines {
				b.WriteString(padAt(i) + uiDimText.Render(truncateUI(line, w-8)) + "\n")
			}
		} else {
			for i := 0; i < show; i++ {
				b.WriteString(padAt(i) + uiDimText.Render(truncateUI(lines[i], w-8)) + "\n")
			}
			b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d matches", total-show)) + "\n")
		}

	default:
		show := collapsedMax
		if expanded {
			show = expandedMax
		}
		if total <= show {
			idx := 0
			for _, line := range lines {
				if line != "" {
					b.WriteString(padAt(idx) + uiDimText.Render(truncateUI(line, w-8)) + "\n")
					idx++
				}
			}
		} else {
			b.WriteString(hookPad + uiDimText.Render(truncateUI(lines[0], w-8)) + "\n")
			b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d lines (ctrl+o to expand)", total-1)) + "\n")
		}
	}
	return b.String()
}

func renderUISection(title, content string, width int) string {
	_ = width
	return "  " + uiPrimaryText.Render(title) + "\n" + strings.TrimRight(content, "\n")
}

func (m *uiModel) renderWelcome() string {
	cwd := m.session.RuntimeState().Cwd
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + cwd[len(home):]
	}
	p := uiPrimaryText
	mt := uiMutedText
	d := uiDimText

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + p.Render("●") + " " + p.Bold(true).Render("modu_code") + mt.Render(" interactive coding session") + "\n")
	b.WriteString(hookPad + mt.Render(fmt.Sprintf("cwd   %s", cwd)) + "\n")
	b.WriteString(dotPad + mt.Render(fmt.Sprintf("model %s", m.model.Name)) + "\n")
	b.WriteString("\n")
	tips := d.Render("Enter") + mt.Render(" send") +
		d.Render("  Shift+Enter") + mt.Render(" newline") +
		d.Render("  Tab") + mt.Render(" complete") +
		d.Render("  /help") + mt.Render(" commands") +
		"\n" +
		d.Render("Ctrl+C") + mt.Render(" cancel") +
		d.Render("  Ctrl+O") + mt.Render(" expand output") +
		d.Render("  Ctrl+L") + mt.Render(" clear") +
		d.Render("  ! cmd") + mt.Render(" shell")

	for _, line := range strings.Split(tips, "\n") {
		b.WriteString("   " + line + "\n")
	}
	b.WriteString("\n\n")
	return b.String()
}

// ─── Block helpers ───────────────────────────────

func assistantMessageFromEvent(msg agent.AgentMessage) (types.AssistantMessage, bool) {
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

func (m *uiModel) latestRunningTool() *uiToolState {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		for j := len(m.blocks[i].Tools) - 1; j >= 0; j-- {
			if m.blocks[i].Tools[j].Status == "running" {
				return m.blocks[i].Tools[j]
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

// ─── Approval ────────────────────────────────────

func (m *uiModel) resolveApproval(decision string) {
	if m.pendingPerm == nil {
		return
	}
	m.pendingPerm.Response <- decision
	m.pendingPerm = nil
	m.state = uiStateQuerying
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

// ─── History persistence ─────────────────────────

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
		if fp, ok := args["file_path"].(string); ok {
			s := shortenUIPath(fp)
			if offset, ok := args["offset"].(float64); ok && offset > 0 {
				s += fmt.Sprintf(":%d", int(offset))
			}
			return s
		}
	case "write":
		if fp, ok := args["file_path"].(string); ok {
			return shortenUIPath(fp)
		}
	case "edit":
		if fp, ok := args["file_path"].(string); ok {
			old, _ := args["old_string"].(string)
			if len(old) > 40 {
				old = old[:40] + "..."
			}
			return fmt.Sprintf("%s: %q → ...", shortenUIPath(fp), old)
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

// ─── Utility ─────────────────────────────────────

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

func formatUIDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
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

func (m *uiModel) renderExitTranscript() string {
	var parts []string
	if meta := m.renderExitSessionMeta(); meta != "" {
		parts = append(parts, meta)
	}
	if conv := m.renderConversation(); conv != "" {
		parts = append(parts, conv)
	}
	if m.errMsg != "" {
		parts = append(parts, uiErrorText.Render("  ! "+m.errMsg))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// ─── Run ─────────────────────────────────────────

func runInteractiveUI(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, mailboxRuntime *moduCodeMailboxRuntime, noApprove bool) error {
	if n, err := session.RestoreMessages(); err == nil && n > 0 {
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
		ui.input.SetHistory(history)
	}

	program := tea.NewProgram(ui, tea.WithAltScreen(), tea.WithMouseCellMotion())

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

	finalModel, err := program.Run()
	if uiFinal, ok := finalModel.(*uiModel); ok && uiFinal != nil {
		if transcript := strings.TrimSpace(uiFinal.renderExitTranscript()); transcript != "" {
			fmt.Printf("\n%s\n", transcript)
		}
	}
	return err
}
