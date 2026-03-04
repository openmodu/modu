package providers

// Role 消息角色
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 对话消息，兼容 OpenAI chat completions 格式
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall 工具调用
type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function FuncCall `json:"function"`
}

// FuncCall 函数调用信息
type FuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool 工具定义
type Tool struct {
	Type     string  `json:"type"`
	Function FuncDef `json:"function"`
}

// FuncDef 函数定义
type FuncDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ChatRequest 请求体
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	MaxTokens   *int      `json:"max_tokens,omitempty"`
}

// Usage token 用量
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponse 最终响应（非流式直接返回 / 流式 Result() 返回）
type ChatResponse struct {
	ID           string     `json:"id"`
	Model        string     `json:"model"`
	Message      Message    `json:"message"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Usage        Usage      `json:"usage"`
	FinishReason string     `json:"finish_reason"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

// StreamEventType 流式事件类型，值与 pkg/llm 保持一致以便迁移
type StreamEventType = string

const (
	EventStart         StreamEventType = "start"
	EventTextStart     StreamEventType = "text_start"
	EventTextDelta     StreamEventType = "text_delta"
	EventTextEnd       StreamEventType = "text_end"
	EventThinkingStart StreamEventType = "thinking_start"
	EventThinkingDelta StreamEventType = "thinking_delta"
	EventThinkingEnd   StreamEventType = "thinking_end"
	EventToolCallStart StreamEventType = "toolcall_start"
	EventToolCallDelta StreamEventType = "toolcall_delta"
	EventToolCallEnd   StreamEventType = "toolcall_end"
	EventDone          StreamEventType = "done"
	EventError         StreamEventType = "error"
)

// StreamEvent 流式事件，字段设计与 pkg/llm.AssistantMessageEvent 对齐
type StreamEvent struct {
	// Type 事件类型，值同 pkg/llm 中的字符串常量
	Type StreamEventType
	// ContentIndex 当前 content block 的下标（text/thinking/toolcall）
	ContentIndex int
	// Delta 增量文本（text_delta / thinking_delta / toolcall_delta）
	Delta string
	// Content 完整内容（text_end / thinking_end 时）
	Content string
	// ToolCall 工具调用（toolcall_start / toolcall_end）
	ToolCall *ToolCall
	// Partial 累积中的响应快照（每个事件都携带，便于调用方渲染进度）
	Partial *ChatResponse
	// Reason 停止原因（done / error）
	Reason string
	// Err 错误（error 事件）
	Err error
}
