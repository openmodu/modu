package tui

import (
	"context"
	"strings"
	"testing"

	modutui "github.com/openmodu/modu/pkg/modu-tui"
)

func TestDialogRunFlowCollectsChoiceTextAndSecret(t *testing.T) {
	var requests []any
	dialog := NewDialog(modutui.NewClient(func(msg any) {
		requests = append(requests, msg)
		switch msg := msg.(type) {
		case modutui.RequestHumanPromptMsg:
			msg.Respond <- "telegram"
		case modutui.RequestHumanTextMsg:
			if msg.Request.Secret {
				msg.Respond <- " secret "
			} else {
				msg.Respond <- " value "
			}
		}
	}), 0)

	result, completed, err := dialog.RunFlow(context.Background(), Flow{
		ID: "channel",
		Steps: []FlowStep{
			{
				ID:      "type",
				Kind:    FlowStepChoice,
				Title:   "Channel",
				Options: []modutui.HumanPromptOption{{Label: "Telegram", Value: "telegram"}},
			},
			{ID: "name", Kind: FlowStepText, Required: true},
			{ID: "token", Kind: FlowStepText, Secret: true, Required: true},
		},
	})
	if err != nil || !completed {
		t.Fatalf("RunFlow() completed=%v err=%v", completed, err)
	}
	if result.ID != "channel" || result.Value("type") != "telegram" || result.Value("name") != "value" || result.Value("token") != " secret " {
		t.Fatalf("result = %#v", result)
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestDialogRunFlowReturnsCancelled(t *testing.T) {
	dialog := NewDialog(modutui.NewClient(func(msg any) {
		if request, ok := msg.(modutui.RequestHumanPromptMsg); ok {
			close(request.Respond)
		}
	}), 0)

	_, completed, err := dialog.RunFlow(context.Background(), Flow{
		ID: "model",
		Steps: []FlowStep{{
			ID:      "target",
			Kind:    FlowStepChoice,
			Options: []modutui.HumanPromptOption{{Label: "Model", Value: "provider/model"}},
		}},
	})
	if err != nil || completed {
		t.Fatalf("RunFlow() completed=%v err=%v", completed, err)
	}
}

func TestDialogRunFlowRejectsInvalidDefinitions(t *testing.T) {
	dialog := NewDialog(modutui.NewClient(func(any) {}), 0)
	for _, flow := range []Flow{
		{},
		{ID: "empty-step", Steps: []FlowStep{{Kind: FlowStepText}}},
		{ID: "duplicate", Steps: []FlowStep{{ID: "value", Kind: FlowStepText}, {ID: "value", Kind: FlowStepText}}},
		{ID: "choice", Steps: []FlowStep{{ID: "value", Kind: FlowStepChoice}}},
		{ID: "kind", Steps: []FlowStep{{ID: "value", Kind: "unknown"}}},
	} {
		_, _, err := dialog.RunFlow(context.Background(), flow)
		if err == nil || !strings.Contains(err.Error(), "flow") {
			t.Fatalf("RunFlow(%#v) error = %v", flow, err)
		}
	}
}
