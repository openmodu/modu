[English](README.md) | [中文](README_zh.md)

# Modu Agent Core (Go)

Stateful agent with tool execution and event streaming.

## Installation

```bash
go get github.com/openmodu/modu/pkg/agent
```

## Quick Start

```go
package main

import (
        "context"
        "fmt"
        "os"

        "github.com/openmodu/modu/pkg/agent"
        "github.com/openmodu/modu/pkg/providers"
        "github.com/openmodu/modu/pkg/types"
)

func main() {
        // Register a provider once at startup
        providers.Register(providers.NewOpenAIChatCompletionsProvider("anthropic",
                providers.WithBaseURL("https://api.anthropic.com"),
                providers.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
        ))

        model := &types.Model{
                ID:         "claude-sonnet-4-5",
                ProviderID: "anthropic",
        }

        // Initialize Agent
        a := agent.NewAgent(agent.AgentOptions{
                InitialState: &agent.AgentState{
                        SystemPrompt: "You are a helpful assistant.",
                        Model:        model,
                },
                GetAPIKey: func(provider string) (string, error) {
                        return os.Getenv("ANTHROPIC_API_KEY"), nil
                },
        })

        // Subscribe to events
        a.Subscribe(func(e agent.AgentEvent) {
                if e.Type == agent.EventTypeMessageUpdate && e.StreamEvent != nil {
                        if e.StreamEvent.Type == "text_delta" {
                                fmt.Print(e.StreamEvent.Delta)
                        }
                }
        })

        // Prompt
        ctx := context.Background()
        _ = a.Prompt(ctx, "Hello!")
        a.WaitForIdle()
}
```

## Core Concepts

### AgentMessage

`AgentMessage` is an alias of `types.AgentMessage` (an empty interface). The conversation history holds `types.UserMessage`, `types.AssistantMessage`, and `types.ToolResultMessage` values.

### Event Flow

The agent utilizes a synchronous event emission mechanism (via [Subscribe](./agent.go)). Understanding the event sequence is key for UI integration.

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
a := agent.NewAgent(agent.AgentOptions{
    InitialState: &agent.AgentState{
        SystemPrompt:  "...",
        Model:         myModel,
        ThinkingLevel: agent.ThinkingLevelOff, // "off", "minimal", "low", "medium", "high", "xhigh"
        Tools:         []agent.AgentTool{weatherTool},
    },

    // Steering mode: "one-at-a-time" (default) or "all"
    SteeringMode: agent.ExecutionModeOneAtATime,

    // Follow-up mode: "one-at-a-time" (default) or "all"
    FollowUpMode: agent.ExecutionModeOneAtATime,

    // Custom stream function (optional, defaults to providers.StreamDefault)
    StreamFn: myStreamFn,
})
```

## Agent State

```go
type AgentState struct {
    SystemPrompt     string
    Model            *types.Model
    ThinkingLevel    ThinkingLevel
    Tools            []AgentTool
    Messages         []AgentMessage
    IsStreaming      bool
    StreamMessage    AgentMessage
    PendingToolCalls map[string]struct{}
    Error            string
}
```

Access via `agent.GetState()` (thread-safe).

## Methods

### Prompting

```go
// String input
a.Prompt(ctx, "Hello")

// Send with images
a.PromptWithImages(ctx, "What's in this image?", []types.ImageContent{
    {Type: "image", Data: base64data, MimeType: "image/png"},
})
```

### Control & Queueing

The Agent gives you fine-grained control over how new messages are injected into an ongoing or paused session through two distinct prioritized queues.

**1. Steering Queue (High Priority / Interrupts)**
Use `agent.Steer(msg)` to inject high-priority messages. These take precedence over any queued follow-ups. When the Agent reads queues (e.g. at the start of `Prompt` or `Continue`), it empties the Steering Queue first.

```go
// Interrupts whatever was planned and prioritizes this context
a.Steer(types.UserMessage{Role: "user", Content: "Stop what you're doing! Answer this instead."})
```

**2. FollowUp Queue (Low Priority / Queueing)**
Use `agent.FollowUp(msg)` to queue messages to be processed after the current task completes and the Steering queue is empty.

```go
// Adds a task to the back of the line
a.FollowUp(types.UserMessage{Role: "user", Content: "After you finish that, summarize the session."})
```

#### Execution Modes (`ExecutionMode`)

You can control *how many* messages the Agent consumes from each queue at a time using `SetSteeringMode(...)` and `SetFollowUpMode(...)`:

- `agent.ExecutionModeOneAtATime` **(Default)**: The Agent consumes exactly **one** message from the queue per turn. 
  - *Business Use Case*: Executing a sequence of discrete tasks that depend on the previous task's completion, such as: "1. Create a file", then "2. Run tests to see if it works", then "3. Git commit the changes". By pulling one at a time, the model stays focused and guarantees chronological execution.
- `agent.ExecutionModeAll`: The Agent consumes **all** currently queued messages from the respective queue at once, cramming them all into the LLM context simultaneously. 
  - *Business Use Case*: Batching state updates, logs, or system notifications that the LLM needs as background context. For example, if a background watcher triggers 5 times with "File modified", you don't want the agent responding 5 separate times. Instead, it reads all 5 events at once and synthesizes a single response.

### Events

```go
unsubscribe := a.Subscribe(func(e agent.AgentEvent) {
    // Handle event
})
defer unsubscribe()
```

## Tools

Implement the `AgentTool` interface:

```go
type MyTool struct{}

func (t *MyTool) Name() string        { return "my_tool" }
func (t *MyTool) Label() string       { return "My Tool" }
func (t *MyTool) Description() string { return "Does something useful" }
func (t *MyTool) Parameters() any     { return nil }

func (t *MyTool) Execute(ctx context.Context, id string, args map[string]any, onUpdate agent.AgentToolUpdateCallback) (agent.AgentToolResult, error) {
    onUpdate(agent.AgentToolResult{
        Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Working..."}},
    })
    return agent.AgentToolResult{
        Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Result"}},
    }, nil
}
```

## License

MIT
