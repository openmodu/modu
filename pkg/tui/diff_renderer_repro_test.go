package tui

import (
	"strings"
	"testing"
)

// Reproduces the "completed turn vanishes on a tall terminal" bug: in diff mode
// each paint() does InsertAbove(pendingScroll) then Render(activeFrame). On a
// terminal much taller than the active frame, the committed turn content must
// survive into scrollback/visible — it must not be erased by a later frame
// repaint.
func TestDiffTurnCommitVisibleOnTallScreen(t *testing.T) {
	const w, h = 90, 30
	vt := newVterm(w, h)
	r := newDiffRenderer(vt)

	header := []string{"HEADER0", "HEADER1", ""}
	input := []string{"❯ ", "status"}

	// startup paint
	r.InsertAbove(header, w)
	r.Render(input, w, h)

	// user echoes a message
	r.InsertAbove([]string{"> say hi", ""}, w)
	r.Render(input, w, h)

	// streaming live region a few times
	for _, live := range []string{"thinking", "Hi (streaming)", "Hi"} {
		r.Render([]string{live, "❯ ", "status"}, w, h)
	}

	// MessageEnd commits the assistant block
	r.InsertAbove([]string{"ANSWER_HI", ""}, w)
	r.Render(input, w, h)

	// finish commits summary + separator
	r.InsertAbove([]string{"  Completed (1s)", "", "────────────────", ""}, w)
	r.Render(input, w, h)

	all := strings.Join(append(append([]string{}, vt.scrollback...), vt.visible()...), "\n")
	for _, want := range []string{"> say hi", "ANSWER_HI", "Completed (1s)"} {
		if !strings.Contains(all, want) {
			t.Fatalf("turn content %q missing from final terminal:\nscrollback=%d\n%s", want, len(vt.scrollback), vt)
		}
	}
}

// lastStableBlockEnd finds the end of the last settled top-level markdown block
// (a blank-line separator outside any open code fence) — the boundary up to which
// streaming content can be safely committed to scrollback.
func TestLastStableBlockEnd(t *testing.T) {
	// One complete paragraph + an in-progress second: commit up to the blank line.
	c := "para one done.\n\npara two in progr"
	got := lastStableBlockEnd(c)
	if c[:got] != "para one done.\n\n" {
		t.Fatalf("want prefix %q, got %q", "para one done.\\n\\n", c[:got])
	}
	// No blank line yet → nothing settled.
	if got := lastStableBlockEnd("still streaming the first paragraph"); got != 0 {
		t.Fatalf("no boundary: want 0, got %d", got)
	}
	// An OPEN code fence must block all commits (its body re-renders on close).
	if got := lastStableBlockEnd("intro\n\n```go\nfunc x() {\n\nbody"); got != 0 {
		t.Fatalf("open fence: want 0, got %d", got)
	}
	// A CLOSED fence followed by a blank line is committable.
	c2 := "intro\n\n```\ncode\n```\n\ntail"
	if got := lastStableBlockEnd(c2); got == 0 || c2[:got] != "intro\n\n```\ncode\n```\n\n" {
		t.Fatalf("closed fence: got %d -> %q", got, c2[:got])
	}
	// A trailing newline is NOT a block separator: the in-progress last block
	// (here a streaming GFM table whose completed rows each end in "\n") must not
	// be committed, or it splits across scrollback at stale column widths.
	c3 := "intro\n\n| a | b |\n|---|---|\n| r1 | r2 |\n"
	if got := lastStableBlockEnd(c3); c3[:got] != "intro\n\n" {
		t.Fatalf("trailing newline mid-table: want prefix %q, got %q", "intro\\n\\n", c3[:got])
	}
}

// frameRows must count the PHYSICAL rows a frame occupies after the terminal
// reflows it at a given width — the move-up distance the resize branch needs to
// reach the true (reflowed) frame top.
func TestFrameRows(t *testing.T) {
	// 3 short lines at width 40 -> 3 rows.
	if got := frameRows([]string{"a", "bb", "ccc"}, 40); got != 3 {
		t.Fatalf("short frame: want 3, got %d", got)
	}
	// A 90-wide line at width 40 reflows to 3 rows; plus 2 short lines -> 5.
	wide := strings.Repeat("z", 90)
	if got := frameRows([]string{wide, "tail", "status"}, 40); got != 5 {
		t.Fatalf("reflowed frame: want 5, got %d", got)
	}
	// Empty frame is at least one row.
	if got := frameRows(nil, 40); got != 1 {
		t.Fatalf("empty frame: want 1, got %d", got)
	}
}
