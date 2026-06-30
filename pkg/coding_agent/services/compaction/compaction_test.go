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
					Text: "[Previous Conversation Summary]\n\nPrior summary body\n\n<read-files>\nold.go\n</read-files>\n\n<modified-files>\nnew.go\n</modified-files>",
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
	if len(result.ReadFiles) != 1 || result.ReadFiles[0] != "old.go" {
		t.Fatalf("expected structured read files to include old.go, got %#v", result.ReadFiles)
	}
	if len(result.ModifiedFiles) != 1 || result.ModifiedFiles[0] != "new.go" {
		t.Fatalf("expected structured modified files to include new.go, got %#v", result.ModifiedFiles)
	}
}

func TestCompactPreservesRecentUserMessagesWithinBudget(t *testing.T) {
	keepText := "Keep this user requirement."
	messages := []types.AgentMessage{
		types.UserMessage{Role: "user", Content: "Older user request should be summarized only."},
		types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "Older answer."},
		}},
		types.UserMessage{Role: "user", Content: keepText},
		types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "Recent compacted answer."},
		}},
		types.UserMessage{Role: "user", Content: "Tail user message."},
		types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "Tail assistant message."},
		}},
	}

	result, err := Compact(context.Background(), messages, Options{
		PreserveRecent:             2,
		PreserveUserMessagesTokens: approximateTextTokens(keepText),
	})
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if len(result.Messages) != 4 {
		t.Fatalf("expected summary, preserved user anchor, and tail messages; got %d", len(result.Messages))
	}
	if result.PreservedUserMessages != 1 {
		t.Fatalf("expected one preserved user anchor, got %d", result.PreservedUserMessages)
	}

	preservedUser, ok := result.Messages[1].(types.UserMessage)
	if !ok {
		t.Fatalf("expected preserved user message after summary, got %T", result.Messages[1])
	}
	if text := userMessageTextForTest(t, preservedUser); text != keepText {
		t.Fatalf("expected preserved user text %q, got %q", keepText, text)
	}
}

func TestCompactTruncatesPreservedUserMessageToBudget(t *testing.T) {
	largeText := strings.Repeat("0123456789", 20)
	messages := []types.AgentMessage{
		types.UserMessage{Role: "user", Content: largeText},
		types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "Middle assistant message."},
		}},
		types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "Another middle assistant message."},
		}},
		types.AssistantMessage{Role: "assistant", Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "Tail assistant message."},
		}},
	}

	result, err := Compact(context.Background(), messages, Options{
		PreserveRecent:             1,
		PreserveUserMessagesTokens: 20,
	})
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("expected summary, truncated user anchor, and tail message; got %d", len(result.Messages))
	}
	if result.PreservedUserMessages != 1 {
		t.Fatalf("expected one preserved user anchor, got %d", result.PreservedUserMessages)
	}

	preservedUser, ok := result.Messages[1].(types.UserMessage)
	if !ok {
		t.Fatalf("expected preserved user message after summary, got %T", result.Messages[1])
	}
	text := userMessageTextForTest(t, preservedUser)
	if !strings.Contains(text, preservedUserMessageTruncated) {
		t.Fatalf("expected truncated marker in preserved user text, got %q", text)
	}
	if len([]rune(text)) > 20*approxCharsPerToken {
		t.Fatalf("expected preserved user text to stay within approximate budget, got %d runes", len([]rune(text)))
	}
}

func userMessageTextForTest(t *testing.T, msg types.UserMessage) string {
	t.Helper()
	blocks, ok := msg.Content.([]types.ContentBlock)
	if !ok || len(blocks) == 0 {
		t.Fatalf("expected text content blocks, got %#v", msg.Content)
	}
	text, ok := blocks[0].(*types.TextContent)
	if !ok {
		t.Fatalf("expected text block, got %T", blocks[0])
	}
	return text.Text
}
