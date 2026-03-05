// Package main demonstrates using the Agent with DeepSeek.
//
// Usage:
//
//	DEEPSEEK_API_KEY=sk-xxx go run ./examples/agent_deepseek/
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/providers"
	"github.com/crosszan/modu/pkg/types"
)

// --- Tool: Calculator ---

type CalculatorTool struct{}

func (t *CalculatorTool) Name() string  { return "calculator" }
func (t *CalculatorTool) Label() string { return "Calculator" }
func (t *CalculatorTool) Description() string {
	return "Perform basic math: add, subtract, multiply, divide, sqrt, power"
}
func (t *CalculatorTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{"add", "subtract", "multiply", "divide", "sqrt", "power"},
			},
			"a": map[string]any{"type": "number", "description": "First operand"},
			"b": map[string]any{"type": "number", "description": "Second operand (not needed for sqrt)"},
		},
		"required": []string{"operation", "a"},
	}
}

func (t *CalculatorTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	op, _ := args["operation"].(string)
	a := toFloat(args["a"])
	b := toFloat(args["b"])

	var result float64
	var desc string
	switch op {
	case "add":
		result = a + b
		desc = fmt.Sprintf("%.4g + %.4g = %.4g", a, b, result)
	case "subtract":
		result = a - b
		desc = fmt.Sprintf("%.4g - %.4g = %.4g", a, b, result)
	case "multiply":
		result = a * b
		desc = fmt.Sprintf("%.4g * %.4g = %.4g", a, b, result)
	case "divide":
		if b == 0 {
			return textResult("Error: division by zero"), nil
		}
		result = a / b
		desc = fmt.Sprintf("%.4g / %.4g = %.4g", a, b, result)
	case "sqrt":
		result = math.Sqrt(a)
		desc = fmt.Sprintf("sqrt(%.4g) = %.4g", a, result)
	case "power":
		result = math.Pow(a, b)
		desc = fmt.Sprintf("%.4g ^ %.4g = %.4g", a, b, result)
	default:
		return textResult("Unknown operation: " + op), nil
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
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *GetTimeTool) Execute(_ context.Context, _ string, _ map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	now := time.Now().Format("2006-01-02 15:04:05 MST")
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: now}},
		Details: map[string]any{"time": now},
	}, nil
}

func textResult(text string) agent.AgentToolResult {
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: text}},
	}
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
	}
	return 0
}

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

	// Register DeepSeek as an OpenAI-compatible provider
	providers.Register(providers.NewDeepSeekProvider(apiKey))

	model := &types.Model{
		ID:         modelID,
		Name:       "DeepSeek Chat",
		ProviderID: "deepseek",
	}

	a := agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentState{
			SystemPrompt: "You are a helpful assistant. Use tools when appropriate. Respond concisely.",
			Model:        model,
			Tools: []agent.AgentTool{
				&CalculatorTool{},
				&GetTimeTool{},
			},
		},
	})

	a.Subscribe(func(event agent.AgentEvent) {
		switch event.Type {
		case agent.EventTypeAgentStart:
			fmt.Println("\n=== Agent Start ===")
		case agent.EventTypeAgentEnd:
			fmt.Println("\n=== Agent End ===")
		case agent.EventTypeMessageUpdate:
			if event.StreamEvent != nil && event.StreamEvent.Type == "text_delta" {
				fmt.Print(event.StreamEvent.Delta)
			}
		case agent.EventTypeToolExecutionStart:
			fmt.Printf("\n>> Tool: %s %v\n", event.ToolName, event.Args)
		case agent.EventTypeToolExecutionEnd:
			if result, ok := event.Result.(agent.AgentToolResult); ok {
				for _, c := range result.Content {
					if tc, ok := c.(*types.TextContent); ok {
						fmt.Printf("<< %s\n", tc.Text)
					}
				}
			}
			if event.IsError {
				fmt.Println("<< [ERROR]")
			}
		}
	})

	testCases := []string{
		"What is 42 * 17 + sqrt(144)?",
		"What time is it now?",
		"Calculate: (2 ^ 10) divided by 8, then subtract 100",
	}

	ctx := context.Background()
	for i, prompt := range testCases {
		fmt.Printf("\n\n%s\nTest %d: %s\n%s\n", strings.Repeat("=", 55), i+1, prompt, strings.Repeat("=", 55))
		if err := a.Prompt(ctx, prompt); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}
		state := a.GetState()
		if state.Error != "" {
			fmt.Printf("\nError: %s\n", state.Error)
		}
		// Print token usage from last assistant message
		for j := len(state.Messages) - 1; j >= 0; j-- {
			var usage types.AgentUsage
			if msg, ok := state.Messages[j].(types.AssistantMessage); ok {
				usage = msg.Usage
			} else if msg, ok := state.Messages[j].(*types.AssistantMessage); ok {
				usage = msg.Usage
			} else {
				continue
			}
			if usage.TotalTokens > 0 {
				fmt.Printf("\nTokens: in=%d out=%d total=%d\n", usage.Input, usage.Output, usage.TotalTokens)
			}
			break
		}
	}
}
