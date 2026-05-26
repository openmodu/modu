package extension

import (
	"testing"
)

// stubExt is a tiny extension used by registry + config tests.
type stubExt struct {
	name string
	cfg  map[string]any
}

func (s *stubExt) Name() string         { return s.name }
func (s *stubExt) Init(ExtensionAPI) error { return nil }

// configurableStub also implements ConfigurableExtension so config_test can
// verify the per-ext config map is delivered before Init.
type configurableStub struct {
	stubExt
	applyErr error
}

func (c *configurableStub) ApplyConfig(cfg map[string]any) error {
	c.cfg = cfg
	return c.applyErr
}

func TestRegisterAndFactoryFor(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register("a", func() Extension { return &stubExt{name: "a"} })
	Register("b", func() Extension { return &stubExt{name: "b"} })

	if got, ok := FactoryFor("a"); !ok || got().Name() != "a" {
		t.Errorf("FactoryFor(a) failed: got=%v ok=%v", got, ok)
	}
	if _, ok := FactoryFor("missing"); ok {
		t.Errorf("FactoryFor(missing) should be false")
	}
}

func TestBuiltinNamesSorted(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register("zeta", func() Extension { return &stubExt{name: "zeta"} })
	Register("alpha", func() Extension { return &stubExt{name: "alpha"} })
	Register("mu", func() Extension { return &stubExt{name: "mu"} })

	got := BuiltinNames()
	want := []string{"alpha", "mu", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("got %d names, want %d", len(got), len(want))
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("BuiltinNames[%d]=%q, want %q", i, got[i], n)
		}
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	Register("x", func() Extension { return &stubExt{name: "x"} })
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on duplicate Register, got nothing")
		}
	}()
	Register("x", func() Extension { return &stubExt{name: "x"} })
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on empty name")
		}
	}()
	Register("", func() Extension { return &stubExt{} })
}

func TestRegisterNilFactoryPanics(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil factory")
		}
	}()
	Register("y", nil)
}
