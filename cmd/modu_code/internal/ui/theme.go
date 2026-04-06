package ui

import (
	"math/rand"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	uiPrimary   = lipgloss.Color("#8FBF7A")
	uiSecondary = lipgloss.Color("#C8A96B")
	uiSuccess   = lipgloss.Color("#8FBF7A")
	uiWarning   = lipgloss.Color("#C8A96B")
	uiError     = lipgloss.Color("#E06C75")
	uiMuted     = lipgloss.Color("#8B9098")
	uiDim       = lipgloss.Color("#5C6370")

	uiWhiteText     = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	uiPrimaryText   = lipgloss.NewStyle().Foreground(uiPrimary)
	uiSecondaryText = lipgloss.NewStyle().Foreground(uiSecondary)
	uiSuccessText   = lipgloss.NewStyle().Foreground(uiSuccess)
	uiWarningText   = lipgloss.NewStyle().Foreground(uiWarning)
	uiErrorText     = lipgloss.NewStyle().Foreground(uiError).Bold(true)
	uiMutedText     = lipgloss.NewStyle().Foreground(uiMuted)
	uiDimText       = lipgloss.NewStyle().Foreground(uiDim)
	uiThinkText     = lipgloss.NewStyle().Foreground(uiDim).Italic(true)
)

var uiSpinnerVerbs = []string{
	"Thinking",
	"Reasoning",
	"Analyzing",
	"Pondering",
	"Computing",
	"Processing",
	"Generating",
	"Crafting",
	"Assembling",
	"Composing",
	"Architecting",
	"Synthesizing",
	"Evaluating",
	"Formulating",
	"Deliberating",
	"Contemplating",
	"Brainstorming",
	"Scheming",
	"Brewing",
	"Cooking",
	"Baking",
	"Simmering",
	"Percolating",
	"Fermenting",
	"Distilling",
	"Forging",
	"Sculpting",
	"Weaving",
	"Tinkering",
	"Wrangling",
	"Crunching",
	"Deciphering",
	"Unraveling",
	"Manifesting",
	"Conjuring",
	"Bootstrapping",
	"Compiling",
	"Optimizing",
	"Debugging",
	"Orchestrating",
	"Calibrating",
	"Polishing",
}

var uiRng = rand.New(rand.NewSource(time.Now().UnixNano()))

func randomSpinnerVerb() string {
	return uiSpinnerVerbs[uiRng.Intn(len(uiSpinnerVerbs))]
}

func uiToolVerb(toolName string) string {
	switch toolName {
	case "bash":
		return "Executing"
	case "read":
		return "Reading"
	case "write":
		return "Writing"
	case "edit":
		return "Editing"
	case "glob":
		return "Searching"
	case "grep":
		return "Scanning"
	case "web_fetch", "web_search":
		return "Fetching"
	case "agent":
		return "Delegating"
	case "todo_write":
		return "Organizing"
	default:
		return "Running"
	}
}
