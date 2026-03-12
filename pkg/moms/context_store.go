package moms

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/types"
)

const (
	// contextJSONLFile is the append-only conversation history file.
	contextJSONLFile = "context.jsonl"
	// contextMetaFile holds per-chat metadata (Skip offset, Count, Summary).
	contextMetaFile = "context.meta.json"
	// maxLineSize is the maximum allowed size of a single JSONL line (10 MB).
	maxLineSize = 10 * 1024 * 1024
)

// contextMeta is persisted alongside the JSONL file.
type contextMeta struct {
	Skip      int       `json:"skip"`
	Count     int       `json:"count"`
	Summary   string    `json:"summary"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ContextStore manages per-chat conversation history using an append-only JSONL
// file. It mirrors PicoClaw's pkg/memory/jsonl.go design:
//
//   - Messages are never physically deleted; instead, a Skip offset in the meta
//     file indicates how many leading lines to ignore when reading history.
//   - TruncateHistory only bumps SkipSkip — it is crash-safe and costs zero I/O
//     on the JSONL file.
//   - Compact rewrites the JSONL file, physically reclaiming disk space.
type ContextStore struct {
	workingDir string
}

// NewContextStore creates a ContextStore rooted at workingDir.
func NewContextStore(workingDir string) *ContextStore {
	return &ContextStore{workingDir: workingDir}
}

func (s *ContextStore) jsonlPath(chatID int64) string {
	return filepath.Join(s.workingDir, fmt.Sprintf("%d", chatID), contextJSONLFile)
}

func (s *ContextStore) metaPath(chatID int64) string {
	return filepath.Join(s.workingDir, fmt.Sprintf("%d", chatID), contextMetaFile)
}

// readMeta loads the meta file. Returns a zero-value if the file does not exist.
func (s *ContextStore) readMeta(chatID int64) (contextMeta, error) {
	data, err := os.ReadFile(s.metaPath(chatID))
	if os.IsNotExist(err) {
		return contextMeta{}, nil
	}
	if err != nil {
		return contextMeta{}, fmt.Errorf("context_store: read meta: %w", err)
	}
	var m contextMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return contextMeta{}, fmt.Errorf("context_store: decode meta: %w", err)
	}
	return m, nil
}

// writeMeta atomically writes the meta file.
func (s *ContextStore) writeMeta(chatID int64, m contextMeta) error {
	m.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("context_store: encode meta: %w", err)
	}
	return writeFileAtomic(s.metaPath(chatID), data)
}

// readMessages reads valid JSON lines from the JSONL file, skipping the first
// `skip` non-empty lines without unmarshaling them (matches PicoClaw's design).
// Corrupt trailing lines (e.g. from a crash) are silently skipped.
func readContextMessages(path string, skip int) ([]agent.AgentMessage, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("context_store: open jsonl: %w", err)
	}
	defer f.Close()

	var msgs []agent.AgentMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	lineNum := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineNum++
		if lineNum <= skip {
			continue
		}
		msg := unmarshalAgentMessage(line)
		if msg != nil {
			msgs = append(msgs, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("context_store: scan jsonl: %w", err)
	}
	return msgs, nil
}

// countJSONLLines counts non-empty lines in a JSONL file without unmarshaling.
func countJSONLLines(path string) (int, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("context_store: open jsonl for count: %w", err)
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			n++
		}
	}
	return n, scanner.Err()
}

func decodeBlocks(raw []json.RawMessage) []types.ContentBlock {
	var blocks []types.ContentBlock
	for _, r := range raw {
		var bType struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(r, &bType); err != nil {
			continue
		}
		switch bType.Type {
		case "text":
			var b types.TextContent
			if err := json.Unmarshal(r, &b); err == nil {
				blocks = append(blocks, &b)
			}
		case "thinking":
			var b types.ThinkingContent
			if err := json.Unmarshal(r, &b); err == nil {
				blocks = append(blocks, &b)
			}
		case "toolCall":
			var b types.ToolCallContent
			if err := json.Unmarshal(r, &b); err == nil {
				blocks = append(blocks, &b)
			}
		case "image":
			var b types.ImageContent
			if err := json.Unmarshal(r, &b); err == nil {
				blocks = append(blocks, &b)
			}
		}
	}
	return blocks
}

// unmarshalAgentMessage dispatches JSON to the correct concrete type by `role`.
func unmarshalAgentMessage(data []byte) agent.AgentMessage {
	var base struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data, &base); err != nil {
		return nil
	}
	switch base.Role {
	case "user":
		var m types.UserMessage
		if err := json.Unmarshal(data, &m); err == nil {
			return m
		}
	case "assistant":
		var raw struct {
			Role         string            `json:"role"`
			Content      []json.RawMessage `json:"content"`
			ProviderID   string            `json:"provider,omitempty"`
			Model        string            `json:"model,omitempty"`
			Usage        types.AgentUsage  `json:"usage"`
			StopReason   types.StopReason  `json:"stopReason,omitempty"`
			ErrorMessage string            `json:"errorMessage,omitempty"`
			Timestamp    int64             `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &raw); err == nil {
			return types.AssistantMessage{
				Role:         raw.Role,
				Content:      decodeBlocks(raw.Content),
				ProviderID:   raw.ProviderID,
				Model:        raw.Model,
				Usage:        raw.Usage,
				StopReason:   raw.StopReason,
				ErrorMessage: raw.ErrorMessage,
				Timestamp:    raw.Timestamp,
			}
		}
	case "tool", "toolResult":
		var raw struct {
			Role       string            `json:"role"`
			ToolCallID string            `json:"toolCallId"`
			ToolName   string            `json:"toolName"`
			Content    []json.RawMessage `json:"content"`
			Details    any               `json:"details,omitempty"`
			IsError    bool              `json:"isError"`
			Timestamp  int64             `json:"timestamp"`
		}
		if err := json.Unmarshal(data, &raw); err == nil {
			return types.ToolResultMessage{
				Role:       raw.Role,
				ToolCallID: raw.ToolCallID,
				ToolName:   raw.ToolName,
				Content:    decodeBlocks(raw.Content),
				Details:    raw.Details,
				IsError:    raw.IsError,
				Timestamp:  raw.Timestamp,
			}
		}
	}
	return nil
}

// writeFileAtomic writes data to path atomically via a temp file + rename.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("context_store: mkdir: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("context_store: open tmp: %w", err)
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("context_store: write tmp: %w", writeErr)
	}
	if syncErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("context_store: sync tmp: %w", syncErr)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("context_store: close tmp: %w", closeErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("context_store: rename: %w", err)
	}
	return nil
}

// ── Public API ────────────────────────────────────────────────────────────────

// AddMessage appends a single message to the JSONL file and updates the count.
// This is the primary write path. It is append-only, so it is crash-safe.
func (s *ContextStore) AddMessage(chatID int64, msg agent.AgentMessage) error {
	line, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("context_store: marshal message: %w", err)
	}
	line = append(line, '\n')

	dir := filepath.Join(s.workingDir, fmt.Sprintf("%d", chatID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("context_store: mkdir: %w", err)
	}

	f, err := os.OpenFile(s.jsonlPath(chatID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("context_store: open jsonl for append: %w", err)
	}
	_, writeErr := f.Write(line)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("context_store: append message: %w", writeErr)
	}
	if syncErr != nil {
		return fmt.Errorf("context_store: sync jsonl: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("context_store: close jsonl: %w", closeErr)
	}

	meta, err := s.readMeta(chatID)
	if err != nil {
		return err
	}
	meta.Count++
	return s.writeMeta(chatID, meta)
}

// GetHistory returns all active (non-skipped) messages for chatID.
func (s *ContextStore) GetHistory(chatID int64) ([]agent.AgentMessage, error) {
	meta, err := s.readMeta(chatID)
	if err != nil {
		return nil, err
	}
	msgs, err := readContextMessages(s.jsonlPath(chatID), meta.Skip)
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

// GetSummary returns the latest summary string for chatID.
func (s *ContextStore) GetSummary(chatID int64) (string, error) {
	meta, err := s.readMeta(chatID)
	if err != nil {
		return "", err
	}
	return meta.Summary, nil
}

// SetSummary persists a new summary for chatID.
func (s *ContextStore) SetSummary(chatID int64, summary string) error {
	meta, err := s.readMeta(chatID)
	if err != nil {
		return err
	}
	meta.Summary = summary
	return s.writeMeta(chatID, meta)
}

// TruncateHistory moves the Skip pointer forward so that only the last keepLast
// messages are visible. No data is removed from disk — this is crash-safe.
func (s *ContextStore) TruncateHistory(chatID int64, keepLast int) error {
	meta, err := s.readMeta(chatID)
	if err != nil {
		return err
	}

	// Reconcile meta.Count with the real line count on disk
	// (a crash between JSONL append and meta update leaves them out of sync).
	n, err := countJSONLLines(s.jsonlPath(chatID))
	if err != nil {
		return err
	}
	meta.Count = n

	if keepLast <= 0 {
		meta.Skip = meta.Count
	} else {
		effective := meta.Count - meta.Skip
		if keepLast < effective {
			meta.Skip = meta.Count - keepLast
		}
	}
	return s.writeMeta(chatID, meta)
}

// SetHistory atomically replaces the entire conversation history.
// Used by force compression where we need to rewrite the whole context.
// Writes meta first (crash-safe: reads will see old content with Skip=0 if
// rename fails after meta write).
func (s *ContextStore) SetHistory(chatID int64, msgs []agent.AgentMessage) error {
	meta, err := s.readMeta(chatID)
	if err != nil {
		return err
	}
	meta.Skip = 0
	meta.Count = len(msgs)
	if err := s.writeMeta(chatID, meta); err != nil {
		return err
	}
	return s.rewriteJSONL(chatID, msgs)
}

// Compact physically rewrites the JSONL file to remove all logically skipped
// lines. It is safe to call at any time; if skip==0 it returns immediately.
func (s *ContextStore) Compact(chatID int64) error {
	meta, err := s.readMeta(chatID)
	if err != nil {
		return err
	}
	if meta.Skip == 0 {
		return nil
	}

	active, err := readContextMessages(s.jsonlPath(chatID), meta.Skip)
	if err != nil {
		return err
	}

	// Write meta BEFORE rewriting — see SetHistory for crash-safety rationale.
	meta.Skip = 0
	meta.Count = len(active)
	if err := s.writeMeta(chatID, meta); err != nil {
		return err
	}
	return s.rewriteJSONL(chatID, active)
}

// rewriteJSONL atomically replaces the JSONL file with the given messages.
func (s *ContextStore) rewriteJSONL(chatID int64, msgs []agent.AgentMessage) error {
	var buf bytes.Buffer
	for _, msg := range msgs {
		line, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("context_store: marshal message during rewrite: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeFileAtomic(s.jsonlPath(chatID), buf.Bytes())
}
