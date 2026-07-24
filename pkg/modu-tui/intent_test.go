package modutui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestIntentHandlerRunsAfterUpdateReturns(t *testing.T) {
	var intents []Intent
	var model tea.Model = NewModel(Options{
		IntentHandler: func(intent Intent) {
			intents = append(intents, intent)
		},
	})
	model, _ = model.Update(tea.PasteMsg{Content: "hello"})

	var cmd tea.Cmd
	model, cmd = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if len(intents) != 0 {
		t.Fatalf("Update executed host callbacks synchronously: intents=%#v", intents)
	}

	model = runImmediateCmd(t, model, cmd)
	var submitted *SubmitIntent
	var history *InputHistoryChangedIntent
	for _, intent := range intents {
		switch intent := intent.(type) {
		case SubmitIntent:
			copy := intent
			submitted = &copy
		case InputHistoryChangedIntent:
			copy := intent
			history = &copy
		}
	}
	if submitted == nil || submitted.Event.Text != "hello" || submitted.Event.Kind != SubmitKindPrompt {
		t.Fatalf("submit intent = %#v, want hello prompt", submitted)
	}
	if history == nil || len(history.History) != 1 || history.History[0] != "hello" {
		t.Fatalf("history intent = %#v, want [hello]", history)
	}
}

func TestPasteResolverRunsAsServiceCommand(t *testing.T) {
	calls := 0
	var model tea.Model = NewModel(Options{
		Services: Services{
			ResolvePastedImages: func(content string) ([]ImageAttachment, bool, error) {
				calls++
				if content != "/tmp/image.png" {
					t.Fatalf("resolver content = %q", content)
				}
				return []ImageAttachment{{Name: "image.png", MimeType: "image/png"}}, true, nil
			},
		},
	})

	var cmd tea.Cmd
	model, cmd = model.Update(tea.PasteMsg{Content: "/tmp/image.png"})
	if calls != 0 {
		t.Fatal("paste resolver ran inside Update")
	}
	renderModel := model.(Model)
	_ = renderModel.render()
	if calls != 0 {
		t.Fatal("paste resolver ran during Render")
	}

	model = runImmediateCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("paste resolver calls = %d, want 1", calls)
	}
	if got := model.(Model).input.ImageAttachments(); len(got) != 1 || got[0].Name != "image.png" {
		t.Fatalf("resolved images = %#v", got)
	}
}

func TestArtifactAndPermissionServicesDoNotRunDuringRender(t *testing.T) {
	artifactCalls := 0
	permissionCalls := 0
	var model tea.Model = NewModel(Options{
		InitialEntries: []Entry{{
			Role: RoleAssistant,
			Nodes: []Node{ToolNode{Call: ToolCall{
				ID:           "call-1",
				Name:         "bash",
				Summary:      "Run tests",
				ArtifactPath: "/tmp/call-1.output",
				NoCollapse:   true,
			}}},
		}},
		Services: Services{
			LoadToolArtifact: func(path string) (string, error) {
				artifactCalls++
				if path != "/tmp/call-1.output" {
					t.Fatalf("artifact path = %q", path)
				}
				return "full output", nil
			},
			ToolPermission: func(call ToolCall) ToolPermissionState {
				permissionCalls++
				if call.ID != "call-1" {
					t.Fatalf("permission call = %#v", call)
				}
				return ToolPermissionPending
			},
		},
	})

	renderModel := model.(Model)
	_ = renderModel.render()
	_ = model.View()
	if artifactCalls != 0 || permissionCalls != 0 {
		t.Fatalf("render executed services: artifact=%d permission=%d", artifactCalls, permissionCalls)
	}

	model = runImmediateCmd(t, model, model.Init())
	if artifactCalls != 1 || permissionCalls != 1 {
		t.Fatalf("service calls after Init = artifact %d permission %d, want 1/1", artifactCalls, permissionCalls)
	}
	renderModel = model.(Model)
	rendered := ansi.Strip(renderModel.render())
	if !strings.Contains(rendered, "full output") {
		t.Fatalf("artifact service result not rendered:\n%s", rendered)
	}
	node, _, ok := toolNodeFromEntry(renderModel.entries[0])
	if !ok {
		t.Fatal("tool entry missing ToolNode")
	}
	if got := node.Permission; got != ToolPermissionPending {
		t.Fatalf("permission service result = %q, want pending", got)
	}
}
