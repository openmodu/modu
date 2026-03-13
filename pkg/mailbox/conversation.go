package mailbox

import "time"

// ConversationEntry 记录 agent 间的一次消息交流
type ConversationEntry struct {
	At      time.Time   `json:"at"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	TaskID  string      `json:"task_id"`
	MsgType MessageType `json:"msg_type"`
	Content string      `json:"content"` // 人类可读摘要
}
