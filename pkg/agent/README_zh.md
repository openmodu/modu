[English](README.md) | [中文](README_zh.md)

# Modu Agent Core (Go)

具有工具执行和事件流支持的有状态 Agent。

## 安装

```bash
go get github.com/openmodu/modu/pkg/agent
```

## 快速开始

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
        // 在启动时注册一次 provider
        providers.Register(providers.NewOpenAIChatCompletionsProvider("anthropic",
                providers.WithBaseURL("https://api.anthropic.com"),
                providers.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
        ))

        model := &types.Model{
                ID:         "claude-sonnet-4-5",
                ProviderID: "anthropic",
        }

        // 初始化 Agent
        a := agent.NewAgent(agent.AgentOptions{
                InitialState: &agent.AgentState{
                        SystemPrompt: "You are a helpful assistant.",
                        Model:        model,
                },
                GetAPIKey: func(provider string) (string, error) {
                        return os.Getenv("ANTHROPIC_API_KEY"), nil
                },
        })

        // 订阅事件
        a.Subscribe(func(e agent.AgentEvent) {
                if e.Type == agent.EventTypeMessageUpdate && e.StreamEvent != nil {
                        if e.StreamEvent.Type == "text_delta" {
                                fmt.Print(e.StreamEvent.Delta)
                        }
                }
        })

        // 发送 Prompt
        ctx := context.Background()
        _ = a.Prompt(ctx, "Hello!")
        a.WaitForIdle()
}
```

## 核心概念

### AgentMessage

`AgentMessage` 是 `types.AgentMessage`（一个空接口）的别名。对话历史中保存着 `types.UserMessage`、`types.AssistantMessage` 和 `types.ToolResultMessage` 等值。

### 事件流

Agent 采用同步的事件派发机制（通过 [Subscribe](./agent.go)）。理解事件的发生顺序对 UI 集成至关重要。

#### prompt() 事件顺序

当调用 `agent.Prompt(ctx, "Hello")` 时：

```
Prompt("Hello")
├─ EventTypeAgentStart
├─ EventTypeMessageStart (用户消息)
├─ EventTypeMessageEnd   (用户消息)
│
├─ EventTypeTurnStart
├─ EventTypeMessageStart (助手消息占位符)
├─ EventTypeMessageUpdate (流式片段...)
├─ EventTypeMessageEnd   (助手消息完成)
├─ EventTypeTurnEnd
└─ EventTypeAgentEnd
```

#### 包含工具调用

```
Prompt("Check Weather")
├─ ...用户消息事件...
├─ EventTypeTurnStart
├─ EventTypeMessageUpdate (思考/流式输出)
├─ EventTypeToolExecutionStart (工具调用)
├─ EventTypeToolExecutionUpdate (工具进度)
├─ EventTypeToolExecutionEnd   (工具结果)
├─ EventTypeMessageStart (工具结果消息)
├─ EventTypeMessageEnd   (工具结果消息)
├─ EventTypeTurnEnd
│
├─ (下一轮: LLM 对工具结果作出反应)
├─ EventTypeTurnStart
├─ ...助手回复...
└─ EventTypeAgentEnd
```

### 事件类型

| 事件类型 | Go 常量 | 描述 |
|------------|-------------|-------------|
| `agent_start` | `EventTypeAgentStart` | Agent 开始处理 |
| `agent_end` | `EventTypeAgentEnd` | Agent 完成执行 |
| `turn_start` | `EventTypeTurnStart` | 新的推理轮次开始 |
| `turn_end` | `EventTypeTurnEnd` | 轮次完成 |
| `message_start` | `EventTypeMessageStart` | 消息加入历史记录 |
| `message_update` | `EventTypeMessageUpdate` | **仅限 Assistant**。部分的流式增量。 |
| `message_end` | `EventTypeMessageEnd` | 消息完全接收/处理完毕 |
| `tool_execution_start` | `EventTypeToolExecutionStart` | 工具执行开始 |
| `tool_execution_update` | `EventTypeToolExecutionUpdate` | 工具报告进度 |
| `tool_execution_end` | `EventTypeToolExecutionEnd` | 工具执行完成 |

## Agent 选项

```go
a := agent.NewAgent(agent.AgentOptions{
    InitialState: &agent.AgentState{
        SystemPrompt:  "...",
        Model:         myModel,
        ThinkingLevel: agent.ThinkingLevelOff, // "off", "minimal", "low", "medium", "high", "xhigh"
        Tools:         []agent.AgentTool{weatherTool},
    },

    // 转向模式: "one-at-a-time" (默认) 或 "all"
    SteeringMode: agent.ExecutionModeOneAtATime,

    // 后续模式: "one-at-a-time" (默认) 或 "all"
    FollowUpMode: agent.ExecutionModeOneAtATime,

    // 自定义流函数 (可选, 默认为 providers.StreamDefault)
    StreamFn: myStreamFn,
})
```

## Agent 状态

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

通过 `agent.GetState()` 访问（线程安全）。

## 方法

### 提示词

```go
// 字符串输入
a.Prompt(ctx, "Hello")

// 带有图片的输入
a.PromptWithImages(ctx, "What's in this image?", []types.ImageContent{
    {Type: "image", Data: base64data, MimeType: "image/png"},
})
```

### 控制与队列

Agent 通过两个不同优先级的队列，为您提供了对如何将新消息注入正在进行或已暂停会话的细粒度控制。

**1. 转向队列 Steering Queue（高优先级 / 中断）**
使用 `agent.Steer(msg)` 注入高优先级消息。这些消息优先于任何排队的后续消息。当 Agent 读取队列时（例如在 `Prompt` 或 `Continue` 开始时），它会先清空转向队列。

```go
// 中断原本的计划，优先处理该上下文
a.Steer(types.UserMessage{Role: "user", Content: "Stop what you're doing! Answer this instead."})
```

**2. 后续队列 FollowUp Queue（低优先级 / 排队）**
使用 `agent.FollowUp(msg)` 将消息排队，以便在当前任务完成且转向队列为空后处理。

```go
// 在队尾添加一个任务
a.FollowUp(types.UserMessage{Role: "user", Content: "After you finish that, summarize the session."})
```

#### 执行模式 (`ExecutionMode`)

您可以通过使用 `SetSteeringMode(...)` 和 `SetFollowUpMode(...)` 控制 Agent 每次从队列中消费的消息数量：

- `agent.ExecutionModeOneAtATime` **(默认)**：Agent 每轮从队列中准确消费**一条**消息。
  - *业务场景*：执行一系列依赖于前一任务完成的独立任务，如：“1. 创建文件”，然后“2. 运行测试看是否正常”，最后“3. Git 提交修改”。通过一次拉取一条，模型能保持专注并保证按时间顺序执行。
- `agent.ExecutionModeAll`：Agent 一次性消费来自相应队列的**所有**排队消息，将它们全部同时塞入 LLM 的上下文中。
  - *业务场景*：批量处理 LLM 作为背景上下文需要的状态更新、日志或系统通知。例如，如果后台监视器触发了 5 次“文件已修改”，您不想让 Agent 分开回复 5 次。相反，它会一次性阅读所有 5 个事件并合成单一的响应。

### 事件

```go
unsubscribe := a.Subscribe(func(e agent.AgentEvent) {
    // 处理事件
})
defer unsubscribe()
```

## 工具

实现 `AgentTool` 接口：

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

## 许可证

MIT
