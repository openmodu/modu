package moms

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
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

// getMessageContent extracts the content payload from an AgentMessage.
func getMessageContent(m types.AgentMessage) any {
	switch v := m.(type) {
	case types.UserMessage:
		return v.Content
	case types.AssistantMessage:
		return v.Content
	case types.ToolResultMessage:
		return v.Content
	}
	return ""
}

// EstimateTokens provides a rough estimate of the number of tokens in the messages.
// It uses a conservative heuristic (2.5 characters per token) to account for CJK.
func EstimateTokens(messages []types.AgentMessage) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += len([]rune(extractText(getMessageContent(m))))
	}
	return totalChars * 2 / 5
}

// CompactContext drops the oldest 50% of messages if the estimated token count exceeds maxTokens.
// It preserves the first message (usually the system prompt) and the most recent messages.
func CompactContext(messages []types.AgentMessage, maxTokens int) []types.AgentMessage {
	if len(messages) <= 4 {
		return messages
	}

	if EstimateTokens(messages) <= maxTokens {
		return messages
	}

	// First message is typically the System prompt or initial context.
	// We want to drop the oldest half of the *subsequent* conversation.
	conversation := messages[1 : len(messages)-1]
	if len(conversation) == 0 {
		return messages
	}

	mid := len(conversation) / 2
	droppedCount := mid
	keptConversation := conversation[mid:]

	newMessages := make([]types.AgentMessage, 0, 1+1+len(keptConversation)+1)

	// Keep the first message
	newMessages = append(newMessages, messages[0])

	// Inject a system note about compression as a generic UserMessage
	note := types.UserMessage{
		Role:      "user",
		Content:   fmt.Sprintf("[System Note: Emergency compression dropped %d oldest messages due to context limit]", droppedCount),
		Timestamp: time.Now().UnixMilli(),
	}
	newMessages = append(newMessages, note)

	newMessages = append(newMessages, keptConversation...)
	newMessages = append(newMessages, messages[len(messages)-1]) // Last message

	return newMessages
}

// sanitizeHistory cleans the loaded message history before passing it to the
// LLM. Mirrors picoclaw's sanitizeHistoryForProvider exactly:
//
//  1. Drop system messages (system prompt is always rebuilt fresh each turn).
//  2. Drop orphaned tool-result messages that have no preceding assistant
//     message with tool calls.
//  3. Drop assistant tool-call turns that appear at the start of history or
//     whose immediate predecessor is not a user / tool-result message.
//  4. (Second pass) Drop any assistant-with-tool-calls message whose following
//     tool results are incomplete, along with those partial tool results.
//     This removes dangling tool calls from sessions interrupted mid-turn.
func sanitizeHistory(msgs []agent.AgentMessage) []agent.AgentMessage {
	if len(msgs) == 0 {
		return msgs
	}

	// ── First pass ──────────────────────────────────────────────────────────
	sanitized := make([]agent.AgentMessage, 0, len(msgs))
	for _, m := range msgs {
		switch msg := m.(type) {
		case types.UserMessage:
			sanitized = append(sanitized, msg)

		case types.AssistantMessage:
			if assistantHasToolCalls(msg) {
				if len(sanitized) == 0 {
					fmt.Println("[moms/sanitize] dropping assistant tool-call turn at history start")
					continue
				}
				prevRole := messageRole(sanitized[len(sanitized)-1])
				if prevRole != "user" && prevRole != "tool" && prevRole != "toolResult" {
					fmt.Printf("[moms/sanitize] dropping assistant tool-call turn: invalid predecessor role=%q\n", prevRole)
					continue
				}
			}
			sanitized = append(sanitized, msg)

		case types.ToolResultMessage:
			if len(sanitized) == 0 {
				fmt.Println("[moms/sanitize] dropping orphaned leading tool-result")
				continue
			}
			// Walk backwards over any preceding tool-result messages to find
			// the nearest assistant message that issued tool calls.
			foundAssistant := false
			for i := len(sanitized) - 1; i >= 0; i-- {
				r := messageRole(sanitized[i])
				if r == "tool" || r == "toolResult" {
					continue
				}
				if am, ok := sanitized[i].(types.AssistantMessage); ok && assistantHasToolCalls(am) {
					foundAssistant = true
				}
				break
			}
			if !foundAssistant {
				fmt.Printf("[moms/sanitize] dropping orphaned tool-result id=%q\n", msg.ToolCallID)
				continue
			}
			sanitized = append(sanitized, msg)

		default:
			sanitized = append(sanitized, m)
		}
	}

	// ── Second pass ──────────────────────────────────────────────────────────
	// Every assistant message with tool calls must be followed by a complete
	// set of tool-result messages. If any tool-call ID is missing, drop the
	// entire group (assistant + partial tool results).
	final := make([]agent.AgentMessage, 0, len(sanitized))
	for i := 0; i < len(sanitized); i++ {
		am, ok := sanitized[i].(types.AssistantMessage)
		if !ok || !assistantHasToolCalls(am) {
			final = append(final, sanitized[i])
			continue
		}

		expected := make(map[string]bool)
		for _, block := range am.Content {
			if tc, ok := block.(*types.ToolCallContent); ok {
				expected[tc.ID] = false
			}
		}

		toolMsgCount := 0
		for j := i + 1; j < len(sanitized); j++ {
			tr, ok := sanitized[j].(types.ToolResultMessage)
			if !ok {
				break
			}
			toolMsgCount++
			if _, exists := expected[tr.ToolCallID]; exists {
				expected[tr.ToolCallID] = true
			}
		}

		allFound := true
		for id, found := range expected {
			if !found {
				allFound = false
				fmt.Printf("[moms/sanitize] dropping incomplete tool-call group: missing id=%q expected=%d found=%d\n",
					id, len(expected), toolMsgCount)
				break
			}
		}
		if !allFound {
			i += toolMsgCount
			continue
		}
		final = append(final, sanitized[i])
	}

	return final
}

// assistantHasToolCalls reports whether an AssistantMessage contains any ToolCallContent blocks.
func assistantHasToolCalls(am types.AssistantMessage) bool {
	for _, block := range am.Content {
		if _, ok := block.(*types.ToolCallContent); ok {
			return true
		}
	}
	return false
}

// messageRole returns the role string for any AgentMessage.
func messageRole(m agent.AgentMessage) string {
	switch msg := m.(type) {
	case types.UserMessage:
		return msg.Role
	case types.AssistantMessage:
		return msg.Role
	case types.ToolResultMessage:
		return msg.Role
	}
	return ""
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
