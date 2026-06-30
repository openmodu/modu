package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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

func TestInputBlockHighlightsSlashCommandToken(t *testing.T) {
	var input InputBlock
	input.Insert("/goal fix the failing test")

	lines, _, _ := input.Render(80, maxInputRows)
	raw := lines[0]
	stripped := ansi.Strip(raw)
	if !strings.Contains(stripped, "❯ /goal fix the failing test") {
		t.Fatalf("rendered slash input stripped text mismatch:\n%s", stripped)
	}
	if got, want := slashInputStyle.GetForeground(), lipgloss.Color("6"); got != want {
		t.Fatalf("slash command token should have a highlight color, got %#v", got)
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

func TestInputBlockDeleteWordBackward(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		cursor     int
		wantValue  string
		wantCursor int
	}{
		{name: "end of word", value: "hello world", cursor: len([]rune("hello world")), wantValue: "hello ", wantCursor: len([]rune("hello "))},
		{name: "trailing spaces", value: "hello world   ", cursor: len([]rune("hello world   ")), wantValue: "hello ", wantCursor: len([]rune("hello "))},
		{name: "middle of word", value: "hello world", cursor: len([]rune("hello wor")), wantValue: "hello ld", wantCursor: len([]rune("hello "))},
		{name: "unicode word", value: "prefix 你好", cursor: len([]rune("prefix 你好")), wantValue: "prefix ", wantCursor: len([]rune("prefix "))},
		{name: "path segment", value: "cat ./pkg/modu-tui", cursor: len([]rune("cat ./pkg/modu-tui")), wantValue: "cat ./pkg/modu-", wantCursor: len([]rune("cat ./pkg/modu-"))},
		{name: "after separator", value: "cat ./pkg/modu-", cursor: len([]rune("cat ./pkg/modu-")), wantValue: "cat ./pkg/", wantCursor: len([]rune("cat ./pkg/"))},
		{name: "comma separated json arg", value: "title,body,state", cursor: len([]rune("title,body,state")), wantValue: "title,body,", wantCursor: len([]rune("title,body,"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := InputBlock{Value: tt.value, Cursor: tt.cursor}
			input.DeleteWordBackward()
			if input.Value != tt.wantValue || input.Cursor != tt.wantCursor {
				t.Fatalf("after DeleteWordBackward value=%q cursor=%d, want value=%q cursor=%d", input.Value, input.Cursor, tt.wantValue, tt.wantCursor)
			}
		})
	}
}

func TestInputBlockDeleteWordBackwardRepeated(t *testing.T) {
	input := InputBlock{Value: "cat ./pkg/modu-tui", Cursor: len([]rune("cat ./pkg/modu-tui"))}

	input.DeleteWordBackward()
	if got, want := input.Value, "cat ./pkg/modu-"; got != want {
		t.Fatalf("after first DeleteWordBackward = %q, want %q", got, want)
	}
	input.DeleteWordBackward()
	if got, want := input.Value, "cat ./pkg/"; got != want {
		t.Fatalf("after second DeleteWordBackward = %q, want %q", got, want)
	}
	input.DeleteWordBackward()
	if got, want := input.Value, "cat ./"; got != want {
		t.Fatalf("after third DeleteWordBackward = %q, want %q", got, want)
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
