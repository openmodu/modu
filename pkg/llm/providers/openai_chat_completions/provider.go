package openai_chat_completions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/llm/utils"
)

// OpenAIChatCompletionsProvider implements ApiProvider for OpenAI-compatible
// /v1/chat/completions endpoints (OpenAI, Azure, LocalAI, Ollama-compat, etc.).
// It uses the standard messages format and supports tool calls.
type OpenAIChatCompletionsProvider struct{}

func (p *OpenAIChatCompletionsProvider) Api() llm.Api {
	return "openai-chat-completions"
}

func (p *OpenAIChatCompletionsProvider) Stream(model *llm.Model, ctx *llm.Context, opts *llm.StreamOptions) (llm.AssistantMessageEventStream, error) {
	var simple *llm.SimpleStreamOptions
	if opts != nil {
		s := llm.SimpleStreamOptions{StreamOptions: *opts}
		simple = &s
	}
	return p.StreamSimple(model, ctx, simple)
}

func (p *OpenAIChatCompletionsProvider) StreamSimple(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
	stream := utils.NewEventStream()

	go func() {
		defer stream.Close()

		output := &llm.AssistantMessage{
			Role:      "assistant",
			Api:       model.Api,
			Provider:  model.Provider,
			Model:     model.ID,
			Timestamp: time.Now().UnixMilli(),
		}

		stream.Push(llm.AssistantMessageEvent{
			Type:    "start",
			Partial: output,
		})

		apiKey := ""
		if opts != nil && opts.APIKey != "" {
			apiKey = opts.APIKey
		} else {
			apiKey = llm.GetEnvAPIKey(string(model.Provider))
		}
		if apiKey == "" {
			emitError(stream, output, "API key is required (provider: "+string(model.Provider)+")", nil)
			return
		}

		baseURL := model.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}

		// --- Build request payload ---
		payload := map[string]any{
			"model":    model.ID,
			"messages": buildMessages(ctx),
			"stream":   true,
		}

		if opts != nil {
			if opts.MaxTokens != nil {
				payload["max_tokens"] = *opts.MaxTokens
			}
			if opts.Temperature != nil {
				payload["temperature"] = *opts.Temperature
			}
		}
		if ctx != nil && len(ctx.Tools) > 0 {
			payload["tools"] = buildTools(ctx.Tools)
		}

		body, err := json.Marshal(payload)
		if err != nil {
			emitError(stream, output, err.Error(), err)
			return
		}

		req, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			emitError(stream, output, err.Error(), err)
			return
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range model.Headers {
			req.Header.Set(k, v)
		}
		if opts != nil {
			for k, v := range opts.Headers {
				req.Header.Set(k, v)
			}
		}

		client := &http.Client{Timeout: 10 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			emitError(stream, output, err.Error(), err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			data, _ := io.ReadAll(resp.Body)
			errMsg := strings.TrimSpace(string(data))
			if errMsg == "" {
				errMsg = resp.Status
			}
			emitError(stream, output, errMsg, nil)
			return
		}

		// --- Read SSE stream ---
		reader := bufio.NewScanner(resp.Body)
		reader.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var textBuilder strings.Builder
		textStarted := false
		stopReason := llm.StopReason("stop")

		type toolCallAccumulator struct {
			id       string
			name     string
			argsJSON strings.Builder
			index    int
		}
		toolCallAccumulators := map[int]*toolCallAccumulator{}

		for reader.Scan() {
			line := reader.Text()
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var chunk map[string]any
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				log.Printf("[openai-chat] SSE JSON parse error: %v (data=%q)", err, data)
				continue
			}

			// Handle error in chunk.
			if errObj, ok := chunk["error"].(map[string]any); ok {
				msg := ""
				if m, ok := errObj["message"].(string); ok {
					msg = m
				} else {
					raw, _ := json.Marshal(chunk)
					msg = string(raw)
				}
				emitError(stream, output, msg, nil)
				return
			}

			// Extract usage if present.
			if usage, ok := chunk["usage"].(map[string]any); ok {
				if v, ok := usage["prompt_tokens"].(float64); ok {
					output.Usage.Input = int(v)
				}
				if v, ok := usage["completion_tokens"].(float64); ok {
					output.Usage.Output = int(v)
				}
				if v, ok := usage["total_tokens"].(float64); ok {
					output.Usage.TotalTokens = int(v)
				}
			}

			// Extract choices[0].
			choices, ok := chunk["choices"].([]any)
			if !ok || len(choices) == 0 {
				continue
			}
			choice, ok := choices[0].(map[string]any)
			if !ok {
				continue
			}

			// Check finish_reason.
			if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
				if finish == "tool_calls" {
					stopReason = "toolUse"
				} else {
					stopReason = llm.StopReason(finish)
				}
			}

			delta, ok := choice["delta"].(map[string]any)
			if !ok {
				continue
			}

			// --- Handle text content ---
			if content, ok := delta["content"].(string); ok && content != "" {
				if !textStarted {
					textStarted = true
					output.Content = append(output.Content, &llm.TextContent{Type: "text", Text: ""})
					stream.Push(llm.AssistantMessageEvent{
						Type:         "text_start",
						ContentIndex: 0,
						Partial:      output,
					})
				}
				if tc, ok := output.Content[0].(*llm.TextContent); ok {
					tc.Text += content
				}
				textBuilder.WriteString(content)
				stream.Push(llm.AssistantMessageEvent{
					Type:         "text_delta",
					ContentIndex: 0,
					Delta:        content,
					Partial:      output,
				})
			}

			// --- Handle tool_calls ---
			if toolCalls, ok := delta["tool_calls"].([]any); ok {
				for _, tc := range toolCalls {
					tcMap, ok := tc.(map[string]any)
					if !ok {
						continue
					}
					idx := 0
					if v, ok := tcMap["index"].(float64); ok {
						idx = int(v)
					}
					acc, exists := toolCallAccumulators[idx]
					if !exists {
						acc = &toolCallAccumulator{index: idx}
						toolCallAccumulators[idx] = acc
					}
					if id, ok := tcMap["id"].(string); ok && id != "" {
						acc.id = id
					}
					if fn, ok := tcMap["function"].(map[string]any); ok {
						if name, ok := fn["name"].(string); ok && name != "" {
							acc.name = name
						}
						if args, ok := fn["arguments"].(string); ok {
							acc.argsJSON.WriteString(args)
						}
					}
				}
			}
		}

		if err := reader.Err(); err != nil {
			emitError(stream, output, err.Error(), err)
			return
		}

		// Close text block if started.
		if textStarted {
			stream.Push(llm.AssistantMessageEvent{
				Type:         "text_end",
				ContentIndex: 0,
				Content:      textBuilder.String(),
				Partial:      output,
			})
		}

		// Finalize tool calls.
		if len(toolCallAccumulators) > 0 {
			for _, acc := range toolCallAccumulators {
				var args map[string]any
				if acc.argsJSON.Len() > 0 {
					if err := json.Unmarshal([]byte(acc.argsJSON.String()), &args); err != nil {
						args = map[string]any{}
					}
				} else {
					args = map[string]any{}
				}
				toolCall := &llm.ToolCall{
					Type:      "toolCall",
					ID:        acc.id,
					Name:      acc.name,
					Arguments: args,
				}
				output.Content = append(output.Content, toolCall)
				contentIdx := len(output.Content) - 1
				stream.Push(llm.AssistantMessageEvent{
					Type:         "toolcall_start",
					ContentIndex: contentIdx,
					ToolCall:     toolCall,
					Partial:      output,
				})
				stream.Push(llm.AssistantMessageEvent{
					Type:         "toolcall_end",
					ContentIndex: contentIdx,
					ToolCall:     toolCall,
					Partial:      output,
				})
			}
			stopReason = "toolUse"
		}

		output.StopReason = stopReason
		stream.Push(llm.AssistantMessageEvent{
			Type:    "done",
			Reason:  stopReason,
			Partial: output,
			Message: output,
		})
	}()

	return stream, nil
}

func init() {
	llm.RegisterApiProvider(&OpenAIChatCompletionsProvider{})
}

// --- Helper functions ---

func emitError(stream *utils.EventStream, output *llm.AssistantMessage, msg string, err error) {
	output.StopReason = "error"
	output.ErrorMessage = msg
	stream.Push(llm.AssistantMessageEvent{
		Type:         "error",
		Reason:       "error",
		Partial:      output,
		Message:      output,
		ErrorMessage: output,
		Error:        err,
	})
}

func buildTools(tools []llm.ToolDefinition) []map[string]any {
	var out []map[string]any
	for _, tool := range tools {
		t := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
			},
		}
		if tool.Parameters != nil {
			t["function"].(map[string]any)["parameters"] = tool.Parameters
		}
		out = append(out, t)
	}
	return out
}

func buildMessages(ctx *llm.Context) []map[string]any {
	if ctx == nil {
		return nil
	}
	var out []map[string]any
	if ctx.SystemPrompt != "" {
		out = append(out, map[string]any{
			"role":    "system",
			"content": ctx.SystemPrompt,
		})
	}
	for _, msg := range ctx.Messages {
		switch m := msg.(type) {
		case llm.UserMessage:
			appendUserMessage(&out, m.Content)
		case *llm.UserMessage:
			appendUserMessage(&out, m.Content)
		case llm.AssistantMessage:
			appendAssistantMessage(&out, &m)
		case *llm.AssistantMessage:
			appendAssistantMessage(&out, m)
		case llm.ToolResultMessage:
			appendToolResultMessage(&out, &m)
		case *llm.ToolResultMessage:
			appendToolResultMessage(&out, m)
		}
	}
	return out
}

func appendUserMessage(out *[]map[string]any, content any) {
	text := extractText(content)
	if text == "" {
		return
	}
	*out = append(*out, map[string]any{
		"role":    "user",
		"content": text,
	})
}

func appendAssistantMessage(out *[]map[string]any, m *llm.AssistantMessage) {
	msg := map[string]any{"role": "assistant"}
	var toolCalls []map[string]any
	var textContent string
	for _, block := range m.Content {
		switch v := block.(type) {
		case *llm.ToolCall:
			argsJSON, _ := json.Marshal(v.Arguments)
			toolCalls = append(toolCalls, map[string]any{
				"id":   v.ID,
				"type": "function",
				"function": map[string]any{
					"name":      v.Name,
					"arguments": string(argsJSON),
				},
			})
		case llm.ToolCall:
			argsJSON, _ := json.Marshal(v.Arguments)
			toolCalls = append(toolCalls, map[string]any{
				"id":   v.ID,
				"type": "function",
				"function": map[string]any{
					"name":      v.Name,
					"arguments": string(argsJSON),
				},
			})
		case *llm.TextContent:
			textContent += v.Text
		case llm.TextContent:
			textContent += v.Text
		}
	}
	if textContent != "" {
		msg["content"] = textContent
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
		if textContent == "" {
			msg["content"] = nil
		}
	}
	*out = append(*out, msg)
}

func appendToolResultMessage(out *[]map[string]any, m *llm.ToolResultMessage) {
	text := extractText(m.Content)
	*out = append(*out, map[string]any{
		"role":         "tool",
		"tool_call_id": m.ToolCallID,
		"content":      text,
	})
}

func extractText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []llm.ContentBlock:
		var b strings.Builder
		for _, block := range v {
			switch t := block.(type) {
			case llm.TextContent:
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(t.Text)
			case *llm.TextContent:
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(t.Text)
			}
		}
		return b.String()
	case []any:
		var b strings.Builder
		for _, block := range v {
			if m, ok := block.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					if b.Len() > 0 {
						b.WriteString("\n")
					}
					b.WriteString(s)
				}
			}
		}
		return b.String()
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
}
