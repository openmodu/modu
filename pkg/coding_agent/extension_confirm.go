package coding_agent

import (
	"strings"

	"github.com/openmodu/modu/pkg/agent"
)

// requestExtensionConfirm asks the interactive host UI for a yes/no decision.
// With no callback (headless / --no-approve), the caller-provided default is
// returned so extensions can choose safe behaviour per prompt.
func (s *CodingSession) requestExtensionConfirm(title, body string, defaultYes bool) bool {
	s.extensionMu.RLock()
	cb := s.extensionConfirmCb
	s.extensionMu.RUnlock()
	if cb == nil {
		return defaultYes
	}
	return cb(title, body, defaultYes)
}

// SetExtensionConfirmCallback wires interactive extension confirmation
// prompts. The callback returns true for yes/allow and false for no/deny.
func (s *CodingSession) SetExtensionConfirmCallback(fn func(title, body string, defaultYes bool) bool) {
	s.extensionMu.Lock()
	s.extensionConfirmCb = fn
	s.extensionMu.Unlock()
}

func (s *CodingSession) requestExtensionSelect(title string, options []string) string {
	s.extensionMu.RLock()
	cb := s.extensionSelectCb
	s.extensionMu.RUnlock()
	if len(options) == 0 {
		return ""
	}
	if cb == nil {
		return options[0]
	}
	choice := strings.TrimSpace(cb(title, append([]string(nil), options...)))
	for _, option := range options {
		if choice == option {
			return choice
		}
	}
	return options[0]
}

// SetExtensionSelectCallback wires interactive extension choice prompts.
func (s *CodingSession) SetExtensionSelectCallback(fn func(title string, options []string) string) {
	s.extensionMu.Lock()
	s.extensionSelectCb = fn
	s.extensionMu.Unlock()
}

// EmitExtensionEvent dispatches a host lifecycle event to registered
// extensions. It is intentionally narrow: callers should use it for host/UI
// readiness boundaries, not for synthetic agent transcript events.
func (s *CodingSession) EmitExtensionEvent(eventType string) {
	if s == nil || s.extensions == nil || strings.TrimSpace(eventType) == "" {
		return
	}
	s.extensions.EmitEvent(agent.AgentEvent{Type: agent.EventType(eventType)})
}
