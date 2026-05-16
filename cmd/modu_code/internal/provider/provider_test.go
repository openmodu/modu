package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWithoutProviderReturnsNil(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_MODEL", "")
	t.Setenv("LMSTUDIO_MODEL", "")
	t.Setenv("LMSTUDIO_BASE_URL", "")

	model, getAPIKey := Resolve()
	if model != nil || getAPIKey != nil {
		t.Fatalf("expected no implicit provider, got model=%#v getAPIKeyNil=%v", model, getAPIKey == nil)
	}
}

func TestResolveUsesMultiModelConfigBeforeEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "env-key")
	t.Setenv("OPENAI_MODEL", "env-model")
	writeConfig(t, home, `{
  "active": "local-qwen",
  "models": [
    {
      "name": "local-qwen",
      "provider": "lmstudio",
      "model": "qwen/qwen3.6-35b-a3b",
      "baseUrl": "http://127.0.0.1:1234/v1",
      "apiKey": "local-key"
    },
    {
      "name": "deepseek",
      "provider": "deepseek",
      "model": "deepseek-chat",
      "baseUrl": "https://api.deepseek.com/v1",
      "apiKey": "deepseek-key"
    }
  ]
}`)

	model, getAPIKey := Resolve()
	if model == nil {
		t.Fatal("expected configured model")
	}
	if model.ProviderID != "lmstudio" || model.ID != "qwen/qwen3.6-35b-a3b" || model.Name != "local-qwen" {
		t.Fatalf("unexpected active model: %#v", model)
	}
	key, err := getAPIKey("lmstudio")
	if err != nil || key != "local-key" {
		t.Fatalf("unexpected api key %q err=%v", key, err)
	}
}

func TestSaveActiveModelUpdatesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, `{
  "active": "local-qwen",
  "models": [
    {"name": "local-qwen", "provider": "lmstudio", "model": "qwen", "baseUrl": "127.0.0.1:1234/v1"},
    {"name": "remote-deepseek", "provider": "deepseek", "model": "deepseek-chat", "baseUrl": "https://api.deepseek.com/v1"}
  ]
}`)

	if err := SaveActiveModel("deepseek", "deepseek-chat"); err != nil {
		t.Fatalf("SaveActiveModel: %v", err)
	}
	cfg, ok := LoadConfig()
	if !ok {
		t.Fatal("expected config to load")
	}
	if cfg.Active != "remote-deepseek" {
		t.Fatalf("active = %q, want remote-deepseek", cfg.Active)
	}
}

func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".coding_agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
