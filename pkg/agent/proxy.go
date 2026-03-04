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

	"github.com/crosszan/modu/pkg/providers"
)

// ProxyStreamOptions extends SimpleStreamOptions with proxy-specific fields.
type ProxyStreamOptions struct {
	providers.SimpleStreamOptions
	// AuthToken is the Bearer token for the proxy server.
	AuthToken string
	// ProxyURL is the proxy server URL (e.g., "https://genai.example.com").
	ProxyURL string
}

// ProxyStreamEvent represents events sent by the proxy server.
// The proxy strips the partial field from delta events to reduce bandwidth.
type ProxyStreamEvent struct {
	Type             string                `json:"type"`
	ContentIndex     int                   `json:"contentIndex,omitempty"`
	Delta            string                `json:"delta,omitempty"`
	ContentSignature string                `json:"contentSignature,omitempty"`
	ID               string                `json:"id,omitempty"`
	ToolName         string                `json:"toolName,omitempty"`
	Reason           string                `json:"reason,omitempty"`
	ErrorMessage     string                `json:"errorMessage,omitempty"`
	Usage            *providers.AgentUsage `json:"usage,omitempty"`
}

// StreamProxy creates a stream function that proxies through a server instead
// of calling LLM providers directly. The server strips the partial field from
// delta events to reduce bandwidth. The partial message is reconstructed client-side.
//
// Usage:
//
//	agent := NewAgent(AgentOptions{
//	    StreamFn: func(ctx context.Context, model *providers.Model, llmCtx *providers.LLMContext, opts *providers.SimpleStreamOptions) (providers.EventStream, error) {
//	        return StreamProxy(ctx, model, llmCtx, &ProxyStreamOptions{
//	            SimpleStreamOptions: *opts,
//	            AuthToken:           authToken,
//	            ProxyURL:            "https://genai.example.com",
//	        })
//	    },
//	})
func StreamProxy(ctx context.Context, model *providers.Model, llmCtx *providers.LLMContext, opts *ProxyStreamOptions) (providers.EventStream, error) {
	stream := providers.NewEventStream()

	// Initialize partial message to build up from events
	partial := &providers.AssistantMessage{
		Role:       "assistant",
		StopReason: "stop",
		Content:    []providers.ContentBlock{},
		ProviderID: model.ProviderID,
		Model:      model.ID,
		Usage:      providers.AgentUsage{},
	}

	go func() {
		defer stream.Close()

		if err := doProxyStreamWithContext(ctx, model, llmCtx, opts, partial, stream); err != nil {
			reason := "error"
			if ctx.Err() != nil {
				reason = "aborted"
			}
			partial.StopReason = reason
			partial.ErrorMessage = err.Error()
			stream.Push(providers.StreamEvent{
				Type:         "error",
				Reason:       reason,
				ErrorMessage: partial,
			})
		}
	}()

	return stream, nil
}

func doProxyStreamWithContext(ctx context.Context, model *providers.Model, llmCtx *providers.LLMContext, opts *ProxyStreamOptions, partial *providers.AssistantMessage, stream *providers.EventStreamImpl) error {
	type streamRequest struct {
		Model   *providers.Model      `json:"model"`
		Context *providers.LLMContext `json:"context"`
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

		var proxyEvent ProxyStreamEvent
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
// Returns the corresponding StreamEvent or nil if the event should be skipped.
func processProxyEvent(proxyEvent *ProxyStreamEvent, partial *providers.AssistantMessage) *providers.StreamEvent {
	switch proxyEvent.Type {
	case "start":
		return &providers.StreamEvent{
			Type:    "start",
			Partial: partial,
		}

	case "text_start":
		ensureContentIndex(partial, proxyEvent.ContentIndex)
		partial.Content[proxyEvent.ContentIndex] = &providers.TextContent{Type: "text", Text: ""}
		return &providers.StreamEvent{
			Type:         "text_start",
			ContentIndex: proxyEvent.ContentIndex,
			Partial:      partial,
		}

	case "text_delta":
		if tc, ok := getTextContent(partial, proxyEvent.ContentIndex); ok {
			tc.Text += proxyEvent.Delta
			return &providers.StreamEvent{
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
			return &providers.StreamEvent{
				Type:         "text_end",
				ContentIndex: proxyEvent.ContentIndex,
				Content:      tc.Text,
				Partial:      partial,
			}
		}
		return nil

	case "thinking_start":
		ensureContentIndex(partial, proxyEvent.ContentIndex)
		partial.Content[proxyEvent.ContentIndex] = &providers.ThinkingContent{Type: "thinking", Thinking: ""}
		return &providers.StreamEvent{
			Type:         "thinking_start",
			ContentIndex: proxyEvent.ContentIndex,
			Partial:      partial,
		}

	case "thinking_delta":
		if tc, ok := getThinkingContent(partial, proxyEvent.ContentIndex); ok {
			tc.Thinking += proxyEvent.Delta
			return &providers.StreamEvent{
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
			return &providers.StreamEvent{
				Type:         "thinking_end",
				ContentIndex: proxyEvent.ContentIndex,
				Content:      tc.Thinking,
				Partial:      partial,
			}
		}
		return nil

	case "toolcall_start":
		ensureContentIndex(partial, proxyEvent.ContentIndex)
		partial.Content[proxyEvent.ContentIndex] = &providers.ToolCallContent{
			Type:      "toolCall",
			ID:        proxyEvent.ID,
			Name:      proxyEvent.ToolName,
			Arguments: map[string]any{},
		}
		return &providers.StreamEvent{
			Type:         "toolcall_start",
			ContentIndex: proxyEvent.ContentIndex,
			Partial:      partial,
		}

	case "toolcall_delta":
		if tc, ok := getToolCallContent(partial, proxyEvent.ContentIndex); ok {
			// Accumulate JSON delta — parse when complete at toolcall_end
			_ = tc
			return &providers.StreamEvent{
				Type:         "toolcall_delta",
				ContentIndex: proxyEvent.ContentIndex,
				Delta:        proxyEvent.Delta,
				Partial:      partial,
			}
		}
		return nil

	case "toolcall_end":
		if tc, ok := getToolCallContent(partial, proxyEvent.ContentIndex); ok {
			return &providers.StreamEvent{
				Type:         "toolcall_end",
				ContentIndex: proxyEvent.ContentIndex,
				ToolCall:     tc,
				Partial:      partial,
			}
		}
		return nil

	case "done":
		partial.StopReason = proxyEvent.Reason
		if proxyEvent.Usage != nil {
			partial.Usage = *proxyEvent.Usage
		}
		return &providers.StreamEvent{
			Type:    "done",
			Reason:  proxyEvent.Reason,
			Message: partial,
		}

	case "error":
		partial.StopReason = proxyEvent.Reason
		partial.ErrorMessage = proxyEvent.ErrorMessage
		if proxyEvent.Usage != nil {
			partial.Usage = *proxyEvent.Usage
		}
		return &providers.StreamEvent{
			Type:         "error",
			Reason:       proxyEvent.Reason,
			ErrorMessage: partial,
		}

	default:
		return nil
	}
}

// ensureContentIndex grows the Content slice to include the given index.
func ensureContentIndex(msg *providers.AssistantMessage, index int) {
	for len(msg.Content) <= index {
		msg.Content = append(msg.Content, nil)
	}
}

func getTextContent(msg *providers.AssistantMessage, index int) (*providers.TextContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*providers.TextContent)
	return tc, ok
}

func getThinkingContent(msg *providers.AssistantMessage, index int) (*providers.ThinkingContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*providers.ThinkingContent)
	return tc, ok
}

func getToolCallContent(msg *providers.AssistantMessage, index int) (*providers.ToolCallContent, bool) {
	if index >= len(msg.Content) {
		return nil, false
	}
	tc, ok := msg.Content[index].(*providers.ToolCallContent)
	return tc, ok
}
