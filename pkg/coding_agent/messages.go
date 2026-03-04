package coding_agent

import (
	"time"

	"github.com/crosszan/modu/pkg/providers"
)

// BashExecutionMessage represents a `!command` inline execution result.
type BashExecutionMessage struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
}

// ToLlmMessage converts a BashExecutionMessage to a UserMessage.
func (m *BashExecutionMessage) ToLlmMessage() providers.UserMessage {
	text := "$ " + m.Command + "\n" + m.Output
	return providers.UserMessage{
		Role: "user",
		Content: []providers.ContentBlock{
			providers.TextContent{
				Type: "text",
				Text: text,
			},
		},
		Timestamp: time.Now().UnixMilli(),
	}
}

// CompactionSummaryMessage represents a context compaction summary.
type CompactionSummaryMessage struct {
	Summary       string `json:"summary"`
	OriginalCount int    `json:"originalCount"`
}

// ToLlmMessage converts a CompactionSummaryMessage to a UserMessage.
func (m *CompactionSummaryMessage) ToLlmMessage() providers.UserMessage {
	return providers.UserMessage{
		Role: "user",
		Content: []providers.ContentBlock{
			providers.TextContent{
				Type: "text",
				Text: "[Context Compaction Summary]\n\n" + m.Summary,
			},
		},
		Timestamp: time.Now().UnixMilli(),
	}
}

// BranchSummaryMessage represents a summary generated when navigating branches.
type BranchSummaryMessage struct {
	Summary string `json:"summary"`
	FromID  string `json:"fromId"`
	ToID    string `json:"toId"`
}

// ToLlmMessage converts a BranchSummaryMessage to a UserMessage.
func (m *BranchSummaryMessage) ToLlmMessage() providers.UserMessage {
	return providers.UserMessage{
		Role: "user",
		Content: []providers.ContentBlock{
			providers.TextContent{
				Type: "text",
				Text: "[Branch Navigation Summary]\n\n" + m.Summary,
			},
		},
		Timestamp: time.Now().UnixMilli(),
	}
}

// CustomMessage represents an extension-injected message.
type CustomMessage struct {
	Source string `json:"source"` // Extension name
	Text   string `json:"text"`
}

// ToLlmMessage converts a CustomMessage to a UserMessage.
func (m *CustomMessage) ToLlmMessage() providers.UserMessage {
	return providers.UserMessage{
		Role: "user",
		Content: []providers.ContentBlock{
			providers.TextContent{
				Type: "text",
				Text: "[" + m.Source + "] " + m.Text,
			},
		},
		Timestamp: time.Now().UnixMilli(),
	}
}
