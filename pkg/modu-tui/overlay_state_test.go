package modutui

import "testing"

func TestOverlayModelKeepsOnlyOneSurfaceOpen(t *testing.T) {
	var overlay overlayModel
	overlay.openPanel(Panel{ID: "workflow"})
	overlay.openApproval(pendingApproval{request: ToolApprovalRequest{ID: "approval"}})

	if overlay.panel != nil || overlay.approval == nil {
		t.Fatalf("approval should replace panel: %#v", overlay)
	}

	overlay.openHumanPrompt(pendingHumanPrompt{request: HumanPromptRequest{ID: "choice"}})
	if overlay.approval != nil || overlay.humanPrompt == nil {
		t.Fatalf("human prompt should replace approval: %#v", overlay)
	}

	overlay.openHumanText(pendingHumanText{request: HumanTextRequest{ID: "text"}})
	if overlay.humanPrompt != nil || overlay.humanText == nil {
		t.Fatalf("human text should replace choice prompt: %#v", overlay)
	}
}

func TestOverlayModelCancelRequiresMatchingID(t *testing.T) {
	var overlay overlayModel
	overlay.openHumanText(pendingHumanText{request: HumanTextRequest{ID: "secret"}})

	if overlay.cancelHumanText("other") {
		t.Fatal("mismatched ID should not close overlay")
	}
	if overlay.humanText == nil {
		t.Fatal("mismatched ID removed overlay")
	}
	if !overlay.cancelHumanText("secret") || overlay.humanText != nil {
		t.Fatal("matching ID should close overlay")
	}
}

func TestOverlayModelRefreshPreservesSelectedRowByID(t *testing.T) {
	var overlay overlayModel
	overlay.openPanel(Panel{
		ID:       "workflow",
		Selected: 1,
		Rows: []PanelRow{
			{Value: "first"},
			{Value: "selected"},
		},
	})
	overlay.panelOffset = 2
	overlay.refreshPanel(Panel{
		ID: "workflow",
		Rows: []PanelRow{
			{Value: "new"},
			{Value: "first"},
			{Value: "selected"},
		},
	})

	if overlay.panelSelected != 2 {
		t.Fatalf("selected row = %d, want 2", overlay.panelSelected)
	}
	if overlay.panelOffset != 2 {
		t.Fatalf("panel offset = %d, want preserved 2", overlay.panelOffset)
	}
}
