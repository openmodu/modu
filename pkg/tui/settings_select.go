package tui

import (
	"fmt"

	gotui "github.com/grindlemire/go-tui"
)

const settingsSelectVisibleRows = 10

type settingsChoice struct {
	Label       string
	Value       func() string
	Description string
	Apply       func()
}

func (r *goTUIRoot) openSettingsSelect() {
	r.settingsChoices = r.buildSettingsChoices()
	if len(r.settingsChoices) == 0 {
		r.model.setTransientStatus("no settings")
		r.bump()
		return
	}
	r.settingsSelectIdx = 0
	r.model.state = uiStateSettingsSelect
	r.slashMatches = nil
	r.bump()
}

func (r *goTUIRoot) buildSettingsChoices() []settingsChoice {
	return []settingsChoice{
		{
			Label:       "Tool output",
			Description: "Expand or collapse tool output in the transcript",
			Value: func() string {
				if r.model.transcriptMode {
					return "expanded"
				}
				return "compact"
			},
			Apply: func() {
				r.model.transcriptMode = !r.model.transcriptMode
				if err := r.savePersistedTUISettings(); err != nil {
					r.model.errMsg = err.Error()
				}
			},
		},
		{
			Label:       "Plan mode",
			Description: "Toggle planning before execution",
			Value: func() string {
				if r.session != nil && r.session.IsPlanMode() {
					return "on"
				}
				return "off"
			},
			Apply: func() {
				if r.session == nil {
					return
				}
				if r.session.IsPlanMode() {
					r.session.ExitPlanMode("disabled from /settings", nil)
				} else {
					r.session.EnterPlanMode()
				}
			},
		},
		{
			Label:       "Thinking level",
			Description: "Cycle reasoning depth",
			Value: func() string {
				if r.session == nil {
					return "unknown"
				}
				return string(r.session.GetThinkingLevel())
			},
			Apply: func() {
				if r.session != nil {
					r.session.CycleThinkingLevel()
				}
			},
		},
		{
			Label:       "Worktree mode",
			Description: "Enter or exit the session worktree",
			Value: func() string {
				if r.session != nil && r.session.ActiveWorktree() != "" {
					return "on"
				}
				return "off"
			},
			Apply: func() {
				if r.session == nil {
					return
				}
				var err error
				if r.session.ActiveWorktree() != "" {
					err = r.session.ExitWorktree()
				} else {
					_, err = r.session.EnterWorktree()
				}
				if err != nil {
					r.model.errMsg = err.Error()
				}
			},
		},
	}
}

func (r *goTUIRoot) settingsSelectKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.closeSettingsSelect("settings unchanged") }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.closeSettingsSelect("settings closed") }),
		gotui.OnStop(gotui.KeyUp, func(ke gotui.KeyEvent) { r.moveSettingsSelect(-1) }),
		gotui.OnStop(gotui.KeyDown, func(ke gotui.KeyEvent) { r.moveSettingsSelect(1) }),
		gotui.OnStop(gotui.KeyPageUp, func(ke gotui.KeyEvent) { r.pageSettingsSelect(-settingsSelectVisibleRows) }),
		gotui.OnStop(gotui.KeyPageDown, func(ke gotui.KeyEvent) { r.pageSettingsSelect(settingsSelectVisibleRows) }),
		gotui.OnStop(gotui.KeyHome, func(ke gotui.KeyEvent) { r.jumpSettingsSelect(0) }),
		gotui.OnStop(gotui.KeyEnd, func(ke gotui.KeyEvent) { r.jumpSettingsSelect(len(r.settingsChoices) - 1) }),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.applySettingsSelect() }),
	}
}

func (r *goTUIRoot) moveSettingsSelect(delta int) {
	if len(r.settingsChoices) == 0 {
		return
	}
	r.settingsSelectIdx = (r.settingsSelectIdx + delta + len(r.settingsChoices)) % len(r.settingsChoices)
	r.bump()
}

func (r *goTUIRoot) pageSettingsSelect(delta int) {
	if len(r.settingsChoices) == 0 {
		return
	}
	r.settingsSelectIdx = clampInt(r.settingsSelectIdx+delta, 0, len(r.settingsChoices)-1)
	r.bump()
}

func (r *goTUIRoot) jumpSettingsSelect(idx int) {
	if len(r.settingsChoices) == 0 {
		return
	}
	r.settingsSelectIdx = clampInt(idx, 0, len(r.settingsChoices)-1)
	r.bump()
}

func (r *goTUIRoot) applySettingsSelect() {
	if len(r.settingsChoices) == 0 || r.settingsSelectIdx >= len(r.settingsChoices) {
		return
	}
	r.settingsChoices[r.settingsSelectIdx].Apply()
	r.settingsChoices = r.buildSettingsChoices()
	r.model.setTransientStatus("setting updated")
	r.bump()
}

func (r *goTUIRoot) closeSettingsSelect(status string) {
	r.model.state = uiStateInput
	r.settingsChoices = nil
	r.settingsSelectIdx = 0
	r.model.setTransientStatus(status)
	r.bump()
}

func (r *goTUIRoot) renderSettingsSelectWidget() *gotui.Element {
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)
	container.AddChild(gotui.New(
		gotui.WithText(selectorHeaderLine(selectorHeaderOptions{
			Title:    "Settings",
			Selected: r.settingsSelectIdx,
			Visible:  len(r.settingsChoices),
			Total:    len(r.settingsChoices),
		})),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))

	end := settingsSelectVisibleRows
	if end > len(r.settingsChoices) {
		end = len(r.settingsChoices)
	}
	for i := 0; i < end; i++ {
		choice := r.settingsChoices[i]
		selected := i == r.settingsSelectIdx
		prefix := "  "
		if selected {
			prefix = "❯ "
		}
		line := fmt.Sprintf("%s%-16s %s  %s", prefix, choice.Label, choice.Value(), choice.Description)
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
		gotui.WithText("  ↑/↓ select  pgup/pgdn page  enter change  esc close"),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	return container
}
