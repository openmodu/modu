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
	ollamaHost := "192.168.5.149"
	ollamaModel := "qwen3-coder-next"

	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		ollamaHost = h
	}
	if m := os.Getenv("OLLAMA_MODEL"); m != "" {
		ollamaModel = m
	}

	model := &llm.Model{
		ID:            ollamaModel,
		Name:          ollamaModel + " (Ollama)",
		Api:           llm.Api(llm.KnownApiOllama),
		Provider:      llm.Provider(llm.KnownProviderOllama),
		BaseURL:       fmt.Sprintf("http://%s:11434", ollamaHost),
		Input:         []string{"text"},
		ContextWindow: 32768,
		MaxTokens:     4096,
	}

	cwd, _ := os.Getwd()
	fmt.Printf("Working directory: %s\n", cwd)
	fmt.Printf("Ollama: http://%s:11434, model: %s\n\n", ollamaHost, ollamaModel)

	// Create CodingSession
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:   cwd,
		Model: model,
		Tools: tools.AllTools(cwd),
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create coding session: %v\n", err)
		os.Exit(1)
	}

	// Subscribe to events
	unsubscribe := session.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.EventTypeAgentStart:
			fmt.Println("\n=== Agent Started ===")
		case agent.EventTypeMessageUpdate:
			if event.AssistantMessageEvent != nil {
				evt := event.AssistantMessageEvent
				if evt.Type == "text_delta" {
					fmt.Print(evt.Delta)
				}
			}
		case agent.EventTypeToolExecutionStart:
			fmt.Printf("\n\n>> Tool: %s\n", event.ToolName)
			if args, ok := event.Args.(map[string]any); ok {
				for k, v := range args {
					val := fmt.Sprintf("%v", v)
					if len(val) > 100 {
						val = val[:100] + "..."
					}
					fmt.Printf("   %s: %s\n", k, val)
				}
			}
		case agent.EventTypeToolExecutionEnd:
			fmt.Printf("<< Result:\n")
			if result, ok := event.Result.(agent.AgentToolResult); ok {
				for _, block := range result.Content {
					if tc, ok := block.(llm.TextContent); ok {
						text := tc.Text
						if len(text) > 500 {
							text = text[:500] + "\n   ... (truncated)"
						}
						fmt.Println(text)
					}
				}
			}
			if event.IsError {
				fmt.Println("   [ERROR]")
			}
		case agent.EventTypeAgentEnd:
			fmt.Println("\n\n=== Agent Finished ===")
		}
	})
	defer unsubscribe()

	// Show active tools
	toolNames := session.GetActiveToolNames()
	fmt.Printf("Active tools: %s\n", strings.Join(toolNames, ", "))

	// Run test cases
	testCases := []struct {
		name   string
		prompt string
	}{
		{
			name:   "List & Read",
			prompt: "List the files in the current directory using the ls tool, then read the go.mod file using the read tool. Tell me what this project is about.",
		},
		{
			name:   "Search Code",
			prompt: "Use the grep tool to find all files containing 'AgentTool' in the pkg/ directory. How many files reference this interface?",
		},
		{
			name:   "Write File",
			prompt: "Create a file called /tmp/coding_agent_test.txt with the content 'Hello from coding agent!' and then read it back to confirm.",
		},
	}

	ctx := context.Background()

	for i, tc := range testCases {
		fmt.Printf("\n\n%s\n", strings.Repeat("=", 60))
		fmt.Printf("Test %d: %s\n", i+1, tc.name)
		fmt.Printf("%s\n", strings.Repeat("=", 60))
		fmt.Printf("Prompt: %s\n", tc.prompt)

		err = session.Prompt(ctx, tc.prompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}

		session.WaitForIdle()
		fmt.Println()
	}

	fmt.Println("\nAll tests done!")
}
