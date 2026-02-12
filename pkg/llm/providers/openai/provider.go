package openai

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

type OpenAIProvider struct{}

func (p *OpenAIProvider) Api() llm.Api {
	return "openai-responses"
}

func (p *OpenAIProvider) Stream(model *llm.Model, ctx *llm.Context, opts *llm.StreamOptions) (llm.AssistantMessageEventStream, error) {
	var simple *llm.SimpleStreamOptions
	if opts != nil {
		s := llm.SimpleStreamOptions{StreamOptions: *opts}
		simple = &s
	}
	return p.StreamSimple(model, ctx, simple)
}

func (p *OpenAIProvider) StreamSimple(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
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
			output.ErrorMessage = "OpenAI API key is required"
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
			baseURL = "https://api.openai.com/v1"
		}

		payload := map[string]any{
			"model":  model.ID,
			"input":  buildInput(ctx),
			"stream": true,
		}
		if opts != nil {
			if opts.MaxTokens != nil {
				payload["max_output_tokens"] = *opts.MaxTokens
			}
			if opts.Temperature != nil {
				payload["temperature"] = *opts.Temperature
			}
			if opts.SessionID != "" && opts.CacheRetention != "none" {
				payload["prompt_cache_key"] = opts.SessionID
			}
			if opts.Reasoning != "" && model.Reasoning {
				payload["reasoning"] = map[string]any{
					"effort": string(opts.Reasoning),
				}
			}
		}

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

		req, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/responses", bytes.NewReader(body))
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
			var payload map[string]any
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				continue
			}
			t, _ := payload["type"].(string)
			if t == "response.error" {
				output.StopReason = "error"
				output.ErrorMessage = extractErrorMessage(payload)
				stream.Push(llm.AssistantMessageEvent{
					Type:         "error",
					Reason:       "error",
					Partial:      output,
					Message:      output,
					ErrorMessage: output,
				})
				return
			}
			if strings.HasSuffix(t, "output_text.delta") {
				delta, _ := payload["delta"].(string)
				if delta == "" {
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
					tc.Text += delta
				}
				text.WriteString(delta)
				stream.Push(llm.AssistantMessageEvent{
					Type:         "text_delta",
					ContentIndex: 0,
					Delta:        delta,
					Partial:      output,
				})
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

		if started {
			stream.Push(llm.AssistantMessageEvent{
				Type:         "text_end",
				ContentIndex: 0,
				Content:      text.String(),
				Partial:      output,
			})
		}

		output.StopReason = "stop"
		stream.Push(llm.AssistantMessageEvent{
			Type:    "done",
			Reason:  "stop",
			Partial: output,
			Message: output,
		})
	}()

	return stream, nil
}

func init() {
	llm.RegisterApiProvider(&OpenAIProvider{})
}

func buildInput(ctx *llm.Context) []map[string]any {
	if ctx == nil {
		return nil
	}
	var out []map[string]any
	if ctx.SystemPrompt != "" {
		out = append(out, map[string]any{
			"role": "system",
			"content": []map[string]any{
				{"type": "input_text", "text": ctx.SystemPrompt},
			},
		})
	}
	for _, msg := range ctx.Messages {
		switch m := msg.(type) {
		case llm.UserMessage:
			appendInputMessage(&out, "user", extractText(m.Content))
		case *llm.UserMessage:
			appendInputMessage(&out, "user", extractText(m.Content))
		case llm.AssistantMessage:
			appendInputMessage(&out, "assistant", extractText(m.Content))
		case *llm.AssistantMessage:
			appendInputMessage(&out, "assistant", extractText(m.Content))
		case llm.ToolResultMessage:
			appendInputMessage(&out, "tool", extractText(m.Content))
		case *llm.ToolResultMessage:
			appendInputMessage(&out, "tool", extractText(m.Content))
		default:
		}
	}
	return out
}

func appendInputMessage(out *[]map[string]any, role string, text string) {
	if text == "" {
		return
	}
	*out = append(*out, map[string]any{
		"role": role,
		"content": []map[string]any{
			{"type": "input_text", "text": text},
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
			switch t := block.(type) {
			case map[string]any:
				if s, ok := t["text"].(string); ok {
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

func extractErrorMessage(payload map[string]any) string {
	if errObj, ok := payload["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok && msg != "" {
			return msg
		}
	}
	if msg, ok := payload["message"].(string); ok && msg != "" {
		return msg
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}
