package modutui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type ThinkingBlock struct {
	Text     string
	Expanded bool
}

func (b ThinkingBlock) Render(ctx RenderContext) BlockRender {
	width := max(1, ctx.ContentWidth)
	out := BlockRender{}
	out.Add(thinkingMarkerStyle.Render("● ")+dimStyle.Render("Thinking"), 2)
	if !b.Expanded {
		return out
	}
	for _, raw := range strings.Split(strings.TrimRight(b.Text, "\n"), "\n") {
		wrapped := ansi.Wrap(raw, max(1, width-2), "")
		if wrapped == "" {
			wrapped = "\n"
		}
		for _, line := range strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n") {
			out.Add(dimStyle.Render("  "+line), 2)
		}
	}
	return out
}
