package modutui

import "github.com/charmbracelet/lipgloss"

var (
	youStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	botStyle          = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	dimStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	ruleStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	selStyle          = lipgloss.NewStyle().Background(lipgloss.Color("240")).Foreground(lipgloss.Color("231"))
	jumpStyle         = lipgloss.NewStyle().Background(lipgloss.Color("63")).Foreground(lipgloss.Color("231")).Padding(0, 1)
	toolExpandedStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))
)
