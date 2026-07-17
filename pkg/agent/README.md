[English](README.md) | [中文](README_zh.md)

# Agent core

`pkg/agent` runs a ReAct-style agent loop and exposes a stateful `Agent` facade. It owns execution behavior, queues, events, interrupts, and resume handling; shared contracts such as messages, configuration, tools, and state remain in `pkg/types`, while concrete tools belong in host packages.

## Dependency boundary

| Component | Responsibility |
|---|---|
| `Loop` | Runs the control flow and depends only on `LLM` and `Tools` |
| `LLM` | Converts an `AgentContext` into one Assistant message |
| `Tools` | Executes tool calls and returns tool-result messages |
| `ToolManager` | Supplies and rebinds tool sets for host runtimes |
| `RuntimeHooks` | Injects queue polling, approval, and max-step resume behavior |
| `EventSink` | Receives execution events; `EventStream` is the default implementation |

Provider streaming, retries, schema validation, approvals, execution, and parallel tool batches stay behind these interfaces. `pkg/agent` defines the boundary; packages such as `pkg/coding_agent/tools` provide concrete implementations.

## Run the loop directly

Use `Loop` when the host owns state and wants explicit input and output:

```go
loop := agent.NewLoop(agent.DefaultLLM{}, agent.DefaultTools{})
events := agent.NewEventStream()

result, err := loop.Run(ctx, types.LoopInput{
	Prompts: []types.AgentMessage{userMessage},
	Context: types.AgentContext{Tools: []types.Tool{tool}},
	Config:  types.Config{Model: model, StreamFn: streamFn},
	Runtime: types.RuntimeHooks{},
	Events:  events,
})
```

## Use the stateful facade

Use `Agent` when the package should own state, subscriptions, Prompt helpers, steering and follow-up queues, and interrupt state:

```go
a := agent.NewAgent(types.Config{
	InitialState: &types.State{
		SystemPrompt:  "You are a helpful assistant.",
		Model:         model,
		ThinkingLevel: types.ThinkingLevelOff,
		Tools:         []types.Tool{weatherTool},
	},
	StreamFn: streamFn,
})

unsubscribe := a.Subscribe(func(event types.Event) {
	// Handle the event.
})
defer unsubscribe()

err := a.Prompt(ctx, "Hello")
```

## Default behavior

- Transient LLM errors use exponential-backoff retries.
- A nil `StreamFn` uses `StreamDefault`, which resolves the Provider through `model.ProviderID`.
- A nil `ConvertToLLM` removes roles that the Provider cannot consume.
- Tool arguments are checked against JSON Schema before execution.
- Tools implementing `ParallelTool` may run in parallel.
- `Agent` appends an Assistant error message when execution fails.
- `Steer`, `FollowUp`, `Continue`, queue inspection, and one-or-all queue consumption are available on `Agent`.
- Tool-approval and max-step interrupts resume through `Resume`.
- `Abort` cancels the active loop and records an aborted Assistant error message.

This package does not provide sessions, persistence, or a built-in tool catalog. Use `pkg/coding_agent` for coding sessions and `pkg/runtime` for checkpoint-based recovery.
