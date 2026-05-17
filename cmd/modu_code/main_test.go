package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunConfigCommandExample(t *testing.T) {
	var out bytes.Buffer
	if err := runConfigCommand([]string{"example"}, &out, nil); err != nil {
		t.Fatalf("runConfigCommand example: %v", err)
	}
	if !strings.Contains(out.String(), `"models"`) || !strings.Contains(out.String(), `"active": "local-qwen"`) {
		t.Fatalf("unexpected example output:\n%s", out.String())
	}
}

func TestRunConfigCommandInitAndValidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var initOut bytes.Buffer
	if err := runConfigCommand([]string{"init"}, &initOut, nil); err != nil {
		t.Fatalf("runConfigCommand init: %v", err)
	}
	path := filepath.Join(home, ".coding_agent", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file: %v", err)
	}
	if !strings.Contains(initOut.String(), path) {
		t.Fatalf("expected init output to include path, got %q", initOut.String())
	}

	var validateOut bytes.Buffer
	if err := runConfigCommand([]string{"validate"}, &validateOut, nil); err != nil {
		t.Fatalf("runConfigCommand validate: %v\n%s", err, validateOut.String())
	}
	if !strings.Contains(validateOut.String(), "status: ok") {
		t.Fatalf("expected validate ok, got:\n%s", validateOut.String())
	}
}

func TestRunConfigCommandValidateFailsInvalidConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".coding_agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"models":[{"provider":"","model":"","baseUrl":""}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runConfigCommand([]string{"validate"}, &out, nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(out.String(), "problems") {
		t.Fatalf("expected problems output, got:\n%s", out.String())
	}
}
