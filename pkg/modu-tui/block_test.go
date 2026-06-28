package modutui

import "github.com/charmbracelet/x/ansi"

func renderedTexts(r BlockRender) []string {
	out := make([]string, 0, len(r.Lines))
	for _, line := range r.Lines {
		out = append(out, ansi.Strip(line.Text))
	}
	return out
}
