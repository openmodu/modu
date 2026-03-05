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
    ├── coding_agent/       # CodingAgent 使用示例
    ├── moms/               # Telegram 机器人示例
    └── ...
```

## 📚 核心模块

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
