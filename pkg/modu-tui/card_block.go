package modutui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type CardBlock struct {
	Lines       []string
	BorderStyle lipgloss.Style
}

func (b CardBlock) Render(ctx RenderContext) BlockRender {
	var out BlockRender
	for _, line := range b.RenderWidth(ctx.ContentWidth) {
		out.Add(line, 0)
	}
	return out
}

func (b CardBlock) RenderWidth(width int) []string {
	width = max(1, width)
	innerWidth := max(1, width-2)
	style := b.BorderStyle
	if style.GetForeground() == nil {
		style = cardBorderStyle
	}
	top := "┏" + strings.Repeat("━", max(0, innerWidth)) + "┓"
	bottom := "┗" + strings.Repeat("━", max(0, innerWidth)) + "┛"
	lines := make([]string, 0, len(b.Lines)+2)
	lines = append(lines, style.Render(fitLine(top, width)))
	for _, line := range b.Lines {
		lines = append(lines, cardLine(line, innerWidth, style))
	}
	lines = append(lines, style.Render(fitLine(bottom, width)))
	return lines
}

func cardLine(content string, innerWidth int, style lipgloss.Style) string {
	plainWidth := max(1, innerWidth)
	fitted := fitLine(content, plainWidth)
	if pad := plainWidth - ansi.StringWidth(fitted); pad > 0 {
		fitted += strings.Repeat(" ", pad)
	}
	return style.Render("┃") + fitted + style.Render("┃")
}
