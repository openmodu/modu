package moms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/types"
)

// SyncLogToMessages reads log.jsonl and returns unseen user messages as types.UserMessage,
// excluding the message with excludeTs (which will be added via Prompt()).
func SyncLogToMessages(chatDir string, existingMessages []types.AgentMessage, excludeTs string) []types.UserMessage {
	logPath := filepath.Join(chatDir, "log.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}

	// Build set of already-seen message texts from the existing context.
	seen := make(map[string]struct{})
	for _, m := range existingMessages {
		um, ok := m.(types.UserMessage)
		if !ok {
			continue
		}
		text := extractText(um.Content)
		// Strip timestamp prefix: "[username]: text" -> just track the core text
		normalized := normalizeMessageText(text)
		seen[normalized] = struct{}{}
	}

	type candidate struct {
		ts      int64
		message types.UserMessage
		key     string
	}
	var candidates []candidate

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var msg LoggedMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Ts == "" || msg.IsBot {
			continue
		}
		if excludeTs != "" && msg.Ts == excludeTs {
			continue
		}

		text := fmt.Sprintf("[%s]: %s", coalesce(msg.UserName, msg.User, "unknown"), msg.Text)
		key := normalizeMessageText(text)
		if _, exists := seen[key]; exists {
			continue
		}

		ts := parseTs(msg.Date)

		candidates = append(candidates, candidate{
			ts:  ts,
			key: key,
			message: types.UserMessage{
				Role:      "user",
				Content:   text,
				Timestamp: ts,
			},
		})
		seen[key] = struct{}{}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ts < candidates[j].ts
	})

	out := make([]types.UserMessage, len(candidates))
	for i, c := range candidates {
		out[i] = c.message
	}
	return out
}

// -----------------------------------------------------------------------
// helpers

func extractText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []types.ContentBlock:
		for _, b := range v {
			if tc, ok := b.(*types.TextContent); ok {
				return tc.Text
			}
		}
	}
	return fmt.Sprintf("%v", content)
}

var timestampPrefixReplacer = strings.NewReplacer()

func normalizeMessageText(s string) string {
	// Strip optional leading timestamp: "[YYYY-MM-DD HH:MM:SS+HH:MM] "
	if len(s) > 0 && s[0] == '[' {
		if idx := strings.Index(s, "] "); idx > 0 && idx < 30 {
			s = s[idx+2:]
		}
	}
	// Strip attachments section
	if idx := strings.Index(s, "\n\n<attachments>"); idx >= 0 {
		s = s[:idx]
	}
	return s
}

func coalesce(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseTs(dateStr string) int64 {
	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return time.Now().UnixMilli()
	}
	return t.UnixMilli()
}

// SaveContextMessages serializes messages to context.jsonl.
func SaveContextMessages(chatDir string, messages []types.AgentMessage) error {
	path := filepath.Join(chatDir, "context.jsonl")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range messages {
		if err := enc.Encode(m); err != nil {
			return err
		}
	}
	return nil
}
