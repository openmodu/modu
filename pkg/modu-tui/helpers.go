package modutui

import (
	"fmt"
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
	fitted := ansi.Truncate(s, width, "")
	if pad := width - ansi.StringWidth(fitted); pad > 0 {
		fitted += strings.Repeat(" ", pad)
	}
	return fitted
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

func jumpHintText() string { return "Jump to bottom (ctrl+End) ↓" }
func jumpHint() string     { return jumpStyle.Render(jumpHintText()) }

func newMessagesHintText(count int) string {
	if count == 1 {
		return "Have 1 new message (ctrl+End) ↓"
	}
	return fmt.Sprintf("Have %d new messages (ctrl+End) ↓", count)
}

func normalizeTodos(items []TodoItem) []TodoItem {
	out := make([]TodoItem, 0, len(items))
	for _, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		status := strings.TrimSpace(item.Status)
		switch status {
		case "pending", "in_progress", "completed":
		default:
			status = "pending"
		}
		out = append(out, TodoItem{Content: content, Status: status})
	}
	return out
}

func (m *Model) jumpHint() string {
	if m.unseen > 0 {
		return jumpStyle.Render(newMessagesHintText(m.unseen))
	}
	return jumpHint()
}

func centeredLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	left := max(0, (width-lipgloss.Width(s))/2)
	return fitLine(strings.Repeat(" ", left)+s, width)
}
