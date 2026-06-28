package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCardBlockRendersHeavyBorderAtWidth(t *testing.T) {
	lines := CardBlock{Lines: []string{"hello"}}.RenderWidth(12)
	if got, want := len(lines), 3; got != want {
		t.Fatalf("card lines = %d, want %d", got, want)
	}
	if !strings.HasPrefix(ansi.Strip(lines[0]), "┏") || !strings.HasSuffix(strings.TrimRight(ansi.Strip(lines[0]), " "), "┓") {
		t.Fatalf("card top border = %q", ansi.Strip(lines[0]))
	}
	if !strings.HasPrefix(ansi.Strip(lines[1]), "┃") || !strings.HasSuffix(strings.TrimRight(ansi.Strip(lines[1]), " "), "┃") {
		t.Fatalf("card body border = %q", ansi.Strip(lines[1]))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != 12 {
			t.Fatalf("card line %d width = %d, want 12: %q", i, got, line)
		}
	}
}
