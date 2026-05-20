package tui

import (
	"fmt"
	"strings"

	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/types"
)

// bottomLine returns the text and style for the single hint row below the
// input box. It surfaces the working indicator while a query is in flight,
// the latest error/status message when one is set, or a static set of key
// hints when idle.
func (r *goTUIRoot) bottomLine() (string, gotui.Style) {
	if r.model.errMsg != "" {
		return "! " + r.model.errMsg, gotui.NewStyle().Foreground(gotui.Red)
	}
	if r.model.statusMsg != "" && r.model.statusMsg != "thinking" {
		return r.model.statusMsg, gotui.NewStyle().Dim()
	}
	style := gotui.NewStyle().Dim()
	if r.session != nil && r.session.IsPlanMode() {
		style = gotui.NewStyle().Foreground(gotui.Yellow)
	}
	return r.idleStatusLine(), style
}

func (r *goTUIRoot) idleStatusLine() string {
	hints := "ctrl+j newline  shift+tab plan  /help  ctrl+c exit"
	parts := r.sessionStatusParts()
	if len(parts) == 0 {
		return hints
	}
	return strings.Join(parts, "  ") + "  |  " + hints
}

func (r *goTUIRoot) sessionStatusParts() []string {
	var parts []string
	if model := r.currentModelForStatus(); model != nil {
		label := model.ID
		if model.ProviderID != "" {
			label = model.ProviderID + "/" + label
		}
		parts = append(parts, "model "+label)
	}
	if r.session == nil {
		return parts
	}
	stats := r.session.GetSessionStats()
	if stats.TotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("~%d tokens", stats.TotalTokens))
	}
	if r.session.IsPlanMode() {
		parts = append(parts, "plan")
	}
	if r.session.ActiveWorktree() != "" {
		parts = append(parts, "worktree")
	}
	return parts
}

func (r *goTUIRoot) currentModelForStatus() *types.Model {
	if r.session != nil {
		if model := r.session.GetModel(); model != nil {
			return model
		}
	}
	if r.modelInfo != nil {
		return r.modelInfo
	}
	if r.model != nil {
		return r.model.model
	}
	return nil
}

func (r *goTUIRoot) activityLine() (string, bool) {
	if r.model.state == uiStateQuerying {
		if activity := r.model.renderActivityLine(); strings.TrimSpace(stripANSIForGoTUI(activity)) != "" {
			return strings.TrimSpace(stripANSIForGoTUI(activity)), true
		}
		return "Working (esc to interrupt)", true
	}
	if strings.TrimSpace(stripANSIForGoTUI(r.model.lastActivity)) != "" {
		return strings.TrimSpace(stripANSIForGoTUI(r.model.lastActivity)), true
	}
	return "", false
}
