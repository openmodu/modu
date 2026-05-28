[English](README.md) | [中文](README_zh.md)

# Modu Agent Core

`pkg/agent` 是 modu 的模块化 Agent 内核。原 V1 实现已经移除，当前稳定导入路径
`github.com/openmodu/modu/pkg/agent` 直接使用 V2 的依赖倒置设计。

核心拆分保持很小：

- `Loop`：负责 ReAct 控制流。
- `LLM`：把 `AgentContext` 转换成一条 assistant 消息。
- `Tools`：执行 tool call，并返回 tool result 消息。

`Loop` 只依赖 `LLM` 和 `Tools` 接口。Provider streaming、重试、工具审批、
工具执行和并行工具批次都放在接口背后，而不是塞进 loop 本身。

运行期行为通过 `RuntimeHooks` 注入，例如 steering/follow-up 队列、工具审批和
max-step resume 处理。事件通过 `EventSink` 输出，默认实现是 `EventStream`。

`Agent` 是 `Loop` 之上的有状态门面，负责状态、订阅、prompt helper、队列和
interrupt/resume 状态。

## Loop 用法

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

## Agent 用法

```go
a := agent.NewAgent(agent.Config{
    InitialState: &agent.State{
        SystemPrompt:  "You are a helpful assistant.",
        Model:         model,
        ThinkingLevel: agent.ThinkingLevelOff,
        Tools:         []agent.Tool{weatherTool},
    },
    StreamFn: streamFn,
})

unsubscribe := a.Subscribe(func(e agent.Event) {
    // handle event
})
defer unsubscribe()

err := a.Prompt(ctx, "Hello")
```

