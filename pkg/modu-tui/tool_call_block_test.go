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

func TestToolCallBlockRendersParallelAndArtifactMeta(t *testing.T) {
	ctx := RenderContext{ContentWidth: 100, Markdown: markdownRenderer(100)}
	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Ran 1 shell command"},
		Call: ToolCall{
			Name:       "bash",
			Output:     "preview only",
			ArtifactID: "call-1",
			Truncated:  true,
			BatchSize:  3,
			Done:       true,
		},
	}
	first := renderedTexts(block.Render(ctx))[0]
	for _, want := range []string{"parallel 3", "artifact call-1"} {
		if !strings.Contains(first, want) {
			t.Fatalf("collapsed tool summary missing %q: %q", want, first)
		}
	}
}

func TestToolCallBlockExpandedRendersCachedArtifact(t *testing.T) {
	ctx := RenderContext{ContentWidth: 100, Markdown: markdownRenderer(100)}
	block := ToolCallBlock{
		CollapsibleBlock: CollapsibleBlock{Summary: "Ran 1 shell command", Expanded: true},
		Call: ToolCall{
			Name:         "bash",
			Output:       "preview only",
			ArtifactID:   "call-1",
			ArtifactPath: "/tmp/call-1.output",
			ArtifactText: "full artifact output\nline two\n",
			ArtifactRead: true,
			Truncated:    true,
			Done:         true,
		},
	}
	got := strings.Join(renderedTexts(block.Render(ctx)), "\n")
	if !strings.Contains(got, "full artifact output") || strings.Contains(got, "preview only") {
		t.Fatalf("expanded tool should render artifact instead of preview, got:\n%s", got)
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

func TestToolCallBlockRendersNumberedCodeWithSeparateGutter(t *testing.T) {
	ctx := RenderContext{ContentWidth: 80, Markdown: markdownRenderer(80)}
	code := strings.Join([]string{
		` 1  #!/usr/bin/env python3`,
		` 2  """`,
		` 3  Rider Follow 报告生成器`,
		` 4  Usage:`,
		` 5    python3 rider_report.py`,
		` 6  """`,
		` 7  `,
		` 8  import argparse`,
		` 9  import csv`,
		`10  import json`,
		`11  `,
		`12  def fetch_rider_data():`,
	}, "\n")
	block := ToolCallBlock{
		Call: ToolCall{
			Name:   "write",
			Input:  "/tmp/rider_report.py",
			Output: "Wrote 12 lines",
			Code:   code,
			// Existing-file writes are marked diff even when an idempotent write
			// falls back to a numbered full-file preview.
			Language:   "diff",
			NoCollapse: true,
		},
	}

	rendered := block.Render(ctx)
	var docstringLine, usageCommandLine, importLine string
	var docstringGutter int
	for _, line := range rendered.Lines {
		plain := ansi.Strip(line.Text)
		switch {
		case strings.Contains(plain, "Rider Follow"):
			docstringLine = line.Text
			docstringGutter = line.Gutter
			if !strings.HasPrefix(plain, "     3  Rider Follow") {
				t.Fatalf("numbered code should use a four-space outer indent and a separate gutter, got %q", plain)
			}
		case strings.Contains(plain, "python3 rider_report.py"):
			usageCommandLine = line.Text
		case strings.Contains(plain, "import argparse"):
			importLine = line.Text
		}
	}
	if docstringLine == "" || usageCommandLine == "" || importLine == "" {
		t.Fatalf("numbered Python render missing expected lines:\n%s", strings.Join(renderedTexts(rendered), "\n"))
	}
	if docstringGutter != 8 {
		t.Fatalf("numbered code selection gutter = %d, want 4 indent + 2 digits + 2 separators", docstringGutter)
	}
	numberEnd := strings.Index(docstringLine, "3  ") + len("3  ")
	sourceStart := strings.Index(docstringLine, "Rider Follow")
	if numberEnd < len("3  ") || sourceStart <= numberEnd || !strings.Contains(docstringLine[numberEnd:sourceStart], "\x1b[") {
		t.Fatalf("line-number gutter should end its style before highlighted source: %q", docstringLine)
	}
	if !strings.Contains(importLine, "\x1b[") {
		t.Fatalf("Python source should retain syntax highlighting: %q", importLine)
	}
	if strings.Contains(docstringLine, "\x1b[48;5;22m") {
		t.Fatalf("idempotent existing-file preview should not use added background: %q", docstringLine)
	}
	commandStart := strings.Index(usageCommandLine, "python3") + len("python3")
	commandEnd := strings.Index(usageCommandLine, ".py") + len(".py")
	if commandStart < len("python3") || commandEnd <= commandStart || strings.Contains(usageCommandLine[commandStart:commandEnd], "\x1b[") {
		t.Fatalf("multiline docstring body was incorrectly highlighted as Python code: %q", usageCommandLine)
	}

	source, ok := splitNumberedToolCode(code)
	if !ok || source[2] != "Rider Follow 报告生成器" || source[7] != "import argparse" {
		t.Fatalf("split numbered source = %#v, %v", source, ok)
	}
}

func TestToolCallBlockRendersNewFileContentWithAddedBackground(t *testing.T) {
	ctx := RenderContext{ContentWidth: 80, Markdown: markdownRenderer(80)}
	block := ToolCallBlock{
		Call: ToolCall{
			Name:       "write",
			Input:      "main.go",
			Output:     "Wrote 2 lines, 32 bytes",
			Code:       "1  package main\n2  func main() {}",
			Language:   "go",
			NoCollapse: true,
		},
	}

	rendered := block.Render(ctx)
	codeLines := 0
	for _, line := range rendered.Lines {
		plain := ansi.Strip(line.Text)
		if strings.Contains(plain, "package main") || strings.Contains(plain, "func main()") {
			codeLines++
			if !strings.Contains(line.Text, "\x1b[48;5;22m") {
				t.Fatalf("new-file source row should use added background: %q", line.Text)
			}
		}
	}
	if codeLines != 2 {
		t.Fatalf("new-file code lines = %d, want 2", codeLines)
	}
}

func TestSelectedTextOmitsNumberedToolCodeGutter(t *testing.T) {
	m := NewModel(Options{
		Width:  80,
		Height: 12,
		InitialMessages: []Message{{
			Tool:           true,
			ToolName:       "write",
			ToolInput:      "main.go",
			ToolOutput:     "Wrote 2 lines, 32 bytes",
			ToolCode:       "1  package main\n2  func main() {}",
			ToolLanguage:   "go",
			ToolNoCollapse: true,
			Expanded:       true,
		}},
	})

	first, last := -1, -1
	for i, line := range m.lines {
		plain := ansi.Strip(line)
		switch {
		case strings.Contains(plain, "package main"):
			first = i
		case strings.Contains(plain, "func main()"):
			last = i
		}
	}
	if first < 0 || last < 0 {
		t.Fatalf("numbered code lines missing from transcript: %#v", m.lines)
	}
	if got, want := m.gutterAt(first), 7; got != want {
		t.Fatalf("one-digit numbered code gutter = %d, want %d", got, want)
	}

	m.selStart = cell{line: first, col: 0}
	m.selEnd = cell{line: last, col: m.gutterAt(last) + len("func main() {}")}
	selected := strings.Split(m.selectedText(), "\n")
	for i := range selected {
		selected[i] = strings.TrimRight(selected[i], " ")
	}
	if got, want := strings.Join(selected, "\n"), "package main\nfunc main() {}"; got != want {
		t.Fatalf("selected numbered code = %q, want %q", got, want)
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
		if strings.Contains(ansi.Strip(line.Text), "if oldValue {") && line.Gutter != 10 {
			t.Fatalf("numbered diff selection gutter = %d, want 4 indent + 6 marker/number columns", line.Gutter)
		}
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

func TestSelectedTextOmitsUpdateDiffMarkersAndLineNumbers(t *testing.T) {
	m := NewModel(Options{
		Width:  80,
		Height: 12,
		InitialMessages: []Message{{
			Tool:           true,
			ToolName:       "update",
			ToolInput:      "main.go",
			ToolOutput:     "Added 1 lines, removed 1 lines",
			ToolCode:       "@@ -12,1 +12,1 @@\n- 12  if oldValue {\n+ 12  if newValue {",
			ToolLanguage:   "diff",
			ToolNoCollapse: true,
			Expanded:       true,
		}},
	})

	first, last := -1, -1
	for i, line := range m.lines {
		plain := ansi.Strip(line)
		switch {
		case strings.Contains(plain, "if oldValue {"):
			first = i
		case strings.Contains(plain, "if newValue {"):
			last = i
		}
	}
	if first < 0 || last < 0 {
		t.Fatalf("update diff lines missing from transcript: %#v", m.lines)
	}
	if got, want := m.gutterAt(first), 10; got != want {
		t.Fatalf("update diff gutter = %d, want %d", got, want)
	}

	m.selStart = cell{line: first, col: 0}
	m.selEnd = cell{line: last, col: m.gutterAt(last) + len("if newValue {")}
	selected := strings.Split(m.selectedText(), "\n")
	for i := range selected {
		selected[i] = strings.TrimRight(selected[i], " ")
	}
	if got, want := strings.Join(selected, "\n"), "if oldValue {\nif newValue {"; got != want {
		t.Fatalf("selected update code = %q, want %q", got, want)
	}
}

func TestToolCallBlockWrapsLongDiffLineWithoutTruncation(t *testing.T) {
	ctx := RenderContext{ContentWidth: 40, Markdown: markdownRenderer(40)}
	longCode := strings.Repeat("a", 50) + "END_MARKER"
	block := ToolCallBlock{
		Call: ToolCall{
			Name:       "update",
			Input:      "/tmp/main.go",
			Output:     "Added 1 lines, removed 0 lines",
			Code:       "@@ -12,0 +12,1 @@\n+ 12  " + longCode + "\n",
			Language:   "diff",
			NoCollapse: true,
		},
	}
	rendered := block.Render(ctx)
	var rawLines []string
	for _, line := range rendered.Lines {
		rawLines = append(rawLines, line.Text)
		if got := lipgloss.Width(line.Text); got != ctx.ContentWidth {
			t.Fatalf("line %q width = %d, want %d", ansi.Strip(line.Text), got, ctx.ContentWidth)
		}
	}
	stripped := ansi.Strip(strings.Join(rawLines, "\n"))
	// The line tail only survives if the long line wrapped rather than being
	// truncated to the viewport width.
	if !strings.Contains(stripped, "END_MARKER") {
		t.Fatalf("long diff line was truncated (END_MARKER missing):\n%s", stripped)
	}
	// The wrapped continuation holding the tail must keep the inserted-line
	// green background band.
	var continuation string
	for _, line := range rawLines {
		if strings.Contains(ansi.Strip(line), "END_MARKER") {
			continuation = line
			break
		}
	}
	if !strings.Contains(continuation, "\x1b[48;5;22m") {
		t.Fatalf("wrapped diff continuation lost its background band:\n%q", continuation)
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
