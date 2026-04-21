// managed_agent_demo demonstrates three patterns inspired by the Anthropic Managed Agents API:
//
//  1. Session lifecycle FSM  — Status transitions idle→running→paused→running→completed
//  2. Interrupt / Resume     — Tool approval and max-steps via EventTypeInterrupt + Agent.Resume()
//  3. Sub-agent as first-class tool — inline sub-agent shares the same AgentTool interface
//
// Run:
//
//	go run ./examples/managed_agent_demo
//
// Set LMSTUDIO_BASE_URL to your local LM Studio endpoint, or swap the provider
// in main() for any other registered provider.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

// ─── Tool: SafeSearch (auto-approved) ────────────────────────────────────────

type SafeSearchTool struct{}

func (t *SafeSearchTool) Name() string  { return "safe_search" }
func (t *SafeSearchTool) Label() string { return "Safe Search" }
func (t *SafeSearchTool) Description() string {
	return "Search for public information. Always auto-approved."
}
func (t *SafeSearchTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query"},
		},
		"required": []string{"query"},
	}
}
func (t *SafeSearchTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	query, _ := args["query"].(string)
	// Simulated result
	result := fmt.Sprintf("[search result for '%s']: Found 3 relevant articles. Top result: "+
		"'A comprehensive overview of %s — published 2025.'", query, query)
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: result}},
	}, nil
}

// ─── Tool: DangerousAction (requires interrupt approval) ─────────────────────

type DangerousActionTool struct{}

func (t *DangerousActionTool) Name() string  { return "dangerous_action" }
func (t *DangerousActionTool) Label() string { return "Dangerous Action" }
func (t *DangerousActionTool) Description() string {
	return "Perform an irreversible side-effect (e.g. send email, delete file). Requires human approval."
}
func (t *DangerousActionTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":  map[string]any{"type": "string", "description": "Action description"},
			"payload": map[string]any{"type": "string", "description": "Action payload"},
		},
		"required": []string{"action"},
	}
}
func (t *DangerousActionTool) Execute(_ context.Context, _ string, args map[string]any, _ agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	action, _ := args["action"].(string)
	payload, _ := args["payload"].(string)
	result := fmt.Sprintf("[dangerous_action executed] action=%q payload=%q — done.", action, payload)
	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: result}},
	}, nil
}

// ─── Tool: SubAgentTool (inline sub-agent, no mailbox) ───────────────────────
//
// Demonstrates "sub-agent as first-class tool": the orchestrator calls this tool
// exactly like any other tool; internally it spins up a fully independent Agent
// with its own conversation, runs it to completion, and returns the result.
// No mailbox, no polling — just a synchronous blocking Execute().

type SubAgentTool struct {
	model *types.Model
}

func (t *SubAgentTool) Name() string  { return "delegate_to_subagent" }
func (t *SubAgentTool) Label() string { return "Delegate to Sub-Agent" }
func (t *SubAgentTool) Description() string {
	return "Delegate a research sub-task to a specialised sub-agent and return its answer."
}
func (t *SubAgentTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The research task for the sub-agent to complete",
			},
		},
		"required": []string{"task"},
	}
}
func (t *SubAgentTool) Execute(ctx context.Context, _ string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
	task, _ := args["task"].(string)

	onUpdate(agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Sub-agent starting…"}},
	})

	// Spin up an independent sub-agent. It has its own tools (only SafeSearch),
	// its own conversation, and no interrupt/resume overhead.
	sub := agent.NewAgent(agent.AgentConfig{
		InitialState: &agent.AgentState{
			SystemPrompt: "You are a research specialist. Answer the given task concisely using the safe_search tool.",
			Model:        t.model,
			Tools:        []agent.AgentTool{&SafeSearchTool{}},
		},
	})

	var answer strings.Builder
	sub.Subscribe(func(ev agent.AgentEvent) {
		if ev.Type == agent.EventTypeMessageUpdate && ev.StreamEvent != nil && ev.StreamEvent.Type == "text_delta" {
			answer.WriteString(ev.StreamEvent.Delta)
		}
	})

	if err := sub.Prompt(ctx, task); err != nil {
		return agent.AgentToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "sub-agent error: " + err.Error()}},
		}, nil
	}

	result := strings.TrimSpace(answer.String())
	if result == "" {
		result = "(sub-agent returned no text)"
	}

	onUpdate(agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Sub-agent completed."}},
	})

	return agent.AgentToolResult{
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: result}},
		Details: map[string]any{"delegated_task": task},
	}, nil
}

// ─── Interrupt UI ─────────────────────────────────────────────────────────────

// handleInterrupt prints the interrupt event and prompts the user to approve/deny.
// Returns the ResumeDecision to pass to Agent.Resume().
func handleInterrupt(ev *agent.InterruptEvent) agent.ResumeDecision {
	fmt.Println()
	switch ev.Reason {
	case agent.InterruptReasonToolApproval:
		fmt.Printf("╔══ INTERRUPT: tool_use_approval ═══════════════╗\n")
		fmt.Printf("║  Tool     : %s\n", ev.ToolName)
		fmt.Printf("║  CallID   : %s\n", ev.ToolCallID)
		if len(ev.ToolArgs) > 0 {
			fmt.Printf("║  Args     : %v\n", ev.ToolArgs)
		}
		fmt.Printf("╚═══════════════════════════════════════════════╝\n")
		fmt.Print("Allow this tool call? [y/n]: ")
	case agent.InterruptReasonMaxSteps:
		fmt.Printf("╔══ INTERRUPT: max_steps_reached ════════════════╗\n")
		fmt.Printf("║  Steps completed: %d\n", ev.StepCount)
		fmt.Printf("╚════════════════════════════════════════════════╝\n")
		fmt.Print("Continue for more steps? [y/n]: ")
	default:
		fmt.Printf("╔══ INTERRUPT: %s\n", ev.Reason)
		fmt.Print("Resume? [y/n]: ")
	}

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	allow := strings.TrimSpace(strings.ToLower(line)) == "y"
	return agent.ResumeDecision{Allow: allow}
}

// ─── Event printer ───────────────────────────────────────────────────────────

func printEvent(ev agent.AgentEvent) {
	switch ev.Type {
	case agent.EventTypeAgentStart:
		fmt.Printf("\n┌── agent_start\n")
	case agent.EventTypeAgentEnd:
		fmt.Printf("\n└── agent_end\n")
	case agent.EventTypeTurnStart:
		fmt.Printf("│  ┌─ turn_start\n")
	case agent.EventTypeTurnEnd:
		fmt.Printf("│  └─ turn_end\n")
	case agent.EventTypeMessageUpdate:
		if ev.StreamEvent != nil && ev.StreamEvent.Type == "text_delta" {
			fmt.Print(ev.StreamEvent.Delta)
		}
	case agent.EventTypeToolExecutionStart:
		fmt.Printf("\n│  ⚙  %s(…)\n", ev.ToolName)
	case agent.EventTypeToolExecutionEnd:
		if ev.IsError {
			fmt.Printf("│  ✗  %s denied/error\n", ev.ToolName)
		} else {
			fmt.Printf("│  ✓  %s done\n", ev.ToolName)
		}
	}
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	baseURL := os.Getenv("LMSTUDIO_BASE_URL")
	if baseURL == "" {
		baseURL = "http://192.168.5.149:1234/v1"
	}
	modelID := os.Getenv("MODEL_ID")
	if modelID == "" {
		modelID = "qwen/qwen3.6-35b-a3b"
	}

	providers.Register(openai.New("lmstudio", openai.WithBaseURL(baseURL)))

	model := &types.Model{ID: modelID, Name: modelID, ProviderID: "lmstudio"}

	// ── Pattern 1 & 2: Interrupt / Resume + Session lifecycle ────────────────
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("Demo 1: Session lifecycle + Interrupt/Resume")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	a := agent.NewAgent(agent.AgentConfig{
		// EnableInterrupts wires the interrupt/resume pattern:
		//   - tool calls emit EventTypeInterrupt (tool_use_approval) + pause
		//   - MaxSteps exceeded emits EventTypeInterrupt (max_steps_reached) + pause
		//   - AgentState.Status tracks: idle→running→paused→running→completed
		EnableInterrupts: true,
		MaxSteps:         3,
		InitialState: &agent.AgentState{
			SystemPrompt: "You are a helpful assistant. " +
				"First use safe_search to look up the topic, " +
				"then call dangerous_action to send a summary email to the user.",
			Model: model,
			Tools: []agent.AgentTool{
				&SafeSearchTool{},
				&DangerousActionTool{},
			},
		},
	})

	// Subscribe for UI + interrupt handling.
	// When we get EventTypeInterrupt, we ask the user and call Resume().
	a.Subscribe(func(ev agent.AgentEvent) {
		printEvent(ev)

		if ev.Type == agent.EventTypeInterrupt && ev.Interrupt != nil {
			decision := handleInterrupt(ev.Interrupt)
			// Resume() is safe to call from any goroutine; the buffered channel
			// ensures it never blocks even if called before interruptBlock() reads it.
			if !a.Resume(decision) {
				fmt.Println("[warning] Resume() called but agent was not paused")
			}
		}
	})

	fmt.Printf("\nInitial status: %s\n", a.GetStatus()) // idle

	ctx := context.Background()
	prompt := "Research the impact of AI on software engineering and then email me a summary."

	fmt.Printf("\nPrompt: %q\n", prompt)
	fmt.Println("(The agent will pause at each dangerous_action call and at MaxSteps=3)")
	fmt.Println()

	start := time.Now()
	if err := a.Prompt(ctx, prompt); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}

	fmt.Printf("\nFinal status : %s  (elapsed %s)\n", a.GetStatus(), time.Since(start).Round(time.Millisecond))

	// ── Pattern 3: Sub-agent as first-class tool ──────────────────────────────
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("Demo 2: Sub-agent as first-class tool (no mailbox)")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	fmt.Println("The orchestrator has a single tool: delegate_to_subagent.")
	fmt.Println("Sub-agents are just AgentTool implementations — no mailbox, no polling.")
	fmt.Println()

	orchestrator := agent.NewAgent(agent.AgentConfig{
		InitialState: &agent.AgentState{
			SystemPrompt: "You are an orchestrator. Use delegate_to_subagent to hand off research tasks.",
			Model:        model,
			Tools: []agent.AgentTool{
				&SubAgentTool{model: model},
			},
		},
	})

	orchestrator.Subscribe(func(ev agent.AgentEvent) {
		printEvent(ev)
	})

	orchPrompt := "Find out what Go generics are and summarise in 2 sentences."
	fmt.Printf("Prompt: %q\n\n", orchPrompt)

	if err := orchestrator.Prompt(ctx, orchPrompt); err != nil {
		fmt.Fprintf(os.Stderr, "orchestrator error: %v\n", err)
	}

	// Print final answer
	state := orchestrator.GetState()
	for i := len(state.Messages) - 1; i >= 0; i-- {
		msg, ok := state.Messages[i].(*types.AssistantMessage)
		if !ok {
			if v, ok2 := state.Messages[i].(types.AssistantMessage); ok2 {
				msg = &v
			} else {
				continue
			}
		}
		fmt.Println()
		for _, block := range msg.Content {
			if tc, ok := block.(*types.TextContent); ok {
				fmt.Println(tc.Text)
			}
		}
		break
	}
}
