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
	r.InsertAbove(header)
	r.Render(input, w, h)

	// user echoes a message
	r.InsertAbove([]string{"> say hi", ""})
	r.Render(input, w, h)

	// streaming live region a few times
	for _, live := range []string{"thinking", "Hi (streaming)", "Hi"} {
		r.Render([]string{live, "❯ ", "status"}, w, h)
	}

	// MessageEnd commits the assistant block
	r.InsertAbove([]string{"ANSWER_HI", ""})
	r.Render(input, w, h)

	// finish commits summary + separator
	r.InsertAbove([]string{"  Completed (1s)", "", "────────────────", ""})
	r.Render(input, w, h)

	all := strings.Join(append(append([]string{}, vt.scrollback...), vt.visible()...), "\n")
	for _, want := range []string{"> say hi", "ANSWER_HI", "Completed (1s)"} {
		if !strings.Contains(all, want) {
			t.Fatalf("turn content %q missing from final terminal:\nscrollback=%d\n%s", want, len(vt.scrollback), vt)
		}
	}
}
