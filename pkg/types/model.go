package types

// ThinkingLevel controls the reasoning effort the model applies.
type ThinkingLevel = string

const (
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
)

// CacheRetention controls prompt caching behaviour.
type CacheRetention = string

// ThinkingBudgets sets per-level token budgets for extended reasoning.
type ThinkingBudgets struct {
	Minimal int
	Low     int
	Medium  int
	High    int
}

// StreamOptions contains common options for a streaming LLM request.
type StreamOptions struct {
	Temperature     *float64          `json:"temperature,omitempty"`
	MaxTokens       *int              `json:"maxTokens,omitempty"`
	APIKey          string            `json:"apiKey,omitempty"`
	CacheRetention  CacheRetention    `json:"cacheRetention,omitempty"`
	SessionID       string            `json:"sessionId,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	MaxRetryDelayMs int               `json:"maxRetryDelayMs,omitempty"`
}

// SimpleStreamOptions extends StreamOptions with reasoning configuration.
type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ThinkingLevel
	ThinkingBudgets *ThinkingBudgets
}

// Api identifies the API protocol to use.
type Api = string

// ProviderName identifies a known provider.
type ProviderName = string

// KnownApi constants for well-known API protocols.
const (
	KnownApiOpenAICompletions     = "openai-completions"
	KnownApiOpenAIChatCompletions = "openai-chat-completions"
	KnownApiOpenAIResponses       = "openai-responses"
	KnownApiAzureOpenAIResponses  = "azure-openai-responses"
	KnownApiOpenAICodexResponses  = "openai-codex-responses"
	KnownApiAnthropicMessages     = "anthropic-messages"
	KnownApiBedrockConverseStream = "bedrock-converse-stream"
	KnownApiGoogleGenerativeAI    = "google-generative-ai"
	KnownApiGoogleGeminiCLI       = "google-gemini-cli"
	KnownApiGoogleVertex          = "google-vertex"
	KnownApiDeepSeekChat          = "deepseek-chat-completions"
	KnownApiOllama                = "ollama"
)

// KnownProvider constants for well-known providers.
const (
	KnownProviderAmazonBedrock     = "amazon-bedrock"
	KnownProviderAnthropic         = "anthropic"
	KnownProviderGoogle            = "google"
	KnownProviderGoogleGeminiCLI   = "google-gemini-cli"
	KnownProviderGoogleAntigravity = "google-antigravity"
	KnownProviderGoogleVertex      = "google-vertex"
	KnownProviderOpenAI            = "openai"
	KnownProviderAzureOpenAI       = "azure-openai-responses"
	KnownProviderOpenAICodex       = "openai-codex"
	KnownProviderGithubCopilot     = "github-copilot"
	KnownProviderXAI               = "xai"
	KnownProviderGroq              = "groq"
	KnownProviderCerebras          = "cerebras"
	KnownProviderOpenRouter        = "openrouter"
	KnownProviderVercelAIGateway   = "vercel-ai-gateway"
	KnownProviderZAI               = "zai"
	KnownProviderMistral           = "mistral"
	KnownProviderMiniMax           = "minimax"
	KnownProviderMiniMaxCN         = "minimax-cn"
	KnownProviderHuggingFace       = "huggingface"
	KnownProviderOpencode          = "opencode"
	KnownProviderKimiCoding        = "kimi-coding"
	KnownProviderDeepSeek          = "deepseek"
	KnownProviderOllama            = "ollama"
)

// ModelCost holds per-million-token cost for a model.
type ModelCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// OpenRouterRouting controls routing for OpenRouter provider.
type OpenRouterRouting struct {
	Only  []string `json:"only,omitempty"`
	Order []string `json:"order,omitempty"`
}

// VercelGatewayRouting controls routing for Vercel AI Gateway.
type VercelGatewayRouting struct {
	Only  []string `json:"only,omitempty"`
	Order []string `json:"order,omitempty"`
}

// OpenAICompletionsCompat holds compatibility options for OpenAI-style APIs.
type OpenAICompletionsCompat struct {
	SupportsStore                    *bool                 `json:"supportsStore,omitempty"`
	SupportsDeveloperRole            *bool                 `json:"supportsDeveloperRole,omitempty"`
	SupportsReasoningEffort          *bool                 `json:"supportsReasoningEffort,omitempty"`
	SupportsUsageInStreaming         *bool                 `json:"supportsUsageInStreaming,omitempty"`
	MaxTokensField                   *string               `json:"maxTokensField,omitempty"`
	RequiresToolResultName           *bool                 `json:"requiresToolResultName,omitempty"`
	RequiresAssistantAfterToolResult *bool                 `json:"requiresAssistantAfterToolResult,omitempty"`
	RequiresThinkingAsText           *bool                 `json:"requiresThinkingAsText,omitempty"`
	RequiresMistralToolIds           *bool                 `json:"requiresMistralToolIds,omitempty"`
	ThinkingFormat                   *string               `json:"thinkingFormat,omitempty"`
	OpenRouterRouting                *OpenRouterRouting    `json:"openRouterRouting,omitempty"`
	VercelGatewayRouting             *VercelGatewayRouting `json:"vercelGatewayRouting,omitempty"`
	SupportsStrictMode               *bool                 `json:"supportsStrictMode,omitempty"`
}

// OpenAIResponsesCompat holds compatibility options for OpenAI responses API.
type OpenAIResponsesCompat struct{}

// ModelCompat holds provider-specific compatibility options.
type ModelCompat struct {
	OpenAICompletions *OpenAICompletionsCompat `json:"openaiCompletions,omitempty"`
	OpenAIResponses   *OpenAIResponsesCompat   `json:"openaiResponses,omitempty"`
}

// Model identifies the model to use for a request.
type Model struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	ProviderID    string            `json:"provider"`
	Api           Api               `json:"api,omitempty"`
	BaseURL       string            `json:"baseUrl,omitempty"`
	Reasoning     bool              `json:"reasoning,omitempty"`
	Input         []string          `json:"input,omitempty"`
	Cost          ModelCost         `json:"-"`
	ContextWindow int               `json:"contextWindow,omitempty"`
	MaxTokens     int               `json:"maxTokens,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	Compat        *ModelCompat      `json:"compat,omitempty"`
}
