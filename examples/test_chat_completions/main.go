package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/types"
)

func main() {
	providers.Register(providers.NewOpenAIChatCompletionsProvider("local",
		providers.WithBaseURL("http://localhost:1234/v1"),
		providers.WithAPIKey("lm-studio"),
	))

	model := &types.Model{
		ID:            "qwen3.5-30b-a3b",
		Name:          "qwen3.5-30b-a3b",
		Api:           types.KnownApiOpenAIChatCompletions,
		ProviderID:    "local",
		ContextWindow: 32768,
		MaxTokens:     4096,
	}

	// Define a simple tool for testing tool call support.
	tools := []types.ToolDefinition{
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

	llmCtx := &types.LLMContext{
		SystemPrompt: "You are a helpful assistant. When the user asks about weather, use the get_weather tool.",
		Messages: []types.AgentMessage{
			types.UserMessage{
				Role:    "user",
				Content: "What's the weather in Beijing?",
			},
		},
		Tools: tools,
	}

	opts := &types.SimpleStreamOptions{
		StreamOptions: types.StreamOptions{
			APIKey: "lm-studio",
		},
	}

	fmt.Println("=== OpenAI Chat Completions Provider Test ===")
	fmt.Printf("Model: %s\n", model.ID)
	fmt.Printf("Tools: %d defined\n", len(tools))
	fmt.Println("---")

	stream, err := providers.StreamDefault(context.Background(), model, llmCtx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Stream error: %v\n", err)
		os.Exit(1)
	}

	var fullText strings.Builder

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
	fmt.Printf("Usage: input=%d output=%d total=%d\n",
		result.Usage.Input, result.Usage.Output, result.Usage.TotalTokens)
	fmt.Printf("Stop reason: %s\n", result.StopReason)
}
