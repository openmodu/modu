// Package agent provides proxy streaming support for routing LLM calls
// through a server that manages auth and proxies requests to LLM providers.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/crosszan/modu/pkg/llm"
	llmutils "github.com/crosszan/modu/pkg/llm/utils"
)

// ProxyStreamOptions extends SimpleStreamOptions with proxy-specific fields.
type ProxyStreamOptions struct {
	llm.SimpleStreamOptions
	// AuthToken is the Bearer token for the proxy server.
	AuthToken string
	// ProxyURL is the proxy server URL (e.g., "https://genai.example.com").
	ProxyURL string
}

// ProxyAssistantMessageEvent represents events sent by the proxy server.
// The proxy strips the partial field from delta events to reduce bandwidth.
type ProxyAssistantMessageEvent struct {
	Type             string     `json:"type"`
	ContentIndex     int        `json:"contentIndex,omitempty"`
	Delta            string     `json:"delta,omitempty"`
	ContentSignature string     `json:"contentSignature,omitempty"`
	ID               string     `json:"id,omitempty"`
	ToolName         string     `json:"toolName,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	ErrorMessage     string     `json:"errorMessage,omitempty"`
	Usage            *llm.Usage `json:"usage,omitempty"`
}

// StreamProxy creates a stream function that proxies through a server instead
// of calling LLM providers directly. The server strips the partial field from
// delta events to reduce bandwidth. The partial message is reconstructed client-side.
//
// Usage:
//
//	agent := NewAgent(AgentOptions{
//	    StreamFn: func(model *llm.Model, ctx *llm.Context, opts *llm.SimpleStreamOptions) (llm.AssistantMessageEventStream, error) {
//	        return StreamProxy(model, ctx, &ProxyStreamOptions{
//	            SimpleStreamOptions: *opts,
//	            AuthToken:           authToken,
//	            ProxyURL:            "https://genai.example.com",
//	        })
//	    },
//	})
func StreamProxy(model *llm.Model, llmCtx *llm.Context, opts *ProxyStreamOptions) (llm.AssistantMessageEventStream, error) {
	stream := llmutils.NewEventStream()

	// Initialize partial message to build up from events
	partial := &llm.AssistantMessage{
		Role:       "assistant",
		StopReason: "stop",
		Content:    []llm.ContentBlock{},
		Api:        model.Api,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      llm.Usage{},
		Timestamp:  0, // Will be set when we start processing
	}

	go func() {
		defer stream.Close()

		if err := doProxyStream(model, llmCtx, opts, partial, stream); err != nil {
			reason := "error"
			partial.StopReason = llm.StopReason(reason)
			partial.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       llm.StopReason(reason),
				ErrorMessage: partial,
			})
		}
	}()

	return stream, nil
}

func doProxyStream(model *llm.Model, llmCtx *llm.Context, opts *ProxyStreamOptions, partial *llm.AssistantMessage, stream *llmutils.EventStream) error {
	// Build request body
	type streamRequest struct {
		Model   *llm.Model   `json:"model"`
		Context *llm.Context `json:"context"`
		Options struct {
			Temperature *float64 `json:"temperature,omitempty"`
			MaxTokens   *int     `json:"maxTokens,omitempty"`
			Reasoning   string   `json:"reasoning,omitempty"`
		} `json:"options"`
	}

	reqBody := streamRequest{
		Model:   model,
		Context: llmCtx,
	}
	reqBody.Options.Temperature = opts.Temperature
	reqBody.Options.MaxTokens = opts.MaxTokens
	if opts.Reasoning != "" {
		reqBody.Options.Reasoning = string(opts.Reasoning)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := strings.TrimRight(opts.ProxyURL, "/") + "/api/stream"
	req, err := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+opts.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("proxy request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("proxy error: %s", errResp.Error)
		}
		return fmt.Errorf("proxy error: %d %s", resp.StatusCode, resp.Status)
	}

	// Read SSE events from response body
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(line[6:])
		if data == "" {
			continue
		}

		var proxyEvent ProxyAssistantMessageEvent
		if err := json.Unmarshal([]byte(data), &proxyEvent); err != nil {
			continue
		}

		event := processProxyEvent(&proxyEvent, partial)
		if event != nil {
			stream.Push(*event)
		}
	}

	return scanner.Err()
}

// StreamProxyWithContext is like StreamProxy but accepts a context.Context for cancellation.
func StreamProxyWithContext(ctx context.Context, model *llm.Model, llmCtx *llm.Context, opts *ProxyStreamOptions) (llm.AssistantMessageEventStream, error) {
	stream := llmutils.NewEventStream()

	partial := &llm.AssistantMessage{
		Role:       "assistant",
		StopReason: "stop",
		Content:    []llm.ContentBlock{},
		Api:        model.Api,
		Provider:   model.Provider,
		Model:      model.ID,
		Usage:      llm.Usage{},
	}

	go func() {
		defer stream.Close()

		if err := doProxyStreamWithContext(ctx, model, llmCtx, opts, partial, stream); err != nil {
			reason := "error"
			if ctx.Err() != nil {
				reason = "aborted"
			}
			partial.StopReason = llm.StopReason(reason)
			partial.ErrorMessage = err.Error()
			stream.Push(llm.AssistantMessageEvent{
				Type:         "error",
				Reason:       llm.StopReason(reason),
				ErrorMessage: partial,
			})
		}
	}()

	return stream, nil
}

func doProxyStreamWithContext(ctx context.Context, model *llm.Model, llmCtx *llm.Context, opts *ProxyStreamOptions, partial *llm.AssistantMessage, stream *llmutils.EventStream) error {
	type streamRequest struct {
		Model   *llm.Model   `json:"model"`
		Context *llm.Context `json:"context"`
		Options struct {
			Temperature *float64 `json:"temperature,omitempty"`
			MaxTokens   *int     `json:"maxTokens,omitempty"`
			Reasoning   string   `json:"reasoning,omitempty"`
		} `json:"options"`
	}

	reqBody := streamRequest{
		Model:   model,
		Context: llmCtx,
	}
	reqBody.Options.Temperature = opts.Temperature
	reqBody.Options.MaxTokens = opts.MaxTokens
	if opts.Reasoning != "" {
		reqBody.Options.Reasoning = string(opts.Reasoning)
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := strings.TrimRight(opts.ProxyURL, "/") + "/api/stream"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+opts.AuthToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("proxy request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("proxy error: %s", errResp.Error)
		}
		return fmt.Errorf("proxy error: %d %s", resp.StatusCode, resp.Status)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimSpace(line[6:])
		if data == "" {
			continue
		}

		var proxyEvent ProxyAssistantMessageEvent
		if err := json.Unmarshal([]byte(data), &proxyEvent); err != nil {
			continue
		}

		event := processProxyEvent(&proxyEvent, partial)
		if event != nil {
			stream.Push(*event)
		}
	}

	return scanner.Err()
}

// processProxyEvent processes a proxy event and updates the partial message.
// Returns the corresponding AssistantMessageEvent or nil if the event should be skipped.
func processProxyEvent(proxyEvent *ProxyAssistantMessageEvent, partial *llm.AssistantMessage) *llm.AssistantMessageEvent {
	switch proxyEvent.Type {
	case "start":
		return &llm.AssistantMessageEvent{
			Type:    "start",
			Partial: partial,
		}

	case "text_start":
		ensureContentIndex(partial, proxyEvent.ContentIndex)
		partial.Content[proxyEvent.ContentIndex] = &llm.TextContent{Type: "text", Text: ""}
		return &llm.AssistantMessageEvent{
			Type:         "text_start",
			ContentIndex: proxyEvent.ContentIndex,
			Partial:      partial,
		}

	case "text_delta":
		if tc, ok := getTextContent(partial, proxyEvent.ContentIndex); ok {
			tc.Text += proxyEvent.Delta
			return &llm.AssistantMessageEvent{
				Type:         "text_delta",
				ContentIndex: proxyEvent.ContentIndex,
				Delta:        proxyEvent.Delta,
				Partial:      partial,
			}
		}
		return nil

	case "text_end":
		if tc, ok := getTextContent(partial, proxyEvent.ContentIndex); ok {
			tc.TextSignature = proxyEvent.ContentSignature
			return &llm.AssistantMessageEvent{
				Type:         "text_end",
				ContentIndex: proxyEvent.ContentIndex,
				Content:      tc.Text,
				Partial:      partial,
			}
		}
		return nil

	case "thinking_start":
		ensureContentIndex(partial, proxyEvent.ContentIndex)
		partial.Content[proxyEvent.ContentIndex] = &llm.ThinkingContent{Type: "thinking", Thinking: ""}
		return &llm.AssistantMessageEvent{
			Type:         "thinking_start",
			ContentIndex: proxyEvent.ContentIndex,
			Partial:      partial,
		}

	case "thinking_delta":
		if tc, ok := getThinkingContent(partial, proxyEvent.ContentIndex); ok {
			tc.Thinking += proxyEvent.Delta
			return &llm.AssistantMessageEvent{
				Type:         "thinking_delta",
				ContentIndex: proxyEvent.ContentIndex,
				Delta:        proxyEvent.Delta,
				Partial:      partial,
			}
		}
		return nil

	case "thinking_end":
		if tc, ok := getThinkingContent(partial, proxyEvent.ContentIndex); ok {
			tc.ThinkingSignature = proxyEvent.ContentSignature
			return &llm.AssistantMessageEvent{
				Type:         "thinking_end",
				ContentIndex: proxyEvent.ContentIndex,
				Content:      tc.Thinking,
				Partial:      partial,
			}
		}
		return nil

	case "toolcall_start":
		ensureContentIndex(partial, proxyEvent.ContentIndex)
		partial.Content[proxyEvent.ContentIndex] = &llm.ToolCall{
			Type:      "toolCall",
			ID:        proxyEvent.ID,
			Name:      proxyEvent.ToolName,
			Arguments: map[string]any{},
		}
		return &llm.AssistantMessageEvent{
			Type:         "toolcall_start",
			ContentIndex: proxyEvent.ContentIndex,
			Partial:      partial,
		}

	case "toolcall_delta":
		if tc, ok := getToolCallContent(partial, proxyEvent.ContentIndex); ok {
			// Accumulate JSON delta — parse when complete at toolcall_end
			_ = tc
			return &llm.AssistantMessageEvent{
				Type:         "toolcall_delta",
				ContentIndex: proxyEvent.ContentIndex,
				Delta:        proxyEvent.Delta,
				Partial:      partial,
			}
		}
		return nil

	case "toolcall_end":
		if tc, ok := getToolCallContent(partial, proxyEvent.ContentIndex); ok {
			return &llm.AssistantMessageEvent{
				Type:         "toolcall_end",
				ContentIndex: proxyEvent.ContentIndex,
				ToolCall:     tc,
				Partial:      partial,
			}
		}
		return nil

	case "done":
		partial.StopReason = llm.StopReason(proxyEvent.Reason)
		if proxyEvent.Usage != nil {
			partial.Usage = *proxyEvent.Usage
		}
		return &llm.AssistantMessageEvent{
			Type:    "done",
			Reason:  llm.StopReason(proxyEvent.Reason),
			Message: partial,
		}

	case "error":
		partial.StopReason = llm.StopReason(proxyEvent.Reason)
		partial.ErrorMessage = proxyEvent.ErrorMessage
		if proxyEvent.Usage != nil {
			partial.Usage = *proxyEvent.Usage
		}
		return &llm.AssistantMessageEvent{
			Type:         "error",
			Reason:       llm.StopReason(proxyEvent.Reason),
			ErrorMessage: partial,
		}

	default:
		return nil
	}
}

// ensureContentIndex grows the Content slice to include the given index.
func ensureContentIndex(msg *llm.AssistantMessage, index int) {
	for len(msg.Content) <= index {
		msg.Content = append(msg.Content, nil)
	}
}

func getTextContent(msg *llm.AssistantMessage, index int) (*llm.TextContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*llm.TextContent)
	return tc, ok
}

func getThinkingContent(msg *llm.AssistantMessage, index int) (*llm.ThinkingContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*llm.ThinkingContent)
	return tc, ok
}

func getToolCallContent(msg *llm.AssistantMessage, index int) (*llm.ToolCall, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*llm.ToolCall)
	return tc, ok
}
