package compaction

import (
	"context"
	"fmt"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/llm"
)

// Options configures the compaction process.
type Options struct {
	// PreserveRecent is the number of recent messages to keep unchanged.
	PreserveRecent int
	// Model is the LLM model to use for generating summaries.
	Model *llm.Model
	// GetAPIKey retrieves an API key for the given provider.
	GetAPIKey func(provider string) (string, error)
	// StreamFn creates an LLM stream for summary generation.
	StreamFn agent.StreamFn
}

// Result holds the outcome of a compaction operation.
type Result struct {
	Summary       string
	OriginalCount int
	NewCount      int
	Messages      []llm.Message
}

const compactionPrompt = `You are summarizing a conversation between a user and a coding assistant. Create a concise summary that captures:

1. The main tasks or goals discussed
2. Key decisions made
3. Important file paths, function names, or code structures mentioned
4. Current state of any in-progress work
5. Any unresolved issues or next steps

Be specific about technical details. The summary will be used to continue the conversation, so include enough context for the assistant to pick up where it left off.

Format as a structured summary with clear sections.`

// Compact compresses older messages into a summary while preserving recent messages.
func Compact(ctx context.Context, messages []llm.Message, opts Options) (*Result, error) {
	if len(messages) == 0 {
		return &Result{Messages: messages}, nil
	}

	preserve := opts.PreserveRecent
	if preserve <= 0 {
		preserve = 4
	}

	if len(messages) <= preserve {
		return &Result{
			Messages:      messages,
			OriginalCount: len(messages),
			NewCount:      len(messages),
		}, nil
	}

	// Split messages into those to compact and those to preserve
	toCompact := messages[:len(messages)-preserve]
	toPreserve := messages[len(messages)-preserve:]

	// Build a text representation of messages to compact
	var sb strings.Builder
	for _, msg := range toCompact {
		sb.WriteString(formatMessageForSummary(msg))
		sb.WriteString("\n\n")
	}

	// Generate summary using LLM
	summary, err := generateSummary(ctx, sb.String(), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate compaction summary: %w", err)
	}

	// Build new message list: summary + preserved messages
	summaryMsg := llm.UserMessage{
		Role: "user",
		Content: []llm.ContentBlock{
			llm.TextContent{
				Type: "text",
				Text: "[Previous Conversation Summary]\n\n" + summary,
			},
		},
	}

	newMessages := make([]llm.Message, 0, 1+len(toPreserve))
	newMessages = append(newMessages, summaryMsg)
	newMessages = append(newMessages, toPreserve...)

	return &Result{
		Summary:       summary,
		OriginalCount: len(messages),
		NewCount:      len(newMessages),
		Messages:      newMessages,
	}, nil
}

func generateSummary(ctx context.Context, conversationText string, opts Options) (string, error) {
	if opts.StreamFn == nil || opts.Model == nil {
		// Fallback: simple truncation without LLM
		if len(conversationText) > 2000 {
			return conversationText[:2000] + "\n\n... (truncated)", nil
		}
		return conversationText, nil
	}

	summaryCtx := &llm.Context{
		SystemPrompt: compactionPrompt,
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    "user",
				Content: "Please summarize this conversation:\n\n" + conversationText,
			},
		},
	}

	apiKey := ""
	if opts.GetAPIKey != nil {
		key, _ := opts.GetAPIKey(string(opts.Model.Provider))
		apiKey = key
	}

	stream, err := opts.StreamFn(opts.Model, summaryCtx, &llm.SimpleStreamOptions{
		StreamOptions: llm.StreamOptions{
			APIKey: apiKey,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create summary stream: %w", err)
	}

	result, err := stream.Result()
	if err != nil {
		return "", fmt.Errorf("summary generation failed: %w", err)
	}

	// Extract text from the assistant message
	var summary strings.Builder
	for _, block := range result.Content {
		if tc, ok := block.(llm.TextContent); ok {
			summary.WriteString(tc.Text)
		}
	}

	return summary.String(), nil
}

func formatMessageForSummary(msg llm.Message) string {
	switch m := msg.(type) {
	case llm.UserMessage:
		content := formatContent(m.Content)
		return fmt.Sprintf("User: %s", content)
	case llm.AssistantMessage:
		var parts []string
		for _, block := range m.Content {
			switch b := block.(type) {
			case llm.TextContent:
				parts = append(parts, b.Text)
			case llm.ToolCall:
				parts = append(parts, fmt.Sprintf("[Tool call: %s]", b.Name))
			}
		}
		return fmt.Sprintf("Assistant: %s", strings.Join(parts, " "))
	case llm.ToolResultMessage:
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
	case []llm.ContentBlock:
		var parts []string
		for _, block := range c {
			if tc, ok := block.(llm.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprintf("%v", content)
	}
}
