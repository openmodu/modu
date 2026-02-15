package session

import (
	"time"

	"github.com/crosszan/modu/pkg/agent"
)

// EntryType represents the type of session entry.
type EntryType string

const (
	EntryTypeMessage           EntryType = "message"
	EntryTypeThinkingChange    EntryType = "thinkingLevelChange"
	EntryTypeModelChange       EntryType = "modelChange"
	EntryTypeCompaction        EntryType = "compaction"
	EntryTypeBranchSummary     EntryType = "branchSummary"
	EntryTypeCustom            EntryType = "custom"
	EntryTypeCustomMessage     EntryType = "customMessage"
	EntryTypeLabel             EntryType = "label"
	EntryTypeSessionInfo       EntryType = "sessionInfo"
)

// SessionEntry represents a single entry in the session history.
type SessionEntry struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId,omitempty"`
	Type      EntryType `json:"type"`
	Timestamp int64     `json:"timestamp"`
	Data      any       `json:"data"`
}

// NewEntry creates a new session entry with a generated ID.
func NewEntry(entryType EntryType, parentID string, data any) SessionEntry {
	return SessionEntry{
		ID:        generateID(),
		ParentID:  parentID,
		Type:      entryType,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
}

// MessageData holds message-related entry data.
type MessageData struct {
	Role    agent.MessageRole `json:"role"`
	Content any               `json:"content"`
}

// ModelChangeData holds model change entry data.
type ModelChangeData struct {
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// ThinkingChangeData holds thinking level change entry data.
type ThinkingChangeData struct {
	Level agent.ThinkingLevel `json:"level"`
}

// CompactionData holds compaction entry data.
type CompactionData struct {
	Summary       string `json:"summary"`
	OriginalCount int    `json:"originalCount"`
	NewCount      int    `json:"newCount"`
}

// BranchSummaryData holds branch summary data.
type BranchSummaryData struct {
	Summary  string `json:"summary"`
	FromID   string `json:"fromId"`
	ToID     string `json:"toId"`
}

// SessionInfoData holds session metadata.
type SessionInfoData struct {
	Cwd       string `json:"cwd"`
	StartTime int64  `json:"startTime"`
}

// LabelData holds label entry data.
type LabelData struct {
	Text string `json:"text"`
}

var idCounter int64

func generateID() string {
	idCounter++
	return time.Now().Format("20060102150405") + "_" + itoa(idCounter)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
