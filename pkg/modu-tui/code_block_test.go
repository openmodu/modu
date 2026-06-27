package modutui

import (
	"strings"
	"testing"
)

func TestCodeBlockRendersHighlightedCode(t *testing.T) {
	ctx := RenderContext{ContentWidth: 60, Markdown: markdownRenderer(60)}
	block := CodeBlock{Marker: botStyle.Render("● "), Language: "go", Code: "package main\nfunc main() {}"}
	rendered := block.Render(ctx)
	got := strings.Join(renderedTexts(rendered), "\n")
	for _, want := range []string{"package", "func", "main"} {
		if !strings.Contains(got, want) {
			t.Fatalf("code block missing %q:\n%s", want, got)
		}
	}
	if len(rendered.Lines) == 0 || rendered.Lines[0].Gutter != 2 {
		t.Fatalf("code block should render body lines with selectable gutter: %+v", rendered.Lines)
	}
}
