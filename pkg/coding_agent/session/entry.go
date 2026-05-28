package session

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/openmodu/modu/pkg/agent"
)

const CurrentSessionVersion = 3

// Header is the first JSONL record in a persisted session file.
type Header struct {
	Type          string `json:"type"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
}

// EntryType represents the type of session entry.
type EntryType string

const (
	EntryTypeMessage        EntryType = "message"
	EntryTypeThinkingChange EntryType = "thinking_level_change"
	EntryTypeModelChange    EntryType = "model_change"
	EntryTypeCompaction     EntryType = "compaction"
	EntryTypeBranchSummary  EntryType = "branch_summary"
	EntryTypeCustom         EntryType = "custom"
	EntryTypeCustomMessage  EntryType = "custom_message"
	EntryTypeLabel          EntryType = "label"
	EntryTypeSessionInfo    EntryType = "session_info"
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
		ID:        generateID(nil),
		ParentID:  parentID,
		Type:      entryType,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
}

func (e SessionEntry) MarshalJSON() ([]byte, error) {
	parentID := any(nil)
	if e.ParentID != "" {
		parentID = e.ParentID
	}
	base := map[string]any{
		"type":      string(e.Type),
		"id":        e.ID,
		"parentId":  parentID,
		"timestamp": time.UnixMilli(e.Timestamp).UTC().Format(time.RFC3339Nano),
	}
	switch e.Type {
	case EntryTypeMessage:
		base["message"] = messagePayload(e.Data)
	case EntryTypeThinkingChange:
		if data, ok := e.Data.(ThinkingChangeData); ok {
			base["thinkingLevel"] = string(data.Level)
		} else {
			base["thinkingLevel"] = e.Data
		}
	case EntryTypeModelChange:
		if data, ok := e.Data.(ModelChangeData); ok {
			base["provider"] = data.Provider
			base["modelId"] = data.ModelID
		} else {
			base["data"] = e.Data
		}
	case EntryTypeCompaction:
		if data, ok := e.Data.(CompactionData); ok {
			base["summary"] = data.Summary
			base["firstKeptEntryId"] = data.FirstKeptEntryID
			base["tokensBefore"] = data.TokensBefore
			base["originalCount"] = data.OriginalCount
			base["newCount"] = data.NewCount
		} else {
			base["data"] = e.Data
		}
	case EntryTypeBranchSummary:
		if data, ok := e.Data.(BranchSummaryData); ok {
			base["summary"] = data.Summary
			base["fromId"] = data.FromID
			if data.ToID != "" {
				base["toId"] = data.ToID
			}
		} else {
			base["data"] = e.Data
		}
	case EntryTypeCustom:
		if data, ok := e.Data.(CustomData); ok {
			base["customType"] = data.CustomType
			base["data"] = data.Data
		} else {
			base["data"] = e.Data
		}
	case EntryTypeCustomMessage:
		if data, ok := e.Data.(CustomMessageData); ok {
			base["customType"] = data.CustomType
			base["content"] = data.Content
			base["details"] = data.Details
			base["display"] = data.Display
		} else {
			base["data"] = e.Data
		}
	case EntryTypeLabel:
		if data, ok := e.Data.(LabelData); ok {
			base["targetId"] = data.TargetID
			base["label"] = data.Text
		} else {
			base["data"] = e.Data
		}
	case EntryTypeSessionInfo:
		if data, ok := e.Data.(SessionInfoData); ok {
			if data.Name != "" {
				base["name"] = data.Name
			}
			if data.Cwd != "" {
				base["cwd"] = data.Cwd
			}
			if data.StartTime != 0 {
				base["startTime"] = data.StartTime
			}
		} else {
			base["data"] = e.Data
		}
	default:
		base["data"] = e.Data
	}
	return json.Marshal(base)
}

func (e *SessionEntry) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var typ string
	_ = json.Unmarshal(raw["type"], &typ)
	e.Type = EntryType(typ)
	_ = json.Unmarshal(raw["id"], &e.ID)
	var parent *string
	if err := json.Unmarshal(raw["parentId"], &parent); err == nil && parent != nil {
		e.ParentID = *parent
	}
	e.Timestamp = parseTimestamp(raw["timestamp"])
	switch e.Type {
	case EntryTypeMessage:
		var msg map[string]any
		if err := json.Unmarshal(raw["message"], &msg); err == nil {
			e.Data = msg
		}
	case EntryTypeThinkingChange:
		var level string
		_ = json.Unmarshal(raw["thinkingLevel"], &level)
		e.Data = ThinkingChangeData{Level: agent.ThinkingLevel(level)}
	case EntryTypeModelChange:
		var provider, modelID string
		_ = json.Unmarshal(raw["provider"], &provider)
		_ = json.Unmarshal(raw["modelId"], &modelID)
		e.Data = ModelChangeData{Provider: provider, ModelID: modelID}
	case EntryTypeCompaction:
		var d CompactionData
		_ = json.Unmarshal(raw["summary"], &d.Summary)
		_ = json.Unmarshal(raw["firstKeptEntryId"], &d.FirstKeptEntryID)
		_ = json.Unmarshal(raw["tokensBefore"], &d.TokensBefore)
		_ = json.Unmarshal(raw["originalCount"], &d.OriginalCount)
		_ = json.Unmarshal(raw["newCount"], &d.NewCount)
		e.Data = d
	case EntryTypeBranchSummary:
		var d BranchSummaryData
		_ = json.Unmarshal(raw["summary"], &d.Summary)
		_ = json.Unmarshal(raw["fromId"], &d.FromID)
		_ = json.Unmarshal(raw["toId"], &d.ToID)
		e.Data = d
	case EntryTypeLabel:
		var d LabelData
		_ = json.Unmarshal(raw["targetId"], &d.TargetID)
		_ = json.Unmarshal(raw["label"], &d.Text)
		e.Data = d
	case EntryTypeSessionInfo:
		var d SessionInfoData
		_ = json.Unmarshal(raw["name"], &d.Name)
		_ = json.Unmarshal(raw["cwd"], &d.Cwd)
		_ = json.Unmarshal(raw["startTime"], &d.StartTime)
		e.Data = d
	default:
		var v any
		_ = json.Unmarshal(raw["data"], &v)
		e.Data = v
	}
	return nil
}

func messagePayload(data any) any {
	if msg, ok := data.(agent.AgentMessage); ok {
		return msg
	}
	if data, ok := data.(MessageData); ok {
		if msg, ok := data.Content.(agent.AgentMessage); ok {
			return msg
		}
		return map[string]any{
			"role":    string(data.Role),
			"content": data.Content,
		}
	}
	return data
}

func parseTimestamp(raw json.RawMessage) int64 {
	var str string
	if err := json.Unmarshal(raw, &str); err == nil && str != "" {
		if t, err := time.Parse(time.RFC3339Nano, str); err == nil {
			return t.UnixMilli()
		}
	}
	var n int64
	_ = json.Unmarshal(raw, &n)
	return n
}

// MessageData holds message-related entry data.
type MessageData struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
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
	Summary          string `json:"summary"`
	FirstKeptEntryID string `json:"firstKeptEntryId,omitempty"`
	TokensBefore     int    `json:"tokensBefore,omitempty"`
	OriginalCount    int    `json:"originalCount"`
	NewCount         int    `json:"newCount"`
}

// BranchSummaryData holds branch summary data.
type BranchSummaryData struct {
	Summary string `json:"summary"`
	FromID  string `json:"fromId"`
	ToID    string `json:"toId"`
}

// SessionInfoData holds session metadata.
type SessionInfoData struct {
	Cwd       string `json:"cwd,omitempty"`
	StartTime int64  `json:"startTime,omitempty"`
	Name      string `json:"name,omitempty"`
}

// LabelData holds label entry data.
type LabelData struct {
	TargetID string `json:"targetId,omitempty"`
	Text     string `json:"text"`
}

type CustomData struct {
	CustomType string `json:"customType"`
	Data       any    `json:"data,omitempty"`
}

type CustomMessageData struct {
	CustomType string `json:"customType"`
	Content    any    `json:"content"`
	Details    any    `json:"details,omitempty"`
	Display    bool   `json:"display"`
}

func generateID(exists func(string) bool) string {
	for i := 0; i < 100; i++ {
		id := uuid.NewString()[:8]
		if exists == nil || !exists(id) {
			return id
		}
	}
	return uuid.NewString()
}
