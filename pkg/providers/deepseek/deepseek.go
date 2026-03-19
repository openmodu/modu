// Package deepseek provides a DeepSeek LLM provider using the OpenAI-compatible
// chat completions API.
package deepseek

import (
	"os"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
)

const baseURL = "https://api.deepseek.com/v1"

// New creates a DeepSeek provider.
// apiKey falls back to the DEEPSEEK_API_KEY environment variable when empty.
func New(apiKey string) providers.Provider {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	return openai.New(providers.ProviderNameDeepSeek,
		openai.WithBaseURL(baseURL),
		openai.WithAPIKey(apiKey),
	)
}
