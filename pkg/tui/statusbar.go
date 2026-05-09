package tui

import (
	"strings"

	gotui "github.com/grindlemire/go-tui"
)

// bottomLine returns the text and style for the single hint row below the
// input box. It surfaces the working indicator while a query is in flight,
// the latest error/status message when one is set, or a static set of key
// hints when idle.
func (r *goTUIRoot) bottomLine() (string, gotui.Style) {
	if r.model.state == uiStateQuerying {
		if activity := r.model.renderActivityLine(); strings.TrimSpace(stripANSIForGoTUI(activity)) != "" {
			return strings.TrimSpace(stripANSIForGoTUI(activity)), gotui.NewStyle().Dim()
		}
		return "working...", gotui.NewStyle().Dim()
	}
	if r.model.errMsg != "" {
		return "! " + r.model.errMsg, gotui.NewStyle().Foreground(gotui.Red)
	}
	if r.model.statusMsg != "" && r.model.statusMsg != "thinking" {
		return r.model.statusMsg, gotui.NewStyle().Dim()
	}
	return "ctrl+j new line  /help  ctrl+c exit", gotui.NewStyle().Dim()
}
