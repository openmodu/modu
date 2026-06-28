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

func TestMarkdownInlineCodeDoesNotRenderAsRedBackgroundBlock(t *testing.T) {
	style := markdownStyleConfig()
	if style.Code.BackgroundColor != nil {
		t.Fatalf("inline code background should be disabled, got %q", *style.Code.BackgroundColor)
	}
	if style.Code.Color != nil {
		t.Fatalf("inline code foreground should use surrounding text color, got %q", *style.Code.Color)
	}
	if style.Code.Prefix != "" || style.Code.Suffix != "" {
		t.Fatalf("inline code should not add padding, prefix=%q suffix=%q", style.Code.Prefix, style.Code.Suffix)
	}

	block := MarkdownBlock{
		Marker: botStyle.Render("● "),
		Text:   "Commit: `233945e` on branch `codex/modu-code-modu-tui` Subject: `feat(modu-code): migrate`",
	}
	rendered := block.Render(RenderContext{ContentWidth: 120, Markdown: markdownRenderer(120)})
	got := strings.Join(renderedTexts(rendered), "\n")
	if !strings.Contains(got, "233945e") || !strings.Contains(got, "feat(modu-code): migrate") {
		t.Fatalf("commit summary lost inline code text:\n%s", got)
	}
	if strings.Contains(got, "\x1b[48;5;236m") || strings.Contains(got, "\x1b[38;5;203m") {
		t.Fatalf("commit summary should not use glamour inline-code red/background style:\n%q", got)
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
