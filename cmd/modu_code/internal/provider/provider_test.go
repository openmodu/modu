package provider

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

func TestResolveAppliesConfiguredContextWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, `{
  "active": "local-qwen",
  "models": [
    {
      "name": "local-qwen",
      "provider": "lmstudio",
      "model": "qwen/qwen3.6-35b-a3b",
      "baseUrl": "http://127.0.0.1:1234/v1",
      "contextWindow": 32768
    }
  ]
}`)

	model, _ := Resolve()
	if model == nil {
		t.Fatal("expected configured model")
	}
	if model.ContextWindow != 32768 {
		t.Fatalf("expected contextWindow 32768, got %d", model.ContextWindow)
	}
}

func TestResolveAppliesProviderDefaultContextWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, `{
  "active": "deepseek",
  "models": [
    {
      "name": "deepseek",
      "provider": "deepseek",
      "model": "deepseek-chat",
      "baseUrl": "https://api.deepseek.com/v1"
    }
  ]
}`)

	model, _ := Resolve()
	if model == nil {
		t.Fatal("expected configured model")
	}
	if model.ContextWindow != 1000000 {
		t.Fatalf("expected default contextWindow 1000000, got %d", model.ContextWindow)
	}
}

func TestResolveAppliesXiaomiMimoDefaultContextWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, `{
  "active": "mimo-v2.5-pro",
  "providers": {
    "xiaomi-mimo": {
      "type": "openai-compatible",
      "baseUrl": "https://token-plan-cn.xiaomimimo.com/v1"
    }
  },
  "models": [
    {
      "name": "mimo-v2.5-pro",
      "provider": "xiaomi-mimo",
      "model": "mimo-v2.5-pro"
    }
  ]
}`)

	model, _ := Resolve()
	if model == nil {
		t.Fatal("expected configured model")
	}
	if model.ContextWindow != 1000000 {
		t.Fatalf("expected default contextWindow 1000000, got %d", model.ContextWindow)
	}
}

func TestResolveEnvProviderDefaultContextWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-key")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-v4-pro")

	model, _ := Resolve()
	if model == nil {
		t.Fatal("expected env model")
	}
	if model.ContextWindow != 1000000 {
		t.Fatalf("expected default contextWindow 1000000, got %d", model.ContextWindow)
	}
}

func TestResolveUsesV2ProviderConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "env-deepseek-key")
	writeConfig(t, home, `{
  "version": 2,
  "active": "deepseek",
  "providers": {
    "deepseek": {
      "type": "openai-compatible",
      "baseUrl": "https://api.deepseek.com/v1",
      "apiKeyEnv": "DEEPSEEK_API_KEY"
    }
  },
  "models": [
    {"name": "deepseek", "description": "remote", "provider": "deepseek", "model": "deepseek-chat", "capabilities": ["tools"]}
  ]
}`)

	model, getAPIKey := Resolve()
	if model == nil {
		t.Fatal("expected configured model")
	}
	if model.ProviderID != "deepseek" || model.ID != "deepseek-chat" || model.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("unexpected model: %#v", model)
	}
	key, err := getAPIKey("deepseek")
	if err != nil || key != "env-deepseek-key" {
		t.Fatalf("unexpected api key %q err=%v", key, err)
	}
}

func TestResolveRejectsConfigWithUnknownActiveModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENAI_API_KEY", "env-key")
	writeConfig(t, home, `{
  "active": "missing-model",
  "models": [
    {
      "name": "local-qwen",
      "provider": "lmstudio",
      "model": "qwen",
      "baseUrl": "http://127.0.0.1:1234/v1"
    }
  ]
}`)

	model, getAPIKey := Resolve()
	if model != nil || getAPIKey != nil {
		t.Fatalf("expected invalid active config to block fallback, got model=%#v keyNil=%v", model, getAPIKey == nil)
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
	if cfg.Models[0].BaseURL != "" || cfg.Models[1].BaseURL != "" {
		t.Fatalf("expected saved config to strip legacy model baseUrl: %#v", cfg.Models)
	}
	if cfg.Providers["lmstudio"].BaseURL == "" || cfg.Providers["deepseek"].BaseURL == "" {
		t.Fatalf("expected provider baseUrls after migration: %#v", cfg.Providers)
	}
}

func TestModelMatchesTarget(t *testing.T) {
	model := ModelConfig{Name: "local", Provider: "lmstudio", Model: "qwen"}
	for _, target := range []string{"local", "qwen", "lmstudio/qwen", "lmstudio:qwen"} {
		if !ModelMatchesTarget(model, target) {
			t.Fatalf("expected target %q to match", target)
		}
	}
	for _, target := range []string{"", "other", "openai/qwen"} {
		if ModelMatchesTarget(model, target) {
			t.Fatalf("expected target %q not to match", target)
		}
	}
}

func TestInitAndValidateConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := InitConfig(false)
	if err != nil {
		t.Fatalf("InitConfig: %v", err)
	}
	if path != filepath.Join(home, ".coding_agent", "config.json") {
		t.Fatalf("unexpected path: %s", path)
	}
	result := ValidateConfig()
	if len(result.Problems) != 0 {
		t.Fatalf("expected valid example config, got %#v", result.Problems)
	}
	if result.ModelCount != 2 || result.Active != "local-qwen" {
		t.Fatalf("unexpected validation result: %#v", result)
	}
	if _, err := InitConfig(false); err == nil {
		t.Fatal("expected init without force to refuse existing config")
	}
}

func TestUpsertUseAndRemoveModelConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	created, err := UpsertModelConfig(ModelConfig{
		Name:        "local-qwen",
		Description: "local coding model",
		Provider:    "lmstudio",
		Model:       "qwen",
		BaseURL:     "127.0.0.1:1234/v1",
		APIKey:      "local-key",
	})
	if err != nil {
		t.Fatalf("UpsertModelConfig create: %v", err)
	}
	if !created {
		t.Fatal("expected model to be created")
	}

	created, err = UpsertModelConfig(ModelConfig{
		Name:        "local-qwen",
		Description: "updated description",
		Provider:    "lmstudio",
		Model:       "qwen2",
		BaseURL:     "http://127.0.0.1:1234/v1",
	})
	if err != nil {
		t.Fatalf("UpsertModelConfig update: %v", err)
	}
	if created {
		t.Fatal("expected existing model to be updated")
	}

	cfg, ok := LoadConfig()
	if !ok {
		t.Fatal("expected config to load")
	}
	if cfg.Active != "local-qwen" || len(cfg.Models) != 1 {
		t.Fatalf("unexpected config after upsert: %#v", cfg)
	}
	if cfg.Models[0].Description != "updated description" || cfg.Models[0].Model != "qwen2" {
		t.Fatalf("unexpected model after update: %#v", cfg.Models[0])
	}

	active, err := SetActiveModel("lmstudio/qwen2")
	if err != nil {
		t.Fatalf("SetActiveModel: %v", err)
	}
	if active.Name != "local-qwen" {
		t.Fatalf("unexpected active model: %#v", active)
	}

	removed, err := RemoveModelConfig("local-qwen")
	if err != nil {
		t.Fatalf("RemoveModelConfig: %v", err)
	}
	if removed.Model != "qwen2" {
		t.Fatalf("unexpected removed model: %#v", removed)
	}
	cfg, exists, err := LoadConfigFile()
	if err != nil || !exists {
		t.Fatalf("expected config file to remain, exists=%v err=%v", exists, err)
	}
	if cfg.Active != "" || len(cfg.Models) != 0 {
		t.Fatalf("unexpected config after remove: %#v", cfg)
	}
}

func TestSetScopedModelIDsPersistsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := InitConfig(false); err != nil {
		t.Fatalf("InitConfig: %v", err)
	}

	if err := SetScopedModelIDs([]string{"deepseek"}); err != nil {
		t.Fatalf("SetScopedModelIDs: %v", err)
	}
	cfg, ok := LoadConfig()
	if !ok {
		t.Fatal("expected config")
	}
	if len(cfg.ScopedModels) != 1 || cfg.ScopedModels[0] != "deepseek" {
		t.Fatalf("unexpected scoped models: %#v", cfg.ScopedModels)
	}
	if got := ConfiguredModelIDs(); len(got) != 1 || got[0] != "deepseek-chat" {
		t.Fatalf("ConfiguredModelIDs = %#v, want deepseek-chat", got)
	}

	if err := SetScopedModelIDs(nil); err != nil {
		t.Fatalf("SetScopedModelIDs clear: %v", err)
	}
	cfg, _ = LoadConfig()
	if len(cfg.ScopedModels) != 0 {
		t.Fatalf("expected scoped models cleared, got %#v", cfg.ScopedModels)
	}
}

func TestDiscoverProviderModelsPersistsOpenAICompatibleModels(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldClient := modelDiscoveryHTTPClient
	modelDiscoveryHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://example.test/v1/models" {
			t.Fatalf("unexpected URL: %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"qwen"},{"id":"gpt-4o"},{"id":"qwen"},{"id":""}]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	t.Cleanup(func() { modelDiscoveryHTTPClient = oldClient })

	if err := UpsertProviderConfig("openai", ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://example.test/v1",
		APIKey:  "test-key",
	}); err != nil {
		t.Fatalf("UpsertProviderConfig: %v", err)
	}

	discovery, err := DiscoverProviderModels(context.Background(), "openai")
	if err != nil {
		t.Fatalf("DiscoverProviderModels: %v", err)
	}
	if discovery.Found != 2 || discovery.Added != 2 || discovery.Updated != 0 {
		t.Fatalf("unexpected discovery result: %#v", discovery)
	}
	cfg, ok := LoadConfig()
	if !ok {
		t.Fatal("expected config to load")
	}
	if cfg.Active != "gpt-4o" {
		t.Fatalf("expected first discovered model active, got %q", cfg.Active)
	}
	if len(cfg.Models) != 2 || cfg.Models[0].Name != "gpt-4o" || cfg.Models[1].Name != "qwen" {
		t.Fatalf("unexpected discovered models: %#v", cfg.Models)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestUpsertProviderConfigPreservesExistingSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := UpsertProviderConfig("openai", ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "old-key",
	}); err != nil {
		t.Fatalf("UpsertProviderConfig first: %v", err)
	}
	if err := UpsertProviderConfig("openai", ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://example.test/v1",
	}); err != nil {
		t.Fatalf("UpsertProviderConfig second: %v", err)
	}
	cfg, exists, err := LoadConfigFile()
	if err != nil || !exists {
		t.Fatalf("expected config, exists=%v err=%v", exists, err)
	}
	if got := cfg.Providers["openai"].APIKey; got != "old-key" {
		t.Fatalf("expected existing API key preserved, got %q", got)
	}
}

func TestValidateConfigReportsProblems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeConfig(t, home, `{
  "active": "missing",
  "models": [
    {"name": "broken", "provider": "", "model": "qwen", "baseUrl": "127.0.0.1:1234/v1"},
    {"name": "broken", "provider": "deepseek", "model": "", "baseUrl": ""}
  ]
}`)

	result := ValidateConfig()
	for _, want := range []string{
		"models[0].provider is required",
		"models[1].model is required",
		"models[1].provider \"deepseek\" has no baseUrl",
		"providers.deepseek.baseUrl is required",
		"models[1].name duplicates \"broken\"",
		"active model does not match any configured model",
	} {
		if !containsString(result.Problems, want) {
			t.Fatalf("expected problem %q in %#v", want, result.Problems)
		}
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

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
