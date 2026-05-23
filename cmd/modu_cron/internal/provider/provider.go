// Package provider resolves an LLM provider from environment variables.
//
// modu_cron deliberately ships a slimmer resolver than modu_code: env only,
// no on-disk config. The daemon form is usually deployed with environment
// secrets injected by systemd / docker / k8s, so a config file in $HOME would
// be an awkward fit. If a shared config surface is wanted later, lift this
// and modu_code's provider into pkg/.
//
// Resolution order (first match wins):
//
//	ANTHROPIC_API_KEY (+ ANTHROPIC_MODEL)
//	OPENAI_API_KEY    (+ OPENAI_MODEL,    OPENAI_BASE_URL)
//	DEEPSEEK_API_KEY  (+ DEEPSEEK_MODEL)
//	OLLAMA_HOST       (+ OLLAMA_MODEL)
//	LMSTUDIO_BASE_URL (+ LMSTUDIO_MODEL)
//
// If none of the above are set, Resolve falls back to a local LM Studio
// instance at http://localhost:1234/v1 running qwen/qwen3.6-35b-a3b. The
// fallback exists so a fresh install can run `add` / `daemon` without
// exporting anything — the user just needs LM Studio running locally with
// that model loaded.
package provider

import (
	"fmt"
	"os"
	"strings"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

// Resolve returns the configured model and an API-key resolver. Returns
// (nil, nil) when no provider env is set.
func Resolve() (*types.Model, func(string) (string, error)) {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		modelID := os.Getenv("ANTHROPIC_MODEL")
		if modelID == "" {
			fmt.Fprintln(os.Stderr, "ANTHROPIC_API_KEY set but ANTHROPIC_MODEL is empty")
			return nil, nil
		}
		baseURL := "https://api.anthropic.com/v1"
		providers.Register(openai.New(
			"anthropic",
			openai.WithBaseURL(baseURL),
			openai.WithAPIKey(key),
			openai.WithHeaders(map[string]string{"anthropic-version": "2023-06-01"}),
		))
		return &types.Model{ID: modelID, Name: modelID + " (Anthropic)", ProviderID: "anthropic", BaseURL: baseURL},
			staticKey("anthropic", key)
	}

	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		modelID := os.Getenv("OPENAI_MODEL")
		if modelID == "" {
			modelID = "gpt-4o"
		}
		baseURL := os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		providers.Register(openai.New("openai", openai.WithBaseURL(baseURL), openai.WithAPIKey(key)))
		return &types.Model{ID: modelID, Name: "OpenAI " + modelID, ProviderID: "openai", BaseURL: baseURL},
			staticKey("openai", key)
	}

	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		modelID := os.Getenv("DEEPSEEK_MODEL")
		if modelID == "" {
			modelID = "deepseek-chat"
		}
		baseURL := "https://api.deepseek.com/v1"
		providers.Register(openai.New("deepseek", openai.WithBaseURL(baseURL), openai.WithAPIKey(key)))
		return &types.Model{ID: modelID, Name: "DeepSeek " + modelID, ProviderID: "deepseek", BaseURL: baseURL},
			staticKey("deepseek", key)
	}

	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		modelID := os.Getenv("OLLAMA_MODEL")
		if modelID == "" {
			fmt.Fprintln(os.Stderr, "OLLAMA_HOST set but OLLAMA_MODEL is empty")
			return nil, nil
		}
		baseURL := strings.TrimRight(host, "/") + "/v1"
		providers.Register(openai.New("ollama", openai.WithBaseURL(baseURL)))
		return &types.Model{ID: modelID, Name: modelID + " (Ollama)", ProviderID: "ollama", BaseURL: baseURL},
			noKey
	}

	if lmURL := os.Getenv("LMSTUDIO_BASE_URL"); lmURL != "" {
		modelID := os.Getenv("LMSTUDIO_MODEL")
		if modelID == "" {
			modelID = "qwen/qwen3.6-35b-a3b"
		}
		providers.Register(openai.New("lmstudio", openai.WithBaseURL(lmURL)))
		return &types.Model{ID: modelID, Name: modelID + " (LM Studio)", ProviderID: "lmstudio", BaseURL: lmURL},
			noKey
	}

	// No provider env set — fall back to a local LM Studio instance. The
	// model needs to actually be loaded in LM Studio for requests to
	// succeed; that failure surfaces when a task runs, not here.
	const (
		fallbackModel = "qwen/qwen3.6-35b-a3b"
		fallbackURL   = "http://localhost:1234/v1"
	)
	fmt.Fprintf(os.Stderr, "no provider env set; defaulting to LM Studio at %s with model %s (start LM Studio locally or export a provider env to override)\n", fallbackURL, fallbackModel)
	providers.Register(openai.New("lmstudio", openai.WithBaseURL(fallbackURL)))
	return &types.Model{ID: fallbackModel, Name: fallbackModel + " (LM Studio, default)", ProviderID: "lmstudio", BaseURL: fallbackURL},
		noKey
}

func staticKey(provider, key string) func(string) (string, error) {
	return func(p string) (string, error) {
		if p == provider {
			return key, nil
		}
		return "", fmt.Errorf("no key for %s", p)
	}
}

func noKey(string) (string, error) { return "", nil }
