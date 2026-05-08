package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"

	"github.com/openmodu/modu/cmd/modu_code/internal/mailboxrt"
)

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
	pushed    bool // set after being pushed to terminal scrollback; prevents double-push
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
	{"/allow", "clear deny decision for a tool"},
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

// ─── Printer implementation ─────────────────────

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

// ─── Model ───────────────────────────────────────

type uiModel struct {
	session        *coding_agent.CodingSession
	model          *types.Model
	mailboxRuntime *mailboxrt.Runtime
	histFile       string
	promptMu       *sync.Mutex
	ctx            context.Context
	tgUsername     string

	width  int
	height int
	ready  bool
	state  uiState

	blocks      []uiBlock
	queryActive bool
	errMsg      string
	statusMsg   string
	pendingPerm *tui.ApprovalRequest

	// Query tracking
	queryStartTime time.Time
	thinkingStart  time.Time

	// Per-query cancellation
	queryCancel context.CancelFunc

	// Toggle modes
	transcriptMode bool
}

func newUIModel(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, mailboxRuntime *mailboxrt.Runtime, histFile string, approvalCh chan tui.ApprovalRequest, promptMu *sync.Mutex, tgUsername string) *uiModel {
	_ = approvalCh
	return &uiModel{
		session:        session,
		model:          model,
		mailboxRuntime: mailboxRuntime,
		histFile:       histFile,
		promptMu:       promptMu,
		ctx:            ctx,
		tgUsername:     tgUsername,
		state:          uiStateInit,
	}
}

// ─── Block helpers ───────────────────────────────

func (m *uiModel) appendBlock(block uiBlock) {
	m.blocks = append(m.blocks, block)
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
