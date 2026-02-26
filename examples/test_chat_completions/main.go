package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/crosszan/modu/pkg/llm"
	_ "github.com/crosszan/modu/pkg/llm/providers/openai_chat_completions"
)

func main() {
	model := &llm.Model{
		ID:       "qwen3-coder-next",
		Name:     "qwen3-coder-next",
		Api:      llm.Api(llm.KnownApiOpenAIChatCompletions),
		Provider: llm.Provider("local"),
		BaseURL:  "http://192.168.5.149:1234/v1",
	}

	// Define a simple tool for testing tool call support.
	tools := []llm.ToolDefinition{
		{
			Name:        "get_weather",
			Description: "Get the current weather for a city",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{
						"type":        "string",
						"description": "The city name",
					},
				},
				"required": []string{"city"},
			},
		},
	}

	ctx := &llm.Context{
		SystemPrompt: "You are a helpful assistant. When the user asks about weather, use the get_weather tool.",
		Messages: []llm.Message{
			llm.UserMessage{
				Role:    "user",
				Content: "What's the weather in Beijing?",
			},
		},
		Tools: tools,
	}

	// Use a dummy API key (local server usually doesn't need one).
	opts := &llm.StreamOptions{
		APIKey: "lm-studio",
	}

	fmt.Println("=== OpenAI Chat Completions Provider Test ===")
	fmt.Printf("Model: %s\n", model.ID)
	fmt.Printf("BaseURL: %s\n", model.BaseURL)
	fmt.Printf("Tools: %d defined\n", len(tools))
	fmt.Println("---")

	stream, err := llm.Stream(model, ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Stream error: %v\n", err)
		os.Exit(1)
	}

	var fullText strings.Builder
	var toolCalls []*llm.ToolCall

	for ev := range stream.Events() {
		switch ev.Type {
		case "start":
			fmt.Println("[start]")
		case "text_start":
			fmt.Print("[text] ")
		case "text_delta":
			fmt.Print(ev.Delta)
			fullText.WriteString(ev.Delta)
		case "text_end":
			fmt.Println()
		case "toolcall_start":
			fmt.Printf("[tool_call] %s (id=%s)\n", ev.ToolCall.Name, ev.ToolCall.ID)
			toolCalls = append(toolCalls, ev.ToolCall)
		case "toolcall_end":
			fmt.Printf("[tool_call_end] args=%v\n", ev.ToolCall.Arguments)
		case "error":
			fmt.Fprintf(os.Stderr, "\n[ERROR] %s\n", ev.Partial.ErrorMessage)
		case "done":
			fmt.Printf("\n--- Done (stop_reason=%s) ---\n", ev.Reason)
		}
	}

	result, err := stream.Result()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Result error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nFull text: %q\n", fullText.String())
	fmt.Printf("Tool calls: %d\n", len(toolCalls))
	fmt.Printf("Usage: input=%d output=%d total=%d\n",
		result.Usage.Input, result.Usage.Output, result.Usage.TotalTokens)
	fmt.Printf("Stop reason: %s\n", result.StopReason)
}
