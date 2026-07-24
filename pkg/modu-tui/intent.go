package modutui

import tea "charm.land/bubbletea/v2"

// Intent is a user action emitted by the TUI for the host to handle.
//
// Model.Update never calls an IntentHandler directly. Intents are delivered by
// a tea.Cmd after Update returns, keeping host work off Bubble Tea's event-loop
// goroutine.
type Intent interface {
	isIntent()
}

type SubmitIntent struct {
	Event SubmitEvent
}

func (SubmitIntent) isIntent() {}

type SlashCommandIntent struct {
	Line string
}

func (SlashCommandIntent) isIntent() {}

type InterruptIntent struct{}

func (InterruptIntent) isIntent() {}

type PanelActionIntent struct {
	Action PanelAction
}

func (PanelActionIntent) isIntent() {}

type PanelClosedIntent struct {
	PanelID string
}

func (PanelClosedIntent) isIntent() {}

type InputHistoryChangedIntent struct {
	History []string
}

func (InputHistoryChangedIntent) isIntent() {}

type ToolApprovalDecisionIntent struct {
	Result ToolApprovalResult
}

func (ToolApprovalDecisionIntent) isIntent() {}

func intentCmd(handler func(Intent), intent Intent) tea.Cmd {
	if handler == nil {
		return nil
	}
	return func() tea.Msg {
		handler(intent)
		return nil
	}
}

func batchCmds(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return tea.Batch(filtered...)
	}
}

func responseCmd[T any](respond chan<- T, value T) tea.Cmd {
	if respond == nil {
		return nil
	}
	return func() tea.Msg {
		respond <- value
		return nil
	}
}
