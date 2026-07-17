[English](README.md) | [中文](README_zh.md)

# Agent 内核

`pkg/agent` 运行 ReAct 风格的 Agent 循环，并提供有状态的 `Agent` 门面。它负责执行行为、队列、事件、中断和恢复；消息、配置、工具、状态等公共契约位于 `pkg/types`，具体工具由宿主包提供。

## 依赖边界

| 组件 | 职责 |
|---|---|
| `Loop` | 运行控制流，只依赖 `LLM` 和 `Tools` |
| `LLM` | 把 `AgentContext` 转换成一条 Assistant 消息 |
| `Tools` | 执行 Tool Call，并返回 Tool Result 消息 |
| `ToolManager` | 为宿主 Runtime 提供和重绑定工具集合 |
| `RuntimeHooks` | 注入队列轮询、审批和达到最大步数后的恢复行为 |
| `EventSink` | 接收执行事件；`EventStream` 是默认实现 |

Provider 流式调用、重试、Schema 校验、审批、工具执行和并行批次都位于这些接口之后。`pkg/agent` 定义边界，`pkg/coding_agent/tools` 等包提供具体实现。

## 直接运行 Loop

宿主自行管理状态，并需要明确控制输入和输出时，直接使用 `Loop`：

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

## 使用有状态门面

需要由包管理状态、订阅、Prompt Helper、Steering/Follow-up 队列和中断状态时，使用 `Agent`：

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
	// 处理事件。
})
defer unsubscribe()

err := a.Prompt(ctx, "Hello")
```

## 默认行为

- LLM 短暂故障使用指数退避重试。
- `StreamFn` 为空时使用 `StreamDefault`，并按 `model.ProviderID` 查找 Provider。
- `ConvertToLLM` 为空时，过滤 Provider 无法处理的消息角色。
- 执行工具前，按 JSON Schema 校验参数。
- 实现 `ParallelTool` 的工具可以并行执行。
- 执行失败时，`Agent` 会追加一条 Assistant 错误消息。
- `Agent` 提供 `Steer`、`FollowUp`、`Continue`、队列查询和单条/全部消费模式。
- 工具审批和最大步数中断通过 `Resume` 恢复。
- `Abort` 取消当前循环，并记录一条已中止的 Assistant 错误消息。

本包不提供会话、持久化或内置工具目录。编程会话使用 `pkg/coding_agent`，基于检查点的恢复使用 `pkg/runtime`。
