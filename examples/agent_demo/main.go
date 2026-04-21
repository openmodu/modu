package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

// --- Tool: Calculator ---

type CalculatorTool struct{}

func (t *CalculatorTool) Name() string  { return "calculator" }
func (t *CalculatorTool) Label() string { return "Calculator" }
func (t *CalculatorTool) Description() string {
	return "Perform basic math operations: add, subtract, multiply, divide, sqrt, power"
}
func (t *CalculatorTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type":        "string",
				"description": "The operation to perform",
				"enum":        []string{"add", "subtract", "multiply", "divide", "sqrt", "power"},
			},
			"a": map[string]any{
				"type":        "number",
				"description": "First operand",
			},
			"b": map[string]any{
				"type":        "number",
				"description": "Second operand (not needed for sqrt)",
			},
		},
		"required": []string{"operation", "a"},
	}
}

func (t *CalculatorTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	op, _ := args["operation"].(string)
	a := toFloat(args["a"])
	b := toFloat(args["b"])

	var result float64
	var desc string

	switch op {
	case "add":
		result = a + b
		desc = fmt.Sprintf("%.2f + %.2f = %.2f", a, b, result)
	case "subtract":
		result = a - b
		desc = fmt.Sprintf("%.2f - %.2f = %.2f", a, b, result)
	case "multiply":
		result = a * b
		desc = fmt.Sprintf("%.2f * %.2f = %.2f", a, b, result)
	case "divide":
		if b == 0 {
			return agent.AgentToolResult{
				Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Error: division by zero"}},
				Details: map[string]any{},
			}, nil
		}
		result = a / b
		desc = fmt.Sprintf("%.2f / %.2f = %.2f", a, b, result)
	case "sqrt":
		result = math.Sqrt(a)
		desc = fmt.Sprintf("sqrt(%.2f) = %.2f", a, result)
	case "power":
		result = math.Pow(a, b)
		desc = fmt.Sprintf("%.2f ^ %.2f = %.2f", a, b, result)
	default:
		return agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Unknown operation: " + op}},
			Details: map[string]any{},
		}, nil
	}

	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: desc}},
		Details: map[string]any{"result": result},
	}, nil
}

// --- Tool: GetCurrentTime ---

type GetTimeTool struct{}

func (t *GetTimeTool) Name() string        { return "get_current_time" }
func (t *GetTimeTool) Label() string       { return "Get Time" }
func (t *GetTimeTool) Description() string { return "Get the current date and time" }
func (t *GetTimeTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *GetTimeTool) Execute(ctx context.Context, toolCallID string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	now := time.Now().Format("2006-01-02 15:04:05 MST")
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: now}},
		Details: map[string]any{"time": now},
	}, nil
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func main() {
	modelName := "qwen/qwen3.6-35b-a3b"
	// modelName := "zai-org/glm-4.7-flash"
	baseURL := "http://192.168.5.149:1234/v1"
	providerID := "lmstudio"

	// Register LM Studio as an OpenAI-compatible provider
	providers.Register(openai.New(
		providerID,
		openai.WithBaseURL(baseURL),
	))

	model := &types.Model{
		ID:         modelName,
		Name:       "Qwen3.5 35B A3B",
		ProviderID: providerID,
	}

	a := agent.NewAgent(agent.AgentConfig{
		InitialState: &agent.AgentState{
			SystemPrompt: "You are a helpful assistant. When asked math questions, use the calculator tool. When asked about the time, use the get_current_time tool. Always respond in the same language as the user.",
			Model:        model,
			Tools: []agent.AgentTool{
				&CalculatorTool{},
				&GetTimeTool{},
			},
		},
	})

	// Subscribe to events for observability
	a.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.EventTypeAgentStart:
			fmt.Println("\n=== Agent Start ===")
		case agent.EventTypeAgentEnd:
			fmt.Println("\n=== Agent End ===")
		case agent.EventTypeTurnStart:
			fmt.Println("--- Turn Start ---")
		case agent.EventTypeTurnEnd:
			fmt.Println("\n--- Turn End ---")
		case agent.EventTypeMessageStart:
			if _, ok := event.Message.(types.AssistantMessage); ok {
				fmt.Printf("[Assistant] ")
			} else if _, ok := event.Message.(*types.AssistantMessage); ok {
				fmt.Printf("[Assistant] ")
			}
		case agent.EventTypeMessageUpdate:
			if event.StreamEvent != nil {
				if event.StreamEvent.Type == "text_delta" {
					fmt.Print(event.StreamEvent.Delta)
				}
			}
		case agent.EventTypeToolExecutionStart:
			fmt.Printf("\n>> Tool Call: %s (id=%s)\n", event.ToolName, event.ToolCallID)
			if args, ok := event.Args.(map[string]any); ok {
				fmt.Printf("   Args: %v\n", args)
			}
		case agent.EventTypeToolExecutionEnd:
			if result, ok := event.Result.(agent.AgentToolResult); ok {
				for _, c := range result.Content {
					if tc, ok := c.(*types.TextContent); ok {
						fmt.Printf("   Result: %s\n", tc.Text)
					}
				}
			}
			if event.IsError {
				fmt.Println("   [ERROR]")
			}
		}
	})

	// Test cases
	testCases := []string{
		"What is 42 * 17 + 3?",
		"What time is it now?",
		"Calculate the square root of 144, then raise it to the power of 3",
		"[Demo: FollowUpMode OneAtATime]",
		"[Demo: FollowUpMode All]",
		"[Demo: SteeringMode Priority]",
	}

	for i, prompt := range testCases {
		fmt.Printf("\n\n========== Test %d: %s ==========\n", i+1, prompt)

		if prompt == "[Demo: FollowUpMode OneAtATime]" {
			fmt.Println("\n[Demo] This test shows how FollowUp queues process exactly one message per completion.")
			a.ClearMessages() // Start fresh

			// 1. Initial Prompt
			fmt.Println("\n>>> 1. Sending initial prompt: 'What is 10 + 10?'")
			err := a.Prompt(context.Background(), "What is 10 + 10?")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}

			// 2. Queue FollowUps
			fmt.Println("\n>>> 2. Injecting TWO FollowUp messages into the queue...")
			a.FollowUp(types.UserMessage{Role: "user", Content: "Now multiply that result by 2.", Timestamp: time.Now().UnixMilli()})
			a.FollowUp(types.UserMessage{Role: "user", Content: "And then subtract 5.", Timestamp: time.Now().UnixMilli()})

			// 3. Set Mode and Continue
			fmt.Println("\n>>> 3. Setting FollowUpMode to ExecutionModeOneAtATime and triggering process...")
			a.SetFollowUpMode(agent.ExecutionModeOneAtATime)

			err = a.Prompt(context.Background(), "Please process the queued follow-ups.")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Prompt Error: %v\n", err)
			}

			fmt.Println("\n>>> 4. Triggering again to process the second FollowUp...")
			err = a.Prompt(context.Background(), "Process the remaining follow-up.")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Prompt Error: %v\n", err)
			}

		} else if prompt == "[Demo: FollowUpMode All]" {
			fmt.Println("\n[Demo] This test shows ExecutionModeAll processing all queued messages simultaneously.")
			a.ClearMessages()

			// 1. Initial Prompt
			fmt.Println("\n>>> 1. Sending initial prompt: 'What is 10 + 10?'")
			err := a.Prompt(context.Background(), "What is 10 + 10?")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}

			// 2. Queue FollowUps
			fmt.Println("\n>>> 2. Injecting TWO FollowUp messages into the queue...")
			a.FollowUp(types.UserMessage{Role: "user", Content: "Now multiply that result by 2.", Timestamp: time.Now().UnixMilli()})
			a.FollowUp(types.UserMessage{Role: "user", Content: "And then subtract 5.", Timestamp: time.Now().UnixMilli()})

			// 3. Set Mode and Continue
			fmt.Println("\n>>> 3. Setting FollowUpMode to ExecutionModeAll and triggering process...")
			a.SetFollowUpMode(agent.ExecutionModeAll)

			// This single prompt will pull BOTH queued follow-up messages into the same context block
			err = a.Prompt(context.Background(), "Please process the queued follow-ups.")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Prompt Error: %v\n", err)
			}

		} else if prompt == "[Demo: SteeringMode Priority]" {
			fmt.Println("\n[Demo] This test shows that Steering messages interrupt and take priority over FollowUp messages.")
			a.ClearMessages()

			// 1. Set modes to OneAtATime to clearly see what goes first
			a.SetFollowUpMode(agent.ExecutionModeOneAtATime)
			a.SetSteeringMode(agent.ExecutionModeOneAtATime)

			// 2. Queue a FollowUp AND a Steering message *before* prompting
			fmt.Println("\n>>> 1. Injecting one FollowUp message and one Steering message...")
			a.FollowUp(types.UserMessage{Role: "user", Content: "[FollowUp Task] What is 2+2? Only answer this math problem.", Timestamp: time.Now().UnixMilli()})
			a.Steer(types.UserMessage{Role: "user", Content: "[Steer Priority] Disregard any queued user tasks. Reply strictly with the exact phrase: 'EMERGENCY OVERRIDE'. Do not compute Math.", Timestamp: time.Now().UnixMilli()})

			// 3. Trigger initial prompt. The agent will read SteeringQueue BEFORE FollowUpQueue!
			fmt.Println("\n>>> 2. Sending trigger. The Steering message should be processed FIRST.")
			err := a.Prompt(context.Background(), "(System action: trigger queues)")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Prompt Error: %v\n", err)
			}

			// 4. Trigger again to catch the FollowUp that was left behind
			fmt.Println("\n>>> 3. Triggering again. Now the FollowUp message should be processed.")
			err = a.Prompt(context.Background(), "(System action: trigger queues)")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Prompt Error: %v\n", err)
			}

		} else {
			err := a.Prompt(context.Background(), prompt)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		}

		// Print final state
		state := a.GetState()
		fmt.Printf("\nFinal Messages count: %d\n", len(state.Messages))
		if state.Error != "" {
			fmt.Printf("Error: %s\n", state.Error)
		}

		// Print last assistant message content
		for j := len(state.Messages) - 1; j >= 0; j-- {
			msg, ok := state.Messages[j].(types.AssistantMessage)
			if !ok {
				if ptr, ok2 := state.Messages[j].(*types.AssistantMessage); ok2 {
					msg = *ptr
				} else {
					continue
				}
			}
			fmt.Printf("Final answer: ")
			for _, c := range msg.Content {
				if tc, ok := c.(*types.TextContent); ok {
					fmt.Print(tc.Text)
				}
			}
			fmt.Println()
			if msg.Usage.TotalTokens > 0 {
				fmt.Printf("Tokens: input=%d output=%d total=%d\n", msg.Usage.Input, msg.Usage.Output, msg.Usage.TotalTokens)
			}
			break
		}

		fmt.Println(strings.Repeat("=", 50))
	}
}
