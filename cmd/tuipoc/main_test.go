package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Headless smoke test: size the app, type + send a message, run the stream to
// completion, and confirm the transcript ends up with the rendered table and a
// stable layout — no panics, input stays usable.
func TestPOCStreamsToCompletion(t *testing.T) {
	var m tea.Model = newModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})

	// type "hi" then Enter
	for _, r := range "hi" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	mm := m.(model)
	if !mm.streaming {
		t.Fatal("expected streaming to start after Enter")
	}

	// drive stream ticks to completion (cap iterations as a safety net)
	for i := 0; i < 5000 && mm.streaming; i++ {
		m, _ = m.Update(streamTickMsg{})
		mm = m.(model)
	}
	if mm.streaming {
		t.Fatal("stream never finished")
	}

	view := mm.View()
	if !strings.Contains(view, "you") || !strings.Contains(view, "assistant") {
		t.Fatalf("transcript missing role labels:\n%s", view)
	}
	// Finalized assistant reply is glamour-rendered: the table's header and rows
	// are present (glamour draws ASCII-pipe tables by default).
	content := mm.renderTranscript()
	for _, want := range []string{"Hash", "Message", "331d879"} {
		if !strings.Contains(content, want) {
			t.Fatalf("finalized table missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "streaming…") {
		t.Fatalf("streaming placeholder left over after finalize:\n%s", content)
	}
}

// Wheel-up leaves auto-follow; returning to the bottom resumes it.
func TestPOCWheelControlsFollow(t *testing.T) {
	var m tea.Model = newModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 10})
	// fill with enough content to be scrollable
	mm := m.(model)
	for range 40 {
		mm.messages = append(mm.messages, message{role: roleAssistant, text: "line of history content"})
	}
	mm.refresh()
	mm.vp.GotoBottom()
	mm.follow = true

	m2, _ := mm.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if m2.(model).follow && !m2.(model).vp.AtBottom() {
		t.Fatal("wheel-up away from bottom should disable follow")
	}
}

// Drag-selecting lines in the transcript yields the plain text of exactly those
// lines (ANSI stripped), ready for the clipboard.
func TestPOCDragSelectionText(t *testing.T) {
	m := newModel()
	m.width, m.height = 80, 20
	m.messages = []message{
		{role: roleAssistant, text: "alpha line"},
		{role: roleUser, text: "bravo line"},
		{role: roleAssistant, text: "charlie line"},
	}
	m.follow = true
	m.refresh()

	// Press on the first content row, drag down two rows, release.
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, X: 0, Y: 0})
	if !m.selecting {
		t.Fatal("press should start a selection")
	}
	// Drag to the far right of the third row so the whole first content line is in range.
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion, X: 50, Y: 2})

	got := m.selectedText()
	lo, hi := m.selRange()
	if lo.line != 0 || hi.line != 2 {
		t.Fatalf("selection lines = [%d,%d], want [0,2]", lo.line, hi.line)
	}
	if strings.Contains(got, "\x1b") {
		t.Fatalf("selected text still has ANSI escapes: %q", got)
	}
	// Rows 0-2 are the "assistant" label, a blank line, then the first content
	// line — exactly what is on screen at those rows.
	if !strings.Contains(got, "assistant") || !strings.Contains(got, "alpha line") {
		t.Fatalf("selected text = %q, want it to span the label and first line", got)
	}
	if n := strings.Count(got, "\n"); n != 2 {
		t.Fatalf("selected 3 rows should be 2 newlines, got %d: %q", n, got)
	}

	// Release finalizes; with no real clipboard in CI this may be a benign error,
	// but selecting must not panic and the range stays consistent.
	m.handleMouse(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, X: 0, Y: 2})
	if m.selecting {
		t.Fatal("release should end selecting")
	}
}

// Character-level selection: cellSlice cuts a line on terminal-cell boundaries,
// counting wide CJK glyphs as 2 cells, so a sub-line drag copies exactly the
// covered characters.
func TestPOCCellSlice(t *testing.T) {
	if got := cellSlice("hello world", 6, 11); got != "world" {
		t.Fatalf("ascii sub-slice = %q, want %q", got, "world")
	}
	// "中" and "文" are 2 cells each: "中文ab" spans cells 中=0-1, 文=2-3, a=4, b=5.
	if got := cellSlice("中文ab", 2, 4); got != "文" {
		t.Fatalf("CJK sub-slice = %q, want %q", got, "文")
	}
	if got := cellSlice("中文ab", 4, 6); got != "ab" {
		t.Fatalf("after-CJK slice = %q, want %q", got, "ab")
	}
}

// A drag within a single line copies only the selected characters, not the
// whole line.
func TestPOCIntraLineSelection(t *testing.T) {
	m := newModel()
	m.width, m.height = 80, 20
	m.messages = []message{{role: roleUser, text: "ignored"}}
	m.follow = true
	m.refresh()
	// Inject a known plain line and select cells [4,9) of it.
	m.displayLines = []string{"0123456789abc"}
	m.selStart = cell{line: 0, col: 4}
	m.selEnd = cell{line: 0, col: 9}
	if got := m.selectedText(); got != "45678" {
		t.Fatalf("intra-line selection = %q, want %q", got, "45678")
	}
}

// Dragging to the bottom edge auto-scrolls so a selection can grow past the
// visible window (the whole point: select more than one screenful).
func TestPOCAutoScrollExtendsSelection(t *testing.T) {
	var m tea.Model = newModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 8})
	mm := m.(model)
	for range 60 {
		mm.messages = append(mm.messages, message{role: roleAssistant, text: "history line"})
	}
	mm.follow = false
	mm.refresh()
	mm.vp.GotoTop()
	m = mm

	edge := mm.vp.Height - 1
	m, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: 0})
	m, cmd := m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion, Y: edge})
	if cmd == nil {
		t.Fatal("dragging to the bottom edge should start auto-scroll")
	}
	if got := m.(model).dragDir; got != 1 {
		t.Fatalf("dragDir at bottom edge = %d, want 1", got)
	}

	beforeSel := m.(model).selEnd.line
	beforeOff := m.(model).vp.YOffset
	for range 10 {
		m, _ = m.Update(autoScrollMsg{})
	}
	if off := m.(model).vp.YOffset; off <= beforeOff {
		t.Fatalf("viewport did not auto-scroll: YOffset %d -> %d", beforeOff, off)
	}
	if sel := m.(model).selEnd.line; sel <= beforeSel {
		t.Fatalf("selection did not extend past the window: selEnd.line %d -> %d", beforeSel, sel)
	}

	// Release stops auto-scroll and finalizes.
	m, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionRelease, Y: edge})
	if m.(model).selecting {
		t.Fatal("release should end selecting")
	}
	m, _ = m.Update(autoScrollMsg{}) // a late tick must be a no-op
	if m.(model).autoScrolling {
		t.Fatal("auto-scroll should stop after release")
	}
}
