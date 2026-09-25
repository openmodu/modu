package modelrouter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestUseAndRestoreCodexPreservesOtherConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "# keep this comment\nmodel = \"gpt-old\"\nmodel_reasoning_effort = \"high\"\n\n[mcp_servers.demo]\ncommand = \"demo\"\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Providers: []Provider{{ID: "p", BaseURL: "https://example.com/v1", Models: []string{"m"}}}}
	mgr := AgentManager{Home: home, GatewayURL: "http://127.0.0.1:3425", Config: cfg}
	if err := mgr.Use("codex", "p/m"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var decoded map[string]any
	if err := toml.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, b)
	}
	if decoded["model"] != "p/m" || decoded["model_provider"] != "modu_router" || !strings.Contains(string(b), "# keep this comment") || !strings.Contains(string(b), "command = \"demo\"") {
		t.Fatalf("unexpected config: %s", b)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "modu-models.json")); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Restore("codex"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), `model = "gpt-old"`) || strings.Contains(string(b), "modu_router") || !strings.Contains(string(b), "# keep this comment") {
		t.Fatalf("restore: %s", b)
	}
}

func TestUseAndRestoreClaudePreservesOtherSettings(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"claude-old","permissions":{"allow":["Read"]},"env":{"KEEP":"yes","ANTHROPIC_BASE_URL":"https://old.example"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Providers: []Provider{{ID: "p", BaseURL: "https://example.com/v1", Models: []string{"m"}}}}
	mgr := AgentManager{Home: home, GatewayURL: "http://127.0.0.1:3425", Config: cfg}
	if err := mgr.Use("claude", "p/m"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	env := got["env"].(map[string]any)
	if got["model"] != "p/m" || env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:3425" || env["KEEP"] != "yes" {
		t.Fatalf("use: %#v", got)
	}
	if err := mgr.Restore("claude"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	_ = json.Unmarshal(b, &got)
	env = got["env"].(map[string]any)
	if got["model"] != "claude-old" || env["ANTHROPIC_BASE_URL"] != "https://old.example" || env["KEEP"] != "yes" {
		t.Fatalf("restore: %#v", got)
	}
}
