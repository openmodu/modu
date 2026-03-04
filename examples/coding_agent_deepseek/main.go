// Package main demonstrates CodingAgent with DeepSeek.
//
// Usage:
//
//	DEEPSEEK_API_KEY=sk-xxx go run ./examples/coding_agent_deepseek/
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
	"github.com/crosszan/modu/pkg/coding_agent/tools"
	"github.com/crosszan/modu/pkg/providers"
)

func main() {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "DEEPSEEK_API_KEY is required")
		os.Exit(1)
	}

	modelID := "deepseek-chat"
	if m := os.Getenv("DEEPSEEK_MODEL"); m != "" {
		modelID = m
	}

	// Register DeepSeek provider
	providers.Register(providers.NewDeepSeekProvider(apiKey))

	model := &providers.Model{
		ID:         modelID,
		Name:       "DeepSeek Chat",
		ProviderID: "deepseek",
	}

	cwd, _ := os.Getwd()
	fmt.Printf("Working directory: %s\n", cwd)
	fmt.Printf("Model: %s\n\n", model.ID)

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:   cwd,
		Model: model,
		Tools: tools.AllTools(cwd),
		GetAPIKey: func(provider string) (string, error) {
			if provider == "deepseek" {
				return apiKey, nil
			}
			return "", nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create session: %v\n", err)
		os.Exit(1)
	}

	unsubscribe := session.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.EventTypeAgentStart:
			fmt.Println("\n=== Agent Started ===")
		case agent.EventTypeMessageUpdate:
			if event.AssistantMessageEvent != nil && event.AssistantMessageEvent.Type == "text_delta" {
				fmt.Print(event.AssistantMessageEvent.Delta)
			}
		case agent.EventTypeToolExecutionStart:
			fmt.Printf("\n\n>> Tool: %s\n", event.ToolName)
			if args, ok := event.Args.(map[string]any); ok {
				for k, v := range args {
					val := fmt.Sprintf("%v", v)
					if len(val) > 120 {
						val = val[:120] + "..."
					}
					fmt.Printf("   %s: %s\n", k, val)
				}
			}
		case agent.EventTypeToolExecutionEnd:
			if result, ok := event.Result.(agent.AgentToolResult); ok {
				for _, block := range result.Content {
					if tc, ok := block.(*providers.TextContent); ok {
						text := tc.Text
						if len(text) > 400 {
							text = text[:400] + "\n   ...(truncated)"
						}
						fmt.Printf("<< %s\n", text)
					} else if tc, ok := block.(*providers.TextContent); ok {
						text := tc.Text
						if len(text) > 400 {
							text = text[:400] + "\n   ...(truncated)"
						}
						fmt.Printf("<< %s\n", text)
					}
				}
			}
			if event.IsError {
				fmt.Println("<< [ERROR]")
			}
		case agent.EventTypeAgentEnd:
			fmt.Println("\n\n=== Agent Finished ===")
		}
	})
	defer unsubscribe()

	toolNames := session.GetActiveToolNames()
	fmt.Printf("Active tools: %s\n", strings.Join(toolNames, ", "))

	testCases := []struct {
		name   string
		prompt string
	}{
		{
			name:   "List & Read",
			prompt: "Use the ls tool to list files in the current directory, then read go.mod. Briefly describe this project.",
		},
		{
			name:   "Search Code",
			prompt: "Use grep to find files containing 'AgentTool' in pkg/. How many files reference it?",
		},
		{
			name:   "Write & Read",
			prompt: "Write a file /tmp/deepseek_test.txt containing 'Hello from DeepSeek coding agent!', then read it back.",
		},
	}

	ctx := context.Background()
	for i, tc := range testCases {
		fmt.Printf("\n\n%s\nTest %d: %s\n%s\n", strings.Repeat("=", 60), i+1, tc.name, strings.Repeat("=", 60))
		fmt.Printf("Prompt: %s\n", tc.prompt)

		if err := session.Prompt(ctx, tc.prompt); err != nil {
			fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
			continue
		}
		session.WaitForIdle()
		fmt.Println()
	}

	fmt.Printf("\nSession ID: %s\n", session.GetSessionID())
	fmt.Printf("Total messages: %d\n", len(session.GetMessages()))
	fmt.Println("\nDone!")
}
