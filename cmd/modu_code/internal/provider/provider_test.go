package provider

import "testing"

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
