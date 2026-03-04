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

func (p *openAIProvider) Stream(ctx context.Context, req *ChatRequest) (Stream, error) {
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

	stream := NewEventStream()
	go p.readSSE(resp.Body, stream)
	return stream, nil
}

// readSSE parses an SSE stream in a separate goroutine and pushes granular events.
func (p *openAIProvider) readSSE(body io.ReadCloser, stream *EventStream) {
	defer body.Close()
	defer stream.Close()

	partial := &ChatResponse{}
	stream.Push(StreamEvent{Type: EventStart, Partial: partial})

	textStarted := false
	thinkingStarted := false
	nextIndex := 0
	textIndex := -1
	thinkingIndex := -1

	var textBuf strings.Builder
	var thinkingBuf strings.Builder

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
			stream.Push(StreamEvent{
				Type:    EventError,
				Partial: partial,
				Err:     fmt.Errorf("%s", chunk.Error.Message),
			})
			return
		}

		if partial.ID == "" && chunk.ID != "" {
			partial.ID = chunk.ID
		}
		if partial.Model == "" && chunk.Model != "" {
			partial.Model = chunk.Model
		}
		if chunk.Usage != nil {
			partial.Usage = Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			partial.FinishReason = choice.FinishReason
		}

		delta := choice.Delta

		if rc := delta.ReasoningContent; rc != "" {
			if !thinkingStarted {
				thinkingStarted = true
				thinkingIndex = nextIndex
				nextIndex++
				stream.Push(StreamEvent{
					Type:         EventThinkingStart,
					ContentIndex: thinkingIndex,
					Partial:      partial,
				})
			}
			thinkingBuf.WriteString(rc)
			stream.Push(StreamEvent{
				Type:         EventThinkingDelta,
				ContentIndex: thinkingIndex,
				Delta:        rc,
				Partial:      partial,
			})
		}

		if content := delta.Content; content != "" {
			if thinkingStarted && !textStarted {
				stream.Push(StreamEvent{
					Type:         EventThinkingEnd,
					ContentIndex: thinkingIndex,
					Content:      thinkingBuf.String(),
					Partial:      partial,
				})
			}
			if !textStarted {
				textStarted = true
				textIndex = nextIndex
				nextIndex++
				stream.Push(StreamEvent{
					Type:         EventTextStart,
					ContentIndex: textIndex,
					Partial:      partial,
				})
			}
			partial.Message.Content += content
			textBuf.WriteString(content)
			stream.Push(StreamEvent{
				Type:         EventTextDelta,
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
					stream.Push(StreamEvent{
						Type:         EventToolCallDelta,
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
		stream.Push(StreamEvent{
			Type:    EventError,
			Partial: partial,
			Err:     err,
		})
		return
	}

	if thinkingStarted && !textStarted {
		stream.Push(StreamEvent{
			Type:         EventThinkingEnd,
			ContentIndex: thinkingIndex,
			Content:      thinkingBuf.String(),
			Partial:      partial,
		})
	}

	if textStarted {
		stream.Push(StreamEvent{
			Type:         EventTextEnd,
			ContentIndex: textIndex,
			Content:      textBuf.String(),
			Partial:      partial,
		})
	}

	for i := 0; i < len(toolAccs); i++ {
		acc, ok := toolAccs[i]
		if !ok {
			continue
		}
		tc := ToolCall{
			ID:   acc.id,
			Type: "function",
			Function: FuncCall{
				Name:      acc.name,
				Arguments: acc.argsJSON.String(),
			},
		}
		partial.ToolCalls = append(partial.ToolCalls, tc)
		idx := nextIndex
		nextIndex++
		stream.Push(StreamEvent{
			Type:         EventToolCallStart,
			ContentIndex: idx,
			ToolCall:     &tc,
			Partial:      partial,
		})
		stream.Push(StreamEvent{
			Type:         EventToolCallEnd,
			ContentIndex: idx,
			ToolCall:     &tc,
			Partial:      partial,
		})
	}
	if len(partial.ToolCalls) > 0 {
		partial.FinishReason = "tool_calls"
	}

	partial.Message.Role = RoleAssistant
	stream.Push(StreamEvent{
		Type:    EventDone,
		Reason:  partial.FinishReason,
		Partial: partial,
	})
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
