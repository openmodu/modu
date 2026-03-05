package types

// ContentBlock is the interface for content within an assistant message.
type ContentBlock interface{}

// TextContent represents a text block in an assistant message.
type TextContent struct {
	Type          string `json:"type"`
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
}

// ThinkingContent represents a thinking/reasoning block in an assistant message.
type ThinkingContent struct {
	Type              string `json:"type"`
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
}

// ImageContent represents an image block, used in user messages for multimodal input.
type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// ToolCallContent represents a tool call block in an assistant message.
// It holds the parsed Arguments map, as opposed to ToolCall which holds a raw JSON string.
type ToolCallContent struct {
	Type             string         `json:"type"`
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Arguments        map[string]any `json:"arguments"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

// AgentUsage tracks token consumption with full cost breakdown.
type AgentUsage struct {
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
