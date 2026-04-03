package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const uiMaxHistory = 200

type uiInputModel struct {
	ta         textarea.Model
	history    []string
	historyIdx int
	historyTmp string
	focused    bool
	width      int
}

func newUIInputModel() *uiInputModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter to send, Shift+Enter for newline)"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 10
	ta.SetHeight(1)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(uiDim)
	ta.FocusedStyle.Text = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(uiPrimary).Bold(true)
	ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(uiDim)
	ta.Prompt = "> "
	ta.Focus()
	ta.SetWidth(80)

	return &uiInputModel{
		ta:         ta,
		history:    make([]string, 0),
		historyIdx: -1,
		focused:    true,
		width:      80,
	}
}

func (i *uiInputModel) Focus() {
	i.focused = true
	i.ta.Focus()
}

func (i *uiInputModel) Blur() {
	i.focused = false
	i.ta.Blur()
}

func (i *uiInputModel) Value() string {
	return strings.TrimSpace(i.ta.Value())
}

func (i *uiInputModel) RawValue() string {
	return i.ta.Value()
}

func (i *uiInputModel) Reset() {
	val := i.Value()
	if val != "" {
		i.history = append(i.history, val)
		if len(i.history) > uiMaxHistory {
			i.history = i.history[1:]
		}
	}
	i.ta.Reset()
	i.ta.SetHeight(1)
	i.historyIdx = -1
	i.historyTmp = ""
}

func (i *uiInputModel) SetWidth(w int) {
	i.width = w
	i.ta.SetWidth(max(1, w-4))
}

func (i *uiInputModel) SetHistory(history []string) {
	i.history = append(i.history[:0], history...)
	if len(i.history) > uiMaxHistory {
		i.history = i.history[len(i.history)-uiMaxHistory:]
	}
}

func (i *uiInputModel) History() []string {
	out := make([]string, len(i.history))
	copy(out, i.history)
	return out
}

func (i *uiInputModel) Update(msg tea.Msg) (bool, tea.Cmd) {
	if !i.focused {
		return false, nil
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			if i.Value() != "" {
				return true, nil
			}
			return false, nil
		case "up":
			if i.ta.Line() == 0 && i.ta.Value() == "" || i.historyIdx >= 0 {
				return false, i.navigateHistory(-1)
			}
		case "down":
			if i.historyIdx >= 0 {
				return false, i.navigateHistory(1)
			}
		case "shift+enter", "alt+enter":
			i.ta.InsertString("\n")
			i.syncHeight()
			return false, nil
		}
	}
	var cmd tea.Cmd
	i.ta, cmd = i.ta.Update(msg)
	i.syncHeight()
	return false, cmd
}

func (i *uiInputModel) navigateHistory(direction int) tea.Cmd {
	if len(i.history) == 0 {
		return nil
	}
	if direction < 0 {
		if i.historyIdx == -1 {
			i.historyTmp = i.ta.Value()
			i.historyIdx = len(i.history) - 1
		} else if i.historyIdx > 0 {
			i.historyIdx--
		}
		i.ta.Reset()
		i.ta.InsertString(i.history[i.historyIdx])
	} else {
		if i.historyIdx >= 0 {
			if i.historyIdx < len(i.history)-1 {
				i.historyIdx++
				i.ta.Reset()
				i.ta.InsertString(i.history[i.historyIdx])
			} else {
				i.historyIdx = -1
				i.ta.Reset()
				i.ta.InsertString(i.historyTmp)
			}
		}
	}
	i.syncHeight()
	return nil
}

func (i *uiInputModel) syncHeight() {
	lines := strings.Count(i.ta.Value(), "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > 10 {
		lines = 10
	}
	i.ta.SetHeight(lines)
}

func (i *uiInputModel) View() string {
	return "  " + i.ta.View()
}
