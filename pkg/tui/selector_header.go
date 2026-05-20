package tui

import (
	"fmt"
	"strings"
)

type selectorHeaderOptions struct {
	Title    string
	Selected int
	Visible  int
	Total    int
	Query    string
	Mode     string
}

func selectorHeaderLine(opts selectorHeaderOptions) string {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "Selector"
	}
	parts := []string{title}
	if opts.Total > 0 {
		selected := clampInt(opts.Selected+1, 1, opts.Total)
		parts = append(parts, fmt.Sprintf("%d/%d", selected, opts.Visible))
		if opts.Visible != opts.Total {
			parts = append(parts, fmt.Sprintf("filtered %d/%d", opts.Visible, opts.Total))
		}
	} else {
		parts = append(parts, "0/0")
	}
	if query := strings.TrimSpace(opts.Query); query != "" {
		parts = append(parts, "search: "+query)
	}
	if mode := strings.TrimSpace(opts.Mode); mode != "" {
		parts = append(parts, "mode: "+mode)
	}
	return "  " + strings.Join(parts, "  ")
}
