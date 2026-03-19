<p align="center">
  <img src="logo.png" alt="Modu Logo" width="200">
</p>

<h1 align="center">Modu（毛肚）</h1>

<p align="center">
  <strong>🚀 快捷高效搭建 Agent 应用的 Go 基础设施工具库</strong>
</p>

<p align="center">
  <em>模块化、事件驱动的多 Agent 协作框架</em>
</p>

---

## 📦 安装

```bash
go get github.com/crosszan/modu
```

## 🗂 项目结构

```
modu/
├── pkg/                    # Core toolkits
│   ├── agent/              # Generic Agent engine (event-driven, tools)
│   ├── coding_agent/       # Advanced programming Agent (sessions, skills, compression)
│   ├── mailbox/            # Agent Teams communication infrastructure
│   │   ├── hub.go          # In-memory message center (AgentInfo + Task registry)
│   │   ├── event.go        # Hub event subscription mechanism
│   │   ├── message.go      # Structured messaging protocol (task_assign / task_result)
│   │   ├── server/         # Redis-protocol Mailbox Server
│   │   ├── client/         # Mailbox Client SDK
│   │   └── dashboard/      # HTTP Dashboard (REST API + SSE + embedded HTML)
│   ├── moms/               # Telegram smart bot (Go/TG port of mom)
│   ├── providers/          # Multi-provider LLM streaming interface abstraction
│   ├── types/              # Shared type definitions (Model, messages, content blocks)
│   ├── env/                # Environment variable loader (.env support)
│   ├── playwright/         # Playwright browser automation wrapper
│   └── utils/              # General utility functions
├── repos/                  # Business repository layer
│   ├── gen_image_repo/     # Image generation (Gemini, etc.)
│   ├── notebooklm/         # Google NotebookLM unofficial SDK
│   └── scraper/            # Web scraper
├── vo/                     # Value objects
├── consts/                 # Constant definitions
└── examples/               # Usage examples
    ├── agent_teams/        # Agent Teams full example (orchestrator + 2 workers)
    ├── agent_mailbox/      # Mailbox messaging example
    ├── coding_agent/       # CodingAgent usage example
    ├── moms/               # Telegram bot example
    └── ...
```

## 📚 Core Modules

### pkg/mailbox — Agent Teams Communication Infrastructure

Complete communication layer for multi-agent collaboration: message passing, task registry, status tracking, and real-time dashboard.

#### Architecture

```
MailboxServer (Redis protocol, :6380)
     │
     Hub ── AgentInfo table (role/status/current task)
     │    ── Task registry (pending→running→completed/failed)
     │    ── Event subscriptions (agent.registered / task.created, etc.)
     │
Dashboard (HTTP, :8080) ── SSE real-time push ── embedded HTML dashboard
```

#### Quick Start

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
// Parameters: target_agent_id, task_description
```

#### Server Commands Reference

| 命令 | Description |
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

| 接口 | Description |
|------|------|
| `GET /` | HTML dashboard with agents grid and tasks list, SSE real-time refresh |
| `GET /api/agents` | 所有 agent 信息 (JSON) |
| `GET /api/tasks` | 所有任务列表 (JSON) |
| `GET /api/tasks/:id` | 任务详情 (JSON) |
| `GET /events` | SSE 事件流（real-time push状态变更） |

Run full example：

```bash
go run ./examples/agent_teams
# Dashboard: http://localhost:8080
```

---

### pkg/agent — Agent Engine

Generic, stateful Agent core with tool calling and event streaming.

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
        SystemPrompt: "You are an assistant",
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

_ = a.Prompt(ctx, "List files in current directory")
a.WaitForIdle()
```

📖 [详细文档](pkg/agent/README.md)

---

### pkg/coding_agent — Programming Agent

Builds on pkg/agent with session management, skill loading, context compression. Built-in tools: bash, read, write, edit, grep, find, ls.

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

_ = session.Prompt(ctx, "Read go.mod and tell me what this project does")
session.WaitForIdle()
```

📖 [详细文档](pkg/coding_agent/README.md)

---

### pkg/moms — Telegram Bot

Telegram bot based on pkg/agent, a Go/Telegram port of the pi-mono mom Slack bot. Supports bash execution, file operations, skills, scheduled events, and cross-session memory.

```bash
# Quick Run
export MOMS_TG_TOKEN="<token>"
export ANTHROPIC_API_KEY="<key>"
go run github.com/crosszan/modu/examples/moms --sandbox=host /tmp/moms-data
```

📖 [详细文档](pkg/moms/README.md) | 📦 [示例代码](examples/moms/main.go)

---

### pkg/providers — LLM Provider Layer

Unified multi-provider streaming LLM interface. Register providers with providers.Register, call with providers.StreamDefault.

```go
import (
    "github.com/crosszan/modu/pkg/providers"
    "github.com/crosszan/modu/pkg/types"
)

// Register provider (call once at startup)
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
    SystemPrompt: "You are an assistant",
    Messages: []types.AgentMessage{types.UserMessage{Role: "user", Content: "Hello"}},
}, &types.SimpleStreamOptions{})

for ev := range stream.Events() {
    if ev.Type == "text_delta" {
        fmt.Print(ev.Delta)
    }
}
```

---

### pkg/env — Environment Loader

```go
import "github.com/crosszan/modu/pkg/env"

env.Load()                              // 加载 .env
env.Load(env.WithFile(".env.local"))    // 加载指定文件
env.Load(env.WithOverride())            // 覆盖已有变量

apiKey := env.GetDefault("API_KEY", "default")
```

---

### repos/ — Business Repositories

| 模块 | 描述 |
|------|------|
| [`repos/notebooklm`](repos/notebooklm/README.md) | Google NotebookLM unofficial SDK supporting Notebooks, Sources, Chat, and Audio generation |
| [`repos/gen_image_repo`](repos/gen_image_repo/README.md) | Image generation abstraction layer supporting Gemini and other providers |
| `repos/scraper` | Web scraper supporting Hacker News and more |

## 🔧 Supported LLM Providers

Registered via providers.NewOpenAIChatCompletionsProvider or dedicated constructors.

| Provider | Register Method |
|----------|----------|
| Anthropic (Claude) | `providers.NewOpenAIChatCompletionsProvider("anthropic", providers.WithBaseURL("https://api.anthropic.com"))` |
| OpenAI (GPT / o-series) | `providers.NewOpenAIChatCompletionsProvider("openai", providers.WithBaseURL("https://api.openai.com/v1"))` |
| DeepSeek | `providers.NewDeepSeekProvider(apiKey)` |
| Ollama（本地） | `providers.NewOpenAIChatCompletionsProvider("ollama", providers.WithBaseURL("http://localhost:11434/v1"))` |
| LM Studio（本地） | `providers.NewOpenAIChatCompletionsProvider("lmstudio", providers.WithBaseURL("http://localhost:1234/v1"))` |
| Any OpenAI-compatible interface | `providers.NewOpenAIChatCompletionsProvider(id, providers.WithBaseURL(url))` |

## 📄 License

MIT License
