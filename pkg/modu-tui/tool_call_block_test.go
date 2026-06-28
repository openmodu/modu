package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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

func TestToolCallBlockRendersClaudeStyleBashCall(t *testing.T) {
	ctx := RenderContext{ContentWidth: 80, Markdown: markdownRenderer(80)}
	collapsed := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Ran 1 shell command"},
		Call: ToolCall{
			Name:   "bash",
			Input:  "go test ./pkg/modu-tui",
			Output: "ok github.com/openmodu/modu/pkg/modu-tui",
			Done:   true,
		},
	}
	if first := renderedTexts(collapsed.Render(ctx))[0]; !strings.HasPrefix(first, "  Ran 1 shell command") {
		t.Fatalf("collapsed bash call should indent summary: %q", first)
	}

	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Ran 1 shell command", Expanded: true},
		Call: ToolCall{
			Name:   "bash",
			Input:  "go test ./pkg/modu-tui",
			Output: "ok github.com/openmodu/modu/pkg/modu-tui",
			Done:   true,
		},
	}
	got := strings.Join(renderedTexts(block.Render(ctx)), "\n")
	if !strings.Contains(got, "⏺ Bash(go test ./pkg/modu-tui)") {
		t.Fatalf("expanded bash call missing Claude-style header:\n%s", got)
	}
	first := renderedTexts(block.Render(ctx))[0]
	if strings.HasPrefix(first, "▾") || strings.HasPrefix(first, "▸") {
		t.Fatalf("expanded bash call should not render an arrow prefix: %q", first)
	}
	if !strings.HasPrefix(first, "⏺ ") {
		t.Fatalf("expanded bash call should start with bullet: %q", first)
	}
	if got, want := toolExpandedMarkerStyle.GetForeground(), lipgloss.Color("2"); got != want {
		t.Fatalf("expanded tool marker foreground = %#v, want %#v", got, want)
	}
	if !strings.Contains(got, "ok github.com/openmodu/modu/pkg/modu-tui") {
		t.Fatalf("expanded bash call missing output:\n%s", got)
	}
	if got, want := toolExpandedStyle.GetBackground(), lipgloss.Color("235"); got != want {
		t.Fatalf("expanded tool background = %#v, want %#v", got, want)
	}
	rendered := block.Render(ctx)
	for i, line := range rendered.Lines {
		if got := lipgloss.Width(line.Text); got != ctx.ContentWidth {
			t.Fatalf("expanded bash call line %d width = %d, want %d: %q", i, got, ctx.ContentWidth, line.Text)
		}
	}
}

func TestToolCallBlockRendersClaudeStyleReadCall(t *testing.T) {
	ctx := RenderContext{ContentWidth: 100, Markdown: markdownRenderer(100)}
	path := "/Users/ityike/Code/go/src/github.com/openmodu/modu/cmd/tuipoc2/main.go"
	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Read 14 lines", Expanded: true},
		Call: ToolCall{
			Name:   "read",
			Input:  path + " · lines 205-218",
			Output: "Read 14 lines",
			Done:   true,
		},
	}

	got := strings.Join(renderedTexts(block.Render(ctx)), "\n")
	if !strings.Contains(got, "⏺ Read("+path+" · lines 205-218)") {
		t.Fatalf("expanded read call missing Claude-style header:\n%s", got)
	}
	if !strings.Contains(got, "Read 14 lines") {
		t.Fatalf("expanded read call missing summary output:\n%s", got)
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
