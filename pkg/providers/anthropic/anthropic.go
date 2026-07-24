// Package anthropic provides an Anthropic Messages API provider.
package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

const (
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
	DefaultModel   = "claude-sonnet-4-5"
)

type anthropicProvider struct {
	apiKey  string
	baseURL string
	model   string
}

// anthropicUsage mirrors the Messages API usage object. input_tokens already
// excludes the cached portions, which are reported separately.
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// New creates an Anthropic provider.
func New(apiKey, model string) providers.Provider {
	if model == "" {
		model = DefaultModel
	}
	return &anthropicProvider{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		model:   model,
	}
}

func (p *anthropicProvider) ID() string { return "anthropic" }

func (p *anthropicProvider) Chat(ctx context.Context, req *providers.ChatRequest) (*providers.ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	body, err := p.buildBody(req, model, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("anthropic: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var raw struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string         `json:"stop_reason"`
		Model      string         `json:"model"`
		Usage      anthropicUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("anthropic: decode: %w", err)
	}
	var sb strings.Builder
	for _, b := range raw.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	// input_tokens excludes the cached portions, so add them back to keep
	// PromptTokens the total input while reporting the cached subset.
	promptTokens := raw.Usage.InputTokens + raw.Usage.CacheReadInputTokens + raw.Usage.CacheCreationInputTokens
	return &providers.ChatResponse{
		Model:        raw.Model,
		Message:      providers.Message{Role: providers.RoleAssistant, Content: sb.String()},
		FinishReason: raw.StopReason,
		Usage: providers.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      promptTokens + raw.Usage.OutputTokens,
			CacheReadTokens:  raw.Usage.CacheReadInputTokens,
			CacheWriteTokens: raw.Usage.CacheCreationInputTokens,
		},
	}, nil
}

func (p *anthropicProvider) Stream(ctx context.Context, req *providers.ChatRequest) (types.EventStream, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	body, err := p.buildBody(req, model, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.doRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	stream := types.NewEventStream()
	go p.readSSE(resp.Body, model, stream)
	return stream, nil
}

func (p *anthropicProvider) readSSE(body io.ReadCloser, model string, stream *types.EventStreamImpl) {
	defer body.Close()
	defer stream.Close()

	partial := &types.AssistantMessage{
		Role:       "assistant",
		Content:    []types.ContentBlock{},
		ProviderID: "anthropic",
		Model:      model,
	}
	stream.Push(types.StreamEvent{Type: types.EventStart, Partial: partial})

	var textBuf strings.Builder
	started := false

	if err := providers.ScanSSEData(body, func(data string) bool {
		var ev struct {
			Type    string `json:"type"`
			Message *struct {
				Usage *anthropicUsage `json:"usage"`
			} `json:"message"`
			Delta *struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *anthropicUsage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return true
		}

		switch ev.Type {
		case "message_start":
			// message_start carries the input side: fresh input_tokens plus
			// the cache-hit and cache-creation counts, which Anthropic already
			// reports separately from input_tokens.
			if ev.Message != nil && ev.Message.Usage != nil {
				u := ev.Message.Usage
				partial.Usage.Input = u.InputTokens
				partial.Usage.CacheRead = u.CacheReadInputTokens
				partial.Usage.CacheWrite = u.CacheCreationInputTokens
				partial.Usage.Output = u.OutputTokens
			}
		case "content_block_start":
			if !started {
				started = true
				partial.Content = append(partial.Content, &types.TextContent{Type: "text", Text: ""})
				stream.Push(types.StreamEvent{Type: types.EventTextStart, ContentIndex: 0, Partial: partial})
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				delta := ev.Delta.Text
				textBuf.WriteString(delta)
				if tc, ok := partial.Content[0].(*types.TextContent); ok {
					tc.Text = textBuf.String()
				}
				stream.Push(types.StreamEvent{Type: types.EventTextDelta, ContentIndex: 0, Delta: delta, Partial: partial})
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				partial.StopReason = types.StopReason(ev.Delta.StopReason)
			}
			// message_delta carries the final cumulative output_tokens.
			if ev.Usage != nil && ev.Usage.OutputTokens > 0 {
				partial.Usage.Output = ev.Usage.OutputTokens
			}
		case "error":
			if ev.Error != nil {
				err := fmt.Errorf("%s", ev.Error.Message)
				stream.Push(types.StreamEvent{Type: types.EventError, Error: err})
				stream.Resolve(partial, err)
				return false
			}
		}
		return true
	}); err != nil {
		stream.Push(types.StreamEvent{Type: types.EventError, Error: err})
		stream.Resolve(partial, err)
		return
	}

	if started {
		stream.Push(types.StreamEvent{Type: types.EventTextEnd, ContentIndex: 0, Content: textBuf.String(), Partial: partial})
	}

	if partial.StopReason == "" {
		partial.StopReason = "end_turn"
	}
	partial.Usage.TotalTokens = partial.Usage.Input + partial.Usage.Output +
		partial.Usage.CacheRead + partial.Usage.CacheWrite
	stream.Push(types.StreamEvent{Type: types.EventDone, Reason: partial.StopReason, Message: partial})
	stream.Resolve(partial, nil)
}

func (p *anthropicProvider) buildBody(req *providers.ChatRequest, model string, stream bool) ([]byte, error) {
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == providers.RoleSystem {
			continue // system handled separately below
		}
		role := string(m.Role)
		var content any
		switch v := m.Content.(type) {
		case string:
			content = v
		default:
			parts, multipart, err := providers.ParseContentParts(v)
			if err != nil {
				return nil, fmt.Errorf("anthropic: convert message content: %w", err)
			}
			if !multipart {
				content = fmt.Sprintf("%v", v)
				break
			}
			blocks := make([]map[string]any, 0, len(parts))
			for _, part := range parts {
				if part.MIMEType == "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
					continue
				}
				blocks = append(blocks, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": part.MIMEType,
						"data":       base64.StdEncoding.EncodeToString(part.Data),
					},
				})
			}
			content = blocks
		}
		msgs = append(msgs, map[string]any{"role": role, "content": content})
	}

	maxTokens := 8192
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}

	payload := map[string]any{
		"model":      model,
		"messages":   msgs,
		"max_tokens": maxTokens,
		"stream":     stream,
	}

	// Extract system message if present.
	for _, m := range req.Messages {
		if m.Role == providers.RoleSystem {
			if s, ok := m.Content.(string); ok {
				payload["system"] = s
			}
			break
		}
	}

	return json.Marshal(payload)
}

func (p *anthropicProvider) doRequest(ctx context.Context, body []byte) (*http.Response, error) {
	url := strings.TrimRight(p.baseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	client := &http.Client{Timeout: 10 * time.Minute}
	return client.Do(req)
}
