package ui

import "github.com/charmbracelet/lipgloss"

func (m *uiModel) recalcViewportHeight() {
	reserved := 0
	if footer := m.renderFooter(); footer != "" {
		reserved = lipgloss.Height(footer) + 1
	}
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
