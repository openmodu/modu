package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
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
			req.Messages = append(req.Messages, userProviderMessage(v.Content))
		case *types.UserMessage:
			req.Messages = append(req.Messages, userProviderMessage(v.Content))
		case types.AssistantMessage:
			req.Messages = append(req.Messages, assistantProviderMessage(v.Content))
		case *types.AssistantMessage:
			req.Messages = append(req.Messages, assistantProviderMessage(v.Content))
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

// userProviderMessage converts a UserMessage content (string, []ContentBlock, or []interface{} from JSON) to a providers.Message.
func userProviderMessage(content any) providers.Message {
	switch c := content.(type) {
	case string:
		return providers.Message{Role: providers.RoleUser, Content: c}
	case []types.ContentBlock:
		parts := contentBlocksToParts(c)
		return providers.Message{Role: providers.RoleUser, Content: parts}
	case []interface{}:
		// JSON round-trip: []ContentBlock becomes []interface{} with map[string]any elements
		parts := rawBlocksToParts(c)
		if len(parts) > 0 {
			return providers.Message{Role: providers.RoleUser, Content: parts}
		}
	}
	return providers.Message{Role: providers.RoleUser}
}

func contentBlocksToParts(blocks []types.ContentBlock) []any {
	var parts []any
	for _, block := range blocks {
		switch b := block.(type) {
		case *types.TextContent:
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
		case *types.ImageContent:
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]any{
					"url": "data:" + b.MimeType + ";base64," + b.Data,
				},
			})
		}
	}
	return parts
}

// rawBlocksToParts converts []interface{} (JSON-deserialized content blocks) to API parts.
func rawBlocksToParts(blocks []interface{}) []any {
	var parts []any
	for _, block := range blocks {
		m, ok := block.(map[string]interface{})
		if !ok {
			continue
		}
		blockType, _ := m["type"].(string)
		switch blockType {
		case "text":
			if text, ok := m["text"].(string); ok && text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "image":
			mimeType, _ := m["mimeType"].(string)
			data, _ := m["data"].(string)
			if mimeType != "" && data != "" {
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": "data:" + mimeType + ";base64," + data,
					},
				})
			}
		}
	}
	return parts
}

// assistantProviderMessage converts AssistantMessage content blocks to a providers.Message.
func assistantProviderMessage(content []types.ContentBlock) providers.Message {
	msg := providers.Message{Role: providers.RoleAssistant}
	var textBuf string
	for _, block := range content {
		if tc, ok := block.(*types.TextContent); ok {
			textBuf += tc.Text
		}
	}
	if textBuf != "" {
		msg.Content = textBuf
	}
	for _, block := range content {
		if tc, ok := block.(*types.ToolCallContent); ok {
			args, _ := json.Marshal(tc.Arguments)
			msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: providers.FuncCall{Name: tc.Name, Arguments: string(args)},
			})
		}
	}
	return msg
}

func toolResultContent(blocks []types.ContentBlock) string {
	for _, b := range blocks {
		if tc, ok := b.(*types.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// --- Tool argument validation ---

var (
	schemaCache sync.Map // map[string]*jsonschema.Schema
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

	// Fast path: check if already compiled
	if cached, ok := schemaCache.Load(schemaKey); ok {
		return validateAgainstSchema(cached.(*jsonschema.Schema), toolCall.Arguments, tool.Name)
	}

	// Slow path: compile and cache
	compiled, err := compileSchema(schemaBytes)
	if err != nil {
		return nil, err
	}

	// Double-check after acquiring write lock
	if cached, ok := schemaCache.LoadOrStore(schemaKey, compiled); ok {
		compiled = cached.(*jsonschema.Schema)
	}

	return validateAgainstSchema(compiled, toolCall.Arguments, tool.Name)
}

func compileSchema(schemaBytes []byte) (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(schemaBytes)); err != nil {
		return nil, err
	}
	return compiler.Compile("schema.json")
}

func validateAgainstSchema(schema *jsonschema.Schema, args any, toolName string) (map[string]any, error) {
	if err := schema.Validate(args); err != nil {
		return nil, fmt.Errorf("validation failed for tool %q: %v", toolName, err)
	}
	result, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid argument type for tool %q", toolName)
	}
	return result, nil
}
