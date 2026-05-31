package tui

const maxInputVisibleRows = 6

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

// ─── Input rendering ─────────────────────────────────────────────────────────
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
