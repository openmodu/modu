package tgbot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPathAndTOMLRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	wantPath := filepath.Join(home, ".modu", "channels", "telegram", "config.toml")
	if got := ConfigPath(); got != wantPath {
		t.Fatalf("ConfigPath() = %q, want %q", got, wantPath)
	}

	if err := SaveConfig(&Config{Token: "telegram-token"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, `token = "telegram-token"`) {
		t.Fatalf("expected TOML token, got:\n%s", got)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Token != "telegram-token" {
		t.Fatalf("Token = %q, want telegram-token", cfg.Token)
	}
}
