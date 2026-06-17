package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// vterm is a tiny terminal emulator that applies the escape-sequence subset
// emitted by diffRenderer, so tests can observe the actual on-screen result
// (visible grid + scrollback) after a render sequence — including resize.
type vterm struct {
	w, h       int
	grid       []string // exactly h rows, visible screen
	scrollback []string // lines that scrolled off the top
	row, col   int       // cursor (0-indexed, within grid)
}

func newVterm(w, h int) *vterm {
	v := &vterm{w: w, h: h}
	v.grid = make([]string, h)
	return v
}

func (v *vterm) resize(w, h int) {
	v.w, v.h = w, h
	// A real terminal reflows here; we don't model reflow because the renderer
	// is expected to fully repaint on resize. Just resize the grid.
	ng := make([]string, h)
	copy(ng, v.grid)
	v.grid = ng
	if v.row >= h {
		v.row = h - 1
	}
}

var csiRe = regexp.MustCompile(`^\x1b\[([0-9;?]*)([A-Za-z])`)

func (v *vterm) Write(p []byte) (int, error) {
	v.write(string(p))
	return len(p), nil
}

func (v *vterm) write(s string) {
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\x1b' {
			if m := csiRe.FindStringSubmatch(s[i:]); m != nil {
				v.csi(m[1], m[2])
				i += len(m[0])
				continue
			}
			// Unknown escape; skip the ESC.
			i++
			continue
		}
		switch c {
		case '\r':
			v.col = 0
		case '\n':
			v.lineFeed()
		default:
			v.putRune(rune(c))
		}
		i++
	}
}

func (v *vterm) csi(params, final string) {
	n := 1
	if params != "" && params[0] != '?' {
		if x, err := strconv.Atoi(strings.Split(params, ";")[0]); err == nil {
			n = x
		}
	}
	switch final {
	case "A":
		v.row = max(0, v.row-n)
	case "B":
		for k := 0; k < n; k++ {
			v.lineFeed()
		}
	case "H":
		v.row, v.col = 0, 0
	case "J":
		switch params {
		case "2": // clear entire screen
			for r := range v.grid {
				v.grid[r] = ""
			}
		case "3": // clear scrollback
			v.scrollback = nil
		}
	case "K":
		if params == "2" || params == "" {
			v.grid[v.row] = ""
		}
	case "C":
		v.col += n
	case "G":
		v.col = max(0, n-1)
	}
}

func (v *vterm) lineFeed() {
	if v.row == v.h-1 {
		v.scrollback = append(v.scrollback, v.grid[0])
		copy(v.grid, v.grid[1:])
		v.grid[v.h-1] = ""
	} else {
		v.row++
	}
}

func (v *vterm) putRune(r rune) {
	line := []rune(v.grid[v.row])
	for len(line) < v.col {
		line = append(line, ' ')
	}
	if v.col < len(line) {
		line[v.col] = r
	} else {
		line = append(line, r)
	}
	v.grid[v.row] = string(line)
	v.col++
}

func (v *vterm) visible() []string {
	out := make([]string, v.h)
	copy(out, v.grid)
	return out
}

func (v *vterm) String() string {
	return fmt.Sprintf("scrollback=%d\n---visible---\n%s", len(v.scrollback), strings.Join(v.visible(), "\n"))
}

// lines builds n numbered single-char lines.
func vtLines(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

func TestDiffRendererResizePreservesTail(t *testing.T) {
	const w, h = 40, 10
	vt := newVterm(w, h)
	r := newDiffRenderer(vt)

	// Tall frame: 20 lines, last is the "input" marker.
	frame := append(vtLines("L", 19), "INPUT")
	r.Render(frame, w, h)
	vt.write("") // no-op, ensure flush applied above

	// After first render the tail (incl. INPUT) must be visible at the bottom.
	if !strings.Contains(vt.grid[h-1], "INPUT") {
		t.Fatalf("after first render, INPUT not on last visible row:\n%s", vt)
	}

	// Resize narrower -> renderer should full-clear + repaint, tail still visible.
	vt.resize(30, h)
	r.Render(frame, 30, h)
	if !strings.Contains(vt.grid[h-1], "INPUT") {
		t.Fatalf("after resize, INPUT not on last visible row:\n%s", vt)
	}
	// The visible grid must not contain duplicated frame lines (a reflow/ghost
	// symptom). Each non-empty visible line should be unique.
	seen := map[string]int{}
	for _, ln := range vt.visible() {
		if ln == "" {
			continue
		}
		seen[ln]++
		if seen[ln] > 1 {
			t.Fatalf("duplicated visible line %q after resize:\n%s", ln, vt)
		}
	}
}

func TestDiffRendererResizeShortFrameTopAnchored(t *testing.T) {
	const w, h = 40, 20
	vt := newVterm(w, h)
	r := newDiffRenderer(vt)

	frame := append(vtLines("L", 5), "INPUT")
	r.Render(frame, w, h)
	vt.resize(30, h)
	r.Render(frame, 30, h)

	// Document current behavior: a frame shorter than the screen is top-anchored
	// (rows 0..n-1), with INPUT on row len-1 and blank rows below.
	t.Logf("short-frame resize result:\n%s", vt)
	if !strings.Contains(vt.grid[5], "INPUT") {
		t.Fatalf("INPUT expected on row 5, got:\n%s", vt)
	}
}

// InsertAbove must commit lines to real scrollback, and a subsequent resize must
// NOT wipe that scrollback — the core fix for "resize loses earlier output". The
// active frame is small and bottom-anchored; completed turns persist above it.
func TestDiffRendererInsertAbovePreservedAcrossResize(t *testing.T) {
	const w, h = 40, 6
	vt := newVterm(w, h)
	r := newDiffRenderer(vt)

	// Active frame: input + status (small, fits the screen).
	r.Render([]string{"❯ ", "status"}, w, h)

	// Commit more completed lines than the screen is tall, forcing some into
	// scrollback as the frame repaints below them.
	r.InsertAbove(vtLines("turn", 8))
	r.Render([]string{"❯ ", "status"}, w, h)

	if len(vt.scrollback) == 0 {
		t.Fatalf("InsertAbove put nothing in scrollback:\n%s", vt)
	}
	before := append([]string(nil), vt.scrollback...)

	// Width shrink: scrollback must be preserved (no \x1b[3J wipe, no home jump).
	vt.resize(30, h)
	r.Render([]string{"❯ ", "status"}, 30, h)
	if len(vt.scrollback) < len(before) {
		t.Fatalf("resize shrank scrollback from %d to %d:\n%s", len(before), len(vt.scrollback), vt)
	}
	if !strings.Contains(vt.grid[h-1], "status") {
		t.Fatalf("after resize, status not on last visible row:\n%s", vt)
	}
}

// PlaceCaret must land the hardware cursor at the given buffer cell after a
// render (IME anchoring), and must reposition it on a caret-only move even when
// no line content changed (the real cursor replaces the fake block, so arrow
// keys produce no diff).
func TestDiffRendererPlaceCaret(t *testing.T) {
	const w, h = 40, 10
	vt := newVterm(w, h)
	r := newDiffRenderer(vt)

	frame := []string{"header", "", "❯ hi你好"}
	r.Render(frame, w, h)

	// Caret after "hi你好": 2 (prefix) + 1 + 1 + 2 + 2 = 8, on the input row (2).
	r.PlaceCaret(true, 2, 8)
	if vt.row != 2 || vt.col != 8 {
		t.Fatalf("caret placed at (%d,%d), want (2,8)\n%s", vt.row, vt.col, vt)
	}

	// Caret-only move (e.g. Left): same frame, no Render, cursor must still move.
	r.PlaceCaret(true, 2, 7)
	if vt.row != 2 || vt.col != 7 {
		t.Fatalf("after caret-only move cursor at (%d,%d), want (2,7)\n%s", vt.row, vt.col, vt)
	}
}
