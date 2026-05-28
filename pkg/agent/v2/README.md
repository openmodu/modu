# Modu Agent v2 Prototype

This package is a dependency-inversion prototype for the agent kernel.

The split is intentionally small:

- `Loop`: owns the ReAct control flow.
- `LLM`: turns an `AgentContext` into one assistant message.
- `Tools`: executes tool calls and returns tool-result messages.

`Loop` depends only on the `LLM` and `Tools` interfaces. Provider streaming,
retry, tool approval, tool execution, and parallel tool batches live behind
those interfaces instead of inside the loop.

`Agent` is a thin stateful facade over `Loop`. It owns state, subscriptions,
prompt helpers, steering/follow-up queues, and interrupt resume state, while
the execution path still goes through the inverted `Loop -> LLM/Tools`
dependencies.

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

The default implementations currently preserve these V1 behaviours:

- transient LLM errors retry with exponential backoff
- nil `ConvertToLLM` filters messages to provider-compatible roles
- tool arguments are validated against JSON schema before execution
- tool calls can run in parallel when tools implement `ParallelTool`
- the stateful `Agent` appends an assistant error message when execution fails
- `Agent` supports V1-style `Steer`, `FollowUp`, `Continue`, queue inspection,
  and one-at-a-time/all queue consumption modes
- `Agent` supports tool-approval and max-step interrupts through `Resume`
- `Agent.Abort` cancels the active loop context and records an aborted assistant
  error message

The old `pkg/agent` package remains unchanged while this v2 API settles.
