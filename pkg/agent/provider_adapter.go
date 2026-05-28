package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

func StreamDefault(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
	p, ok := providers.Get(model.ProviderID)
	if !ok {
		return nil, fmt.Errorf("no provider registered for %q", model.ProviderID)
	}
	return p.Stream(ctx, buildChatRequest(model, llmCtx, opts))
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
	for _, message := range llmCtx.Messages {
		switch v := message.(type) {
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
	for _, tool := range llmCtx.Tools {
		req.Tools = append(req.Tools, providers.Tool{
			Type: "function",
			Function: providers.FuncDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return req
}

func userProviderMessage(content any) providers.Message {
	switch c := content.(type) {
	case string:
		return providers.Message{Role: providers.RoleUser, Content: c}
	case []types.ContentBlock:
		return providers.Message{Role: providers.RoleUser, Content: contentBlocksToParts(c)}
	case []interface{}:
		parts := rawBlocksToParts(c)
		if len(parts) > 0 {
			return providers.Message{Role: providers.RoleUser, Content: parts}
		}
	}
	return providers.Message{Role: providers.RoleUser}
}

func contentBlocksToParts(blocks []types.ContentBlock) []any {
	parts := make([]any, 0, len(blocks))
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

func assistantProviderMessage(content []types.ContentBlock) providers.Message {
	msg := providers.Message{Role: providers.RoleAssistant}
	var textBuf string
	var reasoningBuf string
	for _, block := range content {
		switch tc := block.(type) {
		case *types.ThinkingContent:
			if tc != nil {
				reasoningBuf += tc.Thinking
			}
		case *types.TextContent:
			if tc != nil {
				textBuf += tc.Text
			}
		}
	}
	if reasoningBuf != "" {
		msg.ReasoningContent = reasoningBuf
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
	for _, block := range blocks {
		if tc, ok := block.(*types.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
