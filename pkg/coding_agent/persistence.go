package coding_agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/types"
)

// messagesFilePath returns the path of the per-project messages snapshot.
// We use JSONL format to align with pi-mono.
func (s *engine) messagesFilePath() string {
	if s != nil && s.sessionManager != nil {
		return s.sessionManager.FilePath()
	}
	// Convert absolute path to a safe directory name: strip leading slash,
	// replace remaining slashes with underscores.
	dirName := strings.ReplaceAll(strings.TrimPrefix(s.cwd, "/"), "/", "_")
	if dirName == "" {
		dirName = "root"
	}
	return filepath.Join(s.agentDir, "sessions", dirName, "messages.jsonl")
}

// SaveMessages writes the current conversation messages to a JSONL file.
// It appends only new messages since the last save.
func (s *engine) SaveMessages() error {
	return nil
}

// RestoreMessages loads a previously saved message snapshot from JSONL.
// Returns (number_of_messages, error).
func (s *engine) RestoreMessages() (int, error) {
	var msgs []types.AgentMessage
	for _, entry := range s.sessionTree.GetCurrentPath() {
		if entry.Type == session.EntryTypeBranchSummary {
			if msg, ok := branchSummaryMessageFromSessionData(entry.Data); ok {
				msgs = append(msgs, msg)
			}
			continue
		}
		if entry.Type != session.EntryTypeMessage {
			continue
		}
		msg, ok := agentMessageFromSessionData(entry.Data)
		if ok && msg != nil {
			msgs = append(msgs, msg)
		}
	}

	if len(msgs) == 0 {
		return s.migrateOldMessagesJSON()
	}
	s.agent.ReplaceMessages(msgs)
	s.lastSavedIndex = len(msgs)
	return len(msgs), nil
}

func branchSummaryMessageFromSessionData(data any) (types.UserMessage, bool) {
	switch value := data.(type) {
	case session.BranchSummaryData:
		return (&BranchSummaryMessage{Summary: value.Summary, FromID: value.FromID, ToID: value.ToID}).ToLlmMessage(), true
	case map[string]any:
		summary, _ := value["summary"].(string)
		if strings.TrimSpace(summary) == "" {
			return types.UserMessage{}, false
		}
		fromID, _ := value["fromId"].(string)
		toID, _ := value["toId"].(string)
		return (&BranchSummaryMessage{Summary: summary, FromID: fromID, ToID: toID}).ToLlmMessage(), true
	default:
		return types.UserMessage{}, false
	}
}

// migrateOldMessagesJSON handles migration from older single JSON array to new JSONL.
func (s *engine) migrateOldMessagesJSON() (int, error) {
	oldPath := filepath.Join(filepath.Dir(s.messagesFilePath()), "messages.json")
	data, err := os.ReadFile(oldPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	// Read old format
	msgs, err := unmarshalAgentMessages(data)
	if err != nil {
		return 0, err
	}

	if len(msgs) == 0 {
		return 0, nil
	}

	// Set messages to memory and trigger save in new format
	s.agent.ReplaceMessages(msgs)
	s.lastSavedIndex = 0 // force save all
	if err := s.SaveMessages(); err != nil {
		return 0, err
	}

	// Remove old file after successful migration
	_ = os.Remove(oldPath)

	return len(msgs), nil
}

// InputHistoryFile returns the path of the per-project input history file.
func (s *engine) InputHistoryFile() string {
	return filepath.Join(filepath.Dir(s.messagesFilePath()), "input_history")
}

// ClearSavedMessages deletes the messages snapshot for this project.
func (s *engine) ClearSavedMessages() error {
	s.lastSavedIndex = 0
	if s.sessionManager != nil {
		return s.sessionManager.Clear()
	}
	return nil
}

// ClearConversation clears both in-memory and persisted conversation context.
func (s *engine) ClearConversation() error {
	s.agent.Reset()
	s.ctxMgr.ResetUsage()
	return s.ClearSavedMessages()
}

// ── JSON marshaling ──────────────────────────────────────────────────────────

func unmarshalSingleAgentMessage(raw json.RawMessage) (types.AgentMessage, error) {
	var peek struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil, err
	}
	switch peek.Role {
	case "user":
		var m types.UserMessage
		if err := json.Unmarshal(raw, &m); err == nil {
			return m, nil
		}
	case "assistant":
		return unmarshalAssistantMessage(raw)
	case "tool":
		return unmarshalToolResultMessage(raw)
	}
	return nil, fmt.Errorf("unknown role: %s", peek.Role)
}

func agentMessageFromSessionData(data any) (types.AgentMessage, bool) {
	switch v := data.(type) {
	case session.MessageData:
		if msg, ok := v.Content.(types.AgentMessage); ok {
			return msg, true
		}
		switch v.Role {
		case types.RoleUser:
			return types.UserMessage{Role: "user", Content: v.Content}, true
		case types.RoleAssistant:
			if msg, ok := v.Content.(types.AssistantMessage); ok {
				return msg, true
			}
			if msg, ok := v.Content.(*types.AssistantMessage); ok {
				return msg, true
			}
			text, _ := v.Content.(string)
			return types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}}}, true
		case types.RoleToolResult:
			if msg, ok := v.Content.(types.ToolResultMessage); ok {
				return msg, true
			}
			if msg, ok := v.Content.(*types.ToolResultMessage); ok {
				return msg, true
			}
		}
	case map[string]any:
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, false
		}
		msg, err := unmarshalSingleAgentMessage(raw)
		return msg, err == nil
	}
	return nil, false
}

// unmarshalAgentMessages deserializes messages previously produced by old json array format.
func unmarshalAgentMessages(data []byte) ([]types.AgentMessage, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}

	msgs := make([]types.AgentMessage, 0, len(raws))
	for _, raw := range raws {
		msg, err := unmarshalSingleAgentMessage(raw)
		if err == nil && msg != nil {
			msgs = append(msgs, msg)
		}
	}
	return msgs, nil
}

// unmarshalAssistantMessage reconstructs an AssistantMessage from raw JSON,
// dispatching on each content block's "type" field.
func unmarshalAssistantMessage(raw json.RawMessage) (types.AssistantMessage, error) {
	var wire struct {
		Role         string            `json:"role"`
		Content      []json.RawMessage `json:"content"`
		ProviderID   string            `json:"provider,omitempty"`
		Model        string            `json:"model,omitempty"`
		Usage        types.AgentUsage  `json:"usage"`
		StopReason   string            `json:"stopReason,omitempty"`
		ErrorMessage string            `json:"errorMessage,omitempty"`
		Timestamp    int64             `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return types.AssistantMessage{}, err
	}
	blocks, err := unmarshalContentBlocks(wire.Content)
	if err != nil {
		return types.AssistantMessage{}, err
	}
	return types.AssistantMessage{
		Role:         wire.Role,
		Content:      blocks,
		ProviderID:   wire.ProviderID,
		Model:        wire.Model,
		Usage:        wire.Usage,
		StopReason:   wire.StopReason,
		ErrorMessage: wire.ErrorMessage,
		Timestamp:    wire.Timestamp,
	}, nil
}

// unmarshalToolResultMessage reconstructs a ToolResultMessage from raw JSON.
func unmarshalToolResultMessage(raw json.RawMessage) (types.ToolResultMessage, error) {
	var wire struct {
		Role       string            `json:"role"`
		ToolCallID string            `json:"toolCallId"`
		ToolName   string            `json:"toolName"`
		Content    []json.RawMessage `json:"content"`
		Details    any               `json:"details,omitempty"`
		IsError    bool              `json:"isError"`
		Timestamp  int64             `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return types.ToolResultMessage{}, err
	}
	blocks, err := unmarshalContentBlocks(wire.Content)
	if err != nil {
		return types.ToolResultMessage{}, err
	}
	return types.ToolResultMessage{
		Role:       wire.Role,
		ToolCallID: wire.ToolCallID,
		ToolName:   wire.ToolName,
		Content:    blocks,
		Details:    wire.Details,
		IsError:    wire.IsError,
		Timestamp:  wire.Timestamp,
	}, nil
}

// unmarshalContentBlocks dispatches each raw block on the "type" field.
func unmarshalContentBlocks(raws []json.RawMessage) ([]types.ContentBlock, error) {
	blocks := make([]types.ContentBlock, 0, len(raws))
	for _, raw := range raws {
		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &peek); err != nil {
			continue
		}
		switch peek.Type {
		case "text":
			var b types.TextContent
			if err := json.Unmarshal(raw, &b); err == nil {
				blocks = append(blocks, &b)
			}
		case "thinking":
			var b types.ThinkingContent
			if err := json.Unmarshal(raw, &b); err == nil {
				blocks = append(blocks, &b)
			}
		case "toolCall":
			var b types.ToolCallContent
			if err := json.Unmarshal(raw, &b); err == nil {
				blocks = append(blocks, &b)
			}
		case "image":
			var b types.ImageContent
			if err := json.Unmarshal(raw, &b); err == nil {
				blocks = append(blocks, &b)
			}
		}
	}
	return blocks, nil
}
