package runtime

import (
	"encoding/json"
	"fmt"

	"github.com/openmodu/modu/pkg/types"
)

// The conversation history is built from interface types (types.AgentMessage and
// types.ContentBlock). encoding/json can marshal the concrete values but cannot
// unmarshal back into an interface, so checkpoints persist each message and
// content block inside a tagged envelope that records the concrete kind.

type blockEnvelope struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

type msgEnvelope struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

const (
	blockKindText     = "text"
	blockKindThinking = "thinking"
	blockKindImage    = "image"
	blockKindToolCall = "toolCall"

	msgKindUser       = "user"
	msgKindAssistant  = "assistant"
	msgKindToolResult = "toolResult"
)

func marshalBlock(block types.ContentBlock) (blockEnvelope, error) {
	var kind string
	switch block.(type) {
	case *types.TextContent:
		kind = blockKindText
	case *types.ThinkingContent:
		kind = blockKindThinking
	case *types.ImageContent:
		kind = blockKindImage
	case *types.ToolCallContent:
		kind = blockKindToolCall
	default:
		return blockEnvelope{}, fmt.Errorf("runtime: unknown content block %T", block)
	}
	data, err := json.Marshal(block)
	if err != nil {
		return blockEnvelope{}, err
	}
	return blockEnvelope{Kind: kind, Data: data}, nil
}

func unmarshalBlock(env blockEnvelope) (types.ContentBlock, error) {
	var block types.ContentBlock
	switch env.Kind {
	case blockKindText:
		block = &types.TextContent{}
	case blockKindThinking:
		block = &types.ThinkingContent{}
	case blockKindImage:
		block = &types.ImageContent{}
	case blockKindToolCall:
		block = &types.ToolCallContent{}
	default:
		return nil, fmt.Errorf("runtime: unknown content block kind %q", env.Kind)
	}
	if err := json.Unmarshal(env.Data, block); err != nil {
		return nil, err
	}
	return block, nil
}

func marshalBlocks(blocks []types.ContentBlock) ([]blockEnvelope, error) {
	out := make([]blockEnvelope, 0, len(blocks))
	for _, block := range blocks {
		env, err := marshalBlock(block)
		if err != nil {
			return nil, err
		}
		out = append(out, env)
	}
	return out, nil
}

func unmarshalBlocks(envs []blockEnvelope) ([]types.ContentBlock, error) {
	out := make([]types.ContentBlock, 0, len(envs))
	for _, env := range envs {
		block, err := unmarshalBlock(env)
		if err != nil {
			return nil, err
		}
		out = append(out, block)
	}
	return out, nil
}

// userWire mirrors types.UserMessage. Content is `any` and in practice is either
// a plain string (text prompt) or a []ContentBlock (multimodal); both are kept.
type userWire struct {
	Role      string          `json:"role"`
	Text      *string         `json:"text,omitempty"`
	Blocks    []blockEnvelope `json:"blocks,omitempty"`
	Timestamp int64           `json:"timestamp"`
}

type assistantWire struct {
	Role         string          `json:"role"`
	Content      []blockEnvelope `json:"content"`
	ProviderID   string          `json:"provider,omitempty"`
	Model        string          `json:"model,omitempty"`
	Usage        types.AgentUsage `json:"usage"`
	StopReason   string          `json:"stopReason,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
	Timestamp    int64           `json:"timestamp"`
}

type toolResultWire struct {
	Role       string          `json:"role"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Content    []blockEnvelope `json:"content"`
	Details    json.RawMessage `json:"details,omitempty"`
	IsError    bool            `json:"isError"`
	Timestamp  int64           `json:"timestamp"`
}

func marshalMessage(message types.AgentMessage) (msgEnvelope, error) {
	switch m := normalizeMessage(message).(type) {
	case types.UserMessage:
		wire := userWire{Role: m.Role, Timestamp: m.Timestamp}
		switch content := m.Content.(type) {
		case string:
			wire.Text = &content
		case []types.ContentBlock:
			blocks, err := marshalBlocks(content)
			if err != nil {
				return msgEnvelope{}, err
			}
			wire.Blocks = blocks
		case nil:
			// no content
		default:
			return msgEnvelope{}, fmt.Errorf("runtime: unsupported user content %T", content)
		}
		return wrap(msgKindUser, wire)
	case types.AssistantMessage:
		blocks, err := marshalBlocks(m.Content)
		if err != nil {
			return msgEnvelope{}, err
		}
		return wrap(msgKindAssistant, assistantWire{
			Role:         m.Role,
			Content:      blocks,
			ProviderID:   m.ProviderID,
			Model:        m.Model,
			Usage:        m.Usage,
			StopReason:   m.StopReason,
			ErrorMessage: m.ErrorMessage,
			Timestamp:    m.Timestamp,
		})
	case types.ToolResultMessage:
		blocks, err := marshalBlocks(m.Content)
		if err != nil {
			return msgEnvelope{}, err
		}
		wire := toolResultWire{
			Role:       m.Role,
			ToolCallID: m.ToolCallID,
			ToolName:   m.ToolName,
			Content:    blocks,
			IsError:    m.IsError,
			Timestamp:  m.Timestamp,
		}
		if m.Details != nil {
			details, err := json.Marshal(m.Details)
			if err != nil {
				return msgEnvelope{}, err
			}
			wire.Details = details
		}
		return wrap(msgKindToolResult, wire)
	default:
		return msgEnvelope{}, fmt.Errorf("runtime: unknown message %T", message)
	}
}

func unmarshalMessage(env msgEnvelope) (types.AgentMessage, error) {
	switch env.Kind {
	case msgKindUser:
		var wire userWire
		if err := json.Unmarshal(env.Data, &wire); err != nil {
			return nil, err
		}
		msg := types.UserMessage{Role: wire.Role, Timestamp: wire.Timestamp}
		switch {
		case wire.Text != nil:
			msg.Content = *wire.Text
		case wire.Blocks != nil:
			blocks, err := unmarshalBlocks(wire.Blocks)
			if err != nil {
				return nil, err
			}
			msg.Content = blocks
		}
		return msg, nil
	case msgKindAssistant:
		var wire assistantWire
		if err := json.Unmarshal(env.Data, &wire); err != nil {
			return nil, err
		}
		blocks, err := unmarshalBlocks(wire.Content)
		if err != nil {
			return nil, err
		}
		return types.AssistantMessage{
			Role:         wire.Role,
			Content:      blocks,
			ProviderID:   wire.ProviderID,
			Model:        wire.Model,
			Usage:        wire.Usage,
			StopReason:   wire.StopReason,
			ErrorMessage: wire.ErrorMessage,
			Timestamp:    wire.Timestamp,
		}, nil
	case msgKindToolResult:
		var wire toolResultWire
		if err := json.Unmarshal(env.Data, &wire); err != nil {
			return nil, err
		}
		blocks, err := unmarshalBlocks(wire.Content)
		if err != nil {
			return nil, err
		}
		msg := types.ToolResultMessage{
			Role:       wire.Role,
			ToolCallID: wire.ToolCallID,
			ToolName:   wire.ToolName,
			Content:    blocks,
			IsError:    wire.IsError,
			Timestamp:  wire.Timestamp,
		}
		if wire.Details != nil {
			var details any
			if err := json.Unmarshal(wire.Details, &details); err != nil {
				return nil, err
			}
			msg.Details = details
		}
		return msg, nil
	default:
		return nil, fmt.Errorf("runtime: unknown message kind %q", env.Kind)
	}
}

func marshalMessages(messages []types.AgentMessage) ([]msgEnvelope, error) {
	out := make([]msgEnvelope, 0, len(messages))
	for _, message := range messages {
		env, err := marshalMessage(message)
		if err != nil {
			return nil, err
		}
		out = append(out, env)
	}
	return out, nil
}

func unmarshalMessages(envs []msgEnvelope) ([]types.AgentMessage, error) {
	out := make([]types.AgentMessage, 0, len(envs))
	for _, env := range envs {
		message, err := unmarshalMessage(env)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, nil
}

// normalizeMessage dereferences pointer message variants to their value form so
// the type switch in marshalMessage only needs to handle values.
func normalizeMessage(message types.AgentMessage) types.AgentMessage {
	switch m := message.(type) {
	case *types.UserMessage:
		return *m
	case *types.AssistantMessage:
		return *m
	case *types.ToolResultMessage:
		return *m
	default:
		return message
	}
}

func wrap(kind string, value any) (msgEnvelope, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return msgEnvelope{}, err
	}
	return msgEnvelope{Kind: kind, Data: data}, nil
}
