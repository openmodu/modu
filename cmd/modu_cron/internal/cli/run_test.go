package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

// unsetAllProviders clears every env var the provider resolver looks at so
// Run reliably hits the "no provider configured" branch.
func unsetAllProviders(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL",
		"OPENAI_API_KEY", "OPENAI_MODEL", "OPENAI_BASE_URL",
		"DEEPSEEK_API_KEY", "DEEPSEEK_MODEL",
		"OLLAMA_HOST", "OLLAMA_MODEL",
		"LMSTUDIO_BASE_URL", "LMSTUDIO_MODEL",
	} {
		t.Setenv(k, "")
	}
}

func TestRunMissingTaskIDErrors(t *testing.T) {
	err := Run(context.Background(), filepath.Join(t.TempDir(), "x.yaml"), "", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "task id required") {
		t.Errorf("expected 'task id required', got: %v", err)
	}
}

func TestRunUnknownTaskErrors(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(cfgPath, &config.Config{Tasks: []config.Task{
		{ID: "real", Cron: "* * * * * *", Prompt: "p", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), cfgPath, "ghost", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found', got: %v", err)
	}
}

func TestRunNoProviderErrors(t *testing.T) {
	unsetAllProviders(t)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(cfgPath, &config.Config{Tasks: []config.Task{
		{ID: "demo", Cron: "* * * * * *", Prompt: "p", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), cfgPath, "demo", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no provider configured") {
		t.Errorf("expected 'no provider configured', got: %v", err)
	}
}
