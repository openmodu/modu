<p align="center">
  <img src="logo.png" alt="Modu Logo" width="200">
</p>

<h1 align="center">modu，中文名"毛肚"</h1>

<p align="center">
  <strong>🚀 快捷高效搭建 Agent 应用的 Go 基础设施工具库</strong>
</p>

---

## 📦 安装

```bash
go get github.com/crosszan/modu
```

## 🗂 项目结构

```
modu/
├── pkg/                    # 核心工具包
│   ├── agent/              # 通用 Agent 引擎（事件驱动、工具调用）
│   ├── coding_agent/       # 高级编程 Agent（技能、会话、压缩）
│   ├── mailbox/            # Agent Teams 通信基础设施
│   │   ├── hub.go          # 内存消息中心（AgentInfo + Task 注册表）
│   │   ├── event.go        # Hub 事件订阅机制
│   │   ├── message.go      # 结构化消息协议（task_assign / task_result）
│   │   ├── server/         # Redis 协议 Mailbox Server
│   │   ├── client/         # Mailbox 客户端 SDK
│   │   └── dashboard/      # HTTP 看板（REST API + SSE + 内嵌 HTML）
│   ├── moms/               # Telegram 智能机器人（mom 的 Go/TG 移植）
│   ├── providers/          # 多 Provider LLM 流式调用抽象
│   ├── types/              # 共享类型定义（Model、消息、内容块等）
│   ├── env/                # 环境变量加载（.env 支持）
│   ├── playwright/         # Playwright 浏览器自动化封装
│   └── utils/              # 通用工具函数
├── repos/                  # 业务仓库层
│   ├── gen_image_repo/     # 图片生成（Gemini 等）
│   ├── notebooklm/         # Google NotebookLM 非官方 SDK
│   └── scraper/            # 网页爬虫
├── vo/                     # 值对象
├── consts/                 # 常量定义
└── examples/               # 使用示例
    ├── agent_teams/        # Agent Teams 完整示例（orchestrator + 2 workers）
    ├── agent_mailbox/      # Mailbox 消息传递示例
    ├── coding_agent/       # CodingAgent 使用示例
    ├── moms/               # Telegram 机器人示例
    └── ...
```

## 📚 核心模块

### pkg/mailbox — Agent Teams 通信基础设施

多 agent 协作所需的完整通信层：消息传递、任务注册表、状态追踪、实时看板。

#### 架构

```
MailboxServer (Redis 协议, :6380)
     │
     Hub ── AgentInfo 表（角色/状态/当前任务）
     │    ── Task 注册表（pending→running→completed/failed）
     │    ── 事件订阅（agent.registered / task.created 等）
     │
Dashboard (HTTP, :8080) ── SSE 实时推送 ── 内嵌 HTML 看板
```

#### 快速开始

```go
import (
    "github.com/crosszan/modu/pkg/mailbox/server"
    "github.com/crosszan/modu/pkg/mailbox/client"
    "github.com/crosszan/modu/pkg/mailbox/dashboard"
)

// 启动 Mailbox Server
s := server.NewMailboxServer()
go s.ListenAndServe(":6380")

// 启动 Dashboard（可选）
dash := dashboard.NewDashboard(s.Hub())
go dash.Start(ctx, ":8080")  // http://localhost:8080

// Orchestrator
orch := client.NewMailboxClient("orchestrator", "localhost:6380")
orch.Register(ctx)
orch.SetRole(ctx, "orchestrator")

taskID, _ := orch.CreateTask(ctx, "analyze data")
orch.AssignTask(ctx, taskID, "worker-1")

msg, _ := mailbox.NewTaskAssignMessage("orchestrator", taskID, "analyze data")
orch.Send(ctx, "worker-1", msg)

// Worker
worker := client.NewMailboxClient("worker-1", "localhost:6380")
worker.Register(ctx)
raw, _ := worker.Recv(ctx)
parsed, _ := mailbox.ParseMessage(raw)
worker.StartTask(ctx, parsed.TaskID)
// ... do work ...
worker.CompleteTask(ctx, parsed.TaskID, "result")
```

#### SpawnAgentTool（coding_agent 集成）

`coding_agent.SpawnAgentTool` 将委派逻辑封装为 `AgentTool`，让 orchestrator agent 可以直接通过工具调用派遣任务：

```go
import (
    coding_agent "github.com/crosszan/modu/pkg/coding_agent"
    "github.com/crosszan/modu/pkg/mailbox/client"
)

mc := client.NewMailboxClient("orchestrator", "localhost:6380")
mc.Register(ctx)

tool := coding_agent.NewSpawnAgentTool(mc,
    coding_agent.WithPollInterval(500*time.Millisecond),
    coding_agent.WithSpawnTimeout(5*time.Minute),
)

// 加入 agent 工具列表后，agent 可以调用 spawn_agent 工具
// 参数：target_agent_id, task_description
```

#### 服务端命令参考

| 命令 | 说明 |
|------|------|
| `AGENT.REG <id>` | 注册 agent |
| `AGENT.PING <id>` | 心跳保活 |
| `AGENT.LIST` | 列出所有活跃 agent |
| `AGENT.SETROLE <id> <role>` | 设置角色 |
| `AGENT.SETSTATUS <id> <status> [task_id]` | 设置状态（idle/busy） |
| `AGENT.INFO <id>` | 查询 agent 元数据 (JSON) |
| `TASK.CREATE <creator> <desc>` | 创建任务，返回 task_id |
| `TASK.ASSIGN <task_id> <agent_id>` | 分配任务 |
| `TASK.START <task_id>` | 标记为运行中 |
| `TASK.DONE <task_id> <result>` | 标记为已完成 |
| `TASK.FAIL <task_id> <error>` | 标记为失败 |
| `TASK.LIST` | 列出所有任务 (JSON) |
| `TASK.GET <task_id>` | 查询任务详情 (JSON) |
| `MSG.SEND <target> <msg>` | 发送消息 |
| `MSG.RECV <id>` | 非阻塞接收 |
| `MSG.BCAST <msg>` | 广播 |

#### Dashboard API

| 接口 | 说明 |
|------|------|
| `GET /` | HTML 看板（agents grid + tasks list，SSE 实时刷新） |
| `GET /api/agents` | 所有 agent 信息 (JSON) |
| `GET /api/tasks` | 所有任务列表 (JSON) |
| `GET /api/tasks/:id` | 任务详情 (JSON) |
| `GET /events` | SSE 事件流（实时推送状态变更） |

运行完整示例：

```bash
go run ./examples/agent_teams
# Dashboard: http://localhost:8080
```

---

### pkg/agent — Agent 引擎

通用、有状态的 Agent 核心，支持工具调用和事件流。

```go
import (
    "github.com/crosszan/modu/pkg/agent"
    "github.com/crosszan/modu/pkg/providers"
    "github.com/crosszan/modu/pkg/types"
)

providers.Register(providers.NewOpenAIChatCompletionsProvider("anthropic",
    providers.WithBaseURL("https://api.anthropic.com"),
))

a := agent.NewAgent(agent.AgentOptions{
    InitialState: &agent.AgentState{
        SystemPrompt: "你是一个助手",
        Model: &types.Model{
            ID:         "claude-sonnet-4-5",
            ProviderID: "anthropic",
        },
        Tools: []agent.AgentTool{myTool},
    },
    GetAPIKey: func(provider string) (string, error) { return apiKey, nil },
})

a.Subscribe(func(e agent.AgentEvent) {
    if e.Type == agent.EventTypeMessageUpdate {
        // handle streaming delta via e.StreamEvent
    }
})

_ = a.Prompt(ctx, "帮我列出当前目录的文件")
a.WaitForIdle()
```

📖 [详细文档](pkg/agent/README.md)

---

### pkg/coding_agent — 编程 Agent

在 `pkg/agent` 之上，提供会话管理、技能加载、上下文压缩等高级功能。内置工具：`bash`、`read`、`write`、`edit`、`grep`、`find`、`ls`。

```go
import (
    coding_agent "github.com/crosszan/modu/pkg/coding_agent"
    "github.com/crosszan/modu/pkg/coding_agent/tools"
    "github.com/crosszan/modu/pkg/providers"
    "github.com/crosszan/modu/pkg/types"
)

providers.Register(providers.NewOpenAIChatCompletionsProvider("ollama",
    providers.WithBaseURL("http://localhost:11434/v1"),
))

model := &types.Model{ID: "qwen2.5-coder:7b", ProviderID: "ollama"}

session, _ := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
    Cwd:   "/path/to/project",
    Model: model,
    Tools: tools.AllTools("/path/to/project"),
    GetAPIKey: func(provider string) (string, error) { return "", nil },
})

_ = session.Prompt(ctx, "读取 go.mod 告诉我这个项目是做什么的")
session.WaitForIdle()
```

📖 [详细文档](pkg/coding_agent/README.md)

---

### pkg/moms — Telegram 智能机器人

基于 `pkg/agent` 的 Telegram 机器人，是 [pi-mono mom](https://github.com/mariozechner/pi-mono) Slack 机器人的 Go/Telegram 移植版。支持 bash 执行、文件操作、技能系统、定时事件和跨会话记忆。

```bash
# 快速运行
export MOMS_TG_TOKEN="<token>"
export ANTHROPIC_API_KEY="<key>"
go run github.com/crosszan/modu/examples/moms --sandbox=host /tmp/moms-data
```

📖 [详细文档](pkg/moms/README.md) | 📦 [示例代码](examples/moms/main.go)

---

### pkg/providers — LLM Provider 层

统一的多 Provider 流式 LLM 调用接口。用 `providers.Register` 注册 Provider，用 `providers.StreamDefault` 调用。

```go
import (
    "github.com/crosszan/modu/pkg/providers"
    "github.com/crosszan/modu/pkg/types"
)

// 注册 Provider（程序启动时调用一次）
providers.Register(providers.NewOpenAIChatCompletionsProvider("anthropic",
    providers.WithBaseURL("https://api.anthropic.com"),
    providers.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
))

// DeepSeek 示例
providers.Register(providers.NewDeepSeekProvider(os.Getenv("DEEPSEEK_API_KEY")))

// Ollama 本地模型
providers.Register(providers.NewOpenAIChatCompletionsProvider("ollama",
    providers.WithBaseURL("http://localhost:11434/v1"),
))

model := &types.Model{
    ID:         "claude-sonnet-4-5",
    ProviderID: "anthropic",
}

stream, _ := providers.StreamDefault(ctx, model, &types.LLMContext{
    SystemPrompt: "你是助手",
    Messages:     []types.AgentMessage{types.UserMessage{Role: "user", Content: "你好"}},
}, &types.SimpleStreamOptions{})

for ev := range stream.Events() {
    if ev.Type == "text_delta" {
        fmt.Print(ev.Delta)
    }
}
```

---

### pkg/env — 环境变量加载

```go
import "github.com/crosszan/modu/pkg/env"

env.Load()                              // 加载 .env
env.Load(env.WithFile(".env.local"))    // 加载指定文件
env.Load(env.WithOverride())            // 覆盖已有变量

apiKey := env.GetDefault("API_KEY", "default")
```

---

### repos/ — 业务仓库层

| 模块 | 描述 |
|------|------|
| [`repos/notebooklm`](repos/notebooklm/README.md) | Google NotebookLM 非官方 SDK，支持 Notebook/Source/Chat/音频生成 |
| [`repos/gen_image_repo`](repos/gen_image_repo/README.md) | 图片生成抽象层，支持 Gemini 等 Provider |
| `repos/scraper` | 网页爬虫，支持 Hacker News 等 |

## 🔧 已支持的 LLM Providers

通过 `providers.NewOpenAIChatCompletionsProvider` 或专用构造器注册：

| Provider | 注册方式 |
|----------|----------|
| Anthropic (Claude) | `providers.NewOpenAIChatCompletionsProvider("anthropic", providers.WithBaseURL("https://api.anthropic.com"))` |
| OpenAI (GPT / o-series) | `providers.NewOpenAIChatCompletionsProvider("openai", providers.WithBaseURL("https://api.openai.com/v1"))` |
| DeepSeek | `providers.NewDeepSeekProvider(apiKey)` |
| Ollama（本地） | `providers.NewOpenAIChatCompletionsProvider("ollama", providers.WithBaseURL("http://localhost:11434/v1"))` |
| LM Studio（本地） | `providers.NewOpenAIChatCompletionsProvider("lmstudio", providers.WithBaseURL("http://localhost:1234/v1"))` |
| 任意 OpenAI 兼容接口 | `providers.NewOpenAIChatCompletionsProvider(id, providers.WithBaseURL(url))` |

## 📄 License

MIT License
