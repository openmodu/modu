package coding_agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/session"
	"github.com/openmodu/modu/pkg/types"
)

func writeSubagentSessionFile(path, cwd, parentSession, id string, messages []agent.AgentMessage) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	header := session.Header{
		Type:          "session",
		Version:       session.CurrentSessionVersion,
		ID:            id,
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Cwd:           cwd,
		ParentSession: parentSession,
	}
	if err := enc.Encode(header); err != nil {
		_ = f.Close()
		return err
	}
	parentID := ""
	for _, msg := range messages {
		entry := session.NewEntry(session.EntryTypeMessage, parentID, msg)
		if err := enc.Encode(entry); err != nil {
			_ = f.Close()
			return err
		}
		parentID = entry.ID
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadSubagentSessionMessages(path string) ([]agent.AgentMessage, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []agent.AgentMessage
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		msg, ok := parseSubagentSessionLine(scanner.Bytes())
		if ok {
			messages = append(messages, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func parseSubagentSessionLine(data []byte) (agent.AgentMessage, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false
	}
	var typ string
	_ = json.Unmarshal(raw["type"], &typ)
	if typ != string(session.EntryTypeMessage) {
		return nil, false
	}
	var msg struct {
		Role         string          `json:"role"`
		Content      json.RawMessage `json:"content"`
		ProviderID   string          `json:"provider"`
		Model        string          `json:"model"`
		StopReason   string          `json:"stopReason"`
		Timestamp    int64           `json:"timestamp"`
		ToolCallID   string          `json:"toolCallId"`
		ToolName     string          `json:"toolName"`
		IsError      bool            `json:"isError"`
		Details      json.RawMessage `json:"details"`
		ErrorMessage string          `json:"errorMessage"`
	}
	if err := json.Unmarshal(raw["message"], &msg); err != nil {
		return nil, false
	}
	switch msg.Role {
	case "user":
		var content any
		if len(msg.Content) > 0 {
			_ = json.Unmarshal(msg.Content, &content)
		}
		return types.UserMessage{Role: "user", Content: content, Timestamp: msg.Timestamp}, true
	case "assistant":
		blocks := parseContentBlocks(msg.Content)
		return &types.AssistantMessage{
			Role:         "assistant",
			Content:      blocks,
			ProviderID:   msg.ProviderID,
			Model:        msg.Model,
			StopReason:   msg.StopReason,
			ErrorMessage: msg.ErrorMessage,
			Timestamp:    msg.Timestamp,
		}, true
	case "toolResult":
		return types.ToolResultMessage{
			Role:       "toolResult",
			ToolCallID: msg.ToolCallID,
			ToolName:   msg.ToolName,
			Content:    parseContentBlocks(msg.Content),
			Details:    rawJSONValue(msg.Details),
			IsError:    msg.IsError,
			Timestamp:  msg.Timestamp,
		}, true
	default:
		return nil, false
	}
}

func parseContentBlocks(raw json.RawMessage) []types.ContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil && text != "" {
			return []types.ContentBlock{&types.TextContent{Type: "text", Text: text}}
		}
		return nil
	}
	blocks := make([]types.ContentBlock, 0, len(items))
	for _, item := range items {
		var typ string
		_ = json.Unmarshal(item["type"], &typ)
		switch typ {
		case "text":
			var block types.TextContent
			_ = json.Unmarshal(mustMarshal(item), &block)
			blocks = append(blocks, &block)
		case "thinking":
			var block types.ThinkingContent
			_ = json.Unmarshal(mustMarshal(item), &block)
			blocks = append(blocks, &block)
		case "tool_call", "tool_use":
			var block types.ToolCallContent
			_ = json.Unmarshal(mustMarshal(item), &block)
			blocks = append(blocks, &block)
		}
	}
	return blocks
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

func mustMarshal(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
