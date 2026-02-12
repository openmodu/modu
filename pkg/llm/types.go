package llm

type Api string
type Provider string
type KnownApi string
type KnownProvider string
type ThinkingLevel string
type CacheRetention string

const (
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
)

const (
	KnownApiOpenAICompletions     KnownApi = "openai-completions"
	KnownApiOpenAIResponses       KnownApi = "openai-responses"
	KnownApiAzureOpenAIResponses  KnownApi = "azure-openai-responses"
	KnownApiOpenAICodexResponses  KnownApi = "openai-codex-responses"
	KnownApiAnthropicMessages     KnownApi = "anthropic-messages"
	KnownApiBedrockConverseStream KnownApi = "bedrock-converse-stream"
	KnownApiGoogleGenerativeAI    KnownApi = "google-generative-ai"
	KnownApiGoogleGeminiCLI       KnownApi = "google-gemini-cli"
	KnownApiGoogleVertex          KnownApi = "google-vertex"
)

const (
	KnownProviderAmazonBedrock     KnownProvider = "amazon-bedrock"
	KnownProviderAnthropic         KnownProvider = "anthropic"
	KnownProviderGoogle            KnownProvider = "google"
	KnownProviderGoogleGeminiCLI   KnownProvider = "google-gemini-cli"
	KnownProviderGoogleAntigravity KnownProvider = "google-antigravity"
	KnownProviderGoogleVertex      KnownProvider = "google-vertex"
	KnownProviderOpenAI            KnownProvider = "openai"
	KnownProviderAzureOpenAI       KnownProvider = "azure-openai-responses"
	KnownProviderOpenAICodex       KnownProvider = "openai-codex"
	KnownProviderGithubCopilot     KnownProvider = "github-copilot"
	KnownProviderXAI               KnownProvider = "xai"
	KnownProviderGroq              KnownProvider = "groq"
	KnownProviderCerebras          KnownProvider = "cerebras"
	KnownProviderOpenRouter        KnownProvider = "openrouter"
	KnownProviderVercelAIGateway   KnownProvider = "vercel-ai-gateway"
	KnownProviderZAI               KnownProvider = "zai"
	KnownProviderMistral           KnownProvider = "mistral"
	KnownProviderMiniMax           KnownProvider = "minimax"
	KnownProviderMiniMaxCN         KnownProvider = "minimax-cn"
	KnownProviderHuggingFace       KnownProvider = "huggingface"
	KnownProviderOpencode          KnownProvider = "opencode"
	KnownProviderKimiCoding        KnownProvider = "kimi-coding"
)

type ThinkingBudgets struct {
	Minimal int
	Low     int
	Medium  int
	High    int
}

type StreamOptions struct {
	Temperature     *float64          `json:"temperature,omitempty"`
	MaxTokens       *int              `json:"maxTokens,omitempty"`
	APIKey          string            `json:"apiKey,omitempty"`
	CacheRetention  CacheRetention    `json:"cacheRetention,omitempty"`
	SessionID       string            `json:"sessionId,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	MaxRetryDelayMs int               `json:"maxRetryDelayMs,omitempty"`
}

type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ThinkingLevel
	ThinkingBudgets *ThinkingBudgets
}

type TextContent struct {
	Type          string `json:"type"`
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
}

type ThinkingContent struct {
	Type              string `json:"type"`
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
}

type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

type ToolCall struct {
	Type             string         `json:"type"`
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Arguments        map[string]any `json:"arguments"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

type ContentBlock interface{}

type Usage struct {
	Input       int `json:"input"`
	Output      int `json:"output"`
	CacheRead   int `json:"cacheRead"`
	CacheWrite  int `json:"cacheWrite"`
	TotalTokens int `json:"totalTokens"`
	Cost        struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cacheRead"`
		CacheWrite float64 `json:"cacheWrite"`
		Total      float64 `json:"total"`
	} `json:"cost"`
}

type StopReason string

type UserMessage struct {
	Role      string `json:"role"`
	Content   any    `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

type AssistantMessage struct {
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Api          Api            `json:"api"`
	Provider     Provider       `json:"provider"`
	Model        string         `json:"model"`
	Usage        Usage          `json:"usage"`
	StopReason   StopReason     `json:"stopReason"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Timestamp    int64          `json:"timestamp"`
}

type ToolResultMessage struct {
	Role       string         `json:"role"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Content    []ContentBlock `json:"content"`
	Details    any            `json:"details,omitempty"`
	IsError    bool           `json:"isError"`
	Timestamp  int64          `json:"timestamp"`
}

type Message interface{}

type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type Context struct {
	SystemPrompt string
	Messages     []Message
	Tools        []ToolDefinition
}

type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Api           Api      `json:"api"`
	Provider      Provider `json:"provider"`
	BaseURL       string   `json:"baseUrl"`
	Reasoning     bool     `json:"reasoning"`
	Input         []string `json:"input"`
	Cost          ModelCost
	ContextWindow int               `json:"contextWindow"`
	MaxTokens     int               `json:"maxTokens"`
	Headers       map[string]string `json:"headers,omitempty"`
	Compat        *ModelCompat      `json:"compat,omitempty"`
}

type ModelCost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

type AssistantMessageEvent struct {
	Type         string
	ContentIndex int
	Delta        string
	Content      string
	ToolCall     *ToolCall
	Partial      *AssistantMessage
	Reason       StopReason
	Message      *AssistantMessage
	ErrorMessage *AssistantMessage
	Error        error
}

type AssistantMessageEventStream interface {
	Events() <-chan AssistantMessageEvent
	Close()
	Result() (*AssistantMessage, error)
}

type OpenRouterRouting struct {
	Only  []string `json:"only,omitempty"`
	Order []string `json:"order,omitempty"`
}

type VercelGatewayRouting struct {
	Only  []string `json:"only,omitempty"`
	Order []string `json:"order,omitempty"`
}

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

type OpenAIResponsesCompat struct{}

type ModelCompat struct {
	OpenAICompletions *OpenAICompletionsCompat `json:"openaiCompletions,omitempty"`
	OpenAIResponses   *OpenAIResponsesCompat   `json:"openaiResponses,omitempty"`
}
