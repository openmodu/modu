package provider

import (
	"testing"
)

func clearAllProviderEnv(t *testing.T) {
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

func TestResolveFallbackToLMStudio(t *testing.T) {
	clearAllProviderEnv(t)
	model, getKey := Resolve()
	if model == nil {
		t.Fatal("Resolve should never return nil after the LM Studio fallback")
	}
	if model.ProviderID != "lmstudio" {
		t.Errorf("expected ProviderID=lmstudio, got %q", model.ProviderID)
	}
	if model.ID != "qwen/qwen3.6-35b-a3b" {
		t.Errorf("expected fallback model qwen/qwen3.6-35b-a3b, got %q", model.ID)
	}
	if model.BaseURL != "http://localhost:1234/v1" {
		t.Errorf("expected fallback URL http://localhost:1234/v1, got %q", model.BaseURL)
	}
	// Fallback uses no API key.
	key, err := getKey("lmstudio")
	if err != nil {
		t.Errorf("getKey returned error: %v", err)
	}
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

func TestResolveExplicitLMStudioOverridesFallback(t *testing.T) {
	clearAllProviderEnv(t)
	t.Setenv("LMSTUDIO_BASE_URL", "http://localhost:5678/v1")
	t.Setenv("LMSTUDIO_MODEL", "some/other-model")
	model, _ := Resolve()
	if model.BaseURL != "http://localhost:5678/v1" {
		t.Errorf("explicit URL should win, got %q", model.BaseURL)
	}
	if model.ID != "some/other-model" {
		t.Errorf("explicit model should win, got %q", model.ID)
	}
}

func TestResolveOpenAIBeatsFallback(t *testing.T) {
	clearAllProviderEnv(t)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("OPENAI_MODEL", "gpt-4o-mini")
	model, _ := Resolve()
	if model.ProviderID != "openai" {
		t.Errorf("an explicit provider should beat the fallback, got %q", model.ProviderID)
	}
	if model.ID != "gpt-4o-mini" {
		t.Errorf("expected gpt-4o-mini, got %q", model.ID)
	}
}
