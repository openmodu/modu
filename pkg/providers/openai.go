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

// OpenAIConfig 配置 OpenAI-compatible chat completions provider
type OpenAIConfig struct {
	// BaseURL 默认 https://api.openai.com/v1
	BaseURL string
	// APIKey 鉴权 key
	APIKey string
	// DefaultModel 当 ChatRequest.Model 为空时使用
	DefaultModel string
	// Headers 附加请求头
	Headers map[string]string
}

type openAIProvider struct {
	id     string
	config OpenAIConfig
}

// NewOpenAIProvider 创建 OpenAI chat completions 风格 provider。
// id 用于标识来源（如 "openai"、"localai"、"lmstudio"）。
func NewOpenAIProvider(id string, config OpenAIConfig) Provider {
	return &openAIProvider{id: id, config: config}
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

// readSSE 在独立 goroutine 中解析 SSE 流并推送细粒度事件
func (p *openAIProvider) readSSE(body io.ReadCloser, stream *EventStream) {
	defer body.Close()
	defer stream.Close()

	partial := &ChatResponse{}
	stream.Push(StreamEvent{Type: EventStart, Partial: partial})

	// 追踪各 content block 是否已开始
	textStarted := false
	thinkingStarted := false
	nextIndex := 0
	textIndex := -1
	thinkingIndex := -1

	var textBuf strings.Builder
	var thinkingBuf strings.Builder

	// tool call 增量累积（index → acc）
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

		// --- thinking (reasoning_content) ---
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

		// --- text content ---
		if content := delta.Content; content != "" {
			// 思考块结束（text 开始前关闭）
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

		// --- tool calls 增量 ---
		for _, tc := range delta.ToolCalls {
			acc, ok := toolAccs[tc.Index]
			if !ok {
				acc = &tcAcc{}
				toolAccs[tc.Index] = acc
				// toolcall_start 在首次收到时发出（name 可能稍后才来，等 End 再发完整信息）
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

	// 关闭未关闭的 thinking block
	if thinkingStarted && !textStarted {
		stream.Push(StreamEvent{
			Type:         EventThinkingEnd,
			ContentIndex: thinkingIndex,
			Content:      thinkingBuf.String(),
			Partial:      partial,
		})
	}

	// 关闭 text block
	if textStarted {
		stream.Push(StreamEvent{
			Type:         EventTextEnd,
			ContentIndex: textIndex,
			Content:      textBuf.String(),
			Partial:      partial,
		})
	}

	// 完成所有 tool calls，发出 toolcall_start + toolcall_end
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
	model := req.Model
	if model == "" {
		model = p.config.DefaultModel
	}
	payload := map[string]any{
		"model":    model,
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
	baseURL := p.config.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}
	for k, v := range p.config.Headers {
		httpReq.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	return client.Do(httpReq)
}
