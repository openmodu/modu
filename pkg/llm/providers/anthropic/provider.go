package anthropic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/llm/utils"
)

type AnthropicProvider struct{}

func (p *AnthropicProvider) Api() llm.Api {
	return "anthropic-messages"
}

func (p *AnthropicProvider) Stream(model *llm.Model, ctx *llm.Context, opts *llm.StreamOptions) (llm.AssistantMessageEventStream, error) {
	var simple *llm.SimpleStreamOptions
	if opts != nil {
		s := llm.SimpleStreamOptions{StreamOptions: *opts}
		simple = &s
	}
	return p.StreamSimple(model, ctx, simple)
}

func (p *AnthropicProvider) StreamSimple(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
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
			output.StopReason = "error"
			output.ErrorMessage = "Anthropic API key is required"
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
			})
			return
		}

		baseURL := model.BaseURL
		if baseURL == "" {
			baseURL = "https://api.anthropic.com/v1"
		}

		payload := map[string]any{
			"model":  model.ID,
			"stream": true,
		}
		if opts != nil && opts.MaxTokens != nil {
			payload["max_tokens"] = *opts.MaxTokens
		} else if model.MaxTokens > 0 {
			payload["max_tokens"] = model.MaxTokens
		} else {
			payload["max_tokens"] = 1024
		}
		if opts != nil && opts.Temperature != nil {
			payload["temperature"] = *opts.Temperature
		}
		if ctx != nil && ctx.SystemPrompt != "" {
			payload["system"] = ctx.SystemPrompt
		}
		payload["messages"] = buildMessages(ctx)

		body, err := json.Marshal(payload)
		if err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}

		req, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/messages", bytes.NewReader(body))
		if err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")
		for k, v := range model.Headers {
			req.Header.Set(k, v)
		}
		if opts != nil {
			for k, v := range opts.Headers {
				req.Header.Set(k, v)
			}
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			data, _ := io.ReadAll(resp.Body)
			output.StopReason = "error"
			output.ErrorMessage = strings.TrimSpace(string(data))
			if output.ErrorMessage == "" {
				output.ErrorMessage = resp.Status
			}
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
			})
			return
		}

		reader := bufio.NewScanner(resp.Body)
		reader.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var text strings.Builder
		started := false
		for reader.Scan() {
			line := reader.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "" {
				continue
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				continue
			}
			t, _ := payload["type"].(string)
			if t == "error" {
				output.StopReason = "error"
				output.ErrorMessage = extractAnthropicError(payload)
				stream.Push(llm.AssistantMessageEvent{
					Type:         "error",
					Reason:       "error",
					Partial:      output,
					Message:      output,
					ErrorMessage: output,
				})
				return
			}
			if t == "content_block_delta" {
				delta, _ := payload["delta"].(map[string]any)
				if delta == nil {
					continue
				}
				if deltaType, _ := delta["type"].(string); deltaType == "text_delta" {
					part, _ := delta["text"].(string)
					if part == "" {
						continue
					}
					if !started {
						started = true
						output.Content = append(output.Content, &llm.TextContent{Type: "text", Text: ""})
						stream.Push(llm.AssistantMessageEvent{
							Type:         "text_start",
							ContentIndex: 0,
							Partial:      output,
						})
					}
					if tc, ok := output.Content[0].(*llm.TextContent); ok {
						tc.Text += part
					}
					text.WriteString(part)
					stream.Push(llm.AssistantMessageEvent{
						Type:         "text_delta",
						ContentIndex: 0,
						Delta:        part,
						Partial:      output,
					})
				}
			}
			if t == "message_delta" {
				if delta, ok := payload["delta"].(map[string]any); ok {
					if reason, ok := delta["stop_reason"].(string); ok && reason != "" {
						output.StopReason = llm.StopReason(reason)
					}
				}
			}
			if t == "message_stop" {
				if output.StopReason == "" {
					output.StopReason = "stop"
				}
				if started {
					stream.Push(llm.AssistantMessageEvent{
						Type:         "text_end",
						ContentIndex: 0,
						Content:      text.String(),
						Partial:      output,
					})
				}
				stream.Push(llm.AssistantMessageEvent{
					Type:    "done",
					Reason:  output.StopReason,
					Partial: output,
					Message: output,
				})
				return
			}
		}
		if err := reader.Err(); err != nil {
			output.StopReason = "error"
			output.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
				Error:        err,
			})
			return
		}
	}()

	return stream, nil
}

func init() {
	llm.RegisterApiProvider(&AnthropicProvider{})
}

func buildMessages(ctx *llm.Context) []map[string]any {
	if ctx == nil {
		return nil
	}
	var out []map[string]any
	for _, msg := range ctx.Messages {
		switch m := msg.(type) {
		case llm.UserMessage:
			appendMessage(&out, "user", extractText(m.Content))
		case *llm.UserMessage:
			appendMessage(&out, "user", extractText(m.Content))
		case llm.AssistantMessage:
			appendMessage(&out, "assistant", extractText(m.Content))
		case *llm.AssistantMessage:
			appendMessage(&out, "assistant", extractText(m.Content))
		case llm.ToolResultMessage:
			appendMessage(&out, "user", extractText(m.Content))
		case *llm.ToolResultMessage:
			appendMessage(&out, "user", extractText(m.Content))
		default:
		}
	}
	return out
}

func appendMessage(out *[]map[string]any, role string, text string) {
	if text == "" {
		return
	}
	*out = append(*out, map[string]any{
		"role": role,
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
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
		return ""
	}
}

func extractAnthropicError(payload map[string]any) string {
	if errObj, ok := payload["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && msg != "" {
			return msg
		}
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}
