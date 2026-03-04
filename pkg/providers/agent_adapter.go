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
func StreamDefault(ctx context.Context, model *Model, llmCtx *LLMContext, opts *SimpleStreamOptions) (EventStream, error) {
	p, ok := Get(model.ProviderID)
	if !ok {
		return nil, fmt.Errorf("no provider registered for %q", model.ProviderID)
	}
	req := buildChatRequest(model, llmCtx, opts)
	return p.Stream(ctx, req)
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
