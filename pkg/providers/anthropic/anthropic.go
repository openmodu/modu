// Package anthropic provides an Anthropic Messages API provider.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
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
		StopReason string `json:"stop_reason"`
		Model      string `json:"model"`
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
	return &providers.ChatResponse{
		Model:        raw.Model,
		Message:      providers.Message{Role: providers.RoleAssistant, Content: sb.String()},
		FinishReason: raw.StopReason,
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

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var ev struct {
			Type  string `json:"type"`
			Delta *struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}

		switch ev.Type {
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
		case "error":
			if ev.Error != nil {
				err := fmt.Errorf("%s", ev.Error.Message)
				stream.Push(types.StreamEvent{Type: types.EventError, Error: err})
				stream.Resolve(partial, err)
				return
			}
		}
	}

	if err := sc.Err(); err != nil {
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
		var content string
		switch v := m.Content.(type) {
		case string:
			content = v
		default:
			content = fmt.Sprintf("%v", v)
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
