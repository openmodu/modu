package tui

import modutui "github.com/openmodu/modu/pkg/modu-tui"

// IntentRouter maps reusable UI intents to modu_code business handlers.
// Unknown intents are intentionally ignored so the UI kernel can add intents
// without forcing every host to change in lockstep.
type IntentRouter struct {
	Submit              func(modutui.SubmitEvent)
	SlashCommand        func(string)
	Interrupt           func()
	PanelAction         func(modutui.PanelAction)
	PanelClosed         func(string)
	InputHistoryChanged func([]string)
	ToolApproval        func(modutui.ToolApprovalResult)
}

func (r IntentRouter) Handle(intent modutui.Intent) {
	switch event := intent.(type) {
	case modutui.SubmitIntent:
		if r.Submit != nil {
			r.Submit(event.Event)
		}
	case modutui.SlashCommandIntent:
		if r.SlashCommand != nil {
			r.SlashCommand(event.Line)
		}
	case modutui.InterruptIntent:
		if r.Interrupt != nil {
			r.Interrupt()
		}
	case modutui.PanelActionIntent:
		if r.PanelAction != nil {
			r.PanelAction(event.Action)
		}
	case modutui.PanelClosedIntent:
		if r.PanelClosed != nil {
			r.PanelClosed(event.PanelID)
		}
	case modutui.InputHistoryChangedIntent:
		if r.InputHistoryChanged != nil {
			r.InputHistoryChanged(append([]string(nil), event.History...))
		}
	case modutui.ToolApprovalDecisionIntent:
		if r.ToolApproval != nil {
			r.ToolApproval(event.Result)
		}
	}
}
