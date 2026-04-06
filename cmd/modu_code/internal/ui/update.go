package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/openmodu/modu/pkg/agent"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

func (m *uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		prevContent := m.viewport.View()
		prevOffset := m.viewport.YOffset
		prevScrolled := m.userScrolled
		m.width = msg.Width
		m.height = msg.Height
		m.input.SetWidth(max(20, msg.Width-4))
		m.viewport = viewport.New(msg.Width, 4) // height set by recalcViewportHeight in refreshViewport below
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

	case uiClipboardMsg:
		if msg.err != nil {
			m.statusMsg = "copy failed: " + msg.err.Error()
		} else {
			m.statusMsg = "copied to clipboard"
		}
		return m, nil

	case uiQuitMsg:
		return m, tea.Quit
	}

	return m, nil
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
