package moms

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/types"
)

const (
	// keepRecentMessages is the number of recent messages never included in
	// summarization, ensuring conversational continuity. Mirrors PicoClaw.
	keepRecentMessages = 4

	// maxSummarizationMessages is the threshold above which the history is
	// split into two batches for summarization before merging.
	maxSummarizationMessages = 10

	// summarizeLLMMaxRetries controls retries for the summarization LLM call.
	summarizeLLMMaxRetries = 3

	// summarizeLLMTemperature is the temperature used for summarization calls.
	summarizeLLMTemperature = 0.3

	// fallbackMinContentLength is the minimum excerpt length per message used
	// in the string-truncation fallback when the LLM is unavailable.
	fallbackMinContentLength = 200

	// fallbackMaxContentPercent is the max percentage of message content kept
	// in the string-truncation fallback.
	fallbackMaxContentPercent = 10
)

// Summarizer performs soft (LLM-backed) context summarization for a ContextStore.
// It mirrors the maybeSummarize / summarizeSession / summarizeBatch logic from
// PicoClaw's pkg/agent/loop.go.
type Summarizer struct {
	store       *ContextStore
	callLLM     func(ctx context.Context, prompt string) (string, error)
	summarizing sync.Map // map[string]bool — prevents concurrent summarization for the same chatID
}

// NewSummarizer creates a Summarizer backed by store.
// callLLM is a simple closure that sends a user-prompt to the LLM and returns the response text.
func NewSummarizer(store *ContextStore, callLLM func(ctx context.Context, prompt string) (string, error)) *Summarizer {
	return &Summarizer{
		store:   store,
		callLLM: callLLM,
	}
}

// MaybeSummarize checks whether the current history for chatID exceeds the
// configured thresholds. If so, it asynchronously triggers summarization.
// This is safe to call from the main request path.
func (s *Summarizer) MaybeSummarize(chatID int64, model *types.Model, settings *Settings) {
	if settings == nil {
		return
	}
	c := settings.GetCompaction()
	if !c.SummarizeEnabled {
		return
	}

	history, err := s.store.GetHistory(chatID)
	if err != nil || len(history) == 0 {
		return
	}

	tokenEstimate := estimateTokens(history)
	contextWindow := c.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 32768
	}
	threshold := contextWindow * c.SummarizeTokenPercent / 100
	msgThreshold := c.SummarizeMessageThreshold
	if msgThreshold <= 0 {
		msgThreshold = 20
	}

	if len(history) <= msgThreshold && tokenEstimate <= threshold {
		return
	}

	key := fmt.Sprintf("%d", chatID)
	if _, alreadyRunning := s.summarizing.LoadOrStore(key, true); alreadyRunning {
		return
	}

	go func() {
		defer s.summarizing.Delete(key)
		fmt.Printf("[moms/summarize] chat %d: token threshold reached (%d tokens, %d msgs), summarizing...\n",
			chatID, tokenEstimate, len(history))
		s.summarizeSession(chatID, model)
	}()
}

// summarizeSession performs the actual summarization for chatID.
// It keeps the most recent keepRecentMessages messages untouched and
// summarizes the older portion via the LLM.
func (s *Summarizer) summarizeSession(chatID int64, model *types.Model) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	history, err := s.store.GetHistory(chatID)
	if err != nil {
		fmt.Printf("[moms/summarize] chat %d: failed to get history: %v\n", chatID, err)
		return
	}
	if len(history) <= keepRecentMessages {
		return
	}

	existingSummary, err := s.store.GetSummary(chatID)
	if err != nil {
		existingSummary = ""
	}

	toSummarize := history[:len(history)-keepRecentMessages]

	// Oversized message guard: skip tool messages and messages wider than
	// half the estimated context window.
	maxMsgTokens := 8192 // reasonable cap for a single message
	var validMessages []flatMessage
	omitted := false

	for _, m := range toSummarize {
		role := messageRole(m)
		if role != "user" && role != "assistant" {
			continue
		}
		text := extractText(getMessageContent(m))
		msgTokens := utf8.RuneCountInString(text) * 2 / 5
		if msgTokens > maxMsgTokens {
			omitted = true
			continue
		}
		validMessages = append(validMessages, flatMessage{Role: role, Content: text})
	}

	if len(validMessages) == 0 {
		return
	}

	provider := s.resolveProvider(model)
	if provider == nil {
		fmt.Printf("[moms/summarize] chat %d: no provider available for summarization\n", chatID)
		return
	}

	var finalSummary string

	if len(validMessages) > maxSummarizationMessages {
		mid := len(validMessages) / 2
		mid = findNearestUserMessage(validMessages, mid)

		part1 := validMessages[:mid]
		part2 := validMessages[mid:]

		s1, _ := s.summarizeBatch(ctx, provider, model, part1, "")
		s2, _ := s.summarizeBatch(ctx, provider, model, part2, "")

		mergePrompt := fmt.Sprintf(
			"Merge these two conversation summaries into one cohesive summary:\n\n1: %s\n\n2: %s",
			s1, s2,
		)
		resp, err := retryLLMCall(ctx, provider, model, mergePrompt, summarizeLLMMaxRetries)
		if err == nil && resp != "" {
			finalSummary = resp
		} else {
			finalSummary = s1 + " " + s2
		}
	} else {
		finalSummary, _ = s.summarizeBatch(ctx, provider, model, validMessages, existingSummary)
	}

	if omitted && finalSummary != "" {
		finalSummary += "\n[Note: Some oversized messages were omitted from this summary for efficiency.]"
	}

	if finalSummary != "" {
		if err := s.store.SetSummary(chatID, finalSummary); err != nil {
			fmt.Printf("[moms/summarize] chat %d: failed to set summary: %v\n", chatID, err)
			return
		}
		if err := s.store.TruncateHistory(chatID, keepRecentMessages); err != nil {
			fmt.Printf("[moms/summarize] chat %d: failed to truncate history: %v\n", chatID, err)
			return
		}
		// Kick off async compact to reclaim disk space.
		go func() {
			if err := s.store.Compact(chatID); err != nil {
				fmt.Printf("[moms/summarize] chat %d: compact error: %v\n", chatID, err)
			}
		}()
		fmt.Printf("[moms/summarize] chat %d: summarized, kept last %d messages\n",
			chatID, keepRecentMessages)
	}
}

// summarizeBatch makes a single LLM call to summarize a batch of messages,
// combining them with an optional existingSummary for context. If the LLM
// call fails, it falls back to a truncated string representation.
func (s *Summarizer) summarizeBatch(
	ctx context.Context,
	provider providers.Provider,
	model *types.Model,
	batch []flatMessage,
	existingSummary string,
) (string, error) {
	var sb strings.Builder
	sb.WriteString("Provide a concise summary of this conversation segment, preserving core context and key points.\n")
	if existingSummary != "" {
		sb.WriteString("Existing context: ")
		sb.WriteString(existingSummary)
		sb.WriteString("\n")
	}
	sb.WriteString("\nCONVERSATION:\n")
	for _, m := range batch {
		fmt.Fprintf(&sb, "%s: %s\n", m.Role, m.Content)
	}

	resp, err := retryLLMCall(ctx, provider, model, sb.String(), summarizeLLMMaxRetries)
	if err == nil && resp != "" {
		return strings.TrimSpace(resp), nil
	}

	// Fallback: truncated string representation of the batch.
	var fallback strings.Builder
	fallback.WriteString("Conversation summary: ")
	for i, m := range batch {
		if i > 0 {
			fallback.WriteString(" | ")
		}
		content := strings.TrimSpace(m.Content)
		runes := []rune(content)
		if len(runes) == 0 {
			fmt.Fprintf(&fallback, "%s: ", m.Role)
			continue
		}
		keep := len(runes) * fallbackMaxContentPercent / 100
		if keep < fallbackMinContentLength {
			keep = fallbackMinContentLength
		}
		if keep > len(runes) {
			keep = len(runes)
		}
		excerpt := string(runes[:keep])
		if keep < len(runes) {
			excerpt += "..."
		}
		fmt.Fprintf(&fallback, "%s: %s", m.Role, excerpt)
	}
	return fallback.String(), nil
}

// resolveProvider returns a Provider to use for summarization. It prefers the
// provider that is associated with the agent's model. Falls back to any
// registered provider.
func (s *Summarizer) resolveProvider(model *types.Model) providers.Provider {
	if model != nil && model.ProviderID != "" {
		if p, ok := providers.Get(model.ProviderID); ok {
			return p
		}
	}
	// Fall back to any available provider.
	all := providers.List()
	if len(all) > 0 {
		return all[0]
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// flatMessage is a simplified (role, content) pair used during summarization.
type flatMessage struct {
	Role    string
	Content string
}

// estimateTokens approximates token count using the same 2 chars/5 tokens
// heuristic as PicoClaw (accounts well for CJK and ASCII alike).
func estimateTokens(msgs []agent.AgentMessage) int {
	total := 0
	for _, m := range msgs {
		total += utf8.RuneCountInString(extractText(getMessageContent(m)))
	}
	return total * 2 / 5
}

// findNearestUserMessage finds the closest user-role message to mid, searching
// backward first, then forward. Matches PicoClaw's implementation.
func findNearestUserMessage(msgs []flatMessage, mid int) int {
	orig := mid
	for mid > 0 && msgs[mid].Role != "user" {
		mid--
	}
	if msgs[mid].Role == "user" {
		return mid
	}
	mid = orig
	for mid < len(msgs) && msgs[mid].Role != "user" {
		mid++
	}
	if mid < len(msgs) {
		return mid
	}
	return orig
}

// retryLLMCall calls the LLM with a simple user prompt and retries up to
// maxRetries times with exponential backoff.
func retryLLMCall(
	ctx context.Context,
	p providers.Provider,
	model *types.Model,
	prompt string,
	maxRetries int,
) (string, error) {
	temp := summarizeLLMTemperature
	req := &providers.ChatRequest{
		Model: model.ID,
		Messages: []providers.Message{
			{Role: "user", Content: prompt},
		},
		Temperature: &temp,
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := p.Chat(ctx, req)
		if err == nil && resp != nil {
			if content, ok := resp.Message.Content.(string); ok && content != "" {
				return content, nil
			}
		}
		lastErr = err
		if attempt < maxRetries-1 {
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}
	return "", lastErr
}
