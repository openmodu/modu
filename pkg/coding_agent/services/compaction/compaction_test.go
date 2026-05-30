package compaction

import (
	"context"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func TestCompactFlattensPreviousSummaryMessages(t *testing.T) {
	messages := []types.AgentMessage{
		types.UserMessage{
			Role: "user",
			Content: []types.ContentBlock{
				&types.TextContent{
					Type: "text",
					Text: "[Previous Conversation Summary]\n\nPrior summary body\n\n<read-files>\nold.go\n</read-files>",
				},
			},
		},
		types.AssistantMessage{
			Role: "assistant",
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "I will inspect the parser next."},
			},
		},
		types.UserMessage{Role: "user", Content: "Please continue."},
		types.AssistantMessage{
			Role: "assistant",
			Content: []types.ContentBlock{
				&types.TextContent{Type: "text", Text: "Done."},
			},
		},
	}

	result, err := Compact(context.Background(), messages, Options{PreserveRecent: 1})
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected compacted messages")
	}

	summaryMsg, ok := result.Messages[0].(types.UserMessage)
	if !ok {
		t.Fatalf("expected first compacted message to be user summary, got %T", result.Messages[0])
	}
	textBlocks, ok := summaryMsg.Content.([]types.ContentBlock)
	if !ok || len(textBlocks) == 0 {
		t.Fatalf("expected summary content blocks, got %#v", summaryMsg.Content)
	}
	text := textBlocks[0].(*types.TextContent).Text
	if strings.Count(text, "[Previous Conversation Summary]") != 1 {
		t.Fatalf("expected a single summary header, got:\n%s", text)
	}
	if !strings.Contains(text, "Prior summary body") {
		t.Fatalf("expected prior summary to be preserved, got:\n%s", text)
	}
	if strings.Contains(text, "Existing summary context:\n[Previous Conversation Summary]") {
		t.Fatalf("expected flattened prior summary, got:\n%s", text)
	}
}
