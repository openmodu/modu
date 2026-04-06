package ui

import "github.com/charmbracelet/lipgloss"

func (m *uiModel) recalcViewportHeight() {
	reserved := lipgloss.Height(m.renderInputArea())
	if m.state == uiStateQuerying {
		reserved += 1 // activity line
	}
	if m.showSlash && len(m.slashMatches) > 0 {
		reserved += lipgloss.Height(m.renderSlashSuggestions())
	}
	if m.state == uiStatePermission {
		reserved += lipgloss.Height(m.renderPermissionPrompt())
	}
	reserved += 1 // status bar always occupies 1 line (avoids circular dependency with scroll %)
	m.viewport.Height = max(4, m.height-reserved)
}

func (m *uiModel) refreshViewport() {
	if !m.ready {
		return
	}
	m.recalcViewportHeight()
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
