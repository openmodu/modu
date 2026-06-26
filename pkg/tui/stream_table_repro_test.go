package tui

import (
	"strings"
	"testing"
)

// drives the real commit→InsertAbove→cap→Render→finalize pipeline (mirroring
// paint() + the MessageEnd printAssistantTailCmd handler) against a vterm, so a
// persistent on-screen duplicate of a tall table shows up as the header
// appearing more than once across scrollback+visible.
func runStreamPaints(t *testing.T, w, h int, steps []string, chrome []string) *vterm {
	t.Helper()
	vt := newVterm(w, h)
	r := newDiffRenderer(vt)
	b := &bubbleTUI{model: &uiModel{width: w}, height: h, width: w, renderer: r}
	b.resetStreamTracking()

	paint := func(liveTail []string) {
		if len(b.pendingScroll) > 0 {
			r.InsertAbove(b.pendingScroll, w)
			b.pendingScroll = nil
		}
		active := append(append([]string{}, liveTail...), chrome...)
		if h > 0 && len(active) > h {
			active = active[len(active)-h:]
		}
		r.Render(active, w, h)
	}

	for _, content := range steps {
		b.model.state = uiStateQuerying
		b.model.blocks = []uiBlock{{Kind: "assistant", Streaming: true, Content: content}}
		b.commitStreamingPrefix()
		// mirror renderInlineLive: live shows the uncommitted tail of streamLines
		tail := b.streamLines
		if b.streamCommitN > 0 && b.streamCommitN <= len(tail) {
			tail = tail[b.streamCommitN:]
		}
		paint(tail)
	}

	// MessageEnd: block stops streaming, printAssistantTailCmd commits the tail.
	idx := len(b.model.blocks) - 1
	b.model.blocks[idx].Streaming = false
	b.printAssistantTailCmd(b.model.blocks[idx], idx)
	// a following paint flushes pendingScroll and clears the live region
	b.model.state = uiStateInput
	b.commitStreamingPrefix()
	paint(nil)

	return vt
}

func TestStreamingTallTableNoOnScreenDuplicate(t *testing.T) {
	const w, h = 80, 12

	var rows []string
	for range 16 {
		rows = append(rows, "| grp | COLB val | COLC detail |")
	}
	table := "| COL_ALPHA | COL_BETA | COL_GAMMA |\n|---|---|---|\n" + strings.Join(rows, "\n")
	chrome := []string{"❯ ", "status"}

	cases := map[string][]string{
		"table-last": {
			"intro\n\n",
			"intro\n\n" + table,
		},
		"table-then-footer": {
			"intro\n\n",
			"intro\n\n" + table,
			"intro\n\n" + table + "\n\n",
			"intro\n\n" + table + "\n\nfooter text",
		},
	}

	for name, steps := range cases {
		vt := runStreamPaints(t, w, h, steps, chrome)
		all := stripANSIForGoTUI(strings.Join(append(append([]string{}, vt.scrollback...), vt.visible()...), "\n"))
		if n := strings.Count(all, "COL_ALPHA"); n != 1 {
			t.Errorf("[%s] header on screen+scrollback %d times, want 1:\nscrollback=%d\n%s", name, n, len(vt.scrollback), all)
		}
	}
}
