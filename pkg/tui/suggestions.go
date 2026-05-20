package tui

import (
	"strings"

	gotui "github.com/grindlemire/go-tui"
)

// slashVisibleRows is the maximum number of suggestion rows shown at once.
// Lists longer than this scroll to keep the highlighted row in view.
const slashVisibleRows = 10

// updateSlashMatches recomputes the suggestion list from the current draft.
// Called after every text mutation. A draft that does not start with "/" or
// already contains a space (i.e. user moved on to typing args) clears the list.
func (r *goTUIRoot) updateSlashMatches() {
	draft := r.draft.Get()
	if !strings.HasPrefix(draft, "/") || strings.ContainsRune(draft, ' ') {
		r.slashMatches = nil
		r.slashMatchIdx = 0
		r.slashScrollOffset = 0
		return
	}
	r.slashMatches = matchSlashCommands(draft, r.skillSlashCommands())
	if r.slashMatchIdx >= len(r.slashMatches) {
		r.slashMatchIdx = 0
	}
	r.adjustSlashScroll()
}

// adjustSlashScroll keeps slashMatchIdx within the visible window.
func (r *goTUIRoot) adjustSlashScroll() {
	if len(r.slashMatches) <= slashVisibleRows {
		r.slashScrollOffset = 0
		return
	}
	if r.slashMatchIdx < r.slashScrollOffset {
		r.slashScrollOffset = r.slashMatchIdx
	} else if r.slashMatchIdx >= r.slashScrollOffset+slashVisibleRows {
		r.slashScrollOffset = r.slashMatchIdx - slashVisibleRows + 1
	}
	if r.slashScrollOffset < 0 {
		r.slashScrollOffset = 0
	}
	if maxOffset := len(r.slashMatches) - slashVisibleRows; r.slashScrollOffset > maxOffset {
		r.slashScrollOffset = maxOffset
	}
}

// completeSlashMatch fills the draft with the currently highlighted suggestion
// (without cycling). Recomputes matches afterward so the list reduces to the
// chosen prefix.
func (r *goTUIRoot) completeSlashMatch() bool {
	if len(r.slashMatches) == 0 {
		return false
	}
	chosen := r.slashMatches[r.slashMatchIdx].Name
	r.draft.Set(chosen)
	r.cursor = len([]rune(chosen))
	r.updateInputSuggestions()
	r.bump()
	return true
}

// renderSlashSuggestions returns the rows to insert below the input area
// (suggestion list + detail line for the highlighted entry). Returns nil
// when no suggestions are pending.
func (r *goTUIRoot) renderSlashSuggestions() []*gotui.Element {
	if len(r.slashMatches) == 0 {
		return nil
	}
	nameWidth := 0
	for _, m := range r.slashMatches {
		if w := len([]rune(m.Name)); w > nameWidth {
			nameWidth = w
		}
	}
	end := r.slashScrollOffset + slashVisibleRows
	if end > len(r.slashMatches) {
		end = len(r.slashMatches)
	}

	rows := make([]*gotui.Element, 0, end-r.slashScrollOffset+1)
	for i := r.slashScrollOffset; i < end; i++ {
		m := r.slashMatches[i]
		selected := i == r.slashMatchIdx
		prefix := "  "
		if selected {
			prefix = "❯ "
		}
		padded := m.Name + strings.Repeat(" ", nameWidth-len([]rune(m.Name)))
		text := prefix + padded
		if m.Description != "" {
			text += "  " + m.Description
		}
		// Scroll indicator on first/last visible row when more exist outside.
		if i == r.slashScrollOffset && r.slashScrollOffset > 0 {
			text += "  ↑"
		} else if i == end-1 && end < len(r.slashMatches) {
			text += "  ↓"
		}
		style := gotui.NewStyle().Dim()
		if selected {
			style = gotui.NewStyle().Foreground(gotui.Cyan).Bold()
		}
		rows = append(rows, gotui.New(
			gotui.WithText(text),
			gotui.WithTextStyle(style),
			gotui.WithFlexShrink(0),
		))
	}
	// Detail row: full info for the highlighted command.
	if r.slashMatchIdx < len(r.slashMatches) {
		sel := r.slashMatches[r.slashMatchIdx]
		detail := "  " + sel.Name
		if sel.Description != "" {
			detail += " — " + sel.Description
		}
		rows = append(rows, gotui.New(
			gotui.WithText(detail),
			gotui.WithTextStyle(gotui.NewStyle().Foreground(gotui.Yellow).Italic()),
			gotui.WithFlexShrink(0),
		))
	}
	return rows
}
