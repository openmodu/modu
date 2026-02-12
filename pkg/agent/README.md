# Modu Agent Core (Go)

Stateful agent with tool execution and event streaming.

## Installation

```bash
# Assuming this is part of your module
go get github.com/crosszan/modu/pkg/agent
```

## Quick Start

```go
package main

import (
        "context"
        "fmt"
        "github.com/crosszan/modu/pkg/agent" // pseudo-path
)

func main() {
        // Initialize Agent
        agent := NewAgent(AgentOptions{
                InitialState: AgentState{
                        SystemPrompt: "You are a helpful assistant.",
                        Model:        &MyModel{}, // Implement Model interface
                },
        })

        // Subscribe to events
        agent.Subscribe(func(e AgentEvent) {
                if e.Type == EventTypeMessageUpdate {
                        // Stream partial text
                        fmt.Print(e.Partial)
                }
        })

        // Prompt
        ctx := context.Background()
        agent.Prompt(ctx, "Hello!")
}
```

## Core Concepts

### AgentMessage vs LLM Message

The agent works with a unified [Message](./agent_types.go#L60-L68) struct that supports:
- Standard roles: `user`, `assistant`, `tool`, [system](./agent_types.go#L60-L68)
- Custom extension roles: `custom` via `CustomType` field

The [Model](./agent_types.go#L94-L97) interface implementation is responsible for converting these generic messages into the specific format required by the LLM provider (OpenAI, Anthropic, etc.).

### Event Flow

The agent utilizes a synchronous event emission mechanism (via [Subscribe](./agent.go#L61-L80)). Understanding the event sequence is key for UI integration.

#### prompt() Event Sequence

When calling `agent.Prompt(ctx, "Hello")`:

```
Prompt("Hello")
├─ EventTypeAgentStart
├─ EventTypeMessageStart (User Message)
├─ EventTypeMessageEnd   (User Message)
│
├─ EventTypeTurnStart
├─ EventTypeMessageStart (Assistant Message placeholder)
├─ EventTypeMessageUpdate (Streaming chunks...)
├─ EventTypeMessageEnd   (Assistant Message complete)
├─ EventTypeTurnEnd
└─ EventTypeAgentEnd
```

#### With Tool Calls

```
Prompt("Check Weather")
├─ ...User Message Events...
├─ EventTypeTurnStart
├─ EventTypeMessageUpdate (Thinking/Streaming)
├─ EventTypeToolExecutionStart (Tool Call)
├─ EventTypeToolExecutionUpdate (Tool Progress)
├─ EventTypeToolExecutionEnd   (Tool Result)
├─ EventTypeMessageStart (Tool Result Message)
├─ EventTypeMessageEnd   (Tool Result Message)
├─ EventTypeTurnEnd
│
├─ (Next Turn: LLM reacts to tool result)
├─ EventTypeTurnStart
├─ ...Assistant Response...
└─ EventTypeAgentEnd
```

### Event Types

| Event Type | Go Constant | Description |
|------------|-------------|-------------|
| `agent_start` | `EventTypeAgentStart` | Agent begins processing |
| `agent_end` | `EventTypeAgentEnd` | Agent completes execution |
| `turn_start` | `EventTypeTurnStart` | New reasoning turn begins |
| `turn_end` | `EventTypeTurnEnd` | Turn completes |
| `message_start` | `EventTypeMessageStart` | Message added to history |
| `message_update` | `EventTypeMessageUpdate` | **Assistant only**. Partial streaming delta. |
| `message_end` | `EventTypeMessageEnd` | Message fully received/processed |
| `tool_execution_start` | `EventTypeToolExecutionStart` | Tool execution begins |
| `tool_execution_update` | `EventTypeToolExecutionUpdate` | Tool reports progress |
| `tool_execution_end` | `EventTypeToolExecutionEnd` | Tool execution completes |

## Agent Options

```go
agent := NewAgent(AgentOptions{
    InitialState: AgentState{
        SystemPrompt: "...",
        Model:        myModel,
        ThinkingLevel: ThinkingLevelOff, // "off", "low", "high"
        Tools:        []Tool{weatherTool},
    },

    // Steering mode: "one-at-a-time" (default) or "all"
    SteeringMode: ExecutionModeOneAtATime,

    // Follow-up mode: "one-at-a-time" (default) or "all"
    FollowUpMode: ExecutionModeOneAtATime,
})
```

## Agent State

```go
type AgentState struct {
    SystemPrompt  string
    Model         Model
    ThinkingLevel ThinkingLevel
    Tools         []Tool
    Messages      []Message
    IsStreaming   bool
    Error         error
}
```

Access via `agent.GetState()` (thread-safe).

## Methods

### Prompting

```go
// String input
agent.Prompt(ctx, "Hello")

// Message struct input
agent.Prompt(ctx, Message{Role: RoleUser, Content: ...})
```

### Control & Queueing

**Steering** (Interrupts):
Use [Steer](./agent.go#L105-L110) to inject high-priority messages while the agent is running (e.g., stopping a tool).

```go
agent.Steer(Message{
    Role: RoleUser,
    Content: []ContentBlock{{Type: ContentTypeText, Text: "Stop!"}},
})
```

**Follow-Up** (Queueing):
Use [FollowUp](./agent.go#L111-L116) to queue messages to be processed after the current task completes.

```go
agent.FollowUp(Message{
    Role: RoleUser,
    Content: []ContentBlock{{Type: ContentTypeText, Text: "Summarize session"}},
})
```

### Events

```go
unsubscribe := agent.Subscribe(func(e AgentEvent) {
    // Handle event
})
defer unsubscribe() // Calls the returned function to unregister
```

## Tools

Implement the [Tool](./agent_types.go#L86-L92) interface:

```go
type MyTool struct {}

func (t *MyTool) Name() string { return "my_tool" }
func (t *MyTool) Description() string { return "Does something" }

func (t *MyTool) Execute(ctx context.Context, id string, args string, onUpdate func(interface{})) (string, error) {
    onUpdate("Working...")
    // ... logic ...
    return "Result", nil
}
```

## License

MIT
