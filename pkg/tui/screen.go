package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/text/width"
)

// Screen implements a split-terminal layout using ANSI scroll regions.
//
// Layout (rows are 1-based):
//
//	rows 1 .. height-3 : content scroll region (content scrolls here)
//	row  height-2       : static separator line
//	row  height-1       : input prompt (user types here)
//	row  height         : empty spacer (absorbs Enter's trailing newline)
//
// Thread-safe: content can be written from goroutines while the main
// goroutine is blocked in ReadLine.
type Screen struct {
	mu      sync.Mutex
	out     *os.File
	noColor bool
	height  int
	width   int
	active  bool

	// Tool collapse tracking.
	// After WriteToolHeader, we track how many newlines have been printed
	// into the scroll region so that CollapseToolHeader can cursor-up back
	// to the header line and replace it in-place.
	pendingTool  bool
	toolNewlines int // newlines printed into the scroll region after the header

	// Scroll buffer: all content lines written so far.
	// contentLines holds complete lines (no \n); pendingLine holds the
	// current in-progress line (not yet terminated by \n).
	contentLines []string
	pendingLine  string

	// scrollOff is the number of visual rows from the live bottom that the
	// viewport is currently showing.  0 means live (auto-scroll).
	scrollOff int
}

// NewScreen creates and activates a Screen on out.
// Returns nil if out is not a terminal (caller should fall back to plain output).
func NewScreen(out *os.File) *Screen {
	if !isTerminalFd(uintptr(out.Fd())) {
		return nil
	}
	w, h := termSize()
	if h < 6 {
		return nil // terminal too small
	}
	s := &Screen{
		out:     out,
		noColor: shouldDisableColor(out),
		height:  h,
		width:   w,
	}
	s.enter()
	return s
}

// contentBottom is the last row of the scroll region (rows 1..contentBottom).
func (s *Screen) contentBottom() int { return s.height - 3 }

// ContentBottom returns the height of the content viewport (number of rows).
// Used by Input to compute a sensible page-scroll size.
func (s *Screen) ContentBottom() int { return s.contentBottom() }

// enter initialises the alternate screen and draws the static layout.
func (s *Screen) enter() {
	o := s.out
	// Switch to alternate screen buffer.
	fmt.Fprint(o, ansiAltScreenOn)
	fmt.Fprint(o, ansiHideCursor)
	// Clear the new screen.
	fmt.Fprint(o, "\033[2J")
	// Set scroll region to rows 1..contentBottom.
	fmt.Fprintf(o, "\033[1;%dr", s.contentBottom())
	// Draw the static chrome: separator then hint bar.
	s.redrawSeparator()
	s.redrawHint()
	// Enable mouse wheel tracking (SGR extended mode).
	fmt.Fprint(o, ansiMouseOn)
	// Position cursor at start of content area.
	fmt.Fprint(o, "\033[1;1H")
	fmt.Fprint(o, ansiShowCursor)
	s.active = true
}

// redrawHint draws the keyboard-shortcut hint bar on the bottom-most row.
// Caller must hold mu (or be called before active is set).
func (s *Screen) redrawHint() {
	items := []struct{ key, desc string }{
		{"ctrl+enter", "newline"},
		{"ctrl+r", "expand tool"},
		{"ctrl+c", "abort"},
		{"ctrl+d", "exit"},
		{"shift+drag", "copy text"},
	}
	var parts []string
	for _, it := range items {
		parts = append(parts,
			styled(s.noColor, ansiBold+ansiBrightBlack, it.key)+
				styled(s.noColor, ansiBrightBlack, " "+it.desc))
	}
	sep := styled(s.noColor, ansiBrightBlack, "  ·  ")
	line := "  " + strings.Join(parts, sep)
	// Truncate to terminal width.
	if s.width > 0 && visibleLen(line) > s.width {
		line = line[:s.width]
	}
	fmt.Fprintf(s.out, "\033[%d;1H", s.height)
	fmt.Fprint(s.out, ansiEraseLine)
	fmt.Fprint(s.out, line)
}

// EnableMouse turns on SGR mouse tracking (wheel events).
// Call at the start of a scroll loop; paired with DisableMouse when done.
func (s *Screen) EnableMouse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		fmt.Fprint(s.out, ansiMouseOn)
	}
}

// DisableMouse turns off mouse tracking, restoring normal terminal selection.
func (s *Screen) DisableMouse() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		fmt.Fprint(s.out, ansiMouseOff)
	}
}

// redrawSeparator redraws the static separator row.  Caller must hold mu.
// When scrolled back, it shows a hint like "── ↑ scrolled (Page Down / scroll to return) ──".
func (s *Screen) redrawSeparator() {
	fmt.Fprintf(s.out, "\033[%d;1H", s.height-2)
	fmt.Fprint(s.out, ansiEraseLine)
	if s.scrollOff > 0 {
		hint := " ↑ scrolled "
		hintLen := len([]rune(hint))
		side := (s.width - hintLen) / 2
		if side < 1 {
			side = 1
		}
		line := strings.Repeat("─", side) + hint + strings.Repeat("─", s.width-side-hintLen)
		fmt.Fprint(s.out, styled(s.noColor, ansiYellow, line))
	} else {
		fmt.Fprint(s.out, separator(s.noColor, s.width))
	}
}

// Close restores the terminal to normal mode and replays buffered content
// to the normal screen so the conversation remains visible after exit.
func (s *Screen) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	fmt.Fprint(s.out, ansiMouseOff)     // disable mouse tracking
	fmt.Fprint(s.out, "\033[r")         // reset scroll region
	fmt.Fprint(s.out, ansiAltScreenOff) // leave alternate screen (normal screen now visible)

	// Replay buffered content onto the normal screen.
	for _, line := range s.contentLines {
		fmt.Fprintln(s.out, line)
	}
	if s.pendingLine != "" {
		fmt.Fprintln(s.out, s.pendingLine)
	}

	s.active = false
}

// feedBuffer appends text to the line buffer, splitting on newlines.
// Caller must hold s.mu.
func (s *Screen) feedBuffer(text string) {
	s.pendingLine += text
	for {
		idx := strings.IndexByte(s.pendingLine, '\n')
		if idx < 0 {
			break
		}
		s.contentLines = append(s.contentLines, s.pendingLine[:idx])
		s.pendingLine = s.pendingLine[idx+1:]
	}
}

// visualRowsForLine returns how many terminal rows a single line (no \n)
// occupies at the given terminal width.
func visualRowsForLine(line string, termW int) int {
	if termW <= 0 {
		termW = 80
	}
	vlen := visibleLen(line)
	if vlen == 0 {
		return 1
	}
	return (vlen + termW - 1) / termW
}

// totalVisualRows returns the total number of visual rows in the buffer.
// Caller must hold s.mu.
func (s *Screen) totalVisualRows() int {
	total := 0
	for _, line := range s.contentLines {
		total += visualRowsForLine(line, s.width)
	}
	if s.pendingLine != "" {
		total += visualRowsForLine(s.pendingLine, s.width)
	}
	return total
}

// maxScrollOff returns the maximum allowed scrollOff value.
// Caller must hold s.mu.
func (s *Screen) maxScrollOff() int {
	total := s.totalVisualRows()
	viewH := s.contentBottom()
	if total <= viewH {
		return 0
	}
	return total - viewH
}

// redrawContentArea clears the content scroll region and redraws it from
// the buffer at the current scrollOff.  Caller must hold s.mu.
func (s *Screen) redrawContentArea() {
	viewH := s.contentBottom()
	total := s.totalVisualRows()

	// Visible window: virtual rows [startVRow, endVRow)
	endVRow := total - s.scrollOff
	if endVRow < 0 {
		endVRow = 0
	}
	startVRow := endVRow - viewH
	if startVRow < 0 {
		startVRow = 0
	}

	// Build a flat list of (line, visualRows).
	type entry struct {
		text string
		rows int
	}
	all := make([]entry, 0, len(s.contentLines)+1)
	for _, line := range s.contentLines {
		all = append(all, entry{line, visualRowsForLine(line, s.width)})
	}
	if s.pendingLine != "" {
		all = append(all, entry{s.pendingLine, visualRowsForLine(s.pendingLine, s.width)})
	}

	o := s.out
	fmt.Fprint(o, ansiSaveCursor)
	// Reset scroll region so absolute cursor moves don't cause scrolling.
	fmt.Fprint(o, "\033[r")
	// Clear content area.
	for row := 1; row <= viewH; row++ {
		fmt.Fprintf(o, "\033[%d;1H\033[2K", row)
	}

	// Write visible lines.
	cumVRow := 0
	for _, e := range all {
		entryEnd := cumVRow + e.rows
		if entryEnd <= startVRow {
			cumVRow = entryEnd
			continue
		}
		if cumVRow >= endVRow {
			break
		}
		termRow := 1 + (cumVRow - startVRow)
		if termRow < 1 {
			termRow = 1
		}
		if termRow > viewH {
			break
		}
		fmt.Fprintf(o, "\033[%d;1H", termRow)
		fmt.Fprint(o, e.text)
		cumVRow = entryEnd
	}

	// Restore scroll region.
	fmt.Fprintf(o, "\033[1;%dr", viewH)
	fmt.Fprint(o, ansiRestoreCursor)
}

// ScrollUp scrolls the viewport up by n visual rows.  Thread-safe.
func (s *Screen) ScrollUp(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	maxOff := s.maxScrollOff()
	s.scrollOff += n
	if s.scrollOff > maxOff {
		s.scrollOff = maxOff
	}
	s.redrawContentArea()
	s.redrawSeparator()
}

// ScrollDown scrolls the viewport down by n visual rows.  Thread-safe.
func (s *Screen) ScrollDown(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	s.scrollOff -= n
	if s.scrollOff < 0 {
		s.scrollOff = 0
	}
	s.redrawContentArea()
	s.redrawSeparator()
}

// ScrollToBottom resets the viewport to live (scrollOff=0) and redraws.
// Thread-safe.
func (s *Screen) ScrollToBottom() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active || s.scrollOff == 0 {
		return
	}
	s.scrollOff = 0
	s.redrawContentArea()
	s.redrawSeparator()
}

// appendContent writes text into the scroll region.
// Caller must hold s.mu.  Handles save/restore so the cursor stays on the
// input line between calls.
//
// Strategy: instead of tracking the exact column offset (which breaks for
// East-Asian Ambiguous-width characters like ●/⏺/⎿ that the terminal renders
// as 2 columns but the Unicode library classifies as 1), we always erase the
// bottom line and rewrite the full pendingLine.  For text that contains
// newlines (completed lines) we do a full redraw so the scroll region stays
// consistent.
func (s *Screen) appendContent(text string) {
	s.feedBuffer(text)

	if s.scrollOff > 0 {
		return
	}

	if strings.ContainsRune(text, '\n') {
		// One or more lines completed: full redraw keeps everything in sync.
		// redrawContentArea uses its own save/restore, so don't nest one here.
		if s.pendingTool {
			s.toolNewlines += visualLines(text, s.width)
		}
		s.redrawContentArea()
		return
	}

	// No newline: erase the bottom line and rewrite the current pendingLine
	// from column 1.  This is immune to any character-width miscalculation.
	o := s.out
	fmt.Fprint(o, ansiSaveCursor)
	fmt.Fprint(o, "\033[r") // lift scroll region so absolute moves don't scroll
	fmt.Fprintf(o, "\033[%d;1H\033[2K", s.contentBottom())
	fmt.Fprint(o, s.pendingLine)
	fmt.Fprintf(o, "\033[1;%dr", s.contentBottom()) // restore scroll region
	if s.pendingTool {
		s.toolNewlines += visualLines(text, s.width)
	}
	fmt.Fprint(o, ansiRestoreCursor)
}

// visualLines counts how many terminal rows text occupies, counting both
// explicit newlines and line wraps at the given terminal width.
// ANSI escape sequences are excluded from width accounting.
func visualLines(text string, termW int) int {
	if termW <= 0 {
		termW = 80
	}
	lines := 0
	col := 0
	inEsc := false
	for _, r := range text {
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		if r == '\n' {
			lines++
			col = 0
			continue
		}
		var cw int
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			cw = 2
		default:
			cw = 1
		}
		col += cw
		if col > termW {
			lines++
			col = cw
		}
	}
	return lines
}

// Write appends text to the content area.  Thread-safe.
func (s *Screen) Write(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		fmt.Fprint(s.out, text)
		return
	}
	s.appendContent(text)
}

// Writeln appends text followed by a newline to the content area.  Thread-safe.
func (s *Screen) Writeln(text string) { s.Write(text + "\n") }

// WriteToolHeader appends a tool header line and begins tracking newlines so
// that CollapseToolHeader can replace the header in-place later.  Thread-safe.
func (s *Screen) WriteToolHeader(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		fmt.Fprintln(s.out, text)
		return
	}
	s.pendingTool = true
	s.toolNewlines = 0
	s.appendContent(text + "\n")
}

// CollapseToolHeader replaces the previously written tool header with text.
// If the header has scrolled off the top of the content area, the collapsed
// text is appended instead.  Thread-safe.
func (s *Screen) CollapseToolHeader(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		fmt.Fprintln(s.out, text)
		s.pendingTool = false
		return
	}
	if !s.pendingTool {
		s.appendContent(text + "\n")
		return
	}

	n := s.toolNewlines
	maxUp := s.contentBottom() - 1 // max rows we can go up from contentBottom

	if n < maxUp {
		// Header is still visible – go back and replace it.
		o := s.out
		fmt.Fprint(o, ansiSaveCursor)
		// Move to bottom of scroll region.
		fmt.Fprintf(o, "\033[%d;1H", s.contentBottom())
		// Cursor up (n+1): n lines written after the header, plus the header line itself.
		fmt.Fprintf(o, "\033[%dA", n+1)
		fmt.Fprint(o, ansiEraseLine)
		fmt.Fprint(o, text)
		fmt.Fprint(o, ansiRestoreCursor)
	} else {
		// Header has scrolled off – just append the collapsed line.
		s.appendContent(text + "\n")
	}

	s.pendingTool = false
	s.toolNewlines = 0
}

// InitInputLine clears the input row, prints prompt, and leaves the cursor
// there for ReadLine.  Call this just before each ReadLine invocation.
// Thread-safe.
func (s *Screen) InitInputLine(prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		fmt.Fprint(s.out, prompt)
		return
	}
	o := s.out
	// Draw hint bar on the bottom row.
	s.redrawHint()
	// Clear and repaint the input row.
	fmt.Fprintf(o, "\033[%d;1H", s.height-1)
	fmt.Fprint(o, ansiEraseLine)
	// Print prompt; cursor ends up after the prompt ready for user input.
	fmt.Fprint(o, prompt)
}

// RedrawInputContent redraws the input line with prompt + buf and positions
// the cursor at cursorPos (rune index into buf).  Called by the raw-mode
// readline on every edit.  Thread-safe.
func (s *Screen) RedrawInputContent(prompt, buf string, cursorPos int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	o := s.out
	// Move to input row, erase, reprint prompt + buffer.
	fmt.Fprintf(o, "\033[%d;1H", s.height-1)
	fmt.Fprint(o, ansiEraseLine)
	fmt.Fprint(o, prompt)
	fmt.Fprint(o, buf)

	// Reposition cursor at the right rune offset.
	// We need to count the visible columns used by the prompt and the
	// portion of buf up to cursorPos.
	promptCols := visibleLen(prompt)
	bufRunes := []rune(buf)
	bufBeforeCursor := string(bufRunes[:cursorPos])
	col := promptCols + visibleLen(bufBeforeCursor) + 1 // 1-based
	fmt.Fprintf(o, "\033[%d;%dH", s.height-1, col)
}

// visibleLen returns the number of terminal columns occupied by s,
// ignoring ANSI escape sequences (which have zero width).
// Full-width characters (CJK etc.) count as 2 columns.
func visibleLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if r == '\033' {
			inEsc = true
			continue
		}
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			n += 2
		default:
			n++
		}
	}
	return n
}

// AfterReadLine cleans up after a ReadLine call (clears the input row and the
// spacer row that may have received the echoed newline).  Thread-safe.
func (s *Screen) AfterReadLine() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	o := s.out
	// Redraw the static chrome (separator + hint bar).
	s.redrawSeparator()
	s.redrawHint()
	// Clear the input row.
	fmt.Fprintf(o, "\033[%d;1H", s.height-1)
	fmt.Fprint(o, ansiEraseLine)
	// Park cursor at the input row so it doesn't blink on the separator line
	// while the AI is streaming.
	fmt.Fprintf(o, "\033[%d;1H", s.height-1)
}
