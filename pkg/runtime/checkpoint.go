package runtime

import (
	"time"

	"github.com/openmodu/modu/pkg/types"
)

// checkpointVersion is bumped when the on-disk Checkpoint layout changes in a
// way that old readers cannot understand.
const checkpointVersion = 1

// Checkpoint is a durable, replayable snapshot of a session at a commit
// boundary. The committed conversation history is the source of truth: state is
// rebuilt by loading the messages, so a Checkpoint is everything needed to
// resume or rewind a run.
type Checkpoint struct {
	Version      int                 `json:"version"`
	SessionID    string              `json:"sessionId"`
	Seq          int64               `json:"seq"`       // monotonic per session
	ParentSeq    int64               `json:"parentSeq"` // lineage; -1 for the root
	Label        string              `json:"label,omitempty"`
	SystemPrompt string              `json:"systemPrompt,omitempty"`
	Model        *types.Model        `json:"model,omitempty"`
	Thinking     types.ThinkingLevel `json:"thinking,omitempty"`
	Messages     []msgEnvelope       `json:"messages"`
	Status       types.SessionStatus `json:"status"`
	Error        string              `json:"error,omitempty"`
	CreatedAt    int64               `json:"createdAt"`
}

// snapshot builds a Checkpoint from the current agent state.
func snapshot(state types.State, sessionID string, seq, parentSeq int64, status types.SessionStatus, label string) (Checkpoint, error) {
	messages, err := marshalMessages(state.Messages)
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{
		Version:      checkpointVersion,
		SessionID:    sessionID,
		Seq:          seq,
		ParentSeq:    parentSeq,
		Label:        label,
		SystemPrompt: state.SystemPrompt,
		Model:        state.Model,
		Thinking:     state.ThinkingLevel,
		Messages:     messages,
		Status:       status,
		Error:        state.Error,
		CreatedAt:    time.Now().UnixMilli(),
	}, nil
}

// Restore decodes the persisted messages back into live []types.AgentMessage.
func (c Checkpoint) Restore() ([]types.AgentMessage, error) {
	return unmarshalMessages(c.Messages)
}

// repairDanglingToolCalls makes a restored history valid to resume. If the run
// died after the model requested tools but before (all of) their results were
// committed, the trailing assistant message has tool calls with no matching
// tool_result. Most providers reject that. For each dangling call we append a
// synthetic error result so the conversation is well-formed and the next model
// turn can react to the interruption instead of erroring out.
func repairDanglingToolCalls(messages []types.AgentMessage) []types.AgentMessage {
	satisfied := map[string]struct{}{}
	for _, message := range messages {
		if tr, ok := normalizeMessage(message).(types.ToolResultMessage); ok {
			satisfied[tr.ToolCallID] = struct{}{}
		}
	}

	var dangling []types.ToolCallContent
	for _, message := range messages {
		assistant, ok := normalizeMessage(message).(types.AssistantMessage)
		if !ok {
			continue
		}
		for _, block := range assistant.Content {
			call, ok := block.(*types.ToolCallContent)
			if !ok {
				continue
			}
			if _, done := satisfied[call.ID]; !done {
				dangling = append(dangling, *call)
			}
		}
	}

	if len(dangling) == 0 {
		return messages
	}

	repaired := append([]types.AgentMessage{}, messages...)
	for _, call := range dangling {
		repaired = append(repaired, types.ToolResultMessage{
			Role:       types.RoleToolResult,
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "Tool call interrupted before completion; result unavailable."}},
			IsError:    true,
			Timestamp:  time.Now().UnixMilli(),
		})
	}
	return repaired
}
