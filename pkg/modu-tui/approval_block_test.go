package modutui

import (
	"strings"
	"testing"
)

func TestApprovalBlockRendersPendingToolApproval(t *testing.T) {
	block := ApprovalBlock{
		Request: ToolApprovalRequest{
			ID:       "call-1",
			ToolName: "bash",
			Detail:   `{"command":"go test ./..."}`,
		},
		Expanded: true,
	}

	got := strings.Join(renderedTexts(block.Render(RenderContext{ContentWidth: 80})), "\n")
	for _, want := range []string{"approval required: bash", "permission pending", "go test ./..."} {
		if !strings.Contains(got, want) {
			t.Fatalf("approval block missing %q:\n%s", want, got)
		}
	}
	if actions := block.ActionsLine(); !strings.Contains(actions, "[y] allow") || !strings.Contains(actions, "[d] always deny") {
		t.Fatalf("approval actions line is incomplete: %q", actions)
	}
}
