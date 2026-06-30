package compaction

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/openmodu/modu/pkg/types"
)

var (
	readFilesRegex     = regexp.MustCompile(`(?s)<read-files>\n(.*?)\n</read-files>`)
	modifiedFilesRegex = regexp.MustCompile(`(?s)<modified-files>\n(.*?)\n</modified-files>`)
)

// Options configures the compaction process.
type Options struct {
	// PreserveRecent is the number of recent messages to keep unchanged.
	PreserveRecent int
	// PreserveUserMessagesTokens is an approximate token budget for keeping
	// recent user messages from the compacted range. Zero uses the default;
	// negative disables this preservation.
	PreserveUserMessagesTokens int
	// Model is the LLM model to use for generating summaries.
	Model *types.Model
	// GetAPIKey retrieves an API key for the given provider.
	GetAPIKey func(provider string) (string, error)
	// StreamFn creates an LLM stream for summary generation.
	StreamFn types.StreamFn
}

// Result holds the outcome of a compaction operation.
type Result struct {
	Summary               string
	OriginalCount         int
	NewCount              int
	PreservedUserMessages int
	ReadFiles             []string
	ModifiedFiles         []string
	Messages              []types.AgentMessage
}

const compactionPrompt = `You are summarizing a conversation between a user and a coding assistant. Create a concise summary that captures:

1. The main tasks or goals discussed
2. Key decisions made
3. Important file paths, function names, or code structures mentioned
4. Current state of any in-progress work
5. Any unresolved issues or next steps

Be specific about technical details. The summary will be used to continue the conversation, so include enough context for the assistant to pick up where it left off.

Format as a structured summary with clear sections.`

const previousConversationSummaryPrefix = "[Previous Conversation Summary]"

const (
	defaultPreserveUserMessagesTokens = 1024
	defaultPreserveUserMessagesCount  = 2
	approxCharsPerToken               = 4
	preservedUserMessageTruncated     = "\n\n...[truncated for compaction budget]"
)

// WouldCompact reports whether Compact would reduce this message slice.
func WouldCompact(messages []types.AgentMessage, preserveRecent int) bool {
	if len(messages) == 0 {
		return false
	}
	preserve := preserveRecent
	if preserve <= 0 {
		preserve = 4
	}
	return len(messages) > preserve
}

// ExtractFileOperations inspects messages to find which files were read or modified
// using tools, and recursively extracts these from past summary blocks.
func ExtractFileOperations(messages []types.AgentMessage) ([]string, []string) {
	readSet := make(map[string]bool)
	modifiedSet := make(map[string]bool)

	for _, msg := range messages {
		switch m := msg.(type) {
		case types.AssistantMessage:
			for _, block := range m.Content {
				if tc, ok := block.(*types.ToolCallContent); ok {
					if pathRaw, has := tc.Arguments["path"]; has {
						if pathStr, ok := pathRaw.(string); ok {
							switch tc.Name {
							case "read":
								readSet[pathStr] = true
							case "write", "edit":
								modifiedSet[pathStr] = true
							}
						}
					}
				}
			}
		case *types.AssistantMessage:
			for _, block := range m.Content {
				if tc, ok := block.(*types.ToolCallContent); ok {
					if pathRaw, has := tc.Arguments["path"]; has {
						if pathStr, ok := pathRaw.(string); ok {
							switch tc.Name {
							case "read":
								readSet[pathStr] = true
							case "write", "edit":
								modifiedSet[pathStr] = true
							}
						}
					}
				}
			}
		case types.UserMessage:
			if blocks, ok := m.Content.([]types.ContentBlock); ok {
				for _, block := range blocks {
					if tc, ok := block.(*types.TextContent); ok {
						parseUserMessageText(&readSet, &modifiedSet, tc.Text)
					}
				}
			} else if str, ok := m.Content.(string); ok {
				parseUserMessageText(&readSet, &modifiedSet, str)
			}
		case *types.UserMessage:
			if blocks, ok := m.Content.([]types.ContentBlock); ok {
				for _, block := range blocks {
					if tc, ok := block.(*types.TextContent); ok {
						parseUserMessageText(&readSet, &modifiedSet, tc.Text)
					}
				}
			} else if str, ok := m.Content.(string); ok {
				parseUserMessageText(&readSet, &modifiedSet, str)
			}
		}
	}

	var readFiles, modifiedFiles []string
	for k := range readSet {
		readFiles = append(readFiles, k)
	}
	for k := range modifiedSet {
		modifiedFiles = append(modifiedFiles, k)
	}

	sort.Strings(readFiles)
	sort.Strings(modifiedFiles)

	return readFiles, modifiedFiles
}

func parseUserMessageText(readSet, modifiedSet *map[string]bool, text string) {
	if !strings.HasPrefix(text, previousConversationSummaryPrefix) {
		return
	}
	if matches := readFilesRegex.FindStringSubmatch(text); len(matches) > 1 {
		for _, f := range strings.Split(matches[1], "\n") {
			f = strings.TrimSpace(f)
			if f != "" {
				(*readSet)[f] = true
			}
		}
	}
	if matches := modifiedFilesRegex.FindStringSubmatch(text); len(matches) > 1 {
		for _, f := range strings.Split(matches[1], "\n") {
			f = strings.TrimSpace(f)
			if f != "" {
				(*modifiedSet)[f] = true
			}
		}
	}
}

// Compact compresses older messages into a summary while preserving recent messages.
func Compact(ctx context.Context, messages []types.AgentMessage, opts Options) (*Result, error) {
	if len(messages) == 0 {
		return &Result{Messages: messages}, nil
	}

	if !WouldCompact(messages, opts.PreserveRecent) {
		return &Result{
			Messages:      messages,
			OriginalCount: len(messages),
			NewCount:      len(messages),
		}, nil
	}

	preserve := opts.PreserveRecent
	if preserve <= 0 {
		preserve = 4
	}

	// Split messages into those to compact and those to preserve
	toCompact := messages[:len(messages)-preserve]
	toPreserve := messages[len(messages)-preserve:]
	priorSummary, summaryFreeMessages := flattenPreviousSummaries(toCompact)
	preservedUserMessages := recentUserMessagesWithinBudget(summaryFreeMessages, opts.PreserveUserMessagesTokens)

	// Build a text representation of messages to compact
	var sb strings.Builder
	for _, msg := range summaryFreeMessages {
		sb.WriteString(formatMessageForSummary(msg))
		sb.WriteString("\n\n")
	}

	// Generate summary using LLM
	summary, err := generateSummary(ctx, priorSummary, sb.String(), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate compaction summary: %w", err)
	}

	// Extract tracked files
	readFiles, modifiedFiles := ExtractFileOperations(toCompact)

	// Combine summary with tracked files
	var parts []string
	parts = append(parts, previousConversationSummaryPrefix+"\n\n"+summary)
	if len(readFiles) > 0 {
		parts = append(parts, fmt.Sprintf("<read-files>\n%s\n</read-files>", strings.Join(readFiles, "\n")))
	}
	if len(modifiedFiles) > 0 {
		parts = append(parts, fmt.Sprintf("<modified-files>\n%s\n</modified-files>", strings.Join(modifiedFiles, "\n")))
	}

	// Build new message list: summary + selected user anchors + preserved messages
	summaryMsg := types.UserMessage{
		Role: "user",
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: strings.Join(parts, "\n\n"),
			},
		},
	}

	newMessages := make([]types.AgentMessage, 0, 1+len(preservedUserMessages)+len(toPreserve))
	newMessages = append(newMessages, summaryMsg)
	newMessages = append(newMessages, preservedUserMessages...)
	newMessages = append(newMessages, toPreserve...)

	return &Result{
		Summary:               summary,
		OriginalCount:         len(messages),
		NewCount:              len(newMessages),
		PreservedUserMessages: len(preservedUserMessages),
		ReadFiles:             readFiles,
		ModifiedFiles:         modifiedFiles,
		Messages:              newMessages,
	}, nil
}

func recentUserMessagesWithinBudget(messages []types.AgentMessage, maxTokens int) []types.AgentMessage {
	if maxTokens == 0 {
		maxTokens = defaultPreserveUserMessagesTokens
	}
	if maxTokens < 0 {
		return nil
	}
	maxMessages := min(defaultPreserveUserMessagesCount, len(messages)-2)
	if maxMessages <= 0 {
		return nil
	}

	remaining := maxTokens
	selected := make([]types.AgentMessage, 0)
	for i := len(messages) - 1; i >= 0 && remaining > 0 && len(selected) < maxMessages; i-- {
		text, ok := userMessageText(messages[i])
		if !ok {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" || strings.HasPrefix(text, previousConversationSummaryPrefix) {
			continue
		}

		tokens := approximateTextTokens(text)
		if tokens > remaining {
			text = truncateTextToApproxTokens(text, remaining)
			if text == "" {
				break
			}
			selected = append(selected, canonicalUserMessage(messages[i], text))
			break
		}

		selected = append(selected, canonicalUserMessage(messages[i], text))
		remaining -= tokens
	}

	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected
}

func userMessageText(msg types.AgentMessage) (string, bool) {
	switch m := msg.(type) {
	case types.UserMessage:
		return userContentText(m.Content)
	case *types.UserMessage:
		return userContentText(m.Content)
	default:
		return "", false
	}
}

func userContentText(content any) (string, bool) {
	switch c := content.(type) {
	case string:
		return c, true
	case []types.ContentBlock:
		parts := make([]string, 0, len(c))
		for _, block := range c {
			if tc, ok := block.(*types.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		if len(parts) == 0 {
			return "", false
		}
		return strings.Join(parts, "\n\n"), true
	default:
		return "", false
	}
}

func canonicalUserMessage(original types.AgentMessage, text string) types.UserMessage {
	msg := types.UserMessage{
		Role: "user",
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: text},
		},
	}
	switch m := original.(type) {
	case types.UserMessage:
		if m.Role != "" {
			msg.Role = m.Role
		}
		msg.Timestamp = m.Timestamp
	case *types.UserMessage:
		if m.Role != "" {
			msg.Role = m.Role
		}
		msg.Timestamp = m.Timestamp
	}
	return msg
}

func approximateTextTokens(text string) int {
	runes := len([]rune(text))
	if runes == 0 {
		return 0
	}
	return (runes + approxCharsPerToken - 1) / approxCharsPerToken
}

func truncateTextToApproxTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	maxRunes := maxTokens * approxCharsPerToken
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return strings.TrimSpace(text)
	}

	suffixRunes := []rune(preservedUserMessageTruncated)
	if maxRunes <= len(suffixRunes) {
		return ""
	}
	keep := maxRunes - len(suffixRunes)
	return strings.TrimSpace(string(runes[:keep])) + preservedUserMessageTruncated
}

func generateSummary(ctx context.Context, priorSummary, conversationText string, opts Options) (string, error) {
	promptBody := buildSummaryPromptInput(priorSummary, conversationText)
	if opts.StreamFn == nil || opts.Model == nil {
		// Fallback: simple truncation without LLM
		if len(promptBody) > 2000 {
			return promptBody[:2000] + "\n\n... (truncated)", nil
		}
		return promptBody, nil
	}

	summaryCtx := &types.LLMContext{
		SystemPrompt: compactionPrompt,
		Messages: []types.AgentMessage{
			types.UserMessage{
				Role:    "user",
				Content: "Please summarize this conversation:\n\n" + promptBody,
			},
		},
	}

	apiKey := ""
	if opts.GetAPIKey != nil {
		key, _ := opts.GetAPIKey(opts.Model.ProviderID)
		apiKey = key
	}

	stream, err := opts.StreamFn(ctx, opts.Model, summaryCtx, &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			APIKey: apiKey,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create summary stream: %w", err)
	}
	defer stream.Close()

	// Drain events to unblock the producer
	go func() {
		for range stream.Events() {
		}
	}()

	result, err := stream.Result()
	if err != nil {
		return "", fmt.Errorf("summary generation failed: %w", err)
	}

	// Extract text from the assistant message
	var summary strings.Builder
	for _, block := range result.Content {
		if tc, ok := block.(*types.TextContent); ok {
			summary.WriteString(tc.Text)
		}
	}

	return strings.TrimSpace(summary.String()), nil
}

func buildSummaryPromptInput(priorSummary, conversationText string) string {
	var parts []string
	if strings.TrimSpace(priorSummary) != "" {
		parts = append(parts, "Existing summary context:\n"+strings.TrimSpace(priorSummary))
	}
	if strings.TrimSpace(conversationText) != "" {
		parts = append(parts, "New conversation since that summary:\n"+strings.TrimSpace(conversationText))
	}
	if len(parts) == 0 {
		return "No conversation content available."
	}
	return strings.Join(parts, "\n\n")
}

func flattenPreviousSummaries(messages []types.AgentMessage) (string, []types.AgentMessage) {
	var summaries []string
	remaining := make([]types.AgentMessage, 0, len(messages))
	for _, msg := range messages {
		summaryText, ok := extractPreviousSummaryText(msg)
		if ok {
			if summaryText != "" {
				summaries = append(summaries, summaryText)
			}
			continue
		}
		remaining = append(remaining, msg)
	}
	return strings.TrimSpace(strings.Join(summaries, "\n\n")), remaining
}

func extractPreviousSummaryText(msg types.AgentMessage) (string, bool) {
	switch m := msg.(type) {
	case types.UserMessage:
		return extractPreviousSummaryFromContent(m.Content)
	case *types.UserMessage:
		return extractPreviousSummaryFromContent(m.Content)
	default:
		return "", false
	}
}

func extractPreviousSummaryFromContent(content any) (string, bool) {
	switch c := content.(type) {
	case string:
		return parsePreviousSummaryText(c)
	case []types.ContentBlock:
		for _, block := range c {
			if tc, ok := block.(*types.TextContent); ok {
				if summary, found := parsePreviousSummaryText(tc.Text); found {
					return summary, true
				}
			}
		}
	}
	return "", false
}

func parsePreviousSummaryText(text string) (string, bool) {
	if !strings.HasPrefix(text, previousConversationSummaryPrefix) {
		return "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(text, previousConversationSummaryPrefix))
	if idx := strings.Index(body, "\n<read-files>"); idx >= 0 {
		body = body[:idx]
	}
	if idx := strings.Index(body, "\n<modified-files>"); idx >= 0 {
		body = body[:idx]
	}
	return strings.TrimSpace(body), true
}

func formatMessageForSummary(msg types.AgentMessage) string {
	switch m := msg.(type) {
	case types.UserMessage:
		content := formatContent(m.Content)
		return fmt.Sprintf("User: %s", content)
	case *types.UserMessage:
		content := formatContent(m.Content)
		return fmt.Sprintf("User: %s", content)
	case types.AssistantMessage:
		var parts []string
		for _, block := range m.Content {
			switch b := block.(type) {
			case *types.TextContent:
				parts = append(parts, b.Text)
			case *types.ToolCallContent:
				parts = append(parts, fmt.Sprintf("[Tool call: %s]", b.Name))
			}
		}
		return fmt.Sprintf("Assistant: %s", strings.Join(parts, " "))
	case *types.AssistantMessage:
		var parts []string
		for _, block := range m.Content {
			switch b := block.(type) {
			case *types.TextContent:
				parts = append(parts, b.Text)
			case *types.ToolCallContent:
				parts = append(parts, fmt.Sprintf("[Tool call: %s]", b.Name))
			}
		}
		return fmt.Sprintf("Assistant: %s", strings.Join(parts, " "))
	case types.ToolResultMessage:
		content := formatContent(m.Content)
		return fmt.Sprintf("Tool result (%s): %s", m.ToolName, content)
	case *types.ToolResultMessage:
		content := formatContent(m.Content)
		return fmt.Sprintf("Tool result (%s): %s", m.ToolName, content)
	default:
		return fmt.Sprintf("%v", msg)
	}
}

func formatContent(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []types.ContentBlock:
		var parts []string
		for _, block := range c {
			if tc, ok := block.(*types.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprintf("%v", content)
	}
}
