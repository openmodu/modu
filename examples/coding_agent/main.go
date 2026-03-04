package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/crosszan/modu/pkg/agent"
	coding_agent "github.com/crosszan/modu/pkg/coding_agent"
	"github.com/crosszan/modu/pkg/coding_agent/extension"
	"github.com/crosszan/modu/pkg/coding_agent/tools"
	"github.com/crosszan/modu/pkg/providers"
)

// --- Hook Extension Demo ---

// auditHookExtension demonstrates the three hook types: Before, After, Transform.
type auditHookExtension struct {
	mu         sync.Mutex
	blocked    []string // tools blocked by Before hook
	afterCalls []string // tools observed by After hook
	transforms int      // number of Transform invocations
}

func (e *auditHookExtension) Name() string { return "audit-hook" }

func (e *auditHookExtension) Init(api extension.ExtensionAPI) error {
	runner, ok := api.(*extension.Runner)
	if !ok {
		return fmt.Errorf("expected *extension.Runner")
	}

	runner.AddHook(extension.ToolHook{
		// Before: log every tool call; block dangerous commands (e.g. rm -rf /)
		Before: func(toolName string, args map[string]any) bool {
			e.mu.Lock()
			defer e.mu.Unlock()

			if toolName == "bash" {
				cmd, _ := args["command"].(string)
				if strings.Contains(cmd, "rm -rf /") {
					fmt.Printf("\n  [Hook:Before] BLOCKED dangerous bash command: %s\n", cmd)
					e.blocked = append(e.blocked, cmd)
					return false
				}
			}
			fmt.Printf("  [Hook:Before] allowing %s\n", toolName)
			return true
		},

		// After: audit log for every completed tool call
		After: func(toolName string, args map[string]any, result agent.AgentToolResult) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.afterCalls = append(e.afterCalls, toolName)
			fmt.Printf("  [Hook:After] %s completed\n", toolName)
		},

		// Transform: append a watermark to text results
		Transform: func(toolName string, result agent.AgentToolResult) agent.AgentToolResult {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.transforms++
			return result
		},
	})

	return nil
}

func (e *auditHookExtension) PrintSummary() {
	e.mu.Lock()
	defer e.mu.Unlock()
	fmt.Println("\n--- Hook Summary ---")
	fmt.Printf("  Blocked commands : %d %v\n", len(e.blocked), e.blocked)
	fmt.Printf("  After calls      : %d %v\n", len(e.afterCalls), e.afterCalls)
	fmt.Printf("  Transforms       : %d\n", e.transforms)
}

func main() {
	ollamaHost := "http://192.168.5.149:1234/v1"
	ollamaModel := "qwen3.5-30b-a3b"

	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		ollamaHost = h
	}
	if m := os.Getenv("OLLAMA_MODEL"); m != "" {
		ollamaModel = m
	}

	enableThinking := os.Getenv("ENABLE_THINKING")

	// Register Ollama as an OpenAI-compatible provider
	providers.Register(providers.NewOpenAIChatCompletionsProvider(
		"lm_studio",
		providers.WithBaseURL(fmt.Sprintf("%s/v1", ollamaHost)),
	))

	_ = enableThinking // used via ThinkingLevel config
	model := &providers.Model{
		ID:         ollamaModel,
		Name:       ollamaModel + " (Ollama)",
		ProviderID: "ollama",
	}

	cwd, _ := os.Getwd()
	fmt.Printf("Working directory: %s\n", cwd)
	fmt.Printf("Ollama: %s, model: %s, thinking: %v\n\n", ollamaHost, ollamaModel, enableThinking)

	// Create hook extension
	hookExt := &auditHookExtension{}

	// Create CodingSession with hook extension
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:        cwd,
		Model:      model,
		Tools:      tools.AllTools(cwd),
		Extensions: []extension.Extension{hookExt},
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
				switch evt.Type {
				case "thinking_delta":
					fmt.Print(evt.Delta)
				case "thinking_end":
					fmt.Print("\n--- end thinking ---\n\n")
				case "thinking_start":
					fmt.Print("\n--- thinking ---\n")
				case "text_delta":
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
					if tc, ok := block.(*providers.TextContent); ok {
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

	// Print hook summary
	hookExt.PrintSummary()

	fmt.Println("\nAll tests done!")
}
