package tui

import (
	"fmt"
	"sort"
	"strings"

	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/types"
)

const modelSelectVisibleRows = 10

func (r *goTUIRoot) openModelSelect(initialSearch ...string) {
	if r.session == nil {
		r.model.statusMsg = "no session"
		r.bump()
		return
	}
	allChoices := r.session.GetAllAvailableModels()
	sort.Slice(allChoices, func(i, j int) bool {
		if sameTUIModel(allChoices[i], r.session.GetModel()) {
			return true
		}
		if sameTUIModel(allChoices[j], r.session.GetModel()) {
			return false
		}
		if allChoices[i].ProviderID == allChoices[j].ProviderID {
			return allChoices[i].ID < allChoices[j].ID
		}
		return allChoices[i].ProviderID < allChoices[j].ProviderID
	})
	if len(allChoices) == 0 {
		r.model.statusMsg = "no models configured"
		r.bump()
		return
	}
	r.modelAllChoices = allChoices
	r.modelScopedOnly = len(r.session.GetScopedModelIDs()) > 0
	r.modelSearch = ""
	if len(initialSearch) > 0 {
		r.modelSearch = strings.TrimSpace(initialSearch[0])
	}
	r.filterModelChoices()
	r.modelSelectIdx = currentModelChoiceIndex(r.modelChoices, r.session.GetModel())
	r.adjustModelSelectScroll()
	r.model.state = uiStateModelSelect
	r.model.statusMsg = ""
	r.slashMatches = nil
	r.bump()
}

func (r *goTUIRoot) openScopedModelsSelect() {
	r.openModelSelect()
	if r.model.state != uiStateModelSelect {
		return
	}
	r.modelScopeEdit = true
	r.modelScopedOnly = false
	r.modelScopedIDs = make(map[string]bool)
	scoped := r.session.GetScopedModelIDs()
	if len(scoped) == 0 {
		for _, model := range r.modelAllChoices {
			r.modelScopedIDs[model.ID] = true
		}
	} else {
		for _, id := range scoped {
			r.modelScopedIDs[id] = true
		}
	}
	r.filterModelChoices()
	r.bump()
}

func (r *goTUIRoot) filterModelChoices() {
	choices := r.modelAllChoices
	if r.modelScopedOnly && r.session != nil {
		scoped := make(map[string]struct{})
		for _, id := range r.session.GetScopedModelIDs() {
			scoped[id] = struct{}{}
		}
		if len(scoped) > 0 {
			filtered := make([]*types.Model, 0, len(choices))
			for _, model := range choices {
				if _, ok := scoped[model.ID]; ok {
					filtered = append(filtered, model)
				}
			}
			choices = filtered
		}
	}
	query := strings.ToLower(strings.TrimSpace(r.modelSearch))
	if query != "" {
		filtered := make([]*types.Model, 0, len(choices))
		for _, model := range choices {
			haystack := strings.ToLower(model.ProviderID + " " + model.ID + " " + model.Name + " " + model.ProviderID + "/" + model.ID)
			if strings.Contains(haystack, query) {
				filtered = append(filtered, model)
			}
		}
		choices = filtered
	}
	r.modelChoices = choices
	if r.modelSelectIdx >= len(r.modelChoices) {
		r.modelSelectIdx = max(0, len(r.modelChoices)-1)
	}
	r.adjustModelSelectScroll()
}

func currentModelChoiceIndex(choices []*types.Model, current *types.Model) int {
	if current == nil {
		return 0
	}
	for i, choice := range choices {
		if choice.ProviderID == current.ProviderID && choice.ID == current.ID {
			return i
		}
	}
	return 0
}

func (r *goTUIRoot) modelSelectKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.cancelModelSelect() }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.cancelModelSelect() }),
		gotui.OnStop(gotui.KeyUp, func(ke gotui.KeyEvent) { r.moveModelSelect(-1) }),
		gotui.OnStop(gotui.KeyDown, func(ke gotui.KeyEvent) { r.moveModelSelect(1) }),
		gotui.OnStop(gotui.KeyHome, func(ke gotui.KeyEvent) { r.jumpModelSelect(0) }),
		gotui.OnStop(gotui.KeyEnd, func(ke gotui.KeyEvent) { r.jumpModelSelect(len(r.modelChoices) - 1) }),
		gotui.OnStop(gotui.KeyBackspace, func(ke gotui.KeyEvent) { r.backspaceModelSearch() }),
		gotui.OnStop(gotui.KeyTab, func(ke gotui.KeyEvent) { r.toggleModelScope() }),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.confirmModelSelect() }),
		gotui.OnStop(gotui.Rune(' '), func(ke gotui.KeyEvent) { r.toggleScopedModelSelection() }),
		gotui.OnStop(gotui.AnyRune, func(ke gotui.KeyEvent) { r.appendModelSearch(ke.Rune) }),
	}
}

func (r *goTUIRoot) cancelModelSelect() {
	if r.modelScopeEdit {
		r.closeModelSelect("model scope closed")
		return
	}
	r.closeModelSelect("model unchanged")
}

func (r *goTUIRoot) moveModelSelect(delta int) {
	if len(r.modelChoices) == 0 {
		return
	}
	r.modelSelectIdx = (r.modelSelectIdx + delta + len(r.modelChoices)) % len(r.modelChoices)
	r.adjustModelSelectScroll()
	r.bump()
}

func (r *goTUIRoot) jumpModelSelect(idx int) {
	if len(r.modelChoices) == 0 {
		return
	}
	r.modelSelectIdx = clampInt(idx, 0, len(r.modelChoices)-1)
	r.adjustModelSelectScroll()
	r.bump()
}

func (r *goTUIRoot) appendModelSearch(ch rune) {
	if ch == 0 {
		return
	}
	r.modelSearch += string(ch)
	r.modelSelectIdx = 0
	r.filterModelChoices()
	r.bump()
}

func (r *goTUIRoot) backspaceModelSearch() {
	rs := []rune(r.modelSearch)
	if len(rs) == 0 {
		return
	}
	r.modelSearch = string(rs[:len(rs)-1])
	r.modelSelectIdx = 0
	r.filterModelChoices()
	r.bump()
}

func (r *goTUIRoot) toggleModelScope() {
	if r.modelScopeEdit {
		return
	}
	if r.session == nil || len(r.session.GetScopedModelIDs()) == 0 {
		r.model.statusMsg = "no scoped models configured"
		r.bump()
		return
	}
	r.modelScopedOnly = !r.modelScopedOnly
	r.modelSelectIdx = 0
	r.filterModelChoices()
	r.bump()
}

func (r *goTUIRoot) adjustModelSelectScroll() {
	if len(r.modelChoices) <= modelSelectVisibleRows {
		r.modelSelectScroll = 0
		return
	}
	if r.modelSelectIdx < r.modelSelectScroll {
		r.modelSelectScroll = r.modelSelectIdx
	} else if r.modelSelectIdx >= r.modelSelectScroll+modelSelectVisibleRows {
		r.modelSelectScroll = r.modelSelectIdx - modelSelectVisibleRows + 1
	}
	if r.modelSelectScroll < 0 {
		r.modelSelectScroll = 0
	}
	if maxOffset := len(r.modelChoices) - modelSelectVisibleRows; r.modelSelectScroll > maxOffset {
		r.modelSelectScroll = maxOffset
	}
}

func (r *goTUIRoot) confirmModelSelect() {
	if r.session == nil || len(r.modelChoices) == 0 || r.modelSelectIdx >= len(r.modelChoices) {
		r.closeModelSelect("model unchanged")
		return
	}
	if r.modelScopeEdit {
		r.toggleScopedModelSelection()
		return
	}
	choice := r.modelChoices[r.modelSelectIdx]
	before := r.session.GetModel()
	if err := r.session.SetModelByID(choice.ProviderID, choice.ID); err != nil {
		r.model.errMsg = err.Error()
		r.closeModelSelect("model unchanged")
		return
	}
	if sameTUIModel(before, choice) {
		r.closeModelSelect("model unchanged")
	} else {
		r.closeModelSelect("model changed; context cleared")
	}
}

func (r *goTUIRoot) toggleScopedModelSelection() {
	if !r.modelScopeEdit || r.session == nil || len(r.modelChoices) == 0 || r.modelSelectIdx >= len(r.modelChoices) {
		return
	}
	choice := r.modelChoices[r.modelSelectIdx]
	r.modelScopedIDs[choice.ID] = !r.modelScopedIDs[choice.ID]
	ids := make([]string, 0, len(r.modelScopedIDs))
	for _, model := range r.modelAllChoices {
		if r.modelScopedIDs[model.ID] {
			ids = append(ids, model.ID)
		}
	}
	if len(ids) == len(r.modelAllChoices) {
		ids = nil
	}
	r.session.SetScopedModelIDs(ids)
	r.model.statusMsg = "model scope updated"
	r.bump()
}

func (r *goTUIRoot) cycleModel(direction string) {
	if r.session == nil {
		return
	}
	choices := r.session.GetAvailableModels()
	if len(choices) <= 1 {
		if len(choices) == 0 {
			r.model.statusMsg = "no models configured"
		} else {
			r.model.statusMsg = "only one model available"
		}
		r.bump()
		return
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].ProviderID == choices[j].ProviderID {
			return choices[i].ID < choices[j].ID
		}
		return choices[i].ProviderID < choices[j].ProviderID
	})
	current := currentModelChoiceIndex(choices, r.session.GetModel())
	next := (current + 1) % len(choices)
	if direction == "backward" {
		next = (current - 1 + len(choices)) % len(choices)
	}
	choice := choices[next]
	if err := r.session.SetModelByID(choice.ProviderID, choice.ID); err != nil {
		r.model.errMsg = err.Error()
	} else {
		r.model.statusMsg = "model: " + choice.ID
	}
	r.bump()
}

func (r *goTUIRoot) closeModelSelect(status string) {
	r.model.state = uiStateInput
	r.modelChoices = nil
	r.modelAllChoices = nil
	r.modelSelectIdx = 0
	r.modelSelectScroll = 0
	r.modelSearch = ""
	r.modelScopedOnly = false
	r.modelScopeEdit = false
	r.modelScopedIDs = nil
	r.model.statusMsg = status
	r.bump()
}

func (r *goTUIRoot) renderModelSelectWidget() *gotui.Element {
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)
	scope := "all"
	if r.modelScopedOnly {
		scope = "scoped"
	}
	if r.modelScopeEdit {
		scope = "edit"
	}
	query := r.modelSearch
	if query == "" {
		query = "type to search"
	}
	container.AddChild(gotui.New(
		gotui.WithText("  Select model  scope="+scope),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))
	container.AddChild(gotui.New(
		gotui.WithText("  search: "+query),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))

	end := r.modelSelectScroll + modelSelectVisibleRows
	if end > len(r.modelChoices) {
		end = len(r.modelChoices)
	}
	current := (*types.Model)(nil)
	if r.session != nil {
		current = r.session.GetModel()
	}
	if len(r.modelChoices) == 0 {
		container.AddChild(gotui.New(
			gotui.WithText("  no matching models"),
			gotui.WithTextStyle(gotui.NewStyle().Dim()),
			gotui.WithFlexShrink(0),
		))
	}
	for i := r.modelSelectScroll; i < end; i++ {
		choice := r.modelChoices[i]
		selected := i == r.modelSelectIdx
		active := current != nil && current.ProviderID == choice.ProviderID && current.ID == choice.ID
		line := modelChoiceLine(choice, selected, active)
		if r.modelScopeEdit {
			enabled := r.modelScopedIDs[choice.ID]
			marker := "[ ] "
			if enabled {
				marker = "[x] "
			}
			line = strings.Replace(line, " ", " "+marker, 1)
		}
		if i == r.modelSelectScroll && r.modelSelectScroll > 0 {
			line += "  ↑"
		} else if i == end-1 && end < len(r.modelChoices) {
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
		gotui.WithText(r.modelSelectHint()),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	return container
}

func (r *goTUIRoot) modelSelectHint() string {
	if r.modelScopeEdit {
		return "  ↑/↓ select  enter/space toggle  esc close"
	}
	return "  ↑/↓ select  tab scope  enter confirm  esc cancel"
}

func modelChoiceLine(model *types.Model, selected, active bool) string {
	prefix := "  "
	if selected {
		prefix = "❯ "
	}
	marker := " "
	if active {
		marker = "*"
	}
	name := model.Name
	if strings.TrimSpace(name) == "" {
		name = model.ID
	}
	return fmt.Sprintf("%s%s %s (%s / %s)", prefix, marker, name, model.ProviderID, model.ID)
}

func sameTUIModel(a, b *types.Model) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ProviderID == b.ProviderID && a.ID == b.ID
}
