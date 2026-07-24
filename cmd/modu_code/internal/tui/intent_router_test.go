package tui

import (
	"testing"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func TestIntentRouterDispatchesBusinessEvents(t *testing.T) {
	var submitted string
	var slash string
	var interrupted bool
	var panelCommand string
	var closed string
	var history []string
	router := IntentRouter{
		Submit: func(event modutui.SubmitEvent) {
			submitted = event.Text
		},
		SlashCommand: func(line string) {
			slash = line
		},
		Interrupt: func() {
			interrupted = true
		},
		PanelAction: func(action modutui.PanelAction) {
			panelCommand = action.Command
		},
		PanelClosed: func(panelID string) {
			closed = panelID
		},
		InputHistoryChanged: func(next []string) {
			history = next
		},
	}

	router.Handle(modutui.SubmitIntent{Event: modutui.SubmitEvent{Text: "hello"}})
	router.Handle(modutui.SlashCommandIntent{Line: "/help"})
	router.Handle(modutui.InterruptIntent{})
	router.Handle(modutui.PanelActionIntent{Action: modutui.PanelAction{Command: "/workflow"}})
	router.Handle(modutui.PanelClosedIntent{PanelID: "workflow"})
	input := []string{"one"}
	router.Handle(modutui.InputHistoryChangedIntent{History: input})
	input[0] = "changed"

	if submitted != "hello" || slash != "/help" || !interrupted {
		t.Fatalf("basic intents not dispatched: submit=%q slash=%q interrupted=%v", submitted, slash, interrupted)
	}
	if panelCommand != "/workflow" || closed != "workflow" {
		t.Fatalf("panel intents not dispatched: command=%q closed=%q", panelCommand, closed)
	}
	if len(history) != 1 || history[0] != "one" {
		t.Fatalf("history = %#v, want copied original", history)
	}
}

func TestIntentRouterIgnoresUnknownAndUnsetHandlers(t *testing.T) {
	router := IntentRouter{}
	router.Handle(modutui.SubmitIntent{})
	router.Handle(nil)
}
