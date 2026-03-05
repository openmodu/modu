package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/types"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// StreamDefault is the default StreamFn. It looks up the provider from the
// global registry by model.ProviderID and calls its Stream method.
func StreamDefault(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
	p, ok := providers.Get(model.ProviderID)
	if !ok {
		return nil, fmt.Errorf("no provider registered for %q", model.ProviderID)
	}
	req := buildChatRequest(model, llmCtx, opts)
	return p.Stream(ctx, req)
}

func buildChatRequest(model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) *providers.ChatRequest {
	req := &providers.ChatRequest{
		Model:       model.ID,
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
	}
	if llmCtx.SystemPrompt != "" {
		req.Messages = append(req.Messages, providers.Message{Role: providers.RoleSystem, Content: llmCtx.SystemPrompt})
	}
	for _, m := range llmCtx.Messages {
		switch v := m.(type) {
		case types.UserMessage:
			if s, ok := v.Content.(string); ok {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: s})
			}
		case *types.UserMessage:
			if s, ok := v.Content.(string); ok {
				req.Messages = append(req.Messages, providers.Message{Role: providers.RoleUser, Content: s})
			}
		case types.AssistantMessage:
			msg := providers.Message{Role: providers.RoleAssistant}
			for _, block := range v.Content {
				switch tc := block.(type) {
				case *types.TextContent:
					msg.Content += tc.Text
				case types.TextContent:
					msg.Content += tc.Text
				}
			}
			for _, block := range v.Content {
				switch tc := block.(type) {
				case *types.ToolCallContent:
					args, _ := json.Marshal(tc.Arguments)
					msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
						ID:       tc.ID,
						Type:     "function",
						Function: providers.FuncCall{Name: tc.Name, Arguments: string(args)},
					})
				case types.ToolCallContent:
					args, _ := json.Marshal(tc.Arguments)
					msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
						ID:       tc.ID,
						Type:     "function",
						Function: providers.FuncCall{Name: tc.Name, Arguments: string(args)},
					})
				}
			}
			req.Messages = append(req.Messages, msg)
		case *types.AssistantMessage:
			msg := providers.Message{Role: providers.RoleAssistant}
			for _, block := range v.Content {
				switch tc := block.(type) {
				case *types.TextContent:
					msg.Content += tc.Text
				case types.TextContent:
					msg.Content += tc.Text
				}
			}
			for _, block := range v.Content {
				switch tc := block.(type) {
				case *types.ToolCallContent:
					args, _ := json.Marshal(tc.Arguments)
					msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
						ID:       tc.ID,
						Type:     "function",
						Function: providers.FuncCall{Name: tc.Name, Arguments: string(args)},
					})
				case types.ToolCallContent:
					args, _ := json.Marshal(tc.Arguments)
					msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
						ID:       tc.ID,
						Type:     "function",
						Function: providers.FuncCall{Name: tc.Name, Arguments: string(args)},
					})
				}
			}
			req.Messages = append(req.Messages, msg)
		case types.ToolResultMessage:
			req.Messages = append(req.Messages, providers.Message{
				Role:       providers.RoleTool,
				Content:    toolResultContent(v.Content),
				ToolCallID: v.ToolCallID,
				Name:       v.ToolName,
			})
		case *types.ToolResultMessage:
			req.Messages = append(req.Messages, providers.Message{
				Role:       providers.RoleTool,
				Content:    toolResultContent(v.Content),
				ToolCallID: v.ToolCallID,
				Name:       v.ToolName,
			})
		}
	}
	for _, t := range llmCtx.Tools {
		req.Tools = append(req.Tools, providers.Tool{
			Type: "function",
			Function: providers.FuncDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return req
}

func toolResultContent(blocks []types.ContentBlock) string {
	for _, b := range blocks {
		switch tc := b.(type) {
		case *types.TextContent:
			return tc.Text
		case types.TextContent:
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
func ValidateToolArguments(tool types.ToolDefinition, toolCall types.ToolCallContent) (map[string]any, error) {
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
