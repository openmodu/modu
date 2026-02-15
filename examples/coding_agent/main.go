package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
	"github.com/crosszan/modu/pkg/coding_agent/tools"
	"github.com/crosszan/modu/pkg/llm"
	_ "github.com/crosszan/modu/pkg/llm/providers/ollama"
)

func main() {
	// Configure the LLM model (using local Ollama)
	model := &llm.Model{
		ID:            "qwen3-coder-next",
		Name:          "Qwen3 Coder Next (Ollama)",
		Api:           llm.Api(llm.KnownApiOllama),
		Provider:      llm.Provider(llm.KnownProviderOllama),
		BaseURL:       getEnv("OLLAMA_BASE_URL", "http://localhost:11434"),
		ContextWindow: 32768,
		MaxTokens:     4096,
	}

	cwd, _ := os.Getwd()

	// Create a CodingSession with all tools
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:   cwd,
		Model: model,
		Tools: tools.AllTools(cwd),
		GetAPIKey: func(provider string) (string, error) {
			return "", nil // Ollama doesn't need API key
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create coding session: %v\n", err)
		os.Exit(1)
	}

	// Subscribe to events to observe the agent's behavior
	unsubscribe := session.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.EventTypeAgentStart:
			fmt.Println("=== Agent Started ===")

		case agent.EventTypeMessageUpdate:
			if event.AssistantMessageEvent != nil {
				evt := event.AssistantMessageEvent
				if evt.Type == "text_delta" {
					fmt.Print(evt.Delta)
				}
			}

		case agent.EventTypeToolExecutionStart:
			fmt.Printf("\n--- Tool Call: %s ---\n", event.ToolName)
			if args, ok := event.Args.(map[string]any); ok {
				for k, v := range args {
					fmt.Printf("  %s: %v\n", k, v)
				}
			}

		case agent.EventTypeToolExecutionEnd:
			fmt.Printf("--- Tool Result ---\n")
			if result, ok := event.Result.(agent.AgentToolResult); ok {
				for _, block := range result.Content {
					if tc, ok := block.(llm.TextContent); ok {
						text := tc.Text
						if len(text) > 200 {
							text = text[:200] + "..."
						}
						fmt.Println(text)
					}
				}
			}

		case agent.EventTypeAgentEnd:
			fmt.Println("\n=== Agent Finished ===")
		}
	})
	defer unsubscribe()

	// Show active tools
	toolNames := session.GetActiveToolNames()
	fmt.Printf("Active tools: %s\n\n", strings.Join(toolNames, ", "))

	// Run a coding task
	ctx := context.Background()
	fmt.Println("Sending task: List the files in the current directory and read the go.mod file")

	err = session.Prompt(ctx, "List the files in the current directory using the ls tool, then read the go.mod file using the read tool. Summarize what you found.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Wait for completion
	session.WaitForIdle()

	fmt.Println("\nDone!")
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
