package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// StreamDefault is the default StreamFn. It looks up the provider from the
// global registry by model.ProviderID and adapts the stream to
// AssistantMessageEventStream.
func StreamDefault(ctx context.Context, model *Model, llmCtx *LLMContext, opts *SimpleStreamOptions) (AssistantMessageEventStream, error) {
	p, ok := Get(model.ProviderID)
	if !ok {
		return nil, fmt.Errorf("no provider registered for %q", model.ProviderID)
	}
	req := buildChatRequest(model, llmCtx, opts)
	s, err := p.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	return AdaptStream(model.ProviderID, model.ID, s), nil
}

// AdaptStream wraps a Provider-level Stream (using ChatResponse) into an
// AssistantMessageEventStream (using AssistantMessage with content blocks).
func AdaptStream(providerID, modelID string, s Stream) AssistantMessageEventStream {
	out := NewAssistantEventStream()
	go adaptStreamLoop(providerID, modelID, s, out)
	return out
}

func adaptStreamLoop(providerID, modelID string, s Stream, out *AssistantEventStream) {
	defer out.Close()

	partial := &AssistantMessage{
		Role:       "assistant",
		Content:    []ContentBlock{},
		ProviderID: providerID,
		Model:      modelID,
	}

	for event := range s.Events() {
		switch event.Type {
		case EventStart:
			out.Push(AssistantMessageEvent{Type: "start", Partial: partial})

		case EventTextStart:
			ensureContentIndex(partial, event.ContentIndex)
			partial.Content[event.ContentIndex] = &TextContent{Type: "text", Text: ""}
			out.Push(AssistantMessageEvent{Type: "text_start", ContentIndex: event.ContentIndex, Partial: partial})

		case EventTextDelta:
			if tc, ok := getTextAt(partial, event.ContentIndex); ok {
				tc.Text += event.Delta
			}
			out.Push(AssistantMessageEvent{Type: "text_delta", ContentIndex: event.ContentIndex, Delta: event.Delta, Partial: partial})

		case EventTextEnd:
			content := ""
			if tc, ok := getTextAt(partial, event.ContentIndex); ok {
				content = tc.Text
			}
			out.Push(AssistantMessageEvent{Type: "text_end", ContentIndex: event.ContentIndex, Content: content, Partial: partial})

		case EventThinkingStart:
			ensureContentIndex(partial, event.ContentIndex)
			partial.Content[event.ContentIndex] = &ThinkingContent{Type: "thinking", Thinking: ""}
			out.Push(AssistantMessageEvent{Type: "thinking_start", ContentIndex: event.ContentIndex, Partial: partial})

		case EventThinkingDelta:
			if tc, ok := getThinkingAt(partial, event.ContentIndex); ok {
				tc.Thinking += event.Delta
			}
			out.Push(AssistantMessageEvent{Type: "thinking_delta", ContentIndex: event.ContentIndex, Delta: event.Delta, Partial: partial})

		case EventThinkingEnd:
			content := ""
			if tc, ok := getThinkingAt(partial, event.ContentIndex); ok {
				content = tc.Thinking
			}
			out.Push(AssistantMessageEvent{Type: "thinking_end", ContentIndex: event.ContentIndex, Content: content, Partial: partial})

		case EventToolCallStart:
			if event.ToolCall != nil {
				ensureContentIndex(partial, event.ContentIndex)
				partial.Content[event.ContentIndex] = &ToolCallContent{
					Type:      "toolCall",
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Function.Name,
					Arguments: map[string]any{},
				}
			}
			out.Push(AssistantMessageEvent{Type: "toolcall_start", ContentIndex: event.ContentIndex, Partial: partial})

		case EventToolCallDelta:
			out.Push(AssistantMessageEvent{Type: "toolcall_delta", ContentIndex: event.ContentIndex, Delta: event.Delta, Partial: partial})

		case EventToolCallEnd:
			var tc *ToolCallContent
			if event.ToolCall != nil {
				ensureContentIndex(partial, event.ContentIndex)
				var args map[string]any
				if event.ToolCall.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(event.ToolCall.Function.Arguments), &args)
				}
				if args == nil {
					args = map[string]any{}
				}
				tcc := &ToolCallContent{
					Type:      "toolCall",
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Function.Name,
					Arguments: args,
				}
				partial.Content[event.ContentIndex] = tcc
				tc = tcc
			}
			out.Push(AssistantMessageEvent{Type: "toolcall_end", ContentIndex: event.ContentIndex, ToolCall: tc, Partial: partial})

		case EventDone:
			partial.StopReason = event.Reason
			if event.Partial != nil {
				partial.Usage = AgentUsage{
					Input:  event.Partial.Usage.PromptTokens,
					Output: event.Partial.Usage.CompletionTokens,
				}
				if partial.Model == "" {
					partial.Model = event.Partial.Model
				}
			}
			out.Push(AssistantMessageEvent{Type: "done", Reason: event.Reason, Message: partial})
			return

		case EventError:
			errMsg := ""
			if event.Err != nil {
				errMsg = event.Err.Error()
			}
			partial.ErrorMessage = errMsg
			partial.StopReason = "error"
			out.Push(AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				ErrorMessage: partial,
				Error:        event.Err,
			})
			return
		}
	}
}

func ensureContentIndex(msg *AssistantMessage, index int) {
	for len(msg.Content) <= index {
		msg.Content = append(msg.Content, nil)
	}
}

func getTextAt(msg *AssistantMessage, index int) (*TextContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*TextContent)
	return tc, ok
}

func getThinkingAt(msg *AssistantMessage, index int) (*ThinkingContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*ThinkingContent)
	return tc, ok
}

func buildChatRequest(model *Model, llmCtx *LLMContext, opts *SimpleStreamOptions) *ChatRequest {
	req := &ChatRequest{
		Model:       model.ID,
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
	}
	if llmCtx.SystemPrompt != "" {
		req.Messages = append(req.Messages, Message{Role: RoleSystem, Content: llmCtx.SystemPrompt})
	}
	for _, m := range llmCtx.Messages {
		switch v := m.(type) {
		case UserMessage:
			if s, ok := v.Content.(string); ok {
				req.Messages = append(req.Messages, Message{Role: RoleUser, Content: s})
			}
		case *UserMessage:
			if s, ok := v.Content.(string); ok {
				req.Messages = append(req.Messages, Message{Role: RoleUser, Content: s})
			}
		case AssistantMessage:
			msg := Message{Role: RoleAssistant}
			for _, block := range v.Content {
				if tc, ok := block.(*TextContent); ok {
					msg.Content += tc.Text
				}
			}
			for _, block := range v.Content {
				if tc, ok := block.(*ToolCallContent); ok {
					args, _ := json.Marshal(tc.Arguments)
					msg.ToolCalls = append(msg.ToolCalls, ToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: FuncCall{
							Name:      tc.Name,
							Arguments: string(args),
						},
					})
				}
			}
			req.Messages = append(req.Messages, msg)
		case *AssistantMessage:
			msg := Message{Role: RoleAssistant}
			for _, block := range v.Content {
				if tc, ok := block.(*TextContent); ok {
					msg.Content += tc.Text
				}
			}
			for _, block := range v.Content {
				if tc, ok := block.(*ToolCallContent); ok {
					args, _ := json.Marshal(tc.Arguments)
					msg.ToolCalls = append(msg.ToolCalls, ToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: FuncCall{
							Name:      tc.Name,
							Arguments: string(args),
						},
					})
				}
			}
			req.Messages = append(req.Messages, msg)
		case ToolResultMessage:
			req.Messages = append(req.Messages, Message{
				Role:       RoleTool,
				Content:    toolResultContent(v.Content),
				ToolCallID: v.ToolCallID,
				Name:       v.ToolName,
			})
		case *ToolResultMessage:
			req.Messages = append(req.Messages, Message{
				Role:       RoleTool,
				Content:    toolResultContent(v.Content),
				ToolCallID: v.ToolCallID,
				Name:       v.ToolName,
			})
		}
	}
	for _, t := range llmCtx.Tools {
		req.Tools = append(req.Tools, Tool{
			Type: "function",
			Function: FuncDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return req
}

func toolResultContent(blocks []ContentBlock) string {
	for _, b := range blocks {
		if tc, ok := b.(*TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// --- Tool argument validation ---

var (
	schemaMu    sync.Mutex
	schemaCache = map[string]*jsonschema.Schema{}
)

// ValidateToolArguments validates tool call arguments against the tool's JSON schema.
// Returns the parsed argument map on success.
func ValidateToolArguments(tool ToolDefinition, toolCall ToolCallContent) (map[string]any, error) {
	if tool.Parameters == nil {
		return toolCall.Arguments, nil
	}

	schemaBytes, err := json.Marshal(tool.Parameters)
	if err != nil {
		return nil, err
	}
	schemaKey := string(schemaBytes)

	schemaMu.Lock()
	schema, ok := schemaCache[schemaKey]
	schemaMu.Unlock()

	if !ok {
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource("schema.json", bytes.NewReader(schemaBytes)); err != nil {
			return nil, err
		}
		compiled, err := compiler.Compile("schema.json")
		if err != nil {
			return nil, err
		}
		schemaMu.Lock()
		schemaCache[schemaKey] = compiled
		schemaMu.Unlock()
		schema = compiled
	}

	if err := schema.Validate(toolCall.Arguments); err != nil {
		return nil, fmt.Errorf("validation failed for tool %q: %v", tool.Name, err)
	}
	return toolCall.Arguments, nil
}
