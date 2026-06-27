package modutui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// cell is a transcript position: display-line index + terminal-cell column.
type cell struct{ line, col int }

func (c cell) before(o cell) bool {
	return c.line < o.line || (c.line == o.line && c.col < o.col)
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

func fitLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "")
}

func cellSlice(plain string, from, to int) string {
	if to <= from {
		return ""
	}
	var b strings.Builder
	pos := 0
	for _, r := range plain {
		w := ansi.StringWidth(string(r))
		if pos >= to {
			break
		}
		if pos >= from {
			b.WriteRune(r)
		}
		pos += w
	}
	return b.String()
}

func jumpHint() string           { return jumpStyle.Render("Jump to bottom (ctrl+End) ↓") }
func jumpHintWidth() int         { return lipgloss.Width(jumpHint()) }
func jumpHintLeft(width int) int { return max(0, (width-jumpHintWidth())/2) }

func overlayJumpHint(view string, width int) string {
	if width <= 0 {
		return view
	}
	pill := jumpHint()
	pw := lipgloss.Width(pill)
	left := jumpHintLeft(width)
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		return view
	}
	last := lines[len(lines)-1]
	leftPart := ansi.Truncate(last, left, "")
	if pad := left - lipgloss.Width(leftPart); pad > 0 {
		leftPart += strings.Repeat(" ", pad)
	}
	right := ansi.Truncate(ansi.TruncateLeft(last, left+pw, ""), max(0, width-left-pw), "")
	lines[len(lines)-1] = ansi.Truncate(leftPart+pill+right, width, "")
	return strings.Join(lines, "\n")
}
