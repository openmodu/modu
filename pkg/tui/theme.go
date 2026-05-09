package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	uiPrimary   = lipgloss.Color("#8FBF7A")
	uiSecondary = lipgloss.Color("#C8A96B")
	uiSuccess   = lipgloss.Color("#8FBF7A")
	uiError     = lipgloss.Color("#E06C75")
	uiMuted     = lipgloss.Color("#8B9098")
	uiDim       = lipgloss.Color("#5C6370")

	uiWhiteText     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	uiPrimaryText   = lipgloss.NewStyle().Foreground(uiPrimary)
	uiSecondaryText = lipgloss.NewStyle().Foreground(uiSecondary)
	uiSuccessText   = lipgloss.NewStyle().Foreground(uiSuccess)
	uiErrorText     = lipgloss.NewStyle().Foreground(uiError).Bold(true)
	uiMutedText     = lipgloss.NewStyle().Foreground(uiMuted)
	uiDimText       = lipgloss.NewStyle().Foreground(uiDim)
)
