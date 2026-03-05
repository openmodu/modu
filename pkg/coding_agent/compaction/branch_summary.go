package compaction

import (
	"context"
	"fmt"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/types"
)

const branchSummaryPrompt = `You are generating a context summary for a branch navigation event. The user is jumping from one point in the conversation to another. Summarize what was happening at the target point so the assistant can smoothly continue from there.

Include:
1. What task was being worked on
2. What files were being modified
3. Current state of the work
4. Any pending next steps

Be concise but specific.`

// BranchSummaryOptions configures branch summary generation.
type BranchSummaryOptions struct {
	Model     *types.Model
	GetAPIKey func(provider string) (string, error)
	StreamFn  agent.StreamFn
}

// GenerateBranchSummary creates a summary of the conversation context
// at a specific branch point.
func GenerateBranchSummary(ctx context.Context, messages []types.AgentMessage, opts BranchSummaryOptions) (string, error) {
	if len(messages) == 0 {
		return "No previous context.", nil
	}

	// Build conversation text
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(formatMessageForSummary(msg))
		sb.WriteString("\n\n")
	}

	conversationText := sb.String()

	if opts.StreamFn == nil || opts.Model == nil {
		// Fallback without LLM
		if len(conversationText) > 1000 {
			return conversationText[:1000] + "\n\n... (truncated)", nil
		}
		return conversationText, nil
	}

	summaryCtx := &types.LLMContext{
		SystemPrompt: branchSummaryPrompt,
		Messages: []types.AgentMessage{
			types.UserMessage{
				Role:    "user",
				Content: "Summarize the context at this branch point:\n\n" + conversationText,
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
		return "", fmt.Errorf("failed to create branch summary stream: %w", err)
	}

	result, err := stream.Result()
	if err != nil {
		return "", fmt.Errorf("branch summary generation failed: %w", err)
	}

	var summary strings.Builder
	for _, block := range result.Content {
		if tc, ok := block.(*types.TextContent); ok {
			summary.WriteString(tc.Text)
		}
	}

	return summary.String(), nil
}
