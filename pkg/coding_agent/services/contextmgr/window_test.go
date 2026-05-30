package contextmgr

import (
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestWindowForUsesModelContextWindow(t *testing.T) {
	if got := WindowFor(&types.Model{ContextWindow: 32000}); got != 32000 {
		t.Fatalf("expected model context window 32000, got %d", got)
	}
}

func TestWindowForFallsBackToDefault(t *testing.T) {
	if got := WindowFor(nil); got != defaultContextWindow {
		t.Fatalf("expected default window %d for nil model, got %d", defaultContextWindow, got)
	}
	if got := WindowFor(&types.Model{}); got != defaultContextWindow {
		t.Fatalf("expected default window %d when unset, got %d", defaultContextWindow, got)
	}
}

func TestTokenAccountingAndReset(t *testing.T) {
	m := New(Deps{})
	m.AddUsage(120)
	m.AddUsage(80)
	if m.Tokens() != 200 {
		t.Fatalf("expected 200 accumulated tokens, got %d", m.Tokens())
	}
}
