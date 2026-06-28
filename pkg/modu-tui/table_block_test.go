package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTableBlockRendersBorders(t *testing.T) {
	block := TableBlock{
		Marker: botStyle.Render("● "),
		Rows: [][]string{
			{"Name", "Count"},
			{"apple", "12"},
		},
		Aligns: []lipgloss.Position{lipgloss.Left, lipgloss.Right},
	}

	got := strings.Join(renderedTexts(block.Render(RenderContext{ContentWidth: 60})), "\n")
	for _, want := range []string{"┌", "┬", "└", "│"} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered table missing border %q:\n%s", want, got)
		}
	}
}
