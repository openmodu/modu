package providers

// StopReason is the reason the model stopped generating.
type StopReason = string

// ThinkingLevel controls the reasoning effort the model applies.
type ThinkingLevel = string

const (
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
)

// Transport controls the streaming transport protocol.
type Transport = string

const (
	TransportSSE       Transport = "sse"
	TransportWebSocket Transport = "websocket"
	TransportAuto      Transport = "auto"
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
	Transport       Transport         `json:"transport,omitempty"`
}

// SimpleStreamOptions extends StreamOptions with reasoning configuration.
type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ThinkingLevel
	ThinkingBudgets *ThinkingBudgets
}

// Model identifies the model to use for a request.
type Model struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProviderID string `json:"provider"`
}
