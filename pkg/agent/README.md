# Modu Agent Core

This package contains the modular agent kernel. The previous V1 implementation
has been replaced by the V2 dependency-inversion design at the stable
`github.com/openmodu/modu/pkg/agent` import path.

The split is intentionally small:

- `Loop`: owns the ReAct control flow.
- `LLM`: turns an `AgentContext` into one assistant message.
- `Tools`: executes tool calls and returns tool-result messages.
- `ToolManager`: supplies and rebinds tool sets for host runtimes.

`Loop` depends only on the `LLM` and `Tools` interfaces. Provider streaming,
retry, tool approval, tool execution, and parallel tool batches live behind
those interfaces instead of inside the loop.

Host applications build concrete tool sets through `ToolProvider` /
`ToolManager` from this package. The agent kernel defines the dependency
boundary; packages such as `pkg/coding_agent/tools` provide concrete managers.

Runtime-only behaviour is supplied through `RuntimeHooks`, not public `Config`.
This keeps queue polling, tool approval, and max-step resume handling out of the
configuration object callers persist or pass around.

Events are emitted through the small `EventSink` interface. `EventStream` is the
default sink, but tests and alternate runtimes can use a recorder without
draining channels.

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
    Runtime: agent.RuntimeHooks{},
    Events:  events,
})
```

The default implementations provide these behaviours:

- transient LLM errors retry with exponential backoff
- nil `StreamFn` uses `StreamDefault`, which looks up the provider by
  `model.ProviderID`
- nil `ConvertToLLM` filters messages to provider-compatible roles
- tool arguments are validated against JSON schema before execution
- tool calls can run in parallel when tools implement `ParallelTool`
- the stateful `Agent` appends an assistant error message when execution fails
- `Agent` supports `Steer`, `FollowUp`, `Continue`, queue inspection,
  and one-at-a-time/all queue consumption modes
- `Agent` supports tool-approval and max-step interrupts through `Resume`
- `Agent.Abort` cancels the active loop context and records an aborted assistant
  error message
