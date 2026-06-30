package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadReadsGlobalConfigTomlSettings(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	cwd := filepath.Join(dir, "repo")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalConfigPath(agentDir), []byte(`version = 2

[settings]
thinkingLevel = "high"
autoCompaction = false
disableWorkflows = true

[settings.features]
memoryTool = false

[settings.permissions]
defaultMode = "auto"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(agentDir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThinkingLevel != "high" {
		t.Fatalf("thinking level = %q, want high", cfg.ThinkingLevel)
	}
	if cfg.AutoCompaction {
		t.Fatal("auto compaction should be false from config.toml [settings]")
	}
	if !cfg.DisableWorkflows {
		t.Fatal("disableWorkflows should be true from config.toml [settings]")
	}
	if cfg.FeatureMemoryTool() {
		t.Fatal("memoryTool should be false from config.toml [settings.features]")
	}
	if cfg.Permissions.DefaultMode != "auto" {
		t.Fatalf("permissions.defaultMode = %q, want auto", cfg.Permissions.DefaultMode)
	}
}

func TestLoadMigratesLegacyGlobalSettingsJSONIntoConfigToml(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalConfigPath(agentDir), []byte(`version = 2
active = "deepseek"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LegacyGlobalSettingsPath(agentDir), []byte(`{"disableWorkflows":true,"permissions":{"defaultMode":"auto"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(agentDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DisableWorkflows || cfg.Permissions.DefaultMode != "auto" {
		t.Fatalf("legacy settings not loaded: %#v", cfg)
	}
	data, err := os.ReadFile(GlobalConfigPath(agentDir))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`active = "deepseek"`, `[settings]`, `disableWorkflows = true`, `[settings.permissions]`, `defaultMode = "auto"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("migrated config missing %q:\n%s", want, text)
		}
	}
}

func TestSaveGlobalConfigTomlOmitsDefaultsAndEmptySections(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, ".modu")
	cfg := Default()
	cfg.DisableWorkflows = true
	cfg.Features.MemoryTool = Ptr(false)
	cfg.Permissions.DenyTools = []string{"bash"}

	if err := Save(cfg, GlobalConfigPath(agentDir)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(GlobalConfigPath(agentDir))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`[settings]`, `disableWorkflows = true`, `[settings.features]`, `memoryTool = false`, `[settings.permissions]`, `denyTools = ["bash"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"retrySettings", "harness", "autoCompaction = true", "thinkingLevel = \"medium\"", "todoTool", "taskOutputTool", "planMode", "worktreeMode"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("saved config should omit %q:\n%s", unwanted, text)
		}
	}
}
