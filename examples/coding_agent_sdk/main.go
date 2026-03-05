// Package main demonstrates the SDK factory, EventBus, auto-retry,
// model/thinking cycling, and session events.
//
// Usage:
//
//	OLLAMA_HOST=192.168.5.149 OLLAMA_MODEL=qwen3-coder-next go run ./examples/coding_agent_sdk/
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
	"github.com/crosszan/modu/pkg/types"
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

	// Register Ollama as an OpenAI-compatible provider
	providers.Register(providers.NewOpenAIChatCompletionsProvider(
		"ollama",
		providers.WithBaseURL(fmt.Sprintf("http://%s:11434/v1", ollamaHost)),
	))

	model := &types.Model{
		ID:         ollamaModel,
		Name:       ollamaModel + " (Ollama)",
		ProviderID: "ollama",
	}

	cwd, _ := os.Getwd()
	autoRetry := true

	// -------------------------------------------------------
	// 1. Create session via SDK factory
	// -------------------------------------------------------
	fmt.Println("=== Step 1: CreateSession via SDK factory ===")
	result, err := coding_agent.CreateSession(coding_agent.CreateSessionOptions{
		Cwd:   cwd,
		Model: model,
		Tools: tools.AllTools(cwd),
		// Scoped models for cycling demo
		ScopedModels: []string{ollamaModel, "gpt-4o"},
		// Enable auto-retry
		AutoRetry:     &autoRetry,
		ThinkingLevel: agent.ThinkingLevelMedium,
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "CreateSession failed: %v\n", err)
		os.Exit(1)
	}
	if result.ModelFallbackMessage != "" {
		fmt.Printf("  Model fallback: %s\n", result.ModelFallbackMessage)
	}

	session := result.Session
	fmt.Printf("  Session created, model=%s\n", session.GetModel().ID)
	fmt.Printf("  Thinking level: %s\n", session.GetThinkingLevel())
	fmt.Printf("  Auto-retry: %v\n", session.GetConfig().AutoRetry)

	// -------------------------------------------------------
	// 2. Subscribe to session events (EventBus)
	// -------------------------------------------------------
	fmt.Println("\n=== Step 2: Subscribe to session events ===")
	unsubSession := session.SubscribeSession(func(evt coding_agent.SessionEvent) {
		switch evt.Type {
		case coding_agent.SessionEventModelChange:
			fmt.Printf("  [SessionEvent] Model changed -> %s/%s\n", evt.Provider, evt.ModelID)
		case coding_agent.SessionEventThinkingChange:
			fmt.Printf("  [SessionEvent] Thinking level -> %s\n", evt.Level)
		case coding_agent.SessionEventAutoRetryStart:
			fmt.Printf("  [SessionEvent] Auto-retry starting, attempt %d/%d, delay %dms\n",
				evt.Attempt, evt.MaxAttempts, evt.DelayMs)
		case coding_agent.SessionEventAutoRetryEnd:
			fmt.Printf("  [SessionEvent] Auto-retry ended, success=%v\n", evt.Success)
		default:
			fmt.Printf("  [SessionEvent] %s\n", evt.Type)
		}
	})
	defer unsubSession()

	// Subscribe to agent events for streaming output
	unsubAgent := session.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.EventTypeMessageUpdate:
			if event.StreamEvent != nil && event.StreamEvent.Type == "text_delta" {
				fmt.Print(event.StreamEvent.Delta)
			}
		case agent.EventTypeToolExecutionStart:
			fmt.Printf("\n>> Tool: %s\n", event.ToolName)
		case agent.EventTypeToolExecutionEnd:
			fmt.Println("<< Done")
		}
	})
	defer unsubAgent()

	// -------------------------------------------------------
	// 3. CycleThinkingLevel demo
	// -------------------------------------------------------
	fmt.Println("\n=== Step 3: Cycle thinking level ===")
	for i := 0; i < 4; i++ {
		level := session.CycleThinkingLevel()
		fmt.Printf("  Cycle %d -> %s\n", i+1, level)
	}
	// Set back to medium for the prompt
	session.SetThinkingLevel(agent.ThinkingLevelMedium)

	// -------------------------------------------------------
	// 4. CycleModel demo (no actual LLM call, just cycling)
	// -------------------------------------------------------
	fmt.Println("\n=== Step 4: Cycle model ===")
	for i := 0; i < 3; i++ {
		m := session.CycleModel()
		if m != nil {
			fmt.Printf("  Cycle %d -> %s\n", i+1, m.ID)
		}
	}
	// Set back to the original model for the prompt
	session.SetModel(model)

	// -------------------------------------------------------
	// 5. Auto-retry config
	// -------------------------------------------------------
	fmt.Println("\n=== Step 5: Auto-retry config ===")
	fmt.Printf("  Auto-retry enabled: %v\n", session.GetConfig().AutoRetry)
	session.SetAutoRetry(false)
	fmt.Printf("  After disable: %v\n", session.GetConfig().AutoRetry)
	session.SetAutoRetry(true)
	fmt.Printf("  After re-enable: %v\n", session.GetConfig().AutoRetry)

	// -------------------------------------------------------
	// 6. Auto-compaction config
	// -------------------------------------------------------
	fmt.Println("\n=== Step 6: Auto-compaction config ===")
	fmt.Printf("  Auto-compaction enabled: %v\n", session.GetConfig().AutoCompaction)
	session.SetAutoCompaction(false)
	fmt.Printf("  After disable: %v\n", session.GetConfig().AutoCompaction)
	session.SetAutoCompaction(true)
	fmt.Printf("  After re-enable: %v\n", session.GetConfig().AutoCompaction)

	// -------------------------------------------------------
	// 7. GetMessages / GetSessionID / IsStreaming
	// -------------------------------------------------------
	fmt.Println("\n=== Step 7: Session state getters ===")
	fmt.Printf("  SessionID: %s\n", session.GetSessionID())
	fmt.Printf("  IsStreaming: %v\n", session.IsStreaming())
	fmt.Printf("  Messages: %d\n", len(session.GetMessages()))

	// -------------------------------------------------------
	// 8. Send a prompt
	// -------------------------------------------------------
	fmt.Println("\n=== Step 8: Send a prompt ===")
	fmt.Println(strings.Repeat("-", 50))

	ctx := context.Background()
	err = session.Prompt(ctx, "Use the ls tool to list the files in the current directory. Then briefly summarize what you see.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nPrompt error: %v\n", err)
	}
	session.WaitForIdle()

	fmt.Printf("\n\n  Messages after prompt: %d\n", len(session.GetMessages()))

	// -------------------------------------------------------
	// 9. EventBus direct usage
	// -------------------------------------------------------
	fmt.Println("\n=== Step 9: EventBus direct usage ===")
	bus := session.GetEventBus()
	var received bool
	unsub := bus.On("custom_channel", func(data any) {
		fmt.Printf("  Received on custom_channel: %v\n", data)
		received = true
	})
	bus.Emit("custom_channel", "hello from eventbus!")
	unsub()
	bus.Emit("custom_channel", "this should NOT be received")
	if received {
		fmt.Println("  EventBus works correctly")
	}

	fmt.Println("\n=== All SDK demo steps completed ===")
}
