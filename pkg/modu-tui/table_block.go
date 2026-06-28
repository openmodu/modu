package modutui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

type TableBlock struct {
	Marker string
	Rows   [][]string
	Aligns []lipgloss.Position
}

func (b TableBlock) Render(ctx RenderContext) BlockRender {
	body := b.renderBody(max(1, ctx.ContentWidth))
	return bodyLines(b.Marker, body, max(1, ctx.ContentWidth), func(s string) string { return s })
}

func (b TableBlock) renderBody(maxWidth int) string {
	columnCount := markdownTableColumnCount(b.Rows)
	if columnCount == 0 {
		return ""
	}
	normalize := func(row []string) []string {
		out := make([]string, columnCount)
		for i := range out {
			if i < len(row) {
				out[i] = row[i]
			}
		}
		return out
	}
	build := func(width int) string {
		t := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(dimStyle).
			BorderRow(true).
			Headers(normalize(b.Rows[0])...).
			StyleFunc(func(row, col int) lipgloss.Style {
				style := lipgloss.NewStyle().Padding(0, 1)
				if col >= 0 && col < len(b.Aligns) {
					style = style.Align(b.Aligns[col])
				}
				if row == table.HeaderRow {
					style = style.Bold(true)
				}
				return style
			})
		for _, row := range b.Rows[1:] {
			t = t.Row(normalize(row)...)
		}
		if width > 0 {
			t = t.Width(width)
		}
		return t.Render()
	}

	rendered := build(0)
	if maxWidth <= 0 || markdownTableWidth(rendered) <= maxWidth {
		return rendered
	}
	return build(maxWidth)
}

func markdownTableColumnCount(rows [][]string) int {
	count := 0
	for _, row := range rows {
		if len(row) > count {
			count = len(row)
		}
	}
	return count
}

func markdownTableWidth(rendered string) int {
	width := 0
	for _, line := range strings.Split(rendered, "\n") {
		if lineWidth := lipgloss.Width(line); lineWidth > width {
			width = lineWidth
		}
	}
	return width
}
