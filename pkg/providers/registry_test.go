package providers

import (
	"context"
	"testing"
)

// stubProvider is a minimal Provider implementation for testing.
type stubProvider struct{ id string }

func (s *stubProvider) ID() string                                            { return s.id }
func (s *stubProvider) Chat(_ context.Context, _ *ChatRequest) (*ChatResponse, error) { return nil, nil }
func (s *stubProvider) Stream(_ context.Context, _ *ChatRequest) (Stream, error)      { return nil, nil }

// resetRegistry clears the global registry between tests.
func resetRegistry() {
	registryMu.Lock()
	registry = map[string]Provider{}
	registryMu.Unlock()
}

func TestRegisterAndGet(t *testing.T) {
	resetRegistry()

	p := &stubProvider{id: "test"}
	Register(p)

	got, ok := Get("test")
	if !ok {
		t.Fatal("expected provider to be found")
	}
	if got.ID() != "test" {
		t.Fatalf("got ID %q, want %q", got.ID(), "test")
	}
}

func TestGetMissing(t *testing.T) {
	resetRegistry()

	_, ok := Get("nonexistent")
	if ok {
		t.Fatal("expected Get to return false for unregistered ID")
	}
}

func TestRegisterOverwrite(t *testing.T) {
	resetRegistry()

	Register(&stubProvider{id: "dup"})
	p2 := &stubProvider{id: "dup"}
	Register(p2)

	got, ok := Get("dup")
	if !ok {
		t.Fatal("expected provider to be found")
	}
	if got != p2 {
		t.Fatal("expected second registration to overwrite the first")
	}
}

func TestList(t *testing.T) {
	resetRegistry()

	Register(&stubProvider{id: "a"})
	Register(&stubProvider{id: "b"})
	Register(&stubProvider{id: "c"})

	list := List()
	if len(list) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(list))
	}

	ids := map[string]bool{}
	for _, p := range list {
		ids[p.ID()] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !ids[want] {
			t.Errorf("provider %q not found in List()", want)
		}
	}
}

func TestListEmpty(t *testing.T) {
	resetRegistry()

	if list := List(); len(list) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(list))
	}
}
