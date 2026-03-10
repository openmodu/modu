package providers

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

	"github.com/crosszan/modu/pkg/types"
)

// OpenAIConfig holds configuration for an OpenAI-compatible chat completions provider.
type OpenAIConfig struct {
	baseURL string
	apiKey  string
	headers map[string]string
}

// OpenAIOption is a functional option for OpenAIConfig.
type OpenAIOption func(*OpenAIConfig)

// WithBaseURL sets the API endpoint base URL (required).
func WithBaseURL(url string) OpenAIOption {
	return func(c *OpenAIConfig) { c.baseURL = url }
}

// WithAPIKey sets the bearer token used for authentication.
func WithAPIKey(key string) OpenAIOption {
	return func(c *OpenAIConfig) { c.apiKey = key }
}

// WithHeaders sets additional HTTP headers to include in every request.
func WithHeaders(headers map[string]string) OpenAIOption {
	return func(c *OpenAIConfig) { c.headers = headers }
}

type openAIProvider struct {
	id     string
	config OpenAIConfig
}

// NewOpenAIChatCompletionsProvider creates a provider that speaks the OpenAI
// chat completions protocol. id identifies the provider (e.g. "openai", "lmstudio").
func NewOpenAIChatCompletionsProvider(id string, opts ...OpenAIOption) Provider {
	var cfg OpenAIConfig
	for _, o := range opts {
		o(&cfg)
	}
	return &openAIProvider{id: id, config: cfg}
}

func (p *openAIProvider) ID() string { return p.id }

func (p *openAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	body, err := p.buildBody(req, false)
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
		return nil, fmt.Errorf("%s: %s: %s", p.id, resp.Status, strings.TrimSpace(string(data)))
	}

	var raw struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", p.id, err)
	}
	out := &ChatResponse{
		ID:    raw.ID,
		Model: raw.Model,
		Usage: Usage{
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		},
	}
	if len(raw.Choices) > 0 {
		out.Message = raw.Choices[0].Message
		out.FinishReason = raw.Choices[0].FinishReason
		out.ToolCalls = raw.Choices[0].Message.ToolCalls
	}
	return out, nil
}

func (p *openAIProvider) Stream(ctx context.Context, req *ChatRequest) (types.EventStream, error) {
	body, err := p.buildBody(req, true)
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
		return nil, fmt.Errorf("%s: %s: %s", p.id, resp.Status, strings.TrimSpace(string(data)))
	}

	stream := types.NewEventStream()
	go p.readSSE(resp.Body, stream)
	return stream, nil
}

// readSSE parses an SSE stream in a separate goroutine and pushes granular events.
func (p *openAIProvider) readSSE(body io.ReadCloser, stream *types.EventStreamImpl) {
	defer body.Close()
	defer stream.Close()

	partial := &types.AssistantMessage{
		Role:       "assistant",
		Content:    []types.ContentBlock{},
		ProviderID: p.id,
	}
	stream.Push(types.StreamEvent{Type: types.EventStart, Partial: partial})

	textStarted := false
	thinkingStarted := false
	nextIndex := 0
	textIndex := -1
	thinkingIndex := -1

	type tcAcc struct {
		id       string
		name     string
		argsJSON strings.Builder
	}
	toolAccs := map[int]*tcAcc{}

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

		var chunk struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function *struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Error != nil {
			partial.ErrorMessage = chunk.Error.Message
			partial.StopReason = "error"
			err := fmt.Errorf("%s", chunk.Error.Message)
			stream.Push(types.StreamEvent{
				Type:         types.EventError,
				Reason:       "error",
				ErrorMessage: partial,
				Error:        err,
			})
			stream.Resolve(partial, err)
			return
		}

		if partial.Model == "" && chunk.Model != "" {
			partial.Model = chunk.Model
		}
		if chunk.Usage != nil {
			partial.Usage = types.AgentUsage{
				Input:       chunk.Usage.PromptTokens,
				Output:      chunk.Usage.CompletionTokens,
				TotalTokens: chunk.Usage.TotalTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			partial.StopReason = types.StopReason(choice.FinishReason)
		}

		delta := choice.Delta

		if rc := delta.ReasoningContent; rc != "" {
			if !thinkingStarted {
				thinkingStarted = true
				thinkingIndex = nextIndex
				nextIndex++
				ensureContentIndex(partial, thinkingIndex)
				partial.Content[thinkingIndex] = &types.ThinkingContent{Type: "thinking", Thinking: ""}
				stream.Push(types.StreamEvent{
					Type:         types.EventThinkingStart,
					ContentIndex: thinkingIndex,
					Partial:      partial,
				})
			}
			if tc, ok := getThinkingAt(partial, thinkingIndex); ok {
				tc.Thinking += rc
			}
			stream.Push(types.StreamEvent{
				Type:         types.EventThinkingDelta,
				ContentIndex: thinkingIndex,
				Delta:        rc,
				Partial:      partial,
			})
		}

		if content := delta.Content; content != "" {
			if thinkingStarted && !textStarted {
				contentStr := ""
				if tc, ok := getThinkingAt(partial, thinkingIndex); ok {
					contentStr = tc.Thinking
				}
				stream.Push(types.StreamEvent{
					Type:         types.EventThinkingEnd,
					ContentIndex: thinkingIndex,
					Content:      contentStr,
					Partial:      partial,
				})
			}
			if !textStarted {
				textStarted = true
				textIndex = nextIndex
				nextIndex++
				ensureContentIndex(partial, textIndex)
				partial.Content[textIndex] = &types.TextContent{Type: "text", Text: ""}
				stream.Push(types.StreamEvent{
					Type:         types.EventTextStart,
					ContentIndex: textIndex,
					Partial:      partial,
				})
			}
			if tc, ok := getTextAt(partial, textIndex); ok {
				tc.Text += content
			}
			stream.Push(types.StreamEvent{
				Type:         types.EventTextDelta,
				ContentIndex: textIndex,
				Delta:        content,
				Partial:      partial,
			})
		}

		for _, tc := range delta.ToolCalls {
			acc, ok := toolAccs[tc.Index]
			if !ok {
				acc = &tcAcc{}
				toolAccs[tc.Index] = acc
				ensureContentIndex(partial, nextIndex+tc.Index)
				name := ""
				if tc.Function != nil {
					name = tc.Function.Name
				}
				partial.Content[nextIndex+tc.Index] = &types.ToolCallContent{
					Type:      "toolCall",
					ID:        tc.ID,
					Name:      name,
					Arguments: map[string]any{},
				}
				stream.Push(types.StreamEvent{
					Type:         types.EventToolCallStart,
					ContentIndex: nextIndex + tc.Index,
					Partial:      partial,
				})
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function != nil {
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					acc.argsJSON.WriteString(tc.Function.Arguments)
					stream.Push(types.StreamEvent{
						Type:         types.EventToolCallDelta,
						ContentIndex: nextIndex + tc.Index,
						Delta:        tc.Function.Arguments,
						Partial:      partial,
					})
				}
			}
		}
	}

	if err := sc.Err(); err != nil {
		partial.ErrorMessage = err.Error()
		partial.StopReason = "error"
		stream.Push(types.StreamEvent{
			Type:         types.EventError,
			Reason:       "error",
			ErrorMessage: partial,
			Error:        err,
		})
		stream.Resolve(partial, err)
		return
	}

	if thinkingStarted && !textStarted {
		contentStr := ""
		if tc, ok := getThinkingAt(partial, thinkingIndex); ok {
			contentStr = tc.Thinking
		}
		stream.Push(types.StreamEvent{
			Type:         types.EventThinkingEnd,
			ContentIndex: thinkingIndex,
			Content:      contentStr,
			Partial:      partial,
		})
	}

	if textStarted {
		contentStr := ""
		if tc, ok := getTextAt(partial, textIndex); ok {
			contentStr = tc.Text
		}
		stream.Push(types.StreamEvent{
			Type:         types.EventTextEnd,
			ContentIndex: textIndex,
			Content:      contentStr,
			Partial:      partial,
		})
	}

	for i := 0; i < len(toolAccs); i++ {
		acc, ok := toolAccs[i]
		if !ok {
			continue
		}
		idx := nextIndex
		nextIndex++

		var args map[string]any
		if acc.argsJSON.Len() > 0 {
			_ = json.Unmarshal([]byte(acc.argsJSON.String()), &args)
		}
		if args == nil {
			args = map[string]any{}
		}

		tcc := &types.ToolCallContent{
			Type:      "toolCall",
			ID:        acc.id,
			Name:      acc.name,
			Arguments: args,
		}

		ensureContentIndex(partial, idx)
		partial.Content[idx] = tcc

		stream.Push(types.StreamEvent{
			Type:         types.EventToolCallEnd,
			ContentIndex: idx,
			ToolCall:     tcc,
			Partial:      partial,
		})
	}

	stream.Push(types.StreamEvent{
		Type:    types.EventDone,
		Reason:  partial.StopReason,
		Message: partial,
	})
	stream.Resolve(partial, nil)
}

func (p *openAIProvider) buildBody(req *ChatRequest, stream bool) ([]byte, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("%s: model must be specified in the request", p.id)
	}
	payload := map[string]any{
		"model":    req.Model,
		"messages": req.Messages,
		"stream":   stream,
	}
	if stream {
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	if len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.MaxTokens != nil {
		payload["max_tokens"] = *req.MaxTokens
	}
	return json.Marshal(payload)
}

func (p *openAIProvider) doRequest(ctx context.Context, body []byte) (*http.Response, error) {
	if p.config.baseURL == "" {
		return nil, fmt.Errorf("%s: BaseURL must be set", p.id)
	}
	url := strings.TrimRight(p.config.baseURL, "/") + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.config.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.apiKey)
	}
	for k, v := range p.config.headers {
		httpReq.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	return client.Do(httpReq)
}

// Helpers for array bounds protection and casting
func ensureContentIndex(msg *types.AssistantMessage, index int) {
	for len(msg.Content) <= index {
		msg.Content = append(msg.Content, nil)
	}
}

func getTextAt(msg *types.AssistantMessage, index int) (*types.TextContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*types.TextContent)
	return tc, ok
}

func getThinkingAt(msg *types.AssistantMessage, index int) (*types.ThinkingContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*types.ThinkingContent)
	return tc, ok
}
