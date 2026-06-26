package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
)

func testGlamourRenderer(t *testing.T, width int) *glamour.TermRenderer {
	t.Helper()
	noMargin := uint(0)
	style := glamourstyles.DarkStyleConfig
	style.Document = glamouransi.StyleBlock{
		StylePrimitive: style.Document.StylePrimitive,
		Margin:         &noMargin,
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	if err != nil {
		t.Fatalf("renderer: %v", err)
	}
	return r
}

// While streaming, EVERY table collapses to a one-line progress placeholder (no
// box, no raw markdown) — even a table already settled by a blank line — so the
// live frame height stays small and stable (no fit↔overflow flicker). Tables
// snap to boxes only on finalize (streaming=false).
func TestStreamingTablesRenderAsPlaceholders(t *testing.T) {
	r := testGlamourRenderer(t, 76)
	table := "| COL_ALPHA | COL_BETA |\n|---|---|\n| a1 | b1 |\n| a2 | b2 |"
	box := "┌─└│├┼"

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"open", "intro\n\n" + table},               // table still streaming
		{"settled", "intro\n\n" + table + "\n\nmore"}, // table settled, more text after
	} {
		out, err := renderAssistantMarkdown(r, tc.content, 76, true)
		if err != nil {
			t.Fatalf("[%s] streaming render: %v", tc.name, err)
		}
		if strings.ContainsAny(out, box) {
			t.Errorf("[%s] streaming table drew a box (want placeholder):\n%s", tc.name, out)
		}
		if strings.Contains(out, "COL_ALPHA") {
			t.Errorf("[%s] streaming table leaked raw markdown (want placeholder):\n%s", tc.name, out)
		}
		if !strings.Contains(out, "rendering table") {
			t.Errorf("[%s] missing progress placeholder:\n%s", tc.name, out)
		}
		if !strings.Contains(out, "2 rows") {
			t.Errorf("[%s] placeholder should report the streamed row count:\n%s", tc.name, out)
		}
	}

	// Finalized (streaming=false) → box, header consumed.
	final, err := renderAssistantMarkdown(r, "intro\n\n"+table, 76, false)
	if err != nil {
		t.Fatalf("final render: %v", err)
	}
	if !strings.ContainsAny(final, box) {
		t.Errorf("finalized table did not draw a box:\n%s", final)
	}
	if strings.Contains(final, "| COL_ALPHA | COL_BETA |") {
		t.Errorf("finalized table left raw header line:\n%s", final)
	}
}

// The anti-flash invariant: while a table streams in, the rendered height must
// stay constant (the table is always one placeholder line) so the live frame
// never crosses the fit↔overflow boundary that triggers a full-screen clear.
func TestStreamingTableHeightStaysConstant(t *testing.T) {
	r := testGlamourRenderer(t, 76)
	heightFor := func(rows int) int {
		var b strings.Builder
		b.WriteString("intro\n\n| H | V |\n|---|---|")
		for range rows {
			b.WriteString("\n| a | b |")
		}
		out, err := renderAssistantMarkdown(r, b.String(), 76, true)
		if err != nil {
			t.Fatalf("render %d rows: %v", rows, err)
		}
		return strings.Count(strings.TrimRight(out, "\n"), "\n") + 1
	}
	base := heightFor(1)
	for _, rows := range []int{2, 5, 20, 80} {
		if got := heightFor(rows); got != base {
			t.Fatalf("streaming height changed with row count: %d rows -> %d lines, want %d", rows, got, base)
		}
	}
}

// firstTableStart must ignore pipes inside a code fence (they are literal code,
// not a GFM table) and report the real offset of an actual table.
func TestFirstTableStart(t *testing.T) {
	fenced := "intro\n\n```\n| not | a | table |\n|---|---|---|\n"
	if got := firstTableStart(fenced); got != len(fenced) {
		t.Fatalf("fenced pipes misdetected as a table: got %d, want %d", got, len(fenced))
	}
	withTable := "intro\n\n| H |\n|---|\n| r |"
	want := strings.Index(withTable, "| H |")
	if got := firstTableStart(withTable); got != want {
		t.Fatalf("table offset = %d, want %d", got, want)
	}
	if got := firstTableStart("no table here\njust text"); got != len("no table here\njust text") {
		t.Fatalf("no-table content should return len, got %d", got)
	}
}
