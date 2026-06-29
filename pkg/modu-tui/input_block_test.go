package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestInputBlockEditsAtCursor(t *testing.T) {
	var input InputBlock
	input.Insert("abc")
	input.MoveLeft()
	input.Insert("X")
	if got, want := input.Value, "abXc"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
	input.Backspace()
	if got, want := input.Value, "abc"; got != want {
		t.Fatalf("after backspace = %q, want %q", got, want)
	}
	input.MoveHome()
	input.DeleteForward()
	if got, want := input.Value, "bc"; got != want {
		t.Fatalf("after delete = %q, want %q", got, want)
	}
}

func TestInputBlockReplaceBeforeCursor(t *testing.T) {
	var input InputBlock
	input.Insert("prefix zhege suffix")
	input.Cursor = len([]rune("prefix zhege"))
	input.ReplaceBeforeCursor(len([]rune("zhege")), "这个")

	if got, want := input.Value, "prefix 这个 suffix"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
	if got, want := input.Cursor, len([]rune("prefix 这个")); got != want {
		t.Fatalf("cursor = %d, want %d", got, want)
	}
}

func TestInputBlockLargePasteRendersCollapsedAndExpandsForSubmit(t *testing.T) {
	content := strings.Repeat("alpha ", 50)
	var input InputBlock
	input.Insert("before ")
	input.InsertPaste(content)
	input.Insert(" after")

	if strings.Contains(input.Value, content) {
		t.Fatalf("input Value should keep the paste collapsed, got %q", input.Value)
	}
	if got := input.ExpandedValue(); got != "before "+content+" after" {
		t.Fatalf("expanded value mismatch:\n%q", got)
	}
	lines, _, _ := input.Render(80, maxInputRows)
	rendered := ansi.Strip(lines[0])
	if !strings.Contains(rendered, "[Pasted text") || strings.Contains(rendered, content) {
		t.Fatalf("rendered input should show the paste label only:\n%s", rendered)
	}
}

func TestInputBlockShortPasteKeepsExistingSingleLineBehavior(t *testing.T) {
	var input InputBlock
	input.InsertPaste("alpha\nbeta\rgamma\r\ndelta")
	if got, want := input.Value, "alpha beta gamma delta"; got != want {
		t.Fatalf("short paste value = %q, want %q", got, want)
	}
	if got := input.ExpandedValue(); got != input.Value {
		t.Fatalf("short paste should not use collapsed expansion: %q vs %q", got, input.Value)
	}
}
