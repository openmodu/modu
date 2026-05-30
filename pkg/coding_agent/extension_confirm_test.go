package coding_agent

import "testing"

func TestExtensionPromptsConfirm(t *testing.T) {
	var e extensionPrompts
	// No callback -> returns the caller default.
	if !e.confirm("t", "b", true) {
		t.Fatal("expected default true when no callback")
	}
	if e.confirm("t", "b", false) {
		t.Fatal("expected default false when no callback")
	}

	e.setConfirm(func(title, body string, defaultYes bool) bool { return title == "yes" })
	if !e.confirm("yes", "b", false) {
		t.Fatal("callback should decide true")
	}
	if e.confirm("no", "b", true) {
		t.Fatal("callback should decide false")
	}
}

func TestExtensionPromptsSelect(t *testing.T) {
	var e extensionPrompts
	if got := e.selectOption("t", nil); got != "" {
		t.Fatalf("empty options -> empty, got %q", got)
	}
	// No callback -> first option.
	if got := e.selectOption("t", []string{"a", "b"}); got != "a" {
		t.Fatalf("expected first option without callback, got %q", got)
	}

	e.setSelect(func(title string, options []string) string { return "b" })
	if got := e.selectOption("t", []string{"a", "b"}); got != "b" {
		t.Fatalf("expected callback choice 'b', got %q", got)
	}

	// Callback returning a value not in options falls back to the first option.
	e.setSelect(func(title string, options []string) string { return "zzz" })
	if got := e.selectOption("t", []string{"a", "b"}); got != "a" {
		t.Fatalf("invalid choice should fall back to first option, got %q", got)
	}
}
