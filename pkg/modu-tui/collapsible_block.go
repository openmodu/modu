package modutui

import "strings"

type CollapsibleBlock struct {
	Summary  string
	Detail   string
	Expanded bool
}

func (b CollapsibleBlock) Render(RenderContext) BlockRender {
	arrow := "▸"
	if b.Expanded {
		arrow = "▾"
	}
	out := BlockRender{}
	out.Add(dimStyle.Render(arrow+" "+b.Summary), 0)
	if b.Expanded {
		for dl := range strings.SplitSeq(strings.TrimRight(b.Detail, "\n"), "\n") {
			out.Add(dimStyle.Render("    "+dl), 0)
		}
	}
	return out
}
