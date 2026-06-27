package modutui

import (
	"strings"
	"testing"
)

func TestMarkdownBlockRendersMarkdown(t *testing.T) {
	block := MarkdownBlock{Marker: botStyle.Render("● "), Text: "**bold**"}
	rendered := block.Render(RenderContext{ContentWidth: 40, Markdown: markdownRenderer(40)})
	got := strings.Join(renderedTexts(rendered), "\n")
	if !strings.Contains(got, "bold") {
		t.Fatalf("markdown block missing rendered content:\n%s", got)
	}
}

func TestMarkdownBlockRendersTableWithBorders(t *testing.T) {
	block := MarkdownBlock{
		Marker: botStyle.Render("● "),
		Text:   "| Name | Count |\n| --- | ---: |\n| apple | 12 |\n| banana | 3 |",
	}
	rendered := block.Render(RenderContext{ContentWidth: 60, Markdown: markdownRenderer(60)})
	got := strings.Join(renderedTexts(rendered), "\n")

	for _, want := range []string{"┌", "┬", "└", "│"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered table missing border %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "| --- |") {
		t.Fatalf("rendered table leaked markdown delimiter:\n%s", got)
	}
}

func TestMarkdownBlockDoesNotRenderTablesInsideCodeFence(t *testing.T) {
	block := MarkdownBlock{
		Marker: botStyle.Render("● "),
		Text:   "```\n| not | table |\n| --- | --- |\n```",
	}
	rendered := block.Render(RenderContext{ContentWidth: 60, Markdown: markdownRenderer(60)})
	got := strings.Join(renderedTexts(rendered), "\n")

	if strings.Contains(got, "┌") || strings.Contains(got, "└") {
		t.Fatalf("code fence table text should not render as a table:\n%s", got)
	}
	if !strings.Contains(got, "| not | table |") {
		t.Fatalf("code fence content missing:\n%s", got)
	}
}
