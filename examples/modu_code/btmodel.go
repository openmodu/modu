package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"
)

// ── tea messages ─────────────────────────────────────────────────────────────

type contentMsg struct{ text string }
type agentDoneMsg struct{ err error }
type slashDoneMsg struct{}
type approvalMsg struct{ req tui.ApprovalRequest }
type approvalCancelledMsg struct{}

// ── tea commands ──────────────────────────────────────────────────────────────

func listenContent(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		text, ok := <-ch
		if !ok {
			return nil
		}
		return contentMsg{text}
	}
}

func waitApproval(ch <-chan tui.ApprovalRequest) tea.Cmd {
	return func() tea.Msg {
		req, ok := <-ch
		if !ok {
			return nil
		}
		return approvalMsg{req}
	}
}

func waitApprovalCancel(cancel <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-cancel
		return approvalCancelledMsg{}
	}
}

// ── UI styles ─────────────────────────────────────────────────────────────────

var (
	sepStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#707070"))
	promptStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a0a0a0")).Bold(true)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#505050"))
	streamingHelp = lipgloss.NewStyle().Foreground(lipgloss.Color("#505050")).Italic(true)
	approvalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Bold(true)
)

// ── history helpers ───────────────────────────────────────────────────────────

func loadHistoryLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func saveHistoryLines(path string, lines []string) error {
	const maxHist = 200
	if len(lines) > maxHist {
		lines = lines[len(lines)-maxHist:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// ── model ─────────────────────────────────────────────────────────────────────

type replModel struct {
	viewport  viewport.Model
	textinput textinput.Model
	width     int
	height    int
	ready     bool

	// content is a shared pointer so that appends survive model value copies.
	content *strings.Builder

	// streaming state
	streaming    bool
	promptCancel context.CancelFunc
	promptMu     *sync.Mutex

	// dependencies
	session    *coding_agent.CodingSession
	renderer   *tui.BTRenderer
	contentCh  <-chan string // renderer output channel
	approvalCh <-chan tui.ApprovalRequest

	// input history
	history []string
	histIdx int
	saved   string // saved current input while browsing history

	// approval
	pendingApproval *tui.ApprovalRequest

	// metadata
	model *types.Model
	cwd   string
	ctx   context.Context
}

func newReplModel(
	session *coding_agent.CodingSession,
	renderer *tui.BTRenderer,
	contentCh <-chan string,
	approvalCh <-chan tui.ApprovalRequest,
	model *types.Model,
	cwd string,
	history []string,
	promptMu *sync.Mutex,
	ctx context.Context,
) replModel {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "Message…"
	ti.CharLimit = 0
	ti.Focus()

	return replModel{
		textinput:  ti,
		session:    session,
		renderer:   renderer,
		contentCh:  contentCh,
		approvalCh: approvalCh,
		content:    &strings.Builder{},
		model:      model,
		cwd:        cwd,
		history:    history,
		histIdx:    -1,
		promptMu:   promptMu,
		ctx:        ctx,
	}
}

// ── bubbletea interface ───────────────────────────────────────────────────────

func (m replModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textinput.Blink,
		listenContent(m.contentCh),
	}
	if m.approvalCh != nil {
		cmds = append(cmds, waitApproval(m.approvalCh))
	}
	return tea.Batch(cmds...)
}

func (m replModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		const footerH = 3 // separator + input + help
		vpH := m.height - footerH
		if vpH < 1 {
			vpH = 1
		}
		if !m.ready {
			m.viewport = viewport.New(m.width, vpH)
			m.viewport.SetContent(m.content.String())
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = vpH
		}
		m.textinput.Width = m.width - 3
		m.renderer.SetWidth(m.width)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case contentMsg:
		m.content.WriteString(msg.text)
		if m.ready {
			m.viewport.SetContent(m.content.String())
			m.viewport.GotoBottom()
		}
		cmds = append(cmds, listenContent(m.contentCh))

	case agentDoneMsg:
		m.streaming = false
		if msg.err != nil && msg.err != context.Canceled {
			m.renderer.PrintError(msg.err)
		}
		stats := m.session.GetSessionStats()
		m.renderer.PrintUsage(stats.TotalTokens)
		m.renderer.PrintSeparator()
		_ = m.session.SaveMessages()

	case slashDoneMsg:
		// async /compact finished; output already printed via renderer

	case approvalMsg:
		m.pendingApproval = &msg.req
		m.renderer.MarkApprovalShown()
		text := tui.FormatApproval(msg.req.ToolName, msg.req.Args, m.width)
		m.content.WriteString(text)
		if m.ready {
			m.viewport.SetContent(m.content.String())
			m.viewport.GotoBottom()
		}
		if msg.req.Cancel != nil {
			cmds = append(cmds, waitApprovalCancel(msg.req.Cancel))
		}

	case approvalCancelledMsg:
		if m.pendingApproval != nil {
			m.pendingApproval = nil
			m.content.WriteString(tui.FormatApprovalResult("allow") + "  (decided externally)\n")
			if m.ready {
				m.viewport.SetContent(m.content.String())
				m.viewport.GotoBottom()
			}
		}
		if m.approvalCh != nil {
			cmds = append(cmds, waitApproval(m.approvalCh))
		}
	}

	return m, tea.Batch(cmds...)
}

func (m replModel) View() string {
	if !m.ready {
		return "\n  Starting…\n"
	}

	w := m.width
	if w == 0 {
		w = 80
	}
	sep := sepStyle.Render(strings.Repeat("─", w))

	var helpStr string
	switch {
	case m.pendingApproval != nil:
		helpStr = approvalStyle.Render("  waiting for approval…")
	case m.streaming:
		helpStr = streamingHelp.Render("  esc to interrupt…")
	default:
		helpStr = helpStyle.Render("  /help for commands · ↑↓ history · PgUp/PgDn scroll · ctrl+c exit")
	}

	var inputStr string
	if m.streaming {
		inputStr = streamingHelp.Render("  …")
	} else {
		inputStr = promptStyle.Render("❯ ") + m.textinput.View()
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		m.viewport.View(),
		sep,
		inputStr,
		helpStr,
	)
}

// ── key handling ──────────────────────────────────────────────────────────────

func (m replModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// Approval mode: single-key decision.
	if m.pendingApproval != nil {
		var decision string
		switch msg.String() {
		case "y":
			decision = "allow"
		case "a":
			decision = "allow_always"
		case "n", "esc":
			decision = "deny"
		case "d":
			decision = "deny_always"
		default:
			return m, nil
		}
		m.pendingApproval.Response <- decision
		m.content.WriteString(tui.FormatApprovalResult(decision))
		m.pendingApproval = nil
		if m.ready {
			m.viewport.SetContent(m.content.String())
			m.viewport.GotoBottom()
		}
		if m.approvalCh != nil {
			cmds = append(cmds, waitApproval(m.approvalCh))
		}
		return m, tea.Batch(cmds...)
	}

	switch msg.Type {
	case tea.KeyCtrlC:
		if !m.streaming {
			return m, tea.Quit
		}

	case tea.KeyEsc:
		if m.streaming {
			if m.promptCancel != nil {
				m.promptCancel()
			}
			m.session.Abort()
			m.streaming = false
			m.renderer.PrintInfo("[interrupted]")
		}

	case tea.KeyCtrlD:
		return m, tea.Quit

	case tea.KeyCtrlY:
		// Strip ANSI escape codes and copy plain text to clipboard.
		plain := stripANSI(m.content.String())
		if err := clipboard.WriteAll(plain); err != nil {
			m.renderer.PrintInfo("copy failed: " + err.Error())
		} else {
			m.renderer.PrintInfo("copied to clipboard")
		}

	case tea.KeyCtrlR:
		m.renderer.ExpandLastTool()

	case tea.KeyUp:
		// History navigation when idle
		if !m.streaming && (m.textinput.Value() == "" || m.histIdx >= 0) {
			if m.histIdx < 0 {
				m.saved = m.textinput.Value()
				m.histIdx = len(m.history) - 1
			} else if m.histIdx > 0 {
				m.histIdx--
			}
			if m.histIdx >= 0 {
				m.textinput.SetValue(m.history[m.histIdx])
				m.textinput.CursorEnd()
			}
			return m, nil
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)

	case tea.KeyDown:
		if m.histIdx >= 0 {
			m.histIdx++
			if m.histIdx >= len(m.history) {
				m.histIdx = -1
				m.textinput.SetValue(m.saved)
			} else {
				m.textinput.SetValue(m.history[m.histIdx])
			}
			m.textinput.CursorEnd()
			return m, nil
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)

	case tea.KeyPgUp:
		m.viewport.HalfViewUp()

	case tea.KeyPgDown:
		m.viewport.HalfViewDown()

	case tea.KeyEnter:
		if m.streaming {
			return m, nil
		}
		input := strings.TrimSpace(m.textinput.Value())
		if input == "" {
			return m, nil
		}
		m.textinput.SetValue("")
		m.histIdx = -1
		m.saved = ""

		// Update history (deduplicate; keep most recent at end).
		if len(m.history) == 0 || m.history[len(m.history)-1] != input {
			m.history = append(m.history, input)
			if len(m.history) > 200 {
				m.history = m.history[len(m.history)-200:]
			}
		}

		// Slash commands.
		if strings.HasPrefix(input, "/") {
			handled, shouldQuit, cmd := m.handleSlashBT(input)
			if handled {
				if shouldQuit {
					return m, tea.Quit
				}
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				return m, tea.Batch(cmds...)
			}
		}

		// Show user prompt in viewport.
		m.renderer.PrintUser(input)

		// Start agent prompt in background goroutine.
		promptCtx, cancel := context.WithCancel(m.ctx)
		m.promptCancel = cancel
		m.streaming = true

		session := m.session
		renderer := m.renderer
		promptMu := m.promptMu
		unsub := session.Subscribe(func(ev agent.AgentEvent) {
			renderer.HandleEvent(ev)
		})

		cmds = append(cmds, func() tea.Msg {
			defer unsub()
			if promptMu != nil {
				if !promptMu.TryLock() {
					renderer.PrintInfo("[busy] session is processing another message, please wait…")
					return agentDoneMsg{nil}
				}
				defer promptMu.Unlock()
			}
			err := session.Prompt(promptCtx, input)
			return agentDoneMsg{err}
		})

	default:
		if !m.streaming {
			var cmd tea.Cmd
			m.textinput, cmd = m.textinput.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

// ── slash commands ────────────────────────────────────────────────────────────

func (m replModel) handleSlashBT(line string) (handled bool, shouldQuit bool, cmd tea.Cmd) {
	parts := strings.SplitN(line[1:], " ", 2)
	c := strings.ToLower(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch c {
	case "quit", "exit", "q":
		m.renderer.PrintInfo("bye!")
		return true, true, nil

	case "help", "h":
		printHelp(m.renderer)
		return true, false, nil

	case "clear":
		if err := m.session.ClearSavedMessages(); err != nil {
			m.renderer.PrintError(fmt.Errorf("clear session: %w", err))
		} else {
			m.content.Reset()
			if m.ready {
				m.viewport.SetContent("")
			}
			m.renderer.PrintInfo("session cleared")
		}
		return true, false, nil

	case "model":
		m.renderer.PrintInfo(fmt.Sprintf("current model: %s (%s / %s)",
			m.model.Name, m.model.ProviderID, m.model.ID))
		m.renderer.PrintInfo("restart with a different env var to switch models")
		return true, false, nil

	case "compact":
		m.renderer.PrintInfo("compacting context…")
		session := m.session
		renderer := m.renderer
		ctx := m.ctx
		return true, false, func() tea.Msg {
			if err := session.Compact(ctx); err != nil {
				renderer.PrintError(err)
			} else {
				renderer.PrintInfo("context compacted")
			}
			return slashDoneMsg{}
		}

	case "tokens":
		stats := m.session.GetSessionStats()
		m.renderer.PrintInfo(fmt.Sprintf("tokens used this session: %d", stats.TotalTokens))
		return true, false, nil

	case "tools":
		names := m.session.GetActiveToolNames()
		m.renderer.PrintInfo("active tools: " + strings.Join(names, ", "))
		return true, false, nil

	case "skills":
		skills := m.session.GetSkills()
		if len(skills) == 0 {
			m.renderer.PrintInfo("no skills found")
		} else {
			m.renderer.PrintInfo(fmt.Sprintf("available skills (%d):", len(skills)))
			for _, s := range skills {
				l := "  /" + s.Name
				if s.Description != "" {
					l += " — " + s.Description
				}
				if s.Source != "" {
					l += " [" + s.Source + "]"
				}
				m.renderer.PrintInfo(l)
			}
		}
		return true, false, nil

	case "telegram":
		handleTelegramCommand(arg, m.renderer)
		return true, false, nil

	default:
		return false, false, nil
	}
}

// stripANSI removes ANSI/VT100 escape sequences from s, returning plain text.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inEsc {
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == 'm' {
				inEsc = false
			}
			continue
		}
		if c == '\033' && i+1 < len(s) && s[i+1] == '[' {
			inEsc = true
			i++ // skip '['
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
