package mailbox

import "time"

type ConversationKind string

const (
	ConversationKindGeneral     ConversationKind = "general"
	ConversationKindProgress    ConversationKind = "progress"
	ConversationKindIdea        ConversationKind = "idea"
	ConversationKindDecision    ConversationKind = "decision"
	ConversationKindRisk        ConversationKind = "risk"
	ConversationKindDeliverable ConversationKind = "deliverable"
	ConversationKindSystem      ConversationKind = "system"
)

// ConversationEntry 记录 agent 间的一次消息交流
type ConversationEntry struct {
	At      time.Time        `json:"at"`
	From    string           `json:"from"`
	To      string           `json:"to"`
	TaskID  string           `json:"task_id"`
	MsgType MessageType      `json:"msg_type"`
	Kind    ConversationKind `json:"kind,omitempty"`
	Content string           `json:"content"` // 人类可读摘要
	Pinned  bool             `json:"pinned,omitempty"`
}
