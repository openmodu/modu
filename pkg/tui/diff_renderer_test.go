package tui

import (
	"strings"
	"testing"
)

// captureWriter records everything the renderer emits so tests can assert on
// the raw ANSI stream of a single Render call.
type captureWriter struct{ buf strings.Builder }

func (c *captureWriter) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *captureWriter) last() string                { return c.buf.String() }
func (c *captureWriter) reset()                      { c.buf.Reset() }

func TestDiffRendererFirstRenderNoClear(t *testing.T) {
	w := &captureWriter{}
	r := newDiffRenderer(w)
	r.Render([]string{"first", "second", "third"}, 40, 10)

	out := w.last()
	if strings.Contains(out, "\x1b[3J") || strings.Contains(out, "\x1b[2J") {
		t.Fatalf("first render must not clear screen/scrollback, got %q", out)
	}
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(out, want) {
			t.Fatalf("first render missing %q in %q", want, out)
		}
	}
	if !strings.HasPrefix(out, ansiSyncBegin) || !strings.HasSuffix(out, ansiSyncEnd) {
		t.Fatalf("render not wrapped in synchronized output: %q", out)
	}
}

func TestDiffRendererResizePreservesScrollback(t *testing.T) {
	w := &captureWriter{}
	r := newDiffRenderer(w)
	r.Render([]string{"first", "second", "third"}, 40, 10)

	// Width shrink — the active frame is bottom-anchored and small; completed
	// turns live in native scrollback. The resize must NOT erase the screen or
	// scrollback (no \x1b[2J / \x1b[3J / home); it repaints the frame in place.
	w.reset()
	r.Render([]string{"first", "second", "third"}, 20, 10)
	out := w.last()
	if strings.Contains(out, ansiFullClear) || strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b[3J") {
		t.Fatalf("resize must not clear screen/scrollback, got %q", out)
	}
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(out, want) {
			t.Fatalf("resize repaint missing %q in %q", want, out)
		}
	}

	// Height change likewise repaints in place without clearing scrollback.
	w.reset()
	r.Render([]string{"first", "second", "third"}, 20, 6)
	if strings.Contains(w.last(), "\x1b[3J") {
		t.Fatalf("height change must not erase scrollback, got %q", w.last())
	}
}

func TestDiffRendererSingleLineUpdateIsMinimal(t *testing.T) {
	w := &captureWriter{}
	r := newDiffRenderer(w)
	r.Render([]string{"line-a", "line-b", "line-c"}, 40, 10)

	// Change only the middle line. Must NOT clear screen, and must rewrite only
	// the changed line's text (not the unchanged neighbours).
	w.reset()
	r.Render([]string{"line-a", "CHANGED", "line-c"}, 40, 10)
	out := w.last()
	if strings.Contains(out, "\x1b[3J") || strings.Contains(out, "\x1b[2J") {
		t.Fatalf("single-line update must not clear screen, got %q", out)
	}
	if !strings.Contains(out, "CHANGED") {
		t.Fatalf("update missing changed line in %q", out)
	}
	if strings.Contains(out, "line-a") || strings.Contains(out, "line-c") {
		t.Fatalf("update should not rewrite unchanged lines, got %q", out)
	}
	if !strings.Contains(out, ansiClearLine) {
		t.Fatalf("update should clear the changed line before rewrite, got %q", out)
	}
}

func TestDiffRendererShrinkClearsTrailingRows(t *testing.T) {
	w := &captureWriter{}
	r := newDiffRenderer(w)
	r.Render([]string{"a", "b", "c", "d"}, 40, 10)

	// Drop to a single line. No full clear, but trailing rows must be erased.
	w.reset()
	r.Render([]string{"a"}, 40, 10)
	out := w.last()
	if strings.Contains(out, ansiFullClear) {
		t.Fatalf("shrink within viewport should not full-clear, got %q", out)
	}
	if !strings.Contains(out, ansiClearLine) {
		t.Fatalf("shrink must clear trailing rows, got %q", out)
	}
}
