package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
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
	pendingTool    bool
	toolNewlines   int // newlines printed into the scroll region after the header
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
	// Draw the separator at height-2.
	s.redrawSeparator()
	// Position cursor at start of content area.
	fmt.Fprint(o, "\033[1;1H")
	fmt.Fprint(o, ansiShowCursor)
	s.active = true
}

// redrawSeparator redraws the static separator row.  Caller must hold mu.
func (s *Screen) redrawSeparator() {
	fmt.Fprintf(s.out, "\033[%d;1H", s.height-2)
	fmt.Fprint(s.out, ansiEraseLine)
	fmt.Fprint(s.out, separator(s.noColor, s.width))
}

// Close restores the terminal to normal mode.
func (s *Screen) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return
	}
	fmt.Fprint(s.out, "\033[r")      // reset scroll region
	fmt.Fprint(s.out, ansiAltScreenOff) // leave alternate screen
	s.active = false
}

// appendContent writes text into the scroll region.
// Caller must hold s.mu.  Handles save/restore so the cursor stays on the
// input line between calls.
func (s *Screen) appendContent(text string) {
	o := s.out
	fmt.Fprint(o, ansiSaveCursor)
	// Move to the bottom row of the scroll region and print.
	// A trailing newline here causes the region to scroll up automatically.
	fmt.Fprintf(o, "\033[%d;1H", s.contentBottom())
	fmt.Fprint(o, text)
	if s.pendingTool {
		s.toolNewlines += strings.Count(text, "\n")
	}
	fmt.Fprint(o, ansiRestoreCursor)
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
		// Move back to the bottom of the scroll region so subsequent writes
		// continue from there.
		fmt.Fprintf(o, "\033[%d;1H", s.contentBottom())
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
	// Clear spacer and input rows.
	fmt.Fprintf(o, "\033[%d;1H", s.height)
	fmt.Fprint(o, ansiEraseLine)
	fmt.Fprintf(o, "\033[%d;1H", s.height-1)
	fmt.Fprint(o, ansiEraseLine)
	// Print prompt; cursor ends up after the prompt ready for user input.
	fmt.Fprint(o, prompt)
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
	fmt.Fprintf(o, "\033[%d;1H", s.height)
	fmt.Fprint(o, ansiEraseLine)
	fmt.Fprintf(o, "\033[%d;1H", s.height-1)
	fmt.Fprint(o, ansiEraseLine)
	// Redraw separator in case a terminal scroll disturbed it.
	s.redrawSeparator()
}
