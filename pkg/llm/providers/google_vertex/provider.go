package google_vertex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/llm"
	"github.com/crosszan/modu/pkg/llm/utils"
)

type GoogleVertexProvider struct{}

func (p *GoogleVertexProvider) Api() llm.Api {
	return "google-vertex"
}

func (p *GoogleVertexProvider) Stream(model *llm.Model, ctx *llm.Context, opts *llm.StreamOptions) (llm.AssistantMessageEventStream, error) {
	var simple *llm.SimpleStreamOptions
	if opts != nil {
		s := llm.SimpleStreamOptions{StreamOptions: *opts}
		simple = &s
	}
	return p.StreamSimple(model, ctx, simple)
}

func (p *GoogleVertexProvider) StreamSimple(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
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

		token := ""
		if opts != nil && opts.APIKey != "" {
			token = opts.APIKey
		} else if v := os.Getenv("GOOGLE_OAUTH_ACCESS_TOKEN"); v != "" {
			token = v
		}
		if token == "" {
			output.StopReason = "error"
			output.ErrorMessage = "Vertex access token is required"
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       "error",
				Partial:      output,
				Message:      output,
				ErrorMessage: output,
			})
			return
		}

		project := os.Getenv("GOOGLE_CLOUD_PROJECT")
		if project == "" {
			project = os.Getenv("GCLOUD_PROJECT")
		}
		location := os.Getenv("GOOGLE_CLOUD_LOCATION")
		if project == "" || location == "" {
			output.StopReason = "error"
			output.ErrorMessage = "Vertex project/location is required"
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
			baseURL = "https://" + location + "-aiplatform.googleapis.com/v1"
		}

		endpoint := strings.TrimRight(baseURL, "/") + "/projects/" + url.PathEscape(project) + "/locations/" + url.PathEscape(location) + "/publishers/google/models/" + url.PathEscape(model.ID) + ":streamGenerateContent"
		query := "?alt=sse"

		payload := map[string]any{
			"contents": buildVertexContents(ctx),
		}
		if ctx != nil && ctx.SystemPrompt != "" {
			payload["system_instruction"] = map[string]any{
				"parts": []map[string]any{{"text": ctx.SystemPrompt}},
			}
		}
		if opts != nil {
			config := map[string]any{}
			if opts.MaxTokens != nil {
				config["maxOutputTokens"] = *opts.MaxTokens
			}
			if opts.Temperature != nil {
				config["temperature"] = *opts.Temperature
			}
			if len(config) > 0 {
				payload["generationConfig"] = config
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

		req, err := http.NewRequest("POST", endpoint+query, bytes.NewReader(body))
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
		req.Header.Set("content-type", "application/json")
		req.Header.Set("authorization", "Bearer "+token)
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
			delta := extractVertexDelta(payload)
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
	llm.RegisterApiProvider(&GoogleVertexProvider{})
}

func buildVertexContents(ctx *llm.Context) []map[string]any {
	if ctx == nil {
		return nil
	}
	var out []map[string]any
	for _, msg := range ctx.Messages {
		switch m := msg.(type) {
		case llm.UserMessage:
			appendVertexContent(&out, "user", extractVertexText(m.Content))
		case *llm.UserMessage:
			appendVertexContent(&out, "user", extractVertexText(m.Content))
		case llm.AssistantMessage:
			appendVertexContent(&out, "model", extractVertexText(m.Content))
		case *llm.AssistantMessage:
			appendVertexContent(&out, "model", extractVertexText(m.Content))
		case llm.ToolResultMessage:
			appendVertexContent(&out, "user", extractVertexText(m.Content))
		case *llm.ToolResultMessage:
			appendVertexContent(&out, "user", extractVertexText(m.Content))
		default:
		}
	}
	return out
}

func appendVertexContent(out *[]map[string]any, role string, text string) {
	if text == "" {
		return
	}
	*out = append(*out, map[string]any{
		"role":  role,
		"parts": []map[string]any{{"text": text}},
	})
}

func extractVertexText(content any) string {
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

func extractVertexDelta(payload map[string]any) string {
	candidates, ok := payload["candidates"].([]any)
	if !ok || len(candidates) == 0 {
		return ""
	}
	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := candidate["content"].(map[string]any)
	if !ok {
		return ""
	}
	parts, ok := content["parts"].([]any)
	if !ok || len(parts) == 0 {
		return ""
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := part["text"].(string)
	return text
}
