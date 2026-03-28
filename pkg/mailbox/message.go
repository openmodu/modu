package mailbox

import (
	"encoding/json"
	"fmt"
)

// MessageType 表示结构化消息的类型
type MessageType string

const (
	MessageTypeTaskAssign MessageType = "task_assign"
	MessageTypeTaskResult MessageType = "task_result"
	MessageTypeChat       MessageType = "chat"     // agent 间自由对话（关联 task_id）
	MessageTypeDelegate   MessageType = "delegate" // peer-to-peer 委托（在同一任务内）
	MessageTypeQuery      MessageType = "query"
	MessageTypeInfo       MessageType = "info"
)

// Message 是 agent 间传递的结构化消息，序列化为 JSON 字符串通过 mailbox 传输
type Message struct {
	Type    MessageType     `json:"type"`
	From    string          `json:"from"`
	TaskID  string          `json:"task_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// TaskAssignPayload 是 task_assign 消息的 Payload
type TaskAssignPayload struct {
	Description string `json:"description"`
}

// TaskResultPayload 是 task_result 消息的 Payload
type TaskResultPayload struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// ChatPayload 是 chat 消息的 Payload，用于 agent 间自由对话
type ChatPayload struct {
	Text string `json:"text"`
}

// NewTaskAssignMessage 构造一条任务委派消息
func NewTaskAssignMessage(from, taskID, description string) (string, error) {
	payload, err := json.Marshal(TaskAssignPayload{Description: description})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	msg := Message{
		Type:    MessageTypeTaskAssign,
		From:    from,
		TaskID:  taskID,
		Payload: payload,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}
	return string(b), nil
}

// NewTaskResultMessage 构造一条任务结果消息
func NewTaskResultMessage(from, taskID, result, errMsg string) (string, error) {
	payload, err := json.Marshal(TaskResultPayload{Result: result, Error: errMsg})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	msg := Message{
		Type:    MessageTypeTaskResult,
		From:    from,
		TaskID:  taskID,
		Payload: payload,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}
	return string(b), nil
}

// NewChatMessage 构造一条自由对话消息（关联到某个任务）
func NewChatMessage(from, taskID, text string) (string, error) {
	payload, err := json.Marshal(ChatPayload{Text: text})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	msg := Message{
		Type:    MessageTypeChat,
		From:    from,
		TaskID:  taskID,
		Payload: payload,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}
	return string(b), nil
}

// ParseMessage 将 JSON 字符串解析为 Message
func ParseMessage(s string) (Message, error) {
	var msg Message
	if err := json.Unmarshal([]byte(s), &msg); err != nil {
		return Message{}, fmt.Errorf("parse message: %w", err)
	}
	return msg, nil
}

// ParseTaskAssignPayload 解析 task_assign 消息的 Payload
func ParseTaskAssignPayload(msg Message) (TaskAssignPayload, error) {
	var p TaskAssignPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return TaskAssignPayload{}, fmt.Errorf("parse task_assign payload: %w", err)
	}
	return p, nil
}

// ParseTaskResultPayload 解析 task_result 消息的 Payload
func ParseTaskResultPayload(msg Message) (TaskResultPayload, error) {
	var p TaskResultPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return TaskResultPayload{}, fmt.Errorf("parse task_result payload: %w", err)
	}
	return p, nil
}

// ParseChatPayload 解析 chat 消息的 Payload
func ParseChatPayload(msg Message) (ChatPayload, error) {
	var p ChatPayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return ChatPayload{}, fmt.Errorf("parse chat payload: %w", err)
	}
	return p, nil
}

// DelegatePayload 是 delegate 消息的 Payload，用于 peer-to-peer 任务委托
type DelegatePayload struct {
	Description string `json:"description"`
	DelegatorID string `json:"delegator_id"` // 发起委托的 agent，结果需回传给它
}

// NewDelegateMessage 构造一条委托消息（在同一任务内委托其他 agent 完成子工作）
func NewDelegateMessage(from, taskID, description string) (string, error) {
	payload, err := json.Marshal(DelegatePayload{Description: description, DelegatorID: from})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	msg := Message{
		Type:    MessageTypeDelegate,
		From:    from,
		TaskID:  taskID,
		Payload: payload,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal message: %w", err)
	}
	return string(b), nil
}

// ParseDelegatePayload 解析 delegate 消息的 Payload
func ParseDelegatePayload(msg Message) (DelegatePayload, error) {
	var p DelegatePayload
	if err := json.Unmarshal(msg.Payload, &p); err != nil {
		return DelegatePayload{}, fmt.Errorf("parse delegate payload: %w", err)
	}
	return p, nil
}
