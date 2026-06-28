package modutui

import (
	"strings"
	"testing"
)

func TestCollapsibleBlockRendersCollapsedAndExpanded(t *testing.T) {
	collapsed := CollapsibleBlock{Summary: "Ran command", Detail: "hidden"}.Render(RenderContext{})
	if got := strings.Join(renderedTexts(collapsed), "\n"); strings.Contains(got, "hidden") {
		t.Fatalf("collapsed block leaked detail:\n%s", got)
	}

	expanded := CollapsibleBlock{Summary: "Ran command", Detail: "visible", Expanded: true}.Render(RenderContext{})
	if got := strings.Join(renderedTexts(expanded), "\n"); !strings.Contains(got, "visible") {
		t.Fatalf("expanded block missing detail:\n%s", got)
	}
}
