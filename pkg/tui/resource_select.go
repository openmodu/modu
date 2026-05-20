package tui

import (
	"fmt"
	"strings"

	gotui "github.com/grindlemire/go-tui"
)

const resourceSelectVisibleRows = 10

type resourceChoice struct {
	Name        string
	Description string
	Source      string
	Path        string
}

func (r *goTUIRoot) openResourceSelect(kind string) {
	if r.session == nil {
		r.model.statusMsg = "no session"
		r.bump()
		return
	}
	switch kind {
	case "skills":
		r.resourceTitle = "Skills"
		r.resourceAllChoices = nil
		for _, skill := range r.session.GetSkills() {
			r.resourceAllChoices = append(r.resourceAllChoices, resourceChoice{
				Name:        skill.Name,
				Description: skill.Description,
				Source:      skill.Source,
			})
		}
	case "prompts":
		r.resourceTitle = "Prompts"
		r.resourceAllChoices = nil
		for _, prompt := range r.session.GetPromptTemplates() {
			r.resourceAllChoices = append(r.resourceAllChoices, resourceChoice{
				Name:        prompt.Name,
				Description: prompt.Description,
				Source:      prompt.Source,
				Path:        prompt.FilePath,
			})
		}
	default:
		return
	}
	if len(r.resourceAllChoices) == 0 {
		r.model.statusMsg = "no " + kind + " found"
		r.bump()
		return
	}
	r.resourceSearch = ""
	r.resourceSelectIdx = 0
	r.filterResourceChoices()
	r.model.state = uiStateResourceSelect
	r.slashMatches = nil
	r.bump()
}

func (r *goTUIRoot) filterResourceChoices() {
	query := strings.ToLower(strings.TrimSpace(r.resourceSearch))
	choices := r.resourceAllChoices
	if query != "" {
		filtered := make([]resourceChoice, 0, len(choices))
		for _, choice := range choices {
			if resourceChoiceMatches(choice, query) {
				filtered = append(filtered, choice)
			}
		}
		choices = filtered
	}
	r.resourceChoices = choices
	if r.resourceSelectIdx >= len(r.resourceChoices) {
		r.resourceSelectIdx = max(0, len(r.resourceChoices)-1)
	}
	r.adjustResourceSelectScroll()
}

func resourceChoiceMatches(choice resourceChoice, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{choice.Name, choice.Description, choice.Source, choice.Path}, " "))
	for _, part := range strings.Fields(query) {
		if !strings.Contains(haystack, part) {
			return false
		}
	}
	return true
}

func (r *goTUIRoot) resourceSelectKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.closeResourceSelect("resource selection closed") }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.closeResourceSelect("resource selection closed") }),
		gotui.OnStop(gotui.KeyUp, func(ke gotui.KeyEvent) { r.moveResourceSelect(-1) }),
		gotui.OnStop(gotui.KeyDown, func(ke gotui.KeyEvent) { r.moveResourceSelect(1) }),
		gotui.OnStop(gotui.KeyPageUp, func(ke gotui.KeyEvent) { r.moveResourceSelect(-resourceSelectVisibleRows) }),
		gotui.OnStop(gotui.KeyPageDown, func(ke gotui.KeyEvent) { r.moveResourceSelect(resourceSelectVisibleRows) }),
		gotui.OnStop(gotui.KeyHome, func(ke gotui.KeyEvent) { r.jumpResourceSelect(0) }),
		gotui.OnStop(gotui.KeyEnd, func(ke gotui.KeyEvent) { r.jumpResourceSelect(len(r.resourceChoices) - 1) }),
		gotui.OnStop(gotui.KeyBackspace, func(ke gotui.KeyEvent) { r.backspaceResourceSearch() }),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.confirmResourceSelect() }),
		gotui.OnStop(gotui.AnyRune, func(ke gotui.KeyEvent) { r.appendResourceSearch(ke.Rune) }),
	}
}

func (r *goTUIRoot) moveResourceSelect(delta int) {
	if len(r.resourceChoices) == 0 {
		return
	}
	r.resourceSelectIdx = clampInt(r.resourceSelectIdx+delta, 0, len(r.resourceChoices)-1)
	r.adjustResourceSelectScroll()
	r.bump()
}

func (r *goTUIRoot) jumpResourceSelect(idx int) {
	if len(r.resourceChoices) == 0 {
		return
	}
	r.resourceSelectIdx = clampInt(idx, 0, len(r.resourceChoices)-1)
	r.adjustResourceSelectScroll()
	r.bump()
}

func (r *goTUIRoot) appendResourceSearch(ch rune) {
	if ch == 0 {
		return
	}
	r.resourceSearch += string(ch)
	r.resourceSelectIdx = 0
	r.filterResourceChoices()
	r.bump()
}

func (r *goTUIRoot) backspaceResourceSearch() {
	rs := []rune(r.resourceSearch)
	if len(rs) == 0 {
		return
	}
	r.resourceSearch = string(rs[:len(rs)-1])
	r.resourceSelectIdx = 0
	r.filterResourceChoices()
	r.bump()
}

func (r *goTUIRoot) adjustResourceSelectScroll() {
	if len(r.resourceChoices) <= resourceSelectVisibleRows {
		r.resourceSelectScroll = 0
		return
	}
	if r.resourceSelectIdx < r.resourceSelectScroll {
		r.resourceSelectScroll = r.resourceSelectIdx
	}
	if r.resourceSelectIdx >= r.resourceSelectScroll+resourceSelectVisibleRows {
		r.resourceSelectScroll = r.resourceSelectIdx - resourceSelectVisibleRows + 1
	}
}

func (r *goTUIRoot) confirmResourceSelect() {
	if len(r.resourceChoices) == 0 || r.resourceSelectIdx >= len(r.resourceChoices) {
		return
	}
	command := "/" + r.resourceChoices[r.resourceSelectIdx].Name + " "
	r.draft.Set(command)
	r.cursor = len([]rune(command))
	r.closeResourceSelect("resource selected")
}

func (r *goTUIRoot) closeResourceSelect(status string) {
	r.model.state = uiStateInput
	r.resourceTitle = ""
	r.resourceChoices = nil
	r.resourceAllChoices = nil
	r.resourceSelectIdx = 0
	r.resourceSelectScroll = 0
	r.resourceSearch = ""
	r.model.statusMsg = status
	r.bump()
}

func (r *goTUIRoot) renderResourceSelectWidget() *gotui.Element {
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)
	title := r.resourceTitle
	if title == "" {
		title = "Resources"
	}
	container.AddChild(gotui.New(
		gotui.WithText(selectorHeaderLine(selectorHeaderOptions{
			Title:    title,
			Selected: r.resourceSelectIdx,
			Visible:  len(r.resourceChoices),
			Total:    len(r.resourceAllChoices),
			Query:    r.resourceSearch,
		})),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))
	search := r.resourceSearch
	if search == "" {
		search = "type to search"
	}
	container.AddChild(gotui.New(
		gotui.WithText("  search: "+search),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))

	end := r.resourceSelectScroll + resourceSelectVisibleRows
	if end > len(r.resourceChoices) {
		end = len(r.resourceChoices)
	}
	if len(r.resourceChoices) == 0 {
		container.AddChild(gotui.New(
			gotui.WithText("  no matching resources"),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}
	for i := r.resourceSelectScroll; i < end; i++ {
		choice := r.resourceChoices[i]
		selected := i == r.resourceSelectIdx
		line := resourceChoiceLine(choice, selected)
		if i == r.resourceSelectScroll && r.resourceSelectScroll > 0 {
			line += "  ↑"
		} else if i == end-1 && end < len(r.resourceChoices) {
			line += "  ↓"
		}
		style := gotui.NewStyle().Dim()
		if selected {
			style = gotui.NewStyle().Foreground(gotui.Cyan).Bold()
		}
		container.AddChild(gotui.New(
			gotui.WithText(line),
			gotui.WithTextStyle(style),
			gotui.WithFlexShrink(0),
		))
	}
	container.AddChild(gotui.New(
		gotui.WithText("  ↑/↓ select  pgup/pgdn page  enter insert command  esc close"),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	return container
}

func resourceChoiceLine(choice resourceChoice, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "❯ "
	}
	label := "/" + choice.Name
	desc := strings.ReplaceAll(strings.TrimSpace(choice.Description), "\n", " ")
	if len(desc) > 70 {
		desc = desc[:67] + "..."
	}
	source := choice.Source
	if source == "" {
		source = "local"
	}
	meta := resourceChoiceMeta(source, choice.Path)
	if desc == "" {
		return fmt.Sprintf("%s%-24s %s", prefix, label, meta)
	}
	return fmt.Sprintf("%s%-24s %s  %s", prefix, label, desc, meta)
}

func resourceChoiceMeta(source, path string) string {
	path = compactResourcePath(path)
	if path == "" {
		return "[" + source + "]"
	}
	return fmt.Sprintf("[%s] %s", source, path)
}

func compactResourcePath(path string) string {
	path = strings.ReplaceAll(strings.TrimSpace(path), "\n", " ")
	if path == "" {
		return ""
	}
	if len(path) <= 48 {
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) > 2 {
		tail := strings.Join(parts[len(parts)-2:], "/")
		if len(tail) <= 45 {
			return ".../" + tail
		}
	}
	return "..." + path[len(path)-45:]
}
