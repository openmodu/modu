package tui

import (
	"fmt"
	"strings"
	"time"

	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/types"
)

const (
	transientStatusTTL   = 3 * time.Second
	transientActivityTTL = 8 * time.Second
)

func (m *uiModel) setStatus(msg string) {
	m.statusMsg = msg
	m.statusExpiresAt = time.Time{}
	m.statusExpiresText = ""
}

func (m *uiModel) setTransientStatus(msg string) {
	m.statusMsg = msg
	if msg == "" {
		m.statusExpiresAt = time.Time{}
		m.statusExpiresText = ""
		return
	}
	m.statusExpiresAt = time.Now().Add(transientStatusTTL)
	m.statusExpiresText = msg
}

func (m *uiModel) effectiveStatusMsg(now time.Time) string {
	if m.statusMsg == "" {
		m.statusExpiresAt = time.Time{}
		m.statusExpiresText = ""
		return ""
	}
	if m.statusExpiresAt.IsZero() {
		return m.statusMsg
	}
	if m.statusMsg != m.statusExpiresText {
		m.statusExpiresAt = time.Time{}
		m.statusExpiresText = ""
		return m.statusMsg
	}
	if now.Before(m.statusExpiresAt) {
		return m.statusMsg
	}
	m.statusMsg = ""
	m.statusExpiresAt = time.Time{}
	m.statusExpiresText = ""
	return ""
}

func (m *uiModel) clearActivity() {
	m.lastActivity = ""
	m.activityExpiresAt = time.Time{}
	m.activityExpiresText = ""
}

func (m *uiModel) setTransientActivity(activity string) {
	m.lastActivity = activity
	if activity == "" {
		m.activityExpiresAt = time.Time{}
		m.activityExpiresText = ""
		return
	}
	m.activityExpiresAt = time.Now().Add(transientActivityTTL)
	m.activityExpiresText = activity
}

func (m *uiModel) effectiveLastActivity(now time.Time) string {
	if strings.TrimSpace(stripANSIForGoTUI(m.lastActivity)) == "" {
		m.clearActivity()
		return ""
	}
	if m.activityExpiresAt.IsZero() {
		return m.lastActivity
	}
	if m.lastActivity != m.activityExpiresText {
		m.activityExpiresAt = time.Time{}
		m.activityExpiresText = ""
		return m.lastActivity
	}
	if now.Before(m.activityExpiresAt) {
		return m.lastActivity
	}
	m.clearActivity()
	return ""
}

// bottomLine returns the text and style for the single hint row below the
// input box. It surfaces the working indicator while a query is in flight,
// the latest error/status message when one is set, or a static set of key
// hints when idle.
func (r *goTUIRoot) bottomLine() (string, gotui.Style) {
	if r.model.errMsg != "" {
		return "! " + r.model.errMsg, gotui.NewStyle().Foreground(gotui.Red)
	}
	if status := r.model.effectiveStatusMsg(time.Now()); status != "" && status != "thinking" {
		if queue := r.queueStatusLine(); queue != "" {
			status += "  |  " + queue
		}
		return status, gotui.NewStyle().Dim()
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
	if goal := goalStatusPart(r.session.ExtensionRuntimeStates()); goal != "" {
		parts = append(parts, goal)
	}
	if queue := r.queueStatusLine(); queue != "" {
		parts = append(parts, queue)
	}
	return parts
}

func goalStatusPart(states map[string]any) string {
	if len(states) == 0 {
		return ""
	}
	raw, ok := states["goal"]
	if !ok {
		return ""
	}
	state, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if indicator, ok := state["indicator"].(string); ok && strings.TrimSpace(indicator) != "" {
		return indicator
	}
	status, _ := state["status"].(string)
	if status == "" {
		return ""
	}
	switch status {
	case "active":
		return "goal"
	case "paused":
		return "goal paused"
	case "budgetLimited":
		return "goal limited"
	case "complete":
		return "goal done"
	default:
		return "goal " + status
	}
}

func (r *goTUIRoot) queueStatusLine() string {
	if r.session == nil || r.session.GetAgent() == nil {
		return ""
	}
	steering, followUp := r.session.GetAgent().QueuedMessageCounts()
	var parts []string
	if steering > 0 {
		parts = append(parts, fmt.Sprintf("steer %d", steering))
	}
	if followUp > 0 {
		parts = append(parts, fmt.Sprintf("follow-up %d", followUp))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
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
		return "Working (Enter follow-up, Shift+Enter or /s steer, esc interrupt)", true
	}
	if activity := r.model.effectiveLastActivity(time.Now()); strings.TrimSpace(stripANSIForGoTUI(activity)) != "" {
		return strings.TrimSpace(stripANSIForGoTUI(activity)), true
	}
	return "", false
}
