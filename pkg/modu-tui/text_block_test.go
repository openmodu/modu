package modutui

import (
	"strings"
	"testing"
)

func TestTextBlockRendersWrappedText(t *testing.T) {
	block := TextBlock{Marker: "M ", Text: "abcdef"}
	rendered := block.Render(RenderContext{ContentWidth: 3})
	got := strings.Join(renderedTexts(rendered), "\n")
	if !strings.Contains(got, "M abc") || !strings.Contains(got, "  def") {
		t.Fatalf("wrapped text block mismatch:\n%s", got)
	}
}
