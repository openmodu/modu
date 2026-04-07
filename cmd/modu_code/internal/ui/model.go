package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_code/internal/mailboxrt"
)

// ─── Tea messages ────────────────────────────────

type uiAgentEventMsg struct{ event agent.AgentEvent }
type uiSessionEventMsg struct{ event coding_agent.SessionEvent }
type uiPromptDoneMsg struct{ err error }
type uiStreamTickMsg struct{}
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
	uiStateNormal // vim normal mode
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
	Streaming bool // true while LLM is still streaming this block; skip glamour render
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
	mailboxRuntime *mailboxrt.Runtime
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

	// Per-query cancellation
	queryCancel context.CancelFunc

	// Streaming viewport batching
	viewportDirty bool // set by streaming deltas; cleared by stream tick

	// Toggle modes
	transcriptMode bool
	mouseMode      bool   // true = mouse scroll active, false = terminal text selection
	pendingKey     string // vim multi-key prefix (g, y)

	// Slash autocomplete
	slashMatches []slashCommandDef
	showSlash    bool
}

func newUIModel(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, mailboxRuntime *mailboxrt.Runtime, histFile string, approvalCh chan tui.ApprovalRequest, promptMu *sync.Mutex, tgUsername string) *uiModel {
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
		mouseMode:      true,
	}
}

func (m *uiModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, streamTickCmd())
}

// streamTickCmd schedules the next stream viewport refresh tick (50ms).
func streamTickCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return uiStreamTickMsg{} })
}

// ─── Block helpers ───────────────────────────────

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
