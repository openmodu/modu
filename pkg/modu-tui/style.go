package modutui

import "github.com/charmbracelet/lipgloss"

var (
	youStyle                      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	botStyle                      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	assistantMarkerStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231"))
	streamingAssistantMarkerStyle = lipgloss.NewStyle().Bold(true).Blink(true).Foreground(lipgloss.Color("231"))
	thinkingMarkerStyle           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	toolExpandedMarkerStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	dimStyle                      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	ruleStyle                     = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	cardBorderStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	approvalBorderStyle           = cardBorderStyle
	selStyle                      = lipgloss.NewStyle().Background(lipgloss.Color("240")).Foreground(lipgloss.Color("231"))
	jumpStyle                     = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231")).Padding(0, 1)
	toolExpandedStyle             = lipgloss.NewStyle()
)
