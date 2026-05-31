package tui

import (
	"strings"
	"time"
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

// goalWatchIndicator extracts the goal indicator string from the
// extension RuntimeState map, but only when the goal extension has opted
// the host UI in via /goal-watch (state["watching"] == true). Returns ""
// when the extension is absent, watching is off, or the indicator field
// is missing — never panics on a malformed state map.
func goalWatchIndicator(states map[string]any) string {
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
	watching, _ := state["watching"].(bool)
	if !watching {
		return ""
	}
	indicator, _ := state["indicator"].(string)
	return strings.TrimSpace(indicator)
}
