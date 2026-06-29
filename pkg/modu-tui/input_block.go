package modutui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const pasteTokenBase rune = 0xE000

type inputPaste struct {
	Content string
	Label   string
}

type InputBlock struct {
	Value  string
	Cursor int
	Pastes []inputPaste
}

func (b *InputBlock) Len() int { return len([]rune(b.Value)) }

func (b *InputBlock) Reset() {
	b.Value = ""
	b.Cursor = 0
	b.Pastes = nil
}

func (b *InputBlock) Insert(s string) {
	s = normalizeInputText(s)
	r, ins := []rune(b.Value), []rune(s)
	b.Cursor = clamp(b.Cursor, 0, len(r))
	out := make([]rune, 0, len(r)+len(ins))
	out = append(out, r[:b.Cursor]...)
	out = append(out, ins...)
	out = append(out, r[b.Cursor:]...)
	b.Value, b.Cursor = string(out), b.Cursor+len(ins)
}

func (b *InputBlock) ReplaceBeforeCursor(removeRunes int, s string) {
	s = normalizeInputText(s)
	r, ins := []rune(b.Value), []rune(s)
	b.Cursor = clamp(b.Cursor, 0, len(r))
	start := clamp(b.Cursor-removeRunes, 0, b.Cursor)
	out := make([]rune, 0, len(r)-(b.Cursor-start)+len(ins))
	out = append(out, r[:start]...)
	out = append(out, ins...)
	out = append(out, r[b.Cursor:]...)
	b.Value, b.Cursor = string(out), start+len(ins)
}

func normalizeInputText(s string) string {
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
}

func (b *InputBlock) InsertPaste(content string) {
	if !shouldCollapsePaste(content) {
		b.Insert(content)
		return
	}
	idx := len(b.Pastes)
	b.Pastes = append(b.Pastes, inputPaste{
		Content: content,
		Label:   pasteLabel(content),
	})
	b.insertRune(pasteTokenBase + rune(idx))
}

func (b *InputBlock) insertRune(r rune) {
	rs := []rune(b.Value)
	b.Cursor = clamp(b.Cursor, 0, len(rs))
	rs = append(rs[:b.Cursor], append([]rune{r}, rs[b.Cursor:]...)...)
	b.Cursor++
	b.Value = string(rs)
}

func (b *InputBlock) MoveLeft()  { b.Cursor = max(0, b.Cursor-1) }
func (b *InputBlock) MoveRight() { b.Cursor = min(b.Len(), b.Cursor+1) }
func (b *InputBlock) MoveHome()  { b.Cursor = 0 }
func (b *InputBlock) MoveEnd()   { b.Cursor = b.Len() }

// InsertNewline inserts a literal newline at the cursor, the one way to enter a
// hard line break (ordinary text input strips newlines via normalizeInputText).
func (b *InputBlock) InsertNewline() { b.insertRune('\n') }

// LineCount reports the number of logical lines in the current value.
func (b InputBlock) LineCount() int {
	n := 1
	for _, r := range b.Value {
		if r == '\n' {
			n++
		}
	}
	return n
}

func (b *InputBlock) Backspace() {
	if b.Cursor == 0 {
		return
	}
	r := []rune(b.Value)
	b.Value = string(append(r[:b.Cursor-1], r[b.Cursor:]...))
	b.Cursor--
}

func (b *InputBlock) DeleteForward() {
	r := []rune(b.Value)
	if b.Cursor >= len(r) {
		return
	}
	b.Value = string(append(r[:b.Cursor], r[b.Cursor+1:]...))
}

func (b InputBlock) ExpandedValue() string {
	return b.expandTokens(b.Value)
}

// Render lays the input out across up to maxRows visible lines, wrapping only
// on hard newlines. The first logical line is prefixed with "❯ "; continuations
// are indented to align under it. It returns the rendered lines plus the cursor
// position as a (row, column) within those lines.
func (b InputBlock) Render(width, maxRows int) (lines []string, cursorRow, cursorX int) {
	if maxRows < 1 {
		maxRows = 1
	}
	prefix := youStyle.Render("❯ ")
	const cont = "  "
	prefixWidth := lipgloss.Width(prefix)
	contentWidth := max(1, width-prefixWidth-1)

	runes := []rune(b.Value)
	caret := clamp(b.Cursor, 0, len(runes))

	// Split into logical lines by '\n', tracking rune ranges [start,end).
	type logLine struct{ start, end int }
	var logical []logLine
	lineStart := 0
	for i := 0; i <= len(runes); i++ {
		if i == len(runes) || runes[i] == '\n' {
			logical = append(logical, logLine{lineStart, i})
			lineStart = i + 1
		}
	}

	caretRow := 0
	for idx, ll := range logical {
		if caret >= ll.start && caret <= ll.end {
			caretRow = idx
			break
		}
	}

	// Window of up to maxRows logical lines, scrolled to keep the caret visible.
	total := len(logical)
	start := 0
	if total > maxRows {
		start = clamp(caretRow-(maxRows-1), 0, total-maxRows)
	}
	end := min(total, start+maxRows)

	cursorRow = caretRow - start
	cursorX = prefixWidth

	for row := start; row < end; row++ {
		ll := logical[row]
		lineRunes := runes[ll.start:ll.end]
		pre := cont
		if row == 0 {
			pre = prefix
		}
		if row == caretRow {
			off := clamp(caret-ll.start, 0, len(lineRunes))
			before := b.expandLabels(string(lineRunes[:off]))
			after := b.expandLabels(string(lineRunes[off:]))
			beforeWidth := ansi.StringWidth(before)
			visible := b.expandLabels(string(lineRunes))
			cx := prefixWidth + beforeWidth
			if beforeWidth+ansi.StringWidth(after) > contentWidth {
				if beforeWidth >= contentWidth {
					visible = ansi.TruncateLeft(before, contentWidth, "")
					cx = prefixWidth + ansi.StringWidth(visible)
				} else {
					visible = before + ansi.Truncate(after, contentWidth-beforeWidth, "")
					cx = prefixWidth + beforeWidth
				}
			}
			cursorX = clamp(cx, 0, max(0, width-1))
			lines = append(lines, fitLine(pre+visible, width))
			continue
		}
		visible := b.expandLabels(string(lineRunes))
		if ansi.StringWidth(visible) > contentWidth {
			visible = ansi.Truncate(visible, contentWidth, "")
		}
		lines = append(lines, fitLine(pre+visible, width))
	}
	if len(lines) == 0 {
		lines = append(lines, fitLine(prefix, width))
	}
	return lines, cursorRow, cursorX
}

func (b InputBlock) expandLabels(value string) string {
	return b.expandPasteTokens(value, func(p inputPaste) string {
		return p.Label
	})
}

func (b InputBlock) expandTokens(value string) string {
	return b.expandPasteTokens(value, func(p inputPaste) string {
		return p.Content
	})
}

func (b InputBlock) expandPasteTokens(value string, replace func(inputPaste) string) string {
	if len(b.Pastes) == 0 {
		return value
	}
	var out strings.Builder
	for _, r := range value {
		if idx, ok := pasteTokenIndex(r, len(b.Pastes)); ok {
			out.WriteString(replace(b.Pastes[idx]))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func pasteTokenIndex(r rune, total int) (int, bool) {
	idx := int(r - pasteTokenBase)
	if idx < 0 || idx >= total {
		return 0, false
	}
	return idx, true
}

func shouldCollapsePaste(content string) bool {
	if utf8.RuneCountInString(content) >= 200 {
		return true
	}
	return pasteLineCount(content) >= 6
}

func pasteLabel(content string) string {
	lines := pasteLineCount(content)
	chars := utf8.RuneCountInString(content)
	if lines >= 2 {
		return fmt.Sprintf("[Pasted text %d lines]", lines)
	}
	return fmt.Sprintf("[Pasted text %d chars]", chars)
}

func pasteLineCount(content string) int {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}
