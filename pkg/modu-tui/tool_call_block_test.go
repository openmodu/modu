package modutui

import (
	"strings"
	"testing"
)

func TestToolCallBlockRendersCollapsedAndExpanded(t *testing.T) {
	ctx := RenderContext{ContentWidth: 40, Markdown: markdownRenderer(40)}
	collapsed := ToolCallBlock{CollapsibleBlock: CollapsibleBlock{Summary: "Ran command", Detail: "hidden"}}.Render(ctx)
	if got := strings.Join(renderedTexts(collapsed), "\n"); strings.Contains(got, "hidden") {
		t.Fatalf("collapsed tool block leaked detail:\n%s", got)
	}

	expanded := ToolCallBlock{CollapsibleBlock: CollapsibleBlock{Summary: "Ran command", Detail: "visible", Expanded: true}}.Render(ctx)
	if got := strings.Join(renderedTexts(expanded), "\n"); !strings.Contains(got, "visible") {
		t.Fatalf("expanded tool block missing detail:\n%s", got)
	}
}

func TestToolPermissionHookIsUsedByToolBlock(t *testing.T) {
	ctx := RenderContext{
		ContentWidth: 60,
		Markdown:     markdownRenderer(60),
		Hooks: Hooks{
			ToolPermission: func(call ToolCall) ToolPermissionState {
				if call.Name != "bash" {
					t.Fatalf("tool hook got call name %q, want bash", call.Name)
				}
				return ToolPermissionPending
			},
		},
	}
	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Run shell", Detail: "go test"},
		Call:             ToolCall{Name: "bash"},
	}
	got := strings.Join(renderedTexts(block.Render(ctx)), "\n")
	if !strings.Contains(got, "permission pending") {
		t.Fatalf("tool block missing permission status:\n%s", got)
	}
}
