package tui

import (
	"fmt"
	"sort"
	"strings"

	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/types"
)

const modelSelectVisibleRows = 10

func (r *goTUIRoot) openModelSelect() {
	if r.session == nil {
		r.model.statusMsg = "no session"
		r.bump()
		return
	}
	choices := r.session.GetAvailableModels()
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].ProviderID == choices[j].ProviderID {
			return choices[i].ID < choices[j].ID
		}
		return choices[i].ProviderID < choices[j].ProviderID
	})
	if len(choices) == 0 {
		r.model.statusMsg = "no models configured"
		r.bump()
		return
	}
	r.modelChoices = choices
	r.modelSelectIdx = currentModelChoiceIndex(choices, r.session.GetModel())
	r.adjustModelSelectScroll()
	r.model.state = uiStateModelSelect
	r.model.statusMsg = ""
	r.slashMatches = nil
	r.bump()
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
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.closeModelSelect("model unchanged") }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.closeModelSelect("model unchanged") }),
		gotui.OnStop(gotui.KeyUp, func(ke gotui.KeyEvent) { r.moveModelSelect(-1) }),
		gotui.OnStop(gotui.KeyDown, func(ke gotui.KeyEvent) { r.moveModelSelect(1) }),
		gotui.OnStop(gotui.KeyHome, func(ke gotui.KeyEvent) { r.jumpModelSelect(0) }),
		gotui.OnStop(gotui.KeyEnd, func(ke gotui.KeyEvent) { r.jumpModelSelect(len(r.modelChoices) - 1) }),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.confirmModelSelect() }),
	}
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
	choice := r.modelChoices[r.modelSelectIdx]
	if err := r.session.SetModelByID(choice.ProviderID, choice.ID); err != nil {
		r.model.errMsg = err.Error()
		r.closeModelSelect("model unchanged")
		return
	}
	r.closeModelSelect("model changed")
}

func (r *goTUIRoot) closeModelSelect(status string) {
	r.model.state = uiStateInput
	r.modelChoices = nil
	r.modelSelectIdx = 0
	r.modelSelectScroll = 0
	r.model.statusMsg = status
	r.bump()
}

func (r *goTUIRoot) renderModelSelectWidget() *gotui.Element {
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)
	container.AddChild(gotui.New(
		gotui.WithText("  Select model"),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
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
	for i := r.modelSelectScroll; i < end; i++ {
		choice := r.modelChoices[i]
		selected := i == r.modelSelectIdx
		active := current != nil && current.ProviderID == choice.ProviderID && current.ID == choice.ID
		line := modelChoiceLine(choice, selected, active)
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
		gotui.WithText("  ↑/↓ select  enter confirm  esc cancel"),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	return container
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
