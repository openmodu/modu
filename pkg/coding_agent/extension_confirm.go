package coding_agent

import (
	"strings"
	"sync"

	"github.com/openmodu/modu/pkg/types"
)

// extensionPrompts is a self-contained registry of the host's interactive
// confirm/select callbacks. It has no dependency on the session — the host
// wires callbacks in, extensions ask through it.
type extensionPrompts struct {
	mu        sync.RWMutex
	confirmCb func(title, body string, defaultYes bool) bool
	selectCb  func(title string, options []string) string
}

// confirm asks the host UI for a yes/no decision. With no callback (headless /
// --no-approve) the caller-provided default is returned.
func (e *extensionPrompts) confirm(title, body string, defaultYes bool) bool {
	e.mu.RLock()
	cb := e.confirmCb
	e.mu.RUnlock()
	if cb == nil {
		return defaultYes
	}
	return cb(title, body, defaultYes)
}

func (e *extensionPrompts) setConfirm(fn func(title, body string, defaultYes bool) bool) {
	e.mu.Lock()
	e.confirmCb = fn
	e.mu.Unlock()
}

func (e *extensionPrompts) selectOption(title string, options []string) string {
	e.mu.RLock()
	cb := e.selectCb
	e.mu.RUnlock()
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

func (e *extensionPrompts) setSelect(fn func(title string, options []string) string) {
	e.mu.Lock()
	e.selectCb = fn
	e.mu.Unlock()
}

// --- CodingSession delegates (preserve the public API surface) ---

func (s *engine) requestExtensionConfirm(title, body string, defaultYes bool) bool {
	return s.extPrompts.confirm(title, body, defaultYes)
}

// SetExtensionConfirmCallback wires interactive extension confirmation prompts.
// The callback returns true for yes/allow and false for no/deny.
func (s *engine) SetExtensionConfirmCallback(fn func(title, body string, defaultYes bool) bool) {
	s.extPrompts.setConfirm(fn)
}

func (s *engine) requestExtensionSelect(title string, options []string) string {
	return s.extPrompts.selectOption(title, options)
}

// SetExtensionSelectCallback wires interactive extension choice prompts.
func (s *engine) SetExtensionSelectCallback(fn func(title string, options []string) string) {
	s.extPrompts.setSelect(fn)
}

// EmitExtensionEvent dispatches a host lifecycle event to registered
// extensions. It is intentionally narrow: callers should use it for host/UI
// readiness boundaries, not for synthetic agent transcript events.
func (s *engine) EmitExtensionEvent(eventType string) {
	if s == nil || s.extensions == nil || strings.TrimSpace(eventType) == "" {
		return
	}
	s.extensions.EmitEvent(types.Event{Type: types.EventType(eventType)})
}
