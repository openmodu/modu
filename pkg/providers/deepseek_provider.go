package providers

import "os"

const deepSeekBaseURL = "https://api.deepseek.com/v1"

// NewDeepSeekProvider creates a DeepSeek provider using the OpenAI-compatible
// chat completions API. apiKey falls back to the DEEPSEEK_API_KEY environment
// variable when empty.
func NewDeepSeekProvider(apiKey string) Provider {
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	return NewOpenAIChatCompletionsProvider(ProviderNameDeepSeek,
		WithBaseURL(deepSeekBaseURL),
		WithAPIKey(apiKey),
	)
}
