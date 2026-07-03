package feishu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPathLoadSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	wantPath := filepath.Join(home, ".modu", "channels", "feishu", "config.toml")
	if got := ConfigPath(); got != wantPath {
		t.Fatalf("ConfigPath() = %q, want %q", got, wantPath)
	}

	cfg := &Config{
		AppID:     "cli_a",
		AppSecret: "secret",
		ChatIDs:   []string{"oc_a", " oc_b ", "oc_a"},
	}
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`appID = "cli_a"`, `appSecret = "secret"`, `chatIDs = ["oc_a", "oc_b"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("config file missing %q:\n%s", want, text)
		}
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.AppID != "cli_a" || got.AppSecret != "secret" || strings.Join(got.ChatIDs, ",") != "oc_a,oc_b" {
		t.Fatalf("loaded config = %+v", got)
	}
}

func TestEffectiveConfigUsesEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAppID, "env_app")
	t.Setenv(EnvAppSecret, "env_secret")
	t.Setenv(EnvChatIDs, "oc_env_a, oc_env_b, oc_env_a")

	if err := SaveConfig(&Config{AppID: "file_app", AppSecret: "file_secret", ChatIDs: []string{"oc_file"}}); err != nil {
		t.Fatal(err)
	}

	got, err := EffectiveConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.AppID != "env_app" || got.AppSecret != "env_secret" || strings.Join(got.ChatIDs, ",") != "oc_env_a,oc_env_b" {
		t.Fatalf("effective config = %+v", got)
	}
}
