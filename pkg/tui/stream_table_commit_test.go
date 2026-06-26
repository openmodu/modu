package tui

import (
	"strings"
	"testing"
)

// streamTable drives commitStreamingPrefix over a growing assistant block and
// returns every line that was committed to scrollback plus the final live tail,
// i.e. the exact content the terminal ends up showing. pendingScroll is never
// drained here (paint() would), so it accumulates the full commit history and
// any double-commit shows up as a duplicate line.
func streamTable(t *testing.T, width, height int, steps []string) []string {
	t.Helper()
	b := &bubbleTUI{model: &uiModel{width: width}, height: height, width: width}
	b.resetStreamTracking()
	for _, content := range steps {
		b.model.state = uiStateQuerying
		b.model.blocks = []uiBlock{{Kind: "assistant", Streaming: true, Content: content}}
		b.commitStreamingPrefix()
	}
	// Stream end: MessageEnd marks the block done and printAssistantTailCmd commits
	// whatever wasn't already streamed out (the real finalize path).
	final := steps[len(steps)-1]
	idx := len(b.model.blocks) - 1
	b.model.blocks = []uiBlock{{Kind: "assistant", Streaming: false, Content: final}}
	b.printAssistantTailCmd(b.model.blocks[idx], idx)
	return b.pendingScroll
}

// A GFM table taller than the screen must commit to scrollback as ONE complete
// box (header included) exactly once — never split mid-table and never a stale
// partial left above the final copy. This is the "无表头的残表与完整表重叠" the
// streaming committer must not produce.
func TestStreamingOversizedTableCommitsOnceWithHeader(t *testing.T) {
	const w, h = 80, 18

	var rows []string
	for range 16 {
		rows = append(rows, "| group "+strings.Repeat("x", 3)+" | COLB val | COLC detail text here |")
	}
	table := "| COL_ALPHA | COL_BETA | COL_GAMMA |\n|---|---|---|\n" + strings.Join(rows, "\n")

	// Case A: table followed by more content (a trailing blank line settles it
	// mid-stream, so it commits while the reply is still streaming).
	stepsA := []string{
		"intro paragraph\n\n",
		"intro paragraph\n\n" + table + "\n",
		"intro paragraph\n\n" + table + "\n\n",
		"intro paragraph\n\n" + table + "\n\n### after\n\ntail text",
	}
	// Case B: the table is the last block and the stream just ends on it.
	stepsB := []string{
		"intro paragraph\n\n",
		"intro paragraph\n\n" + table + "\n",
		"intro paragraph\n\n" + table,
	}

	for name, steps := range map[string][]string{"trailing-content": stepsA, "table-last": stepsB} {
		committed := streamTable(t, w, h, steps)
		all := stripANSIForGoTUI(strings.Join(committed, "\n"))

		if n := strings.Count(all, "COL_ALPHA"); n != 1 {
			t.Fatalf("[%s] header committed %d times, want exactly 1 (split/duplicate table):\n%s", name, n, all)
		}
		// Every data row must be present exactly once too — no row dropped, none doubled.
		if n := strings.Count(all, "COLC detail"); n != 16 {
			t.Fatalf("[%s] data cell committed %d times, want 16:\n%s", name, n, all)
		}
	}
}
