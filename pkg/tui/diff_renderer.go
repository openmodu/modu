package tui

import (
	"fmt"
	"io"
	"strings"
)

// diffRenderer is a Go port of pi's differential terminal renderer
// (pi/packages/tui/src/tui.ts, TUI.doRender). It owns the full-screen line
// model and writes the minimal ANSI updates needed to reconcile the terminal
// with the latest frame.
//
// The property we are after: on any width/height change it clears the screen
// AND scrollback (ESC[2J ESC[H ESC[3J) and repaints every line from its own
// model at absolute home. Because the repaint is anchored to absolute home —
// not to wherever the terminal reflowed the previous frame — a resize never
// leaves the orphan "串行" rows that bubbletea's relative-cursor inline
// renderer does. The cheap relative-cursor diff path is used only while width
// and height are stable.
//
// Trimmed vs pi: no kitty-image, overlay, IME-cursor, or clearOnShrink
// handling — modu's scoped use of this renderer does not need them yet. The
// branch structure and cursor math are kept faithful so the dropped pieces can
// be grafted back in later.
type diffRenderer struct {
	w io.Writer

	previousLines       []string
	previousWidth       int
	previousHeight      int
	cursorRow           int // logical end-of-content row
	hardwareCursorRow   int // actual terminal cursor row (relative to buffer top)
	maxLinesRendered    int
	previousViewportTop int
}

func newDiffRenderer(w io.Writer) *diffRenderer {
	return &diffRenderer{w: w}
}

const (
	ansiSyncBegin = "\x1b[?2026h" // begin synchronized output (atomic, flicker-free)
	ansiSyncEnd   = "\x1b[?2026l" // end synchronized output
	ansiFullClear = "\x1b[2J\x1b[H\x1b[3J"
	ansiClearLine = "\x1b[2K"
)

// Render reconciles the terminal with newLines for the given terminal size.
func (s *diffRenderer) Render(newLines []string, width, height int) {
	widthChanged := s.previousWidth != 0 && s.previousWidth != width
	heightChanged := s.previousHeight != 0 && s.previousHeight != height

	previousBufferLength := height
	if s.previousHeight > 0 {
		previousBufferLength = s.previousViewportTop + s.previousHeight
	}
	prevViewportTop := s.previousViewportTop
	if heightChanged {
		prevViewportTop = max(0, previousBufferLength-height)
	}
	viewportTop := prevViewportTop
	hardwareCursorRow := s.hardwareCursorRow
	computeLineDiff := func(targetRow int) int {
		currentScreenRow := hardwareCursorRow - prevViewportTop
		targetScreenRow := targetRow - viewportTop
		return targetScreenRow - currentScreenRow
	}

	fullRender := func(clear bool) {
		var b strings.Builder
		b.WriteString(ansiSyncBegin)
		if clear {
			b.WriteString(ansiFullClear)
		}
		for i, line := range newLines {
			if i > 0 {
				b.WriteString("\r\n")
			}
			b.WriteString(line)
		}
		b.WriteString(ansiSyncEnd)
		io.WriteString(s.w, b.String())

		s.cursorRow = max(0, len(newLines)-1)
		s.hardwareCursorRow = s.cursorRow
		if clear {
			s.maxLinesRendered = len(newLines)
		} else {
			s.maxLinesRendered = max(s.maxLinesRendered, len(newLines))
		}
		bufferLength := max(height, len(newLines))
		s.previousViewportTop = max(0, bufferLength-height)
		s.previousLines = newLines
		s.previousWidth = width
		s.previousHeight = height
	}

	// First render — write everything without clearing so existing scrollback
	// above us is preserved.
	if len(s.previousLines) == 0 && !widthChanged && !heightChanged {
		fullRender(false)
		return
	}

	// Resize. The active frame is small and bottom-anchored; completed turns live
	// in native scrollback, which the terminal reflows itself. So instead of a
	// full clear + home (which would erase scrollback and snap to the top), erase
	// just the old frame in place and repaint it at the new size — history is
	// preserved. After a width change the terminal has already reflowed the old
	// frame's wrapped rows, so the move-to-frame-top is best-effort; with a small
	// frame of mostly short lines this is exact in practice.
	if widthChanged || heightChanged {
		var b strings.Builder
		b.WriteString(ansiSyncBegin)
		if d := s.cursorRow - s.hardwareCursorRow; d > 0 {
			fmt.Fprintf(&b, "\x1b[%dB", d)
		} else if d < 0 {
			fmt.Fprintf(&b, "\x1b[%dA", -d)
		}
		if up := len(s.previousLines) - 1; up > 0 {
			fmt.Fprintf(&b, "\x1b[%dA", up)
		}
		b.WriteString("\r\x1b[0J")
		for i, line := range newLines {
			if i > 0 {
				b.WriteString("\r\n")
			}
			b.WriteString(line)
		}
		b.WriteString(ansiSyncEnd)
		io.WriteString(s.w, b.String())
		s.cursorRow = max(0, len(newLines)-1)
		s.hardwareCursorRow = s.cursorRow
		s.maxLinesRendered = max(s.maxLinesRendered, len(newLines))
		s.previousViewportTop = 0
		s.previousLines = newLines
		s.previousWidth = width
		s.previousHeight = height
		return
	}

	// Stable size — diff against the previous frame.
	firstChanged, lastChanged := -1, -1
	maxLines := max(len(newLines), len(s.previousLines))
	for i := 0; i < maxLines; i++ {
		oldLine := ""
		if i < len(s.previousLines) {
			oldLine = s.previousLines[i]
		}
		newLine := ""
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if firstChanged == -1 {
				firstChanged = i
			}
			lastChanged = i
		}
	}
	appendedLines := len(newLines) > len(s.previousLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(s.previousLines)
		}
		lastChanged = len(newLines) - 1
	}
	appendStart := appendedLines && firstChanged == len(s.previousLines) && firstChanged > 0

	// No changes.
	if firstChanged == -1 {
		s.previousViewportTop = prevViewportTop
		s.previousHeight = height
		return
	}

	// All changes are in deleted lines (content shrank) — nothing new to draw,
	// just clear the trailing rows.
	if firstChanged >= len(newLines) {
		if len(s.previousLines) > len(newLines) {
			targetRow := max(0, len(newLines)-1)
			if targetRow < prevViewportTop {
				fullRender(true)
				return
			}
			extraLines := len(s.previousLines) - len(newLines)
			if extraLines > height {
				fullRender(true)
				return
			}
			var b strings.Builder
			b.WriteString(ansiSyncBegin)
			lineDiff := computeLineDiff(targetRow)
			if lineDiff > 0 {
				fmt.Fprintf(&b, "\x1b[%dB", lineDiff)
			} else if lineDiff < 0 {
				fmt.Fprintf(&b, "\x1b[%dA", -lineDiff)
			}
			b.WriteString("\r")
			clearStartOffset := 1
			if len(newLines) == 0 {
				clearStartOffset = 0
			}
			if extraLines > 0 && clearStartOffset > 0 {
				fmt.Fprintf(&b, "\x1b[%dB", clearStartOffset)
			}
			for i := 0; i < extraLines; i++ {
				b.WriteString("\r" + ansiClearLine)
				if i < extraLines-1 {
					b.WriteString("\x1b[1B")
				}
			}
			moveBack := max(0, extraLines-1+clearStartOffset)
			if moveBack > 0 {
				fmt.Fprintf(&b, "\x1b[%dA", moveBack)
			}
			b.WriteString(ansiSyncEnd)
			io.WriteString(s.w, b.String())
			s.cursorRow = targetRow
			s.hardwareCursorRow = targetRow
		}
		s.previousLines = newLines
		s.previousWidth = width
		s.previousHeight = height
		s.previousViewportTop = prevViewportTop
		return
	}

	// The first changed line scrolled above the visible viewport — we can no
	// longer touch it with relative moves, so fall back to a full repaint.
	if firstChanged < prevViewportTop {
		fullRender(true)
		return
	}

	var b strings.Builder
	b.WriteString(ansiSyncBegin)
	prevViewportBottom := prevViewportTop + height - 1
	moveTargetRow := firstChanged
	if appendStart {
		moveTargetRow = firstChanged - 1
	}
	if moveTargetRow > prevViewportBottom {
		currentScreenRow := max(0, min(height-1, hardwareCursorRow-prevViewportTop))
		moveToBottom := height - 1 - currentScreenRow
		if moveToBottom > 0 {
			fmt.Fprintf(&b, "\x1b[%dB", moveToBottom)
		}
		scroll := moveTargetRow - prevViewportBottom
		b.WriteString(strings.Repeat("\r\n", scroll))
		prevViewportTop += scroll
		viewportTop += scroll
		hardwareCursorRow = moveTargetRow
	}

	lineDiff := computeLineDiff(moveTargetRow)
	if lineDiff > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", lineDiff)
	} else if lineDiff < 0 {
		fmt.Fprintf(&b, "\x1b[%dA", -lineDiff)
	}
	if appendStart {
		b.WriteString("\r\n")
	} else {
		b.WriteString("\r")
	}

	renderEnd := min(lastChanged, len(newLines)-1)
	for i := firstChanged; i <= renderEnd; i++ {
		if i > firstChanged {
			b.WriteString("\r\n")
		}
		b.WriteString(ansiClearLine)
		b.WriteString(newLines[i])
	}

	finalCursorRow := renderEnd
	if len(s.previousLines) > len(newLines) {
		if renderEnd < len(newLines)-1 {
			moveDown := len(newLines) - 1 - renderEnd
			fmt.Fprintf(&b, "\x1b[%dB", moveDown)
			finalCursorRow = len(newLines) - 1
		}
		extraLines := len(s.previousLines) - len(newLines)
		for i := len(newLines); i < len(s.previousLines); i++ {
			b.WriteString("\r\n" + ansiClearLine)
		}
		fmt.Fprintf(&b, "\x1b[%dA", extraLines)
	}
	b.WriteString(ansiSyncEnd)
	io.WriteString(s.w, b.String())

	s.cursorRow = max(0, len(newLines)-1)
	s.hardwareCursorRow = finalCursorRow
	s.maxLinesRendered = max(s.maxLinesRendered, len(newLines))
	s.previousViewportTop = max(prevViewportTop, finalCursorRow-height+1)
	s.previousLines = newLines
	s.previousWidth = width
	s.previousHeight = height
}

// InsertAbove commits lines to real terminal scrollback, just above the current
// active frame, then invalidates the frame model so the next Render repaints it
// fresh below the inserted lines. The active frame is bottom-anchored and small,
// so this scrolls older content up into native scrollback without ever clearing
// it — completed turns persist and the terminal reflows them on resize.
func (s *diffRenderer) InsertAbove(lines []string) {
	if len(lines) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString(ansiSyncBegin)

	if len(s.previousLines) == 0 {
		// No active frame yet (startup): the inserted lines are simply the first
		// scrollback content; the next Render draws the frame below them.
		for _, ln := range lines {
			b.WriteString("\r")
			b.WriteString(ansiClearLine)
			b.WriteString(ln)
			b.WriteString("\r\n")
		}
		b.WriteString(ansiSyncEnd)
		io.WriteString(s.w, b.String())
		s.cursorRow = 0
		s.hardwareCursorRow = 0
		return
	}

	// Move to the top of the active frame: down to its last row, then up over it.
	if d := s.cursorRow - s.hardwareCursorRow; d > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", d)
	} else if d < 0 {
		fmt.Fprintf(&b, "\x1b[%dA", -d)
	}
	if up := len(s.previousLines) - 1; up > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", up)
	}
	b.WriteString("\r")
	b.WriteString("\x1b[0J") // erase the old frame from the screen (not scrollback)
	for _, ln := range lines {
		// Clear BEFORE writing: ansiClearLine (\x1b[2K) erases the whole row
		// regardless of cursor column, so emitting it after the text would blank
		// the line we just wrote. (The startup branch above already does this in
		// the right order.)
		b.WriteString("\r")
		b.WriteString(ansiClearLine)
		b.WriteString(ln)
		b.WriteString("\r\n")
	}
	b.WriteString(ansiSyncEnd)
	io.WriteString(s.w, b.String())

	// Frame model invalidated: the next Render takes the no-clear fresh-paint
	// path at the current cursor (just below the inserted lines). previousWidth/
	// Height are left intact so a genuine resize is still detected.
	s.previousLines = nil
	s.cursorRow = 0
	s.hardwareCursorRow = 0
	s.previousViewportTop = 0
}

// PlaceCaret positions the real hardware cursor at the input caret (row/col are
// absolute within the rendered buffer) and shows it, so a CJK IME anchors its
// composition/candidate window to the caret instead of the terminal's stale
// last-cursor cell. It runs after every Render — including renders that drew
// nothing — so a caret move that changes no line content (arrow keys, since the
// real cursor replaces the fake block) still repositions the cursor.
//
// When active is false the caret is hidden (popups/approvals draw their own
// markers and want no blinking cursor). Emitted outside the frame's sync block;
// a single cursor move is cheap and imperceptible.
func (s *diffRenderer) PlaceCaret(active bool, row, col int) {
	if !active {
		io.WriteString(s.w, "\x1b[?25l")
		return
	}
	var b strings.Builder
	delta := row - s.hardwareCursorRow
	if delta > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", delta)
	} else if delta < 0 {
		fmt.Fprintf(&b, "\x1b[%dA", -delta)
	}
	b.WriteString("\r")
	if col > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", col)
	}
	b.WriteString("\x1b[?25h")
	io.WriteString(s.w, b.String())
	s.hardwareCursorRow = row
}

// Finish moves the cursor past the rendered content and is called on shutdown
// so the shell prompt does not overwrite the final frame (port of pi's stop()).
func (s *diffRenderer) Finish() {
	if len(s.previousLines) == 0 {
		return
	}
	targetRow := len(s.previousLines)
	lineDiff := targetRow - s.hardwareCursorRow
	var b strings.Builder
	if lineDiff > 0 {
		fmt.Fprintf(&b, "\x1b[%dB", lineDiff)
	} else if lineDiff < 0 {
		fmt.Fprintf(&b, "\x1b[%dA", -lineDiff)
	}
	b.WriteString("\r\n")
	io.WriteString(s.w, b.String())
}
