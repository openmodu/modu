package types

// ContentBlock is the interface for all content blocks in messages.
// All implementations use pointer receivers — blocks must be stored as pointers
// (e.g. &types.TextContent{..}), which is already the convention everywhere.
type ContentBlock interface {
	isContentBlock()
}

// TextContent represents a text block in an assistant message.
type TextContent struct {
	Type          string `json:"type"`
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
}

func (*TextContent) isContentBlock() {}

// ThinkingContent represents a thinking/reasoning block in an assistant message.
type ThinkingContent struct {
	Type              string `json:"type"`
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
}

func (*ThinkingContent) isContentBlock() {}

// ImageContent represents an image block, used in user messages for multimodal input.
type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

func (*ImageContent) isContentBlock() {}

// ToolCallContent represents a tool call block in an assistant message.
// It holds the parsed Arguments map, as opposed to ToolCall which holds a raw JSON string.
type ToolCallContent struct {
	Type             string         `json:"type"`
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Arguments        map[string]any `json:"arguments"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

func (*ToolCallContent) isContentBlock() {}

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
