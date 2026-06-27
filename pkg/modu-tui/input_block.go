package modutui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type InputBlock struct {
	Value  string
	Cursor int
}

func (b *InputBlock) Len() int { return len([]rune(b.Value)) }

func (b *InputBlock) Reset() {
	b.Value = ""
	b.Cursor = 0
}

func (b *InputBlock) Insert(s string) {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	r, ins := []rune(b.Value), []rune(s)
	b.Cursor = clamp(b.Cursor, 0, len(r))
	out := make([]rune, 0, len(r)+len(ins))
	out = append(out, r[:b.Cursor]...)
	out = append(out, ins...)
	out = append(out, r[b.Cursor:]...)
	b.Value, b.Cursor = string(out), b.Cursor+len(ins)
}

func (b *InputBlock) MoveLeft()  { b.Cursor = max(0, b.Cursor-1) }
func (b *InputBlock) MoveRight() { b.Cursor = min(b.Len(), b.Cursor+1) }
func (b *InputBlock) MoveHome()  { b.Cursor = 0 }
func (b *InputBlock) MoveEnd()   { b.Cursor = b.Len() }

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

func (b InputBlock) Render(width int) (line string, cursorX int) {
	prefix := youStyle.Render("❯ ")
	prefixWidth := lipgloss.Width(prefix)
	contentWidth := max(1, width-prefixWidth-1)
	runes := []rune(b.Value)
	caret := clamp(b.Cursor, 0, len(runes))
	before := string(runes[:caret])
	after := string(runes[caret:])
	beforeWidth := ansi.StringWidth(before)
	totalWidth := beforeWidth + ansi.StringWidth(after)

	visible := b.Value
	cursorX = prefixWidth + beforeWidth
	if totalWidth > contentWidth {
		if beforeWidth >= contentWidth {
			visible = ansi.TruncateLeft(before, contentWidth, "")
			cursorX = prefixWidth + ansi.StringWidth(visible)
		} else {
			visible = before + ansi.Truncate(after, contentWidth-beforeWidth, "")
			cursorX = prefixWidth + beforeWidth
		}
	}
	cursorX = clamp(cursorX, 0, max(0, width-1))
	return fitLine(prefix+visible, width), cursorX
}
