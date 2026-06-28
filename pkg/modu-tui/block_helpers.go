package modutui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func bodyLines(marker, body string, contentWidth int, styleLine func(string) string) BlockRender {
	var out BlockRender
	contentWidth = max(1, contentWidth)
	first := true
	for _, raw := range strings.Split(body, "\n") {
		wrapped := ansi.Wrap(raw, contentWidth, "")
		if wrapped == "" {
			wrapped = "\n"
		}
		for _, bl := range strings.Split(strings.TrimSuffix(wrapped, "\n"), "\n") {
			prefix := "  "
			if first {
				prefix = marker
				first = false
			}
			out.Add(prefix+styleLine(bl), 2)
		}
	}
	return out
}
