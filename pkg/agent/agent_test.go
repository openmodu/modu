package agent

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// --- Mock Implementations ---

// MockWeatherTool implements new Tool interface
type MockWeatherTool struct{}

func (t *MockWeatherTool) Name() string        { return "get_weather" }
func (t *MockWeatherTool) Description() string { return "Get current weather" }
func (t *MockWeatherTool) Execute(ctx context.Context, id string, args string, onUpdate func(partial interface{})) (string, error) {
	// Simulate work
	onUpdate("Checking satellite...")
	time.Sleep(100 * time.Millisecond)
	onUpdate("Querying sensor...")

	return "Sunny, 25C", nil
}

// MockModel implements new Model interface
type MockModel struct{}

func (m *MockModel) Stream(ctx context.Context, messages []Message, tools []Tool) (<-chan ModelStreamEvent, error) {
	ch := make(chan ModelStreamEvent)
	go func() {
		defer close(ch)
		lastMsg := messages[len(messages)-1]

		if lastMsg.Role == RoleUser {
			ch <- ModelStreamEvent{Type: "text_delta", Payload: "Thinking..."}
			time.Sleep(200 * time.Millisecond)
			ch <- ModelStreamEvent{Type: "tool_call", Payload: ToolCall{ID: "call_1", Name: "get_weather", Args: "{}"}}
		} else if lastMsg.Role == RoleTool {
			ch <- ModelStreamEvent{Type: "text_delta", Payload: "It is sunny today."}
		}
	}()
	return ch, nil
}

func TestAgent(t *testing.T) {
	agent := NewAgent(AgentOptions{
		InitialState: AgentState{
			Model:        &MockModel{},
			Tools:        []Tool{&MockWeatherTool{}},
			SystemPrompt: "You are a helpful assistant.",
		},
	})

	// Subscribe to events to visualize specific lifecycle hooks
	agent.Subscribe(func(e AgentEvent) {
		switch e.Type {
		case EventTypeAgentStart:
			fmt.Println("🟢 [Event] Agent Start")
		case EventTypeTurnStart:
			fmt.Println("🔄 [Event] Turn Start")
		case EventTypeMessageStart:
			fmt.Printf("📩 [Event] Message Start (%s)\n", e.Message.Role)
		case EventTypeToolExecutionStart:
			fmt.Printf("🛠️ [Event] Tool Start: %s\n", e.ToolName)
		case EventTypeToolExecutionUpdate:
			fmt.Printf(".. [Event] Tool Update: %v\n", e.Partial)
		case EventTypeAgentEnd:
			fmt.Println("🏁 [Event] Agent End")
		}
	})

	ctx := context.Background()
	fmt.Println("User: Check weather")
	agent.Prompt(ctx, "Check weather")
}
