package modutui

import (
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
)

// markdownRenderer builds a glamour renderer with the document Margin zeroed, so
// finalized markdown sits flush against the left edge.
func markdownRenderer(width int) *glamour.TermRenderer {
	style := glamourstyles.DarkStyleConfig
	if glamourStyle() == "light" {
		style = glamourstyles.LightStyleConfig
	}
	noMargin := uint(0)
	style.Document = glamouransi.StyleBlock{
		StylePrimitive: style.Document.StylePrimitive,
		Margin:         &noMargin,
	}
	r, _ := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(width))
	return r
}

// glamourStyle picks dark/light WITHOUT querying the terminal (no OSC leak).
func glamourStyle() string {
	if s := os.Getenv("TUIPOC_STYLE"); s == "light" || s == "dark" {
		return s
	}
	if fgbg := os.Getenv("COLORFGBG"); fgbg != "" {
		parts := strings.Split(fgbg, ";")
		if last := parts[len(parts)-1]; last == "7" || last == "15" {
			return "light"
		}
	}
	return "dark"
}
