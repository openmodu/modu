package providers

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
// Distinguished from ToolCall (which is used in API request/response) by having
// a parsed Arguments map instead of a raw JSON string.
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

// AgentMessage is the interface for all message types in the conversation history.
type AgentMessage interface{}

// UserMessage is a message from the user.
type UserMessage struct {
	Role      string `json:"role"`
	Content   any    `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

// AssistantMessage is a message from the assistant, containing rich content blocks.
type AssistantMessage struct {
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	ProviderID   string         `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	Usage        AgentUsage     `json:"usage"`
	StopReason   StopReason     `json:"stopReason,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Timestamp    int64          `json:"timestamp"`
}

// ToolResultMessage carries the result of a tool call back to the model.
type ToolResultMessage struct {
	Role       string         `json:"role"`
	ToolCallID string         `json:"toolCallId"`
	ToolName   string         `json:"toolName"`
	Content    []ContentBlock `json:"content"`
	Details    any            `json:"details,omitempty"`
	IsError    bool           `json:"isError"`
	Timestamp  int64          `json:"timestamp"`
}

// ToolDefinition describes a tool available to the model.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// LLMContext holds the full context passed to the LLM for a single turn.
type LLMContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []ToolDefinition
}
