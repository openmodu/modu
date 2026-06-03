package evals

import "testing"

func TestProviderSpecsFromEnv(t *testing.T) {
	t.Setenv("EVAL_PROVIDER", "lmstudio,openai")
	t.Setenv("EVAL_MODEL", "fallback-model")
	t.Setenv("EVAL_OPENAI_MODEL", "gpt-test")
	t.Setenv("EVAL_OPENAI_API_KEY", "openai-key")

	specs := providerSpecsFromEnv()
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	if specs[0].ProviderID != "lmstudio" || specs[0].BaseURL != "http://localhost:1234/v1" || specs[0].ModelID != "fallback-model" {
		t.Fatalf("unexpected lmstudio spec: %#v", specs[0])
	}
	if specs[1].ProviderID != "openai" || specs[1].BaseURL != "https://api.openai.com/v1" || specs[1].ModelID != "gpt-test" || specs[1].APIKey != "openai-key" {
		t.Fatalf("unexpected openai spec: %#v", specs[1])
	}
}

func TestGraderSpecFallsBackToEvalSpec(t *testing.T) {
	fallback := ProviderSpec{
		ProviderID: "lmstudio",
		BaseURL:    "http://localhost:1234/v1",
		APIKey:     "main-key",
		ModelID:    "main-model",
	}

	spec := graderSpecFromEnv(fallback)
	if spec != fallback {
		t.Fatalf("expected grader to fall back to eval spec, got %#v", spec)
	}
}
