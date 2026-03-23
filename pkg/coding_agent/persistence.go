package coding_agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

// messagesFilePath returns the path of the per-project messages snapshot.
// The directory name is derived from the cwd so it is human-readable, e.g.
// ~/.coding_agent/sessions/Users_alice_Code_myproject/messages.json
func (s *CodingSession) messagesFilePath() string {
	// Convert absolute path to a safe directory name: strip leading slash,
	// replace remaining slashes with underscores.
	dirName := strings.ReplaceAll(strings.TrimPrefix(s.cwd, "/"), "/", "_")
	if dirName == "" {
		dirName = "root"
	}
	return filepath.Join(s.agentDir, "sessions", dirName, "messages.json")
}

// SaveMessages writes the current conversation messages to a JSON file.
// Called after each successful Prompt so the session survives restarts.
func (s *CodingSession) SaveMessages() error {
	msgs := s.agent.GetState().Messages
	data, err := marshalAgentMessages(msgs)
	if err != nil {
		return err
	}
	path := s.messagesFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// RestoreMessages loads a previously saved message snapshot and replaces the
// current (empty) conversation. Returns (0, nil) when there is no snapshot.
func (s *CodingSession) RestoreMessages() (int, error) {
	path := s.messagesFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	msgs, err := unmarshalAgentMessages(data)
	if err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 0, nil
	}
	s.agent.ReplaceMessages(msgs)
	return len(msgs), nil
}

// ClearSavedMessages deletes the messages snapshot for this project.
func (s *CodingSession) ClearSavedMessages() error {
	err := os.Remove(s.messagesFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ── JSON marshaling ──────────────────────────────────────────────────────────

// marshalAgentMessages serializes a slice of agent messages to JSON.
// Each element is serialized by its concrete type; the "role" discriminator
// is used by unmarshalAgentMessages to reconstruct the right type.
func marshalAgentMessages(msgs []agent.AgentMessage) ([]byte, error) {
	raws := make([]json.RawMessage, 0, len(msgs))
	for _, msg := range msgs {
		b, err := json.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("marshal message: %w", err)
		}
		raws = append(raws, b)
	}
	return json.Marshal(raws)
}

// unmarshalAgentMessages deserializes messages previously produced by
// marshalAgentMessages.  It dispatches on the "role" field to reconstruct
// UserMessage, AssistantMessage, or ToolResultMessage.
func unmarshalAgentMessages(data []byte) ([]agent.AgentMessage, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}

	msgs := make([]agent.AgentMessage, 0, len(raws))
	for _, raw := range raws {
		var peek struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(raw, &peek); err != nil {
			continue
		}
		switch peek.Role {
		case "user":
			var m types.UserMessage
			if err := json.Unmarshal(raw, &m); err == nil {
				msgs = append(msgs, m)
			}
		case "assistant":
			m, err := unmarshalAssistantMessage(raw)
			if err == nil {
				msgs = append(msgs, m)
			}
		case "tool":
			m, err := unmarshalToolResultMessage(raw)
			if err == nil {
				msgs = append(msgs, m)
			}
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
