package coding_agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// SessionHeader matches pi-mono's session header format.
type SessionHeader struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
}

// SessionMessageEntry matches pi-mono's message entry format.
type SessionMessageEntry struct {
	Type      string      `json:"type"`
	ID        string      `json:"id"`
	ParentID  string      `json:"parentId"`
	Timestamp string      `json:"timestamp"`
	Message   interface{} `json:"message"`
}

// messagesFilePath returns the path of the per-project messages snapshot.
// We use JSONL format to align with pi-mono.
func (s *CodingSession) messagesFilePath() string {
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
func (s *CodingSession) SaveMessages() error {
	msgs := s.agent.GetState().Messages
	if len(msgs) <= s.lastSavedIndex {
		return nil // Nothing new to save
	}

	path := s.messagesFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Check if file exists, if not, write header
	var file *os.File
	var err error
	if _, errStat := os.Stat(path); os.IsNotExist(errStat) {
		file, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}

		header := SessionHeader{
			Type:      "session",
			Version:   3,
			ID:        uuid.New().String(),
			Timestamp: time.Now().Format(time.RFC3339),
			Cwd:       s.cwd,
		}
		b, _ := json.Marshal(header)
		file.Write(b)
		file.WriteString("\n")
	} else {
		file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
	}
	defer file.Close()

	// Append new messages
	for i := s.lastSavedIndex; i < len(msgs); i++ {
		msg := msgs[i]
		if isTransientContextMessage(msg) {
			continue
		}

		entry := SessionMessageEntry{
			Type:      "message",
			ID:        uuid.New().String(),
			ParentID:  "", // keeping linear for now
			Timestamp: time.Now().Format(time.RFC3339),
			Message:   msg,
		}
		b, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("marshal entry: %w", err)
		}
		if _, err := file.Write(b); err != nil {
			return err
		}
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}

	s.lastSavedIndex = len(msgs)
	return nil
}

// RestoreMessages loads a previously saved message snapshot from JSONL.
// Returns (number_of_messages, error).
func (s *CodingSession) RestoreMessages() (int, error) {
	path := s.messagesFilePath()
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Try to migrate from old messages.json if it exists
			return s.migrateOldMessagesJSON()
		}
		return 0, err
	}
	defer file.Close()

	var msgs []agent.AgentMessage
	scanner := bufio.NewScanner(file)

	// Increase scanner buffer size to handle large messages
	const maxCapacity = 10 * 1024 * 1024 // 10MB
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &peek); err != nil {
			continue
		}

		if peek.Type == "message" {
			var entry struct {
				Message json.RawMessage `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &entry); err == nil {
				msg, err := unmarshalSingleAgentMessage(entry.Message)
				if err == nil && msg != nil {
					msgs = append(msgs, msg)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}

	if len(msgs) == 0 {
		return 0, nil
	}
	s.agent.ReplaceMessages(msgs)
	s.lastSavedIndex = len(msgs)
	return len(msgs), nil
}

// migrateOldMessagesJSON handles migration from older single JSON array to new JSONL.
func (s *CodingSession) migrateOldMessagesJSON() (int, error) {
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
func (s *CodingSession) InputHistoryFile() string {
	return filepath.Join(filepath.Dir(s.messagesFilePath()), "input_history")
}

// ClearSavedMessages deletes the messages snapshot for this project.
func (s *CodingSession) ClearSavedMessages() error {
	s.lastSavedIndex = 0
	err := os.Remove(s.messagesFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ── JSON marshaling ──────────────────────────────────────────────────────────

func unmarshalSingleAgentMessage(raw json.RawMessage) (agent.AgentMessage, error) {
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

// unmarshalAgentMessages deserializes messages previously produced by old json array format.
func unmarshalAgentMessages(data []byte) ([]agent.AgentMessage, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}

	msgs := make([]agent.AgentMessage, 0, len(raws))
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
