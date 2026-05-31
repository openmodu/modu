package coding_agent

import (
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/types"
)

// BashExecutionMessage represents a `!command` inline execution result.
type BashExecutionMessage struct {
	Command  string `json:"command"`
	Output   string `json:"output"`
	ExitCode int    `json:"exitCode"`
}

// ToLlmMessage converts a BashExecutionMessage to a UserMessage.
func (m *BashExecutionMessage) ToLlmMessage() types.UserMessage {
	text := "$ " + m.Command + "\n" + m.Output
	return types.UserMessage{
		Role: "user",
		Content: []types.ContentBlock{
			&types.TextContent{
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
func (m *CompactionSummaryMessage) ToLlmMessage() types.UserMessage {
	return types.UserMessage{
		Role: "user",
		Content: []types.ContentBlock{
			&types.TextContent{
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
func (m *BranchSummaryMessage) ToLlmMessage() types.UserMessage {
	return types.UserMessage{
		Role: "user",
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: "[Branch Navigation Summary]\n\n" + m.Summary,
			},
		},
		Timestamp: time.Now().UnixMilli(),
	}
}

// CustomMessage represents an extension-injected message.
type CustomMessage struct {
	Source     string `json:"source"` // Extension name
	Text       string `json:"text"`
	CustomType string `json:"customType,omitempty"`
	Display    bool   `json:"display,omitempty"`
	DeliverAs  string `json:"deliverAs,omitempty"`
}

const nestedContextSource = "nested_context"
const explicitSkillSource = "explicit_skill"
const hiddenExtensionSource = "extension_hidden"

// ToLlmMessage converts a CustomMessage to a UserMessage.
func (m *CustomMessage) ToLlmMessage() types.UserMessage {
	return types.UserMessage{
		Role: "user",
		Content: []types.ContentBlock{
			&types.TextContent{
				Type: "text",
				Text: "[" + m.Source + "] " + m.Text,
			},
		},
		Timestamp: time.Now().UnixMilli(),
	}
}

// isTransientContextMessage reports whether a message is host-injected context
// that PruneTransient drops from the live context at the end of each turn.
// Explicitly invoked skills (explicit_skill) are deliberately excluded: their
// body stays resident across turns so a multi-turn skill is not re-read every
// turn, and context pressure is reclaimed by compaction instead. The broader
// "never persist" set is isNonPersistentMessage.
func isTransientContextMessage(msg types.AgentMessage) bool {
	switch m := msg.(type) {
	case types.UserMessage:
		return customMessageHasSource(m.Content, nestedContextSource) ||
			customMessageHasSource(m.Content, hiddenExtensionSource)
	case *types.UserMessage:
		return customMessageHasSource(m.Content, nestedContextSource) ||
			customMessageHasSource(m.Content, hiddenExtensionSource)
	default:
		return false
	}
}

// isNonPersistentMessage reports whether a message is host-injected context that
// must never be written to the saved session: everything transient, plus
// explicit_skill. A skill body lives in-context for the task but should not
// pollute persisted history (it would replay as a bogus user message on resume)
// and is cheaply re-injected when the skill is invoked again.
func isNonPersistentMessage(msg types.AgentMessage) bool {
	if isTransientContextMessage(msg) {
		return true
	}
	switch m := msg.(type) {
	case types.UserMessage:
		return customMessageHasSource(m.Content, explicitSkillSource)
	case *types.UserMessage:
		return customMessageHasSource(m.Content, explicitSkillSource)
	default:
		return false
	}
}

func customMessageHasSource(content any, source string) bool {
	prefix := "[" + source + "] "
	switch c := content.(type) {
	case string:
		return strings.HasPrefix(c, prefix)
	case []types.ContentBlock:
		for _, block := range c {
			if tc, ok := block.(*types.TextContent); ok && strings.HasPrefix(tc.Text, prefix) {
				return true
			}
		}
	}
	return false
}
