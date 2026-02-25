package deepseek

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/llm/utils"
)

type DeepSeekProvider struct{}

func (p *DeepSeekProvider) Api() llm.Api {
	return "deepseek-chat-completions"
}

func (p *DeepSeekProvider) Stream(model *llm.Model, ctx *llm.Context, opts *llm.StreamOptions) (llm.AssistantMessageEventStream, error) {
	var simple *llm.SimpleStreamOptions
	if opts != nil {
		s := llm.SimpleStreamOptions{StreamOptions: *opts}
		simple = &s
	}
	return p.StreamSimple(model, ctx, simple)
}

func (p *DeepSeekProvider) StreamSimple(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
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
			output.ErrorMessage = "DeepSeek API key is required"
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
			baseURL = "https://api.deepseek.com/v1"
		}

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

		req, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(body))
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
		stopReason := llm.StopReason("stop")
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
				log.Printf("[deepseek] SSE JSON parse error: %v (data=%q)", err, data)
				continue
			}
			if errObj, ok := payload["error"].(map[string]any); ok {
				output.StopReason = "error"
				if msg, ok := errObj["message"].(string); ok {
					output.ErrorMessage = msg
				} else {
					raw, _ := json.Marshal(payload)
					output.ErrorMessage = string(raw)
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
			choices, ok := payload["choices"].([]any)
			if !ok || len(choices) == 0 {
				continue
			}
			choice, ok := choices[0].(map[string]any)
			if !ok {
				continue
			}
			if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
				stopReason = llm.StopReason(finish)
			}
			delta, ok := choice["delta"].(map[string]any)
			if !ok {
				continue
			}
			part, _ := delta["content"].(string)
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
	llm.RegisterApiProvider(&DeepSeekProvider{})
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
			appendMessage(&out, "user", extractText(m.Content))
		case *llm.UserMessage:
			appendMessage(&out, "user", extractText(m.Content))
		case llm.AssistantMessage:
			appendMessage(&out, "assistant", extractText(m.Content))
		case *llm.AssistantMessage:
			appendMessage(&out, "assistant", extractText(m.Content))
		case llm.ToolResultMessage:
			appendMessage(&out, "tool", extractText(m.Content))
		case *llm.ToolResultMessage:
			appendMessage(&out, "tool", extractText(m.Content))
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
		"role":    role,
		"content": text,
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
