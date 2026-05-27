package extension

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// register a couple of stubs for config_test scenarios. Tests reset the
// registry so the helper can re-seed it consistently.
func seedRegistry(t *testing.T, stubs ...string) {
	t.Helper()
	resetRegistryForTest()
	for _, name := range stubs {
		Register(name, func() Extension { return &configurableStub{stubExt: stubExt{name: name}} })
	}
}

func writeConfigFile(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "extensions.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadEnabledNoFileFallsBackToAllBuiltins(t *testing.T) {
	seedRegistry(t, "alpha", "beta")
	t.Cleanup(resetRegistryForTest)

	var stderr bytes.Buffer
	got, err := LoadEnabled(LoadOptions{
		ConfigPath: filepath.Join(t.TempDir(), "nope.yaml"),
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d exts, want 2", len(got))
	}
	// Lexicographic order: alpha before beta.
	if got[0].Name() != "alpha" || got[1].Name() != "beta" {
		t.Errorf("default order wrong: %v %v", got[0].Name(), got[1].Name())
	}
	if stderr.Len() != 0 {
		t.Errorf("expected no stderr warnings, got: %s", stderr.String())
	}
}

func TestLoadEnabledEmptyFileFallsBackToAllBuiltins(t *testing.T) {
	seedRegistry(t, "alpha")
	t.Cleanup(resetRegistryForTest)

	dir := t.TempDir()
	path := writeConfigFile(t, dir, "")

	got, err := LoadEnabled(LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	if len(got) != 1 || got[0].Name() != "alpha" {
		t.Errorf("expected single alpha builtin, got %v", got)
	}
}

func TestLoadEnabledRespectsExplicitDisable(t *testing.T) {
	seedRegistry(t, "alpha", "beta")
	t.Cleanup(resetRegistryForTest)

	dir := t.TempDir()
	body := `
extensions:
  - name: alpha
    enabled: false
  - name: beta
`
	path := writeConfigFile(t, dir, body)

	got, err := LoadEnabled(LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	if len(got) != 1 || got[0].Name() != "beta" {
		t.Errorf("expected only beta enabled, got %v", got)
	}
}

func TestLoadEnabledUnknownNameWarnsButContinues(t *testing.T) {
	seedRegistry(t, "alpha")
	t.Cleanup(resetRegistryForTest)

	dir := t.TempDir()
	body := `
extensions:
  - name: ghost
  - name: alpha
`
	path := writeConfigFile(t, dir, body)

	var stderr bytes.Buffer
	got, err := LoadEnabled(LoadOptions{ConfigPath: path, Stderr: &stderr})
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	if len(got) != 1 || got[0].Name() != "alpha" {
		t.Errorf("expected alpha only, got %v", got)
	}
	if !strings.Contains(stderr.String(), `"ghost"`) || !strings.Contains(stderr.String(), "not registered") {
		t.Errorf("expected unknown-name warning, got: %s", stderr.String())
	}
}

func TestLoadEnabledDuplicateNameWarnsKeepsFirst(t *testing.T) {
	seedRegistry(t, "alpha")
	t.Cleanup(resetRegistryForTest)

	dir := t.TempDir()
	body := `
extensions:
  - name: alpha
    config:
      first: true
  - name: alpha
    config:
      second: true
`
	path := writeConfigFile(t, dir, body)

	var stderr bytes.Buffer
	got, err := LoadEnabled(LoadOptions{ConfigPath: path, Stderr: &stderr})
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected single ext, got %d", len(got))
	}
	if !strings.Contains(stderr.String(), "listed twice") {
		t.Errorf("expected duplicate warning, got: %s", stderr.String())
	}
	// First occurrence wins → first:true should have been applied.
	cs, ok := got[0].(*configurableStub)
	if !ok {
		t.Fatalf("ext is not configurableStub: %T", got[0])
	}
	if cs.cfg["first"] != true {
		t.Errorf("expected first-occurrence config to win, got: %v", cs.cfg)
	}
}

func TestLoadEnabledPreservesFileOrder(t *testing.T) {
	seedRegistry(t, "alpha", "beta", "gamma")
	t.Cleanup(resetRegistryForTest)

	dir := t.TempDir()
	// File order intentionally non-lexicographic.
	body := `
extensions:
  - name: gamma
  - name: alpha
  - name: beta
`
	path := writeConfigFile(t, dir, body)

	got, err := LoadEnabled(LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	want := []string{"gamma", "alpha", "beta"}
	for i, n := range want {
		if got[i].Name() != n {
			t.Errorf("position %d: got %q want %q", i, got[i].Name(), n)
		}
	}
}

func TestLoadEnabledApplyConfigCalled(t *testing.T) {
	seedRegistry(t, "alpha")
	t.Cleanup(resetRegistryForTest)

	dir := t.TempDir()
	body := `
extensions:
  - name: alpha
    config:
      mode: fast
      retries: 3
`
	path := writeConfigFile(t, dir, body)

	got, err := LoadEnabled(LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	cs, ok := got[0].(*configurableStub)
	if !ok {
		t.Fatalf("ext is not configurableStub: %T", got[0])
	}
	if cs.cfg["mode"] != "fast" {
		t.Errorf("mode not delivered: %v", cs.cfg)
	}
	if v, _ := cs.cfg["retries"].(int); v != 3 {
		t.Errorf("retries not delivered as int: got %#v", cs.cfg["retries"])
	}
}

func TestLoadEnabledApplyConfigErrorPropagates(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)
	failing := errors.New("schema bad")
	Register("alpha", func() Extension {
		return &configurableStub{stubExt: stubExt{name: "alpha"}, applyErr: failing}
	})

	dir := t.TempDir()
	body := `
extensions:
  - name: alpha
    config: { x: 1 }
`
	path := writeConfigFile(t, dir, body)

	_, err := LoadEnabled(LoadOptions{ConfigPath: path})
	if err == nil {
		t.Fatalf("expected error from ApplyConfig, got nil")
	}
	if !errors.Is(err, failing) {
		t.Errorf("error chain missing root cause: %v", err)
	}
}

func TestLoadEnabledNonConfigurableSilentlyIgnoresConfig(t *testing.T) {
	resetRegistryForTest()
	t.Cleanup(resetRegistryForTest)
	// Plain stubExt — does NOT implement ConfigurableExtension.
	Register("plain", func() Extension { return &stubExt{name: "plain"} })

	dir := t.TempDir()
	body := `
extensions:
  - name: plain
    config: { whatever: 1 }
`
	path := writeConfigFile(t, dir, body)

	got, err := LoadEnabled(LoadOptions{ConfigPath: path})
	if err != nil {
		t.Fatalf("LoadEnabled: %v", err)
	}
	if len(got) != 1 || got[0].Name() != "plain" {
		t.Errorf("expected plain ext to be loaded, got %v", got)
	}
}

func TestLoadEnabledMalformedYAMLErrors(t *testing.T) {
	seedRegistry(t, "alpha")
	t.Cleanup(resetRegistryForTest)

	dir := t.TempDir()
	path := writeConfigFile(t, dir, "extensions: [this is not valid yaml")

	_, err := LoadEnabled(LoadOptions{ConfigPath: path})
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention parse: %v", err)
	}
}
