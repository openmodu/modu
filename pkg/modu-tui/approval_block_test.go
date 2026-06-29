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
	for _, want := range []string{"⏺ Bash({\"command\":\"go test ./...\"})", "no content data"} {
		if !strings.Contains(got, want) {
			t.Fatalf("approval block missing %q:\n%s", want, got)
		}
	}
	if actions := block.ActionsLine(); !strings.Contains(actions, "[y] allow") || !strings.Contains(actions, "[d] always deny") || !strings.Contains(actions, "[esc] cancel") {
		t.Fatalf("approval actions line is incomplete: %q", actions)
	}
}
