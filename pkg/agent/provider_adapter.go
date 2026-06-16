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
	knownTools := knownToolNames(llmCtx.Tools)
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
	if llmCtx.SystemPrompt != "" {
		req.Messages = append(req.Messages, providers.Message{Role: providers.RoleSystem, Content: llmCtx.SystemPrompt})
	}
	// Only the most recent assistant message keeps its thinking/reasoning content.
	// Replaying thinking from prior turns re-injects stale reasoning paths into the
	// model's context, so historical thinking is dropped here.
	lastAssistantIdx := -1
	for i, message := range llmCtx.Messages {
		switch message.(type) {
		case types.AssistantMessage, *types.AssistantMessage:
			lastAssistantIdx = i
		}
	}
	for i, message := range llmCtx.Messages {
		switch v := message.(type) {
		case types.UserMessage:
			req.Messages = append(req.Messages, userProviderMessage(v.Content))
		case *types.UserMessage:
			req.Messages = append(req.Messages, userProviderMessage(v.Content))
		case types.AssistantMessage:
			req.Messages = append(req.Messages, assistantProviderMessage(v.Content, knownTools, i == lastAssistantIdx))
		case *types.AssistantMessage:
			req.Messages = append(req.Messages, assistantProviderMessage(v.Content, knownTools, i == lastAssistantIdx))
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
	req.Messages = sanitizeProviderMessages(req.Messages)
	return req
}

func knownToolNames(tools []types.ToolDefinition) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if tool.Name != "" {
			out[tool.Name] = true
		}
	}
	return out
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

func assistantProviderMessage(content []types.ContentBlock, knownTools map[string]bool, keepThinking bool) providers.Message {
	msg := providers.Message{Role: providers.RoleAssistant}
	var textBuf string
	var reasoningBuf string
	for _, block := range content {
		switch tc := block.(type) {
		case *types.ThinkingContent:
			if tc != nil && keepThinking {
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
			if !knownTools[tc.Name] {
				continue
			}
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

func sanitizeProviderMessages(messages []providers.Message) []providers.Message {
	out := make([]providers.Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == providers.RoleTool {
			continue
		}
		if msg.Role == providers.RoleAssistant && len(msg.ToolCalls) == 0 && !providerMessageHasContent(msg) {
			continue
		}
		if msg.Role != providers.RoleAssistant || len(msg.ToolCalls) == 0 {
			out = append(out, msg)
			continue
		}

		toolIDs := make(map[string]providers.ToolCall, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			if call.ID != "" {
				toolIDs[call.ID] = call
			}
		}
		var toolResults []providers.Message
		j := i + 1
		for j < len(messages) && messages[j].Role == providers.RoleTool {
			if _, ok := toolIDs[messages[j].ToolCallID]; ok {
				toolResults = append(toolResults, messages[j])
			}
			j++
		}

		if len(toolResults) == 0 {
			msg.ToolCalls = nil
			if providerMessageHasContent(msg) {
				out = append(out, msg)
			}
			i = j - 1
			continue
		}

		validCalls := make([]providers.ToolCall, 0, len(toolResults))
		for _, result := range toolResults {
			if call, ok := toolIDs[result.ToolCallID]; ok {
				validCalls = append(validCalls, call)
				delete(toolIDs, result.ToolCallID)
			}
		}
		msg.ToolCalls = validCalls
		out = append(out, msg)
		out = append(out, toolResults...)
		i = j - 1
	}
	return out
}

func providerMessageHasContent(msg providers.Message) bool {
	if msg.Content == nil {
		return false
	}
	if s, ok := msg.Content.(string); ok {
		return s != ""
	}
	return true
}

func toolResultContent(blocks []types.ContentBlock) string {
	for _, block := range blocks {
		if tc, ok := block.(*types.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
