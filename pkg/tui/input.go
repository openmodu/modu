package tui

import (
	"unicode"

	gotui "github.com/grindlemire/go-tui"
)

const maxInputVisibleRows = 6

// handleInputKey routes individual key events into the text-buffer / cursor
// state. Permission mode bypasses the input box entirely (handled elsewhere
// via permissionKeyMap), so we early-return there.
func (r *goTUIRoot) handleInputKey(ke gotui.KeyEvent) {
	if r.model.state == uiStatePermission {
		return
	}
	rs := []rune(r.draft.Get())
	if r.cursor < 0 {
		r.cursor = 0
	}
	if r.cursor > len(rs) {
		r.cursor = len(rs)
	}
	switch ke.Key {
	case gotui.KeyRune:
		if ke.Rune == 0 {
			return
		}
		if ke.Mod == gotui.ModCtrl && ke.Rune == 'j' {
			ke.Rune = '\n'
		} else if ke.Mod != 0 {
			return
		}
		rs = append(rs[:r.cursor], append([]rune{ke.Rune}, rs[r.cursor:]...)...)
		r.cursor++
		r.draft.Set(string(rs))
		r.updateInputSuggestions()
	case gotui.KeyBackspace:
		if r.cursor == 0 {
			return
		}
		rs = append(rs[:r.cursor-1], rs[r.cursor:]...)
		r.cursor--
		r.draft.Set(string(rs))
		r.updateInputSuggestions()
	case gotui.KeyDelete:
		if r.cursor >= len(rs) {
			return
		}
		rs = append(rs[:r.cursor], rs[r.cursor+1:]...)
		r.draft.Set(string(rs))
		r.updateInputSuggestions()
	case gotui.KeyTab:
		if r.completeSlashMatch() {
			return
		}
		if r.completeFileMatch() {
			return
		}
		r.completePathToken()
	case gotui.KeyLeft:
		if r.cursor > 0 {
			r.cursor--
			r.bump()
		}
	case gotui.KeyRight:
		if r.cursor < len(rs) {
			r.cursor++
			r.bump()
		}
	case gotui.KeyHome:
		r.cursor = inputLineStart(rs, r.cursor)
		r.bump()
	case gotui.KeyEnd:
		r.cursor = inputLineEnd(rs, r.cursor)
		r.bump()
	case gotui.KeyUp:
		if len(r.slashMatches) > 0 {
			r.slashMatchIdx = (r.slashMatchIdx - 1 + len(r.slashMatches)) % len(r.slashMatches)
			r.adjustSlashScroll()
			r.bump()
		} else if r.moveFileMatch(-1) {
			return
		} else if moved := moveInputCursorVertical(rs, r.cursor, -1); moved != r.cursor {
			r.cursor = moved
			r.bump()
		} else {
			r.navigateHistory(-1)
		}
	case gotui.KeyDown:
		if len(r.slashMatches) > 0 {
			r.slashMatchIdx = (r.slashMatchIdx + 1) % len(r.slashMatches)
			r.adjustSlashScroll()
			r.bump()
		} else if r.moveFileMatch(1) {
			return
		} else if moved := moveInputCursorVertical(rs, r.cursor, 1); moved != r.cursor {
			r.cursor = moved
			r.bump()
		} else {
			r.navigateHistory(1)
		}
	case gotui.KeyEnter:
		// If suggestions are visible, complete + submit the highlighted command.
		if len(r.slashMatches) > 0 {
			chosen := r.slashMatches[r.slashMatchIdx].Name
			r.slashMatches = nil
			r.slashMatchIdx = 0
			r.submit(chosen)
			return
		}
		if r.completeFileMatch() {
			return
		}
		if ke.Mod == gotui.ModShift && r.model.queryActive {
			r.submitSteer(r.draft.Get())
			return
		}
		r.submit(r.draft.Get())
	}
}

// ─── Cursor helpers ──────────────────────────────────────────────────────────

func inputLineStart(rs []rune, cursor int) int {
	cursor = clampInt(cursor, 0, len(rs))
	for cursor > 0 && rs[cursor-1] != '\n' {
		cursor--
	}
	return cursor
}

func inputLineEnd(rs []rune, cursor int) int {
	cursor = clampInt(cursor, 0, len(rs))
	for cursor < len(rs) && rs[cursor] != '\n' {
		cursor++
	}
	return cursor
}

func moveInputCursorVertical(rs []rune, cursor, delta int) int {
	cursor = clampInt(cursor, 0, len(rs))
	start := inputLineStart(rs, cursor)
	col := cursor - start
	if delta < 0 {
		if start == 0 {
			return cursor
		}
		prevEnd := start - 1
		prevStart := inputLineStart(rs, prevEnd)
		return min(prevStart+col, prevEnd)
	}
	end := inputLineEnd(rs, cursor)
	if end >= len(rs) {
		return cursor
	}
	nextStart := end + 1
	nextEnd := inputLineEnd(rs, nextStart)
	return min(nextStart+col, nextEnd)
}

type inputLineRange struct {
	Start int
	End   int
}

func inputLineRanges(rs []rune) []inputLineRange {
	ranges := make([]inputLineRange, 0, 1)
	start := 0
	for i, ch := range rs {
		if ch != '\n' {
			continue
		}
		ranges = append(ranges, inputLineRange{Start: start, End: i})
		start = i + 1
	}
	ranges = append(ranges, inputLineRange{Start: start, End: len(rs)})
	return ranges
}

func inputCursorLine(ranges []inputLineRange, cursor int) int {
	if len(ranges) == 0 {
		return 0
	}
	for i, line := range ranges {
		if cursor <= line.End {
			return i
		}
	}
	return len(ranges) - 1
}

func inputVisibleRange(totalLines, cursorLine, maxRows int) (start, end int, above, below bool) {
	if totalLines <= 0 {
		return 0, 0, false, false
	}
	if maxRows < 3 {
		maxRows = 3
	}
	if totalLines <= maxRows {
		return 0, totalLines, false, false
	}
	edgeRows := maxRows - 1
	if cursorLine < edgeRows {
		return 0, edgeRows, false, true
	}
	if cursorLine >= totalLines-edgeRows {
		return totalLines - edgeRows, totalLines, true, false
	}
	contentRows := maxRows - 2
	start = cursorLine - contentRows/2
	end = start + contentRows
	if cursorLine >= end {
		end = cursorLine + 1
		start = end - contentRows
	}
	return start, end, true, true
}

func inputVisibleRows(text string) int {
	rs := []rune(text)
	ranges := inputLineRanges(rs)
	start, end, above, below := inputVisibleRange(len(ranges), inputCursorLine(ranges, len(rs)), maxInputVisibleRows)
	rows := end - start
	if above {
		rows++
	}
	if below {
		rows++
	}
	if rows < 1 {
		return 1
	}
	return rows
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// ─── History navigation ──────────────────────────────────────────────────────

func (r *goTUIRoot) appendHistory(line string) {
	r.history = append(r.history, line)
	r.historyIndex = len(r.history)
	r.historyDraft = ""
	if r.histFile != "" {
		_ = saveHistoryFile(r.histFile, r.history)
	}
}

// navigateHistory browses the input history. delta=-1 goes to older entries,
// delta=+1 goes to newer. The current draft is saved and restored when
// navigating back past the newest entry.
func (r *goTUIRoot) navigateHistory(delta int) {
	if len(r.history) == 0 {
		return
	}
	if r.historyIndex == len(r.history) {
		r.historyDraft = r.draft.Get()
	}
	newIndex := r.historyIndex + delta
	if newIndex < 0 {
		newIndex = 0
	}
	if newIndex > len(r.history) {
		newIndex = len(r.history)
	}
	r.historyIndex = newIndex
	var text string
	if r.historyIndex == len(r.history) {
		text = r.historyDraft
	} else {
		text = r.history[r.historyIndex]
	}
	r.draft.Set(text)
	r.cursor = len([]rune(text))
	r.bump()
}

// ─── Input rendering ─────────────────────────────────────────────────────────

// renderInput builds the "❯ draft" widget. Each rune becomes its own gotui
// element so CJK wide characters are not pushed sideways by an inline cursor
// block; the rune at the cursor position is reverse-video styled (Claude Code
// style — looks like a solid block highlighting the character). At end of line
// the cursor is a single reverse-video space, which the terminal paints as a
// full-cell solid block.
func (r *goTUIRoot) renderInput(width int) *gotui.Element {
	_ = width
	rs := []rune(r.draft.Get())
	r.cursor = clampInt(r.cursor, 0, len(rs))
	ranges := inputLineRanges(rs)
	cursorLine := inputCursorLine(ranges, r.cursor)
	startLine, endLine, above, below := inputVisibleRange(len(ranges), cursorLine, maxInputVisibleRows)

	const promptStr = "❯ "
	const promptIndent = "  " // aligns continuation lines with text after "❯ "

	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)

	// Space cursors use U+2588 FULL BLOCK rather than a reverse-styled space —
	// go-tui's flex renderer skips elements whose text is only whitespace
	// (Reverse / Background attrs are never written to the cell), so empty
	// input and cursor-on-space positions would otherwise be invisible.
	eolCursor := func() *gotui.Element {
		return gotui.New(
			gotui.WithText("█"),
			gotui.WithFlexShrink(0),
		)
	}

	if len(rs) == 0 {
		row := gotui.New(
			gotui.WithDisplay(gotui.DisplayFlex),
			gotui.WithDirection(gotui.Row),
			gotui.WithFlexShrink(0),
		)
		row.AddChild(gotui.New(
			gotui.WithText(promptStr),
			gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Green)),
			gotui.WithFlexShrink(0),
		))
		row.AddChild(eolCursor())
		container.AddChild(row)
		return container
	}

	addHint := func(text string) {
		container.AddChild(gotui.New(
			gotui.WithText(promptIndent+text),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}
	addLine := func(lineIdx int, rng inputLineRange) {
		line := gotui.New(
			gotui.WithDisplay(gotui.DisplayFlex),
			gotui.WithDirection(gotui.Row),
			gotui.WithFlexShrink(0),
		)
		prefix := promptIndent
		style := gotui.NewStyle()
		if lineIdx == 0 {
			prefix = promptStr
			style = gotui.NewStyle().Foreground(gotui.Green)
		}
		line.AddChild(gotui.New(
			gotui.WithText(prefix),
			gotui.WithTextStyle(style),
			gotui.WithFlexShrink(0),
		))
		for i := rng.Start; i < rng.End; i++ {
			charStyle := gotui.NewStyle()
			text := string(rs[i])
			if i == r.cursor {
				if unicode.IsSpace(rs[i]) {
					text = "█"
				} else {
					charStyle = gotui.NewStyle().Reverse()
				}
			}
			line.AddChild(gotui.New(
				gotui.WithText(text),
				gotui.WithTextStyle(charStyle),
				gotui.WithFlexShrink(0),
			))
		}
		if r.cursor == rng.End {
			line.AddChild(eolCursor())
		}
		container.AddChild(line)
	}

	if above {
		addHint("... " + itoa(startLine) + " lines above")
	}
	for lineIdx := startLine; lineIdx < endLine; lineIdx++ {
		addLine(lineIdx, ranges[lineIdx])
	}
	if below {
		addHint("... " + itoa(len(ranges)-endLine) + " lines below")
	}
	return container
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
