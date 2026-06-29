package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	if first := renderedTexts(collapsed.Render(ctx))[0]; !strings.HasPrefix(first, "  Ran 1 shell command") || strings.Contains(first, "└") {
		t.Fatalf("collapsed bash call should render only summary, got %q", first)
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
	if !strings.Contains(got, "  └ ok github.com/openmodu/modu/pkg/modu-tui") {
		t.Fatalf("expanded bash call missing output line:\n%s", got)
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
	if got, blocked := toolExpandedStyle.GetBackground(), lipgloss.Color("235"); got == blocked {
		t.Fatalf("expanded tool background should not use dark container color %#v", got)
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
	path := "cmd/tuipoc2/main.go"
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
	if !strings.Contains(got, "  └ Read 14 lines") {
		t.Fatalf("expanded read call missing summary output:\n%s", got)
	}
}

func TestToolCallBlockRendersNoCollapseCode(t *testing.T) {
	ctx := RenderContext{ContentWidth: 80, Markdown: markdownRenderer(80)}
	block := ToolCallBlock{
		Call: ToolCall{
			Name:       "update",
			Input:      "main.go",
			Output:     "Added 1 lines, removed 1 lines",
			Code:       "- fmt.Println(\"old\")\n+ fmt.Println(\"new\")",
			Language:   "diff",
			NoCollapse: true,
		},
	}
	got := strings.Join(renderedTexts(block.Render(ctx)), "\n")
	for _, want := range []string{"⏺ Update(main.go)", "  └ Added 1 lines, removed 1 lines", "fmt.Println(\"old\")", "fmt.Println(\"new\")"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded write block missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Ran update") {
		t.Fatalf("no-collapse write block should not render collapsed summary:\n%s", got)
	}
	rendered := block.Render(ctx)
	var codeRaw strings.Builder
	for _, line := range rendered.Lines {
		if strings.Contains(line.Text, "fmt.Println") {
			codeRaw.WriteString(line.Text)
			codeRaw.WriteByte('\n')
		}
	}
	if !strings.Contains(codeRaw.String(), "\x1b[") {
		t.Fatalf("code lines should preserve syntax-highlight ANSI sequences: %q", codeRaw.String())
	}
	if strings.Contains(codeRaw.String(), "\x1b[48;5;235") {
		t.Fatalf("code lines should not inherit tool background: %q", codeRaw.String())
	}
}

func TestToolCallBlockRendersDiffWithLineNumbersAndBackground(t *testing.T) {
	ctx := RenderContext{ContentWidth: 96, Markdown: markdownRenderer(96)}
	block := ToolCallBlock{
		Call: ToolCall{
			Name:       "update",
			Input:      "/tmp/main.go",
			Output:     "Added 1 lines, removed 1 lines",
			Code:       "--- /tmp/main.go\n+++ /tmp/main.go\n@@ -12,1 +12,1 @@\n  11  func main() {\n- 12  if oldValue {\n+ 12  if newValue {\n  13  }\n",
			Language:   "diff",
			NoCollapse: true,
		},
	}
	rendered := block.Render(ctx)
	var rawLines []string
	for _, line := range rendered.Lines {
		rawLines = append(rawLines, line.Text)
	}
	raw := strings.Join(rawLines, "\n")
	stripped := ansi.Strip(raw)

	for _, want := range []string{"⏺ Update(/tmp/main.go)", "  └ Added 1 lines, removed 1 lines", "    - 12  if oldValue {", "    + 12  if newValue {", "      11  func main() {"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered diff missing %q:\n%s", want, stripped)
		}
	}
	if !strings.Contains(raw, "\x1b[48;5;52m") {
		t.Fatalf("deleted diff line should have red background ANSI, got:\n%q", raw)
	}
	if !strings.Contains(raw, "\x1b[48;5;22m") {
		t.Fatalf("inserted diff line should have green background ANSI, got:\n%q", raw)
	}
	if !strings.Contains(raw, "\x1b[38;5;") {
		t.Fatalf("diff code should preserve syntax foreground ANSI, got:\n%q", raw)
	}
}

func TestPOC2NoCollapseToolBlockIsNotClickable(t *testing.T) {
	m := NewModel(Options{
		Width:  80,
		Height: 12,
		InitialMessages: []Message{{
			Tool:           true,
			ToolID:         "call-1",
			ToolName:       "update",
			ToolInput:      "main.go",
			ToolOutput:     "Added 1 lines, removed 1 lines",
			ToolCode:       "- old\n+ new",
			ToolLanguage:   "diff",
			ToolNoCollapse: true,
			Expanded:       true,
		}},
	})
	if len(m.headers) != 0 {
		t.Fatalf("no-collapse tool should not register clickable headers: %#v", m.headers)
	}
	before := m.messages[0].Expanded
	_ = m.onPress(1, 1)
	if m.messages[0].Expanded != before {
		t.Fatal("no-collapse tool should not toggle expanded state")
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

func TestToolCallBlockRendersMultilineOutputWithIndentedContinuation(t *testing.T) {
	ctx := RenderContext{ContentWidth: 72, Markdown: markdownRenderer(72)}
	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Ran custom", Expanded: true},
		Call: ToolCall{
			Name:   "custom_tool",
			Input:  "first arg",
			Output: "line one\nline two",
			Done:   true,
		},
	}

	got := strings.Join(renderedTexts(block.Render(ctx)), "\n")
	for _, want := range []string{
		"⏺ Custom_tool(first arg)",
		"  └ line one",
		"    line two",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded custom tool missing %q:\n%s", want, got)
		}
	}
}

func TestToolCallBlockRendersNoContentDataForEmptyOutput(t *testing.T) {
	ctx := RenderContext{ContentWidth: 72, Markdown: markdownRenderer(72)}
	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Ran custom", Expanded: true},
		Call: ToolCall{
			Name:  "custom_tool",
			Input: "first arg",
			Done:  true,
		},
	}

	got := strings.Join(renderedTexts(block.Render(ctx)), "\n")
	if !strings.Contains(got, "  └ no content data") {
		t.Fatalf("expanded custom tool missing no-content output:\n%s", got)
	}
}

func TestToolCallBlockWrapsLongOutputWithoutTruncation(t *testing.T) {
	ctx := RenderContext{ContentWidth: 42, Markdown: markdownRenderer(42)}
	output := `{"baseRefName":"main","body":"## Changes\n\n### Fixes\n- modu-code: Run agent interrupt, steer, and follow-up offline path"}`
	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Ran command", Expanded: true},
		Call: ToolCall{
			Name:   "bash",
			Input:  "gh pr view 62 --json title,body,state,headRefName,baseRefName",
			Output: output,
			Done:   true,
		},
	}

	lines := renderedTexts(block.Render(ctx))
	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "  └ {\"baseRefName\":\"main\"") {
		t.Fatalf("long output should start under branch prefix:\n%s", got)
	}
	if !strings.Contains(got, "    es\\n\\n### Fixes") || !strings.Contains(got, "    ine path\"}") {
		t.Fatalf("long output should wrap with four-space continuation and keep tail:\n%s", got)
	}
	for _, line := range lines {
		if strings.Contains(line, "offline path") && !strings.HasPrefix(line, "    ") {
			t.Fatalf("wrapped output tail should use four-space indentation: %q", line)
		}
	}
}

func TestToolCallBlockWrapsLongInputWithVerticalConnector(t *testing.T) {
	ctx := RenderContext{ContentWidth: 36, Markdown: markdownRenderer(36)}
	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Ran custom", Expanded: true},
		Call: ToolCall{
			Name:   "custom_tool",
			Input:  "alpha beta gamma delta epsilon zeta eta theta",
			Output: "done",
			Done:   true,
		},
	}

	lines := renderedTexts(block.Render(ctx))
	if len(lines) < 3 {
		t.Fatalf("expected wrapped header and output, got %#v", lines)
	}
	if !strings.HasPrefix(lines[0], "⏺ Custom_tool(") {
		t.Fatalf("first header line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  │ ") {
		t.Fatalf("wrapped header continuation should use vertical connector, got %q", lines[1])
	}
	if !strings.HasPrefix(lines[len(lines)-1], "  └ done") {
		t.Fatalf("output should align under connector, got %#v", lines)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != ctx.ContentWidth {
			t.Fatalf("line %d width = %d, want %d: %q", i, got, ctx.ContentWidth, line)
		}
	}
}
