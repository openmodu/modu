package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultCompactionUserAnchorBudget(t *testing.T) {
	cfg := Default()
	if cfg.CompactionSettings.PreserveUserMessagesTokens != 1024 {
		t.Fatalf("expected default user anchor budget 1024, got %d", cfg.CompactionSettings.PreserveUserMessagesTokens)
	}
}

func TestLoadPreservesCompactionUserAnchorBudgetWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".coding_agent")
	cwd := filepath.Join(dir, "repo")
	settingsDir := filepath.Join(cwd, ".coding_agent")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{
  "compactionSettings": {
    "preserveRecentMessages": 2
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(agentDir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CompactionSettings.PreserveRecentMessages != 2 {
		t.Fatalf("expected project preserveRecentMessages override, got %d", cfg.CompactionSettings.PreserveRecentMessages)
	}
	if cfg.CompactionSettings.PreserveUserMessagesTokens != 1024 {
		t.Fatalf("expected omitted user anchor budget to preserve default 1024, got %d", cfg.CompactionSettings.PreserveUserMessagesTokens)
	}
}
