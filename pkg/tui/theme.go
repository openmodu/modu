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
	uiCodeText      = lipgloss.NewStyle().Foreground(uiSecondary)
	// No padding: the background block hugs ❯ on the left and the last content
	// character on the right, no surrounding bg gutters.
	uiUserPrompt         = lipgloss.NewStyle().Foreground(lipgloss.Color("#E6EDF3")).Background(lipgloss.Color("#1F2A2E"))
	uiExternalUserPrompt = lipgloss.NewStyle().Foreground(lipgloss.Color("#F2E7C9")).Background(lipgloss.Color("#2E2618"))
	uiBubbleHeader       = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(uiPrimary).
				Padding(0, 2)
	uiBubblePopup = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(uiSecondary).
			Padding(0, 1)
	// Diff row backgrounds live as raw SGR sequences in highlight.go
	// (sgrDiffAddedBg / sgrDiffRemovedBg) — they're emitted directly to
	// bypass lipgloss's profile-detection color stripping.
)
