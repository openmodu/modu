package modelrouter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func toChat(payload map[string]json.RawMessage, endpoint string) (map[string]json.RawMessage, error) {
	var messages []chatMessage
	var tools []map[string]any
	switch endpoint {
	case "responses":
		var instructions string
		_ = json.Unmarshal(payload["instructions"], &instructions)
		if instructions != "" {
			messages = append(messages, chatMessage{Role: "system", Content: instructions})
		}
		var input string
		if json.Unmarshal(payload["input"], &input) == nil {
			messages = append(messages, chatMessage{Role: "user", Content: input})
		} else {
			var items []map[string]any
			if err := json.Unmarshal(payload["input"], &items); err != nil {
				return nil, fmt.Errorf("invalid Responses input: %w", err)
			}
			for _, item := range items {
				typ, _ := item["type"].(string)
				switch typ {
				case "function_call":
					args, _ := item["arguments"].(string)
					call := chatToolCall{ID: stringValue(item["call_id"]), Type: "function"}
					call.Function.Name = stringValue(item["name"])
					call.Function.Arguments = args
					messages = append(messages, chatMessage{Role: "assistant", ToolCalls: []chatToolCall{call}})
				case "function_call_output":
					messages = append(messages, chatMessage{Role: "tool", Content: item["output"], ToolCallID: stringValue(item["call_id"])})
				default:
					role := stringValue(item["role"])
					if role == "" {
						continue
					}
					messages = append(messages, chatMessage{Role: role, Content: chatContent(item["content"])})
				}
			}
		}
		var funcs []map[string]any
		_ = json.Unmarshal(payload["tools"], &funcs)
		for _, f := range funcs {
			if f["type"] == "function" {
				tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": f["name"], "description": f["description"], "parameters": f["parameters"]}})
			}
		}
	case "messages":
		var system any
		_ = json.Unmarshal(payload["system"], &system)
		if s := textContent(system); s != "" {
			messages = append(messages, chatMessage{Role: "system", Content: s})
		}
		var items []map[string]any
		if err := json.Unmarshal(payload["messages"], &items); err != nil {
			return nil, fmt.Errorf("invalid Messages input: %w", err)
		}
		for _, item := range items {
			role := stringValue(item["role"])
			content := item["content"]
			if blocks, ok := content.([]any); ok {
				var calls []chatToolCall
				for _, block := range blocks {
					b, ok := block.(map[string]any)
					if !ok {
						continue
					}
					switch b["type"] {
					case "tool_use":
						call := chatToolCall{ID: stringValue(b["id"]), Type: "function"}
						call.Function.Name = stringValue(b["name"])
						args, _ := json.Marshal(b["input"])
						call.Function.Arguments = string(args)
						calls = append(calls, call)
					case "tool_result":
						messages = append(messages, chatMessage{Role: "tool", Content: textContent(b["content"]), ToolCallID: stringValue(b["tool_use_id"])})
					}
				}
				if len(calls) > 0 {
					messages = append(messages, chatMessage{Role: "assistant", Content: chatContent(content), ToolCalls: calls})
				} else if text := textContent(content); text != "" {
					messages = append(messages, chatMessage{Role: role, Content: chatContent(content)})
				} else if hasImage(content) {
					messages = append(messages, chatMessage{Role: role, Content: chatContent(content)})
				}
			} else {
				messages = append(messages, chatMessage{Role: role, Content: textContent(content)})
			}
		}
		var defs []map[string]any
		_ = json.Unmarshal(payload["tools"], &defs)
		for _, f := range defs {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": f["name"], "description": f["description"], "parameters": f["input_schema"]}})
		}
	}
	result := map[string]json.RawMessage{"model": payload["model"]}
	result["messages"], _ = json.Marshal(messages)
	if len(tools) > 0 {
		result["tools"], _ = json.Marshal(tools)
	}
	if endpoint == "messages" {
		result["max_tokens"] = payload["max_tokens"]
	} else {
		result["max_tokens"] = payload["max_output_tokens"]
	}
	if len(result["max_tokens"]) == 0 {
		delete(result, "max_tokens")
	}
	return result, nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func textContent(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	blocks, ok := v.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, raw := range blocks {
		b, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if typ := stringValue(b["type"]); typ == "text" || typ == "input_text" || typ == "output_text" {
			parts = append(parts, stringValue(b["text"]))
		}
	}
	return strings.Join(parts, "")
}

func hasImage(v any) bool {
	blocks, ok := v.([]any)
	if !ok {
		return false
	}
	for _, raw := range blocks {
		b, ok := raw.(map[string]any)
		if ok && (b["type"] == "image" || b["type"] == "input_image") {
			return true
		}
	}
	return false
}

func chatContent(v any) any {
	if !hasImage(v) {
		return textContent(v)
	}
	blocks, _ := v.([]any)
	var out []map[string]any
	for _, raw := range blocks {
		b, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch b["type"] {
		case "text", "input_text", "output_text":
			out = append(out, map[string]any{"type": "text", "text": b["text"]})
		case "input_image":
			if imageURL := stringValue(b["image_url"]); imageURL != "" {
				out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
			}
		case "image":
			source, _ := b["source"].(map[string]any)
			if source != nil && source["type"] == "base64" {
				url := "data:" + stringValue(source["media_type"]) + ";base64," + stringValue(source["data"])
				out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			}
		}
	}
	return out
}

func fromChat(body []byte, endpoint, id string) ([]byte, error) {
	var chat struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content   string         `json:"content"`
				ToolCalls []chatToolCall `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &chat); err != nil || len(chat.Choices) == 0 {
		return nil, fmt.Errorf("invalid Chat response")
	}
	choice := chat.Choices[0]
	if chat.Usage.TotalTokens == 0 {
		chat.Usage.TotalTokens = chat.Usage.PromptTokens + chat.Usage.CompletionTokens
	}
	if endpoint == "responses" {
		var output []map[string]any
		if choice.Message.Content != "" {
			output = append(output, map[string]any{"id": "msg_" + chat.ID, "type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": choice.Message.Content, "annotations": []any{}}}})
		}
		for _, call := range choice.Message.ToolCalls {
			output = append(output, map[string]any{"id": "fc_" + call.ID, "type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments, "status": "completed"})
		}
		return json.Marshal(map[string]any{"id": "resp_" + chat.ID, "object": "response", "status": "completed", "model": id, "output": output, "usage": map[string]any{"input_tokens": chat.Usage.PromptTokens, "output_tokens": chat.Usage.CompletionTokens, "total_tokens": chat.Usage.TotalTokens}})
	}
	var content []map[string]any
	if choice.Message.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": choice.Message.Content})
	}
	for _, call := range choice.Message.ToolCalls {
		var args any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			args = map[string]any{}
		}
		content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Function.Name, "input": args})
	}
	stop := "end_turn"
	if len(choice.Message.ToolCalls) > 0 {
		stop = "tool_use"
	} else if choice.FinishReason == "length" {
		stop = "max_tokens"
	}
	return json.Marshal(map[string]any{"id": "msg_" + chat.ID, "type": "message", "role": "assistant", "model": id, "content": content, "stop_reason": stop, "stop_sequence": nil, "usage": map[string]any{"input_tokens": chat.Usage.PromptTokens, "output_tokens": chat.Usage.CompletionTokens}})
}

func writeStream(w http.ResponseWriter, endpoint string, body []byte) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if endpoint == "responses" {
		var resp map[string]any
		_ = json.Unmarshal(body, &resp)
		items, _ := resp["output"].([]any)
		started := map[string]any{}
		for key, value := range resp {
			started[key] = value
		}
		started["status"] = "in_progress"
		started["output"] = []any{}
		emitSSE(w, "response.created", map[string]any{"type": "response.created", "response": started})
		for i, raw := range items {
			item, _ := raw.(map[string]any)
			if item == nil {
				continue
			}
			initial := map[string]any{}
			for key, value := range item {
				initial[key] = value
			}
			initial["status"] = "in_progress"
			if item["type"] == "message" {
				initial["content"] = []any{}
			}
			emitSSE(w, "response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": i, "item": initial})
			if item["type"] == "message" {
				parts, _ := item["content"].([]any)
				for j, p := range parts {
					part, _ := p.(map[string]any)
					if part == nil {
						continue
					}
					emitSSE(w, "response.content_part.added", map[string]any{"type": "response.content_part.added", "output_index": i, "content_index": j, "item_id": item["id"], "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
					emitSSE(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": i, "content_index": j, "item_id": item["id"], "delta": part["text"]})
					emitSSE(w, "response.output_text.done", map[string]any{"type": "response.output_text.done", "output_index": i, "content_index": j, "item_id": item["id"], "text": part["text"]})
					emitSSE(w, "response.content_part.done", map[string]any{"type": "response.content_part.done", "output_index": i, "content_index": j, "item_id": item["id"], "part": part})
				}
			} else if item["type"] == "function_call" {
				emitSSE(w, "response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "output_index": i, "item_id": item["id"], "delta": item["arguments"]})
				emitSSE(w, "response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "output_index": i, "item_id": item["id"], "arguments": item["arguments"]})
			}
			emitSSE(w, "response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
		}
		emitSSE(w, "response.completed", map[string]any{"type": "response.completed", "response": resp})
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	var msg map[string]any
	_ = json.Unmarshal(body, &msg)
	content, _ := msg["content"].([]any)
	stopReason := msg["stop_reason"]
	msg["content"] = []any{}
	msg["stop_reason"] = nil
	emitSSE(w, "message_start", map[string]any{"type": "message_start", "message": msg})
	for i, raw := range content {
		block, _ := raw.(map[string]any)
		if block == nil {
			continue
		}
		switch block["type"] {
		case "text":
			emitSSE(w, "content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "text", "text": ""}})
			emitSSE(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": block["text"]}})
		case "tool_use":
			emitSSE(w, "content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "tool_use", "id": block["id"], "name": block["name"], "input": map[string]any{}}})
			input, _ := json.Marshal(block["input"])
			emitSSE(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}})
		}
		emitSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	emitSSE(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason}, "usage": msg["usage"]})
	emitSSE(w, "message_stop", map[string]any{"type": "message_stop"})
}

func emitSSE(w http.ResponseWriter, event string, payload any) {
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
