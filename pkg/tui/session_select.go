package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	gotui "github.com/grindlemire/go-tui"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
)

const sessionSelectVisibleRows = 10

func (r *goTUIRoot) openSessionSelect(all bool) {
	if r.session == nil {
		r.model.statusMsg = "no session"
		r.bump()
		return
	}
	var (
		choices []coding_agent.SessionInfo
		err     error
	)
	if all {
		choices, err = r.session.ListAllSessionInfos()
	} else {
		choices, err = r.session.ListSessionInfos()
	}
	if err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	if len(choices) == 0 {
		r.model.statusMsg = "no sessions found"
		r.bump()
		return
	}
	r.sessionChoices = choices
	r.sessionSelectIdx = currentSessionChoiceIndex(choices, r.session.GetSessionFile())
	r.adjustSessionSelectScroll()
	r.model.state = uiStateSessionSelect
	r.model.statusMsg = ""
	r.slashMatches = nil
	r.bump()
}

func currentSessionChoiceIndex(choices []coding_agent.SessionInfo, current string) int {
	currentAbs, _ := filepath.Abs(current)
	for i, choice := range choices {
		choiceAbs, _ := filepath.Abs(choice.Path)
		if choice.Path == current || (currentAbs != "" && choiceAbs == currentAbs) {
			return i
		}
	}
	return 0
}

func (r *goTUIRoot) sessionSelectKeyMap() gotui.KeyMap {
	return gotui.KeyMap{
		gotui.OnStop(gotui.KeyCtrlC, func(ke gotui.KeyEvent) { r.closeSessionSelect("session unchanged") }),
		gotui.OnStop(gotui.KeyEscape, func(ke gotui.KeyEvent) { r.closeSessionSelect("session unchanged") }),
		gotui.OnStop(gotui.KeyUp, func(ke gotui.KeyEvent) { r.moveSessionSelect(-1) }),
		gotui.OnStop(gotui.KeyDown, func(ke gotui.KeyEvent) { r.moveSessionSelect(1) }),
		gotui.OnStop(gotui.KeyHome, func(ke gotui.KeyEvent) { r.jumpSessionSelect(0) }),
		gotui.OnStop(gotui.KeyEnd, func(ke gotui.KeyEvent) { r.jumpSessionSelect(len(r.sessionChoices) - 1) }),
		gotui.OnStop(gotui.KeyEnter, func(ke gotui.KeyEvent) { r.confirmSessionSelect() }),
		gotui.OnStop(gotui.Rune('r'), func(ke gotui.KeyEvent) { r.confirmSessionSelect() }),
		gotui.OnStop(gotui.Rune('f'), func(ke gotui.KeyEvent) { r.forkSessionSelect() }),
		gotui.OnStop(gotui.Rune('d'), func(ke gotui.KeyEvent) { r.deleteSessionSelect() }),
	}
}

func (r *goTUIRoot) moveSessionSelect(delta int) {
	if len(r.sessionChoices) == 0 {
		return
	}
	r.sessionSelectIdx = (r.sessionSelectIdx + delta + len(r.sessionChoices)) % len(r.sessionChoices)
	r.adjustSessionSelectScroll()
	r.bump()
}

func (r *goTUIRoot) jumpSessionSelect(idx int) {
	if len(r.sessionChoices) == 0 {
		return
	}
	r.sessionSelectIdx = clampInt(idx, 0, len(r.sessionChoices)-1)
	r.adjustSessionSelectScroll()
	r.bump()
}

func (r *goTUIRoot) adjustSessionSelectScroll() {
	if len(r.sessionChoices) <= sessionSelectVisibleRows {
		r.sessionSelectScroll = 0
		return
	}
	if r.sessionSelectIdx < r.sessionSelectScroll {
		r.sessionSelectScroll = r.sessionSelectIdx
	} else if r.sessionSelectIdx >= r.sessionSelectScroll+sessionSelectVisibleRows {
		r.sessionSelectScroll = r.sessionSelectIdx - sessionSelectVisibleRows + 1
	}
	if r.sessionSelectScroll < 0 {
		r.sessionSelectScroll = 0
	}
	if maxOffset := len(r.sessionChoices) - sessionSelectVisibleRows; r.sessionSelectScroll > maxOffset {
		r.sessionSelectScroll = maxOffset
	}
}

func (r *goTUIRoot) selectedSessionChoice() (coding_agent.SessionInfo, bool) {
	if len(r.sessionChoices) == 0 || r.sessionSelectIdx < 0 || r.sessionSelectIdx >= len(r.sessionChoices) {
		return coding_agent.SessionInfo{}, false
	}
	return r.sessionChoices[r.sessionSelectIdx], true
}

func (r *goTUIRoot) confirmSessionSelect() {
	choice, ok := r.selectedSessionChoice()
	if r.session == nil || !ok {
		r.closeSessionSelect("session unchanged")
		return
	}
	if err := r.session.SwitchSession(choice.Path); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	r.closeSessionSelect("resumed session")
}

func (r *goTUIRoot) forkSessionSelect() {
	choice, ok := r.selectedSessionChoice()
	if r.session == nil || !ok {
		r.closeSessionSelect("session unchanged")
		return
	}
	if err := r.session.ForkFromSession(choice.Path); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	r.closeSessionSelect("forked session")
}

func (r *goTUIRoot) deleteSessionSelect() {
	choice, ok := r.selectedSessionChoice()
	if r.session == nil || !ok {
		r.closeSessionSelect("session unchanged")
		return
	}
	if err := r.session.DeleteSession(choice.Path); err != nil {
		r.model.errMsg = err.Error()
		r.bump()
		return
	}
	r.sessionChoices = append(r.sessionChoices[:r.sessionSelectIdx], r.sessionChoices[r.sessionSelectIdx+1:]...)
	if len(r.sessionChoices) == 0 {
		r.closeSessionSelect("deleted session")
		return
	}
	if r.sessionSelectIdx >= len(r.sessionChoices) {
		r.sessionSelectIdx = len(r.sessionChoices) - 1
	}
	r.adjustSessionSelectScroll()
	r.model.statusMsg = "deleted session"
	r.bump()
}

func (r *goTUIRoot) closeSessionSelect(status string) {
	r.model.state = uiStateInput
	r.sessionChoices = nil
	r.sessionSelectIdx = 0
	r.sessionSelectScroll = 0
	r.model.statusMsg = status
	r.bump()
}

func (r *goTUIRoot) renderSessionSelectWidget() *gotui.Element {
	container := gotui.New(
		gotui.WithDisplay(gotui.DisplayFlex),
		gotui.WithDirection(gotui.Column),
		gotui.WithFlexShrink(0),
	)
	container.AddChild(gotui.New(
		gotui.WithText("  Select session"),
		gotui.WithTextStyle(gotui.NewStyle().Bold()),
		gotui.WithFlexShrink(0),
	))

	end := r.sessionSelectScroll + sessionSelectVisibleRows
	if end > len(r.sessionChoices) {
		end = len(r.sessionChoices)
	}
	current := ""
	if r.session != nil {
		current = r.session.GetSessionFile()
	}
	for i := r.sessionSelectScroll; i < end; i++ {
		choice := r.sessionChoices[i]
		selected := i == r.sessionSelectIdx
		active := sameSessionPath(choice.Path, current)
		line := sessionChoiceLine(choice, selected, active)
		if i == r.sessionSelectScroll && r.sessionSelectScroll > 0 {
			line += "  ↑"
		} else if i == end-1 && end < len(r.sessionChoices) {
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
		gotui.WithText("  ↑/↓ select  enter resume  f fork  d delete  esc cancel"),
		gotui.WithTextStyle(gotui.NewStyle().Dim()),
		gotui.WithFlexShrink(0),
	))
	return container
}

func sessionChoiceLine(info coding_agent.SessionInfo, selected, active bool) string {
	prefix := "  "
	if selected {
		prefix = "❯ "
	}
	marker := " "
	if active {
		marker = "*"
	}
	label := strings.TrimSpace(info.Name)
	if label == "" {
		label = strings.TrimSpace(info.FirstMessage)
	}
	if label == "" {
		label = filepath.Base(info.Path)
	}
	if len(label) > 54 {
		label = label[:51] + "..."
	}
	return fmt.Sprintf("%s%s %s  messages=%d", prefix, marker, label, info.MessageCount)
}

func sameSessionPath(a, b string) bool {
	if a == b {
		return true
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && aa == bb
}
