package ui

import (
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type uiClipboardMsg struct{ err error }

func clipboardCmd(text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return uiClipboardMsg{err: cmd.Run()}
	}
}

func (m *uiModel) yankLastResponseCmd() tea.Cmd {
	// Find last assistant block
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind == "assistant" && strings.TrimSpace(m.blocks[i].Content) != "" {
			return clipboardCmd(m.blocks[i].Content)
		}
	}
	return clipboardCmd("")
}

func (m *uiModel) yankAllCmd() tea.Cmd {
	var sb strings.Builder
	for i, block := range m.blocks {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch block.Kind {
		case "user":
			sb.WriteString("> " + block.Content)
		case "assistant":
			sb.WriteString(block.Content)
		case "tool":
			for _, t := range block.Tools {
				sb.WriteString("[" + t.Name + "] " + t.Input + "\n")
				if t.Output != "" {
					sb.WriteString(t.Output)
				}
			}
		default:
			sb.WriteString(block.Content)
		}
	}
	return clipboardCmd(sb.String())
}

func (m *uiModel) copyToClipboardCmd() tea.Cmd {
	var sb strings.Builder
	for i, block := range m.blocks {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		switch block.Kind {
		case "user":
			sb.WriteString("> " + block.Content)
		case "assistant":
			sb.WriteString(block.Content)
		case "tool":
			for _, t := range block.Tools {
				sb.WriteString("[tool:" + t.Name + "] " + t.Input + "\n")
				if t.Output != "" {
					sb.WriteString(t.Output)
				}
			}
		default:
			sb.WriteString(block.Content)
		}
	}
	text := sb.String()
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return uiClipboardMsg{err: cmd.Run()}
	}
}
