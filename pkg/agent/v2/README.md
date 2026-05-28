# Modu Agent v2 Prototype

This package is a dependency-inversion prototype for the agent kernel.

The split is intentionally small:

- `Loop`: owns the ReAct control flow.
- `LLM`: turns an `AgentContext` into one assistant message.
- `Tools`: executes tool calls and returns tool-result messages.

`Loop` depends only on the `LLM` and `Tools` interfaces. Provider streaming,
retry, tool approval, tool execution, and parallel tool batches live behind
those interfaces instead of inside the loop.

```go
loop := agent.NewLoop(agent.DefaultLLM{}, agent.DefaultTools{})
events := agent.NewEventStream()

result, err := loop.Run(ctx, agent.LoopInput{
    Prompts: []agent.AgentMessage{userMessage},
    Context: agent.AgentContext{Tools: []agent.Tool{tool}},
    Config:  agent.Config{Model: model, StreamFn: streamFn},
    Events:  events,
})
```

The old `pkg/agent` package remains unchanged while this v2 API settles.

