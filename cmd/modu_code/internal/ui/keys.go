package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/openmodu/modu/cmd/modu_code/internal/slash"
)

// ─── Key handling ────────────────────────────────

// abortQuery cancels the running query context and resets state to input.
func (m *uiModel) abortQuery() tea.Cmd {
	if m.queryCancel != nil {
		m.queryCancel()
		m.queryCancel = nil
	}
	if m.session != nil {
		m.session.Abort()
		m.session.AbortBash()
	}
	m.queryActive = false
	m.pendingPerm = nil
	m.state = uiStateInput
	m.mouseMode = true
	m.input.Focus()
	m.statusMsg = "interrupted"
	m.refreshViewport()
	return tea.EnableMouseCellMotion
}

func (m *uiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.state == uiStateQuerying || m.state == uiStatePermission {
			return m, m.abortQuery()
		}
		return m, tea.Quit
	case "ctrl+d":
		if (m.state == uiStateInput || m.state == uiStateNormal) && strings.TrimSpace(m.input.Value()) == "" {
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
	case uiStateNormal:
		return m.handleNormalKey(msg)
	default:
		return m.handleInputKey(msg)
	}
}

// ─── Vim normal mode ─────────────────────────────

func (m *uiModel) enterInsert() (tea.Model, tea.Cmd) {
	m.state = uiStateInput
	m.pendingKey = ""
	m.mouseMode = true
	m.input.Focus()
	return m, tea.EnableMouseCellMotion
}

func (m *uiModel) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Handle pending prefix
	if m.pendingKey != "" {
		seq := m.pendingKey + key
		m.pendingKey = ""
		switch seq {
		case "gg":
			m.viewport.GotoTop()
			m.userScrolled = true
		case "yy", "yR": // yank last assistant response
			return m, m.yankLastResponseCmd()
		case "yG": // yank entire conversation
			return m, m.yankAllCmd()
		}
		return m, nil
	}

	switch key {
	// ── Back to insert ──
	case "i", "a", "esc":
		return m.enterInsert()

	// ── Scroll ──
	case "j":
		m.viewport.LineDown(1)
		if m.viewport.AtBottom() {
			m.userScrolled = false
		}
	case "k":
		m.viewport.LineUp(1)
		m.userScrolled = true
	case "ctrl+d":
		m.viewport.HalfViewDown()
		if m.viewport.AtBottom() {
			m.userScrolled = false
		}
	case "ctrl+u":
		m.viewport.HalfViewUp()
		m.userScrolled = true
	case "ctrl+f", " ":
		m.viewport.ViewDown()
		if m.viewport.AtBottom() {
			m.userScrolled = false
		}
	case "ctrl+b":
		m.viewport.ViewUp()
		m.userScrolled = true
	case "G":
		m.viewport.GotoBottom()
		m.userScrolled = false

	// ── Pending prefix ──
	case "g", "y":
		m.pendingKey = key

	// ── Misc ──
	case "ctrl+l":
		m.blocks = nil
		m.errMsg = ""
		m.statusMsg = "cleared"
		m.refreshViewport()
	case "ctrl+o":
		m.transcriptMode = !m.transcriptMode
		m.refreshViewport()
	}
	return m, nil
}

func (m *uiModel) handleQueryingKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, m.abortQuery()
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
		// Keep mouse capture enabled so wheel scroll stays inside the
		// viewport and the footer remains fixed at the bottom.
		m.state = uiStateNormal
		m.pendingKey = ""
		m.mouseMode = true
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
			handled, exit := slash.Handle(m.ctx, line, m.session, printer, m.model, m.mailboxRuntime)
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
	queryCtx, queryCancel := context.WithCancel(m.ctx)
	m.queryCancel = queryCancel
	return func() tea.Msg {
		defer queryCancel()
		if !m.promptMu.TryLock() {
			return uiPromptDoneMsg{err: fmt.Errorf("session is busy")}
		}
		defer m.promptMu.Unlock()
		err := m.session.Prompt(queryCtx, line)
		return uiPromptDoneMsg{err: err}
	}
}
