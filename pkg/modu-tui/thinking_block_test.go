package modutui

import (
	"strings"
	"testing"
)

func TestThinkingBlockDefaultsCollapsedAndExpands(t *testing.T) {
	collapsed := ThinkingBlock{Text: "hidden reasoning"}.Render(RenderContext{ContentWidth: 40})
	if got := strings.Join(renderedTexts(collapsed), "\n"); strings.Contains(got, "hidden reasoning") {
		t.Fatalf("collapsed thinking block leaked detail:\n%s", got)
	}
	if got := strings.Join(renderedTexts(collapsed), "\n"); !strings.Contains(got, "● Thinking") {
		t.Fatalf("collapsed thinking block missing summary:\n%s", got)
	}

	expanded := ThinkingBlock{Text: "visible reasoning", Expanded: true}.Render(RenderContext{ContentWidth: 40})
	if got := strings.Join(renderedTexts(expanded), "\n"); !strings.Contains(got, "visible reasoning") {
		t.Fatalf("expanded thinking block missing detail:\n%s", got)
	}
}
