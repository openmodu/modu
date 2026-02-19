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
│   ├── llm/                # 多 Provider LLM 流式调用抽象
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
import "github.com/crosszan/modu/pkg/agent"

a := agent.NewAgent(agent.AgentOptions{
    InitialState: &agent.AgentState{
        SystemPrompt: "你是一个助手",
        Model:        myModel,
        Tools:        []agent.AgentTool{myTool},
    },
    GetAPIKey: func(provider string) (string, error) { return apiKey, nil },
})

a.Subscribe(func(e agent.AgentEvent) {
    if e.Type == agent.EventTypeMessageUpdate && e.AssistantMessageEvent != nil {
        fmt.Print(e.AssistantMessageEvent.Delta)
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
    _ "github.com/crosszan/modu/pkg/llm/providers/ollama"
)

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

### pkg/llm — LLM 抽象层

统一的多 Provider 流式 LLM 调用接口，支持 Anthropic、OpenAI、Google、Ollama、DeepSeek、Mistral 等。

```go
import (
    "github.com/crosszan/modu/pkg/llm"
    _ "github.com/crosszan/modu/pkg/llm/providers/anthropic"
)

model := &llm.Model{
    ID:       "claude-sonnet-4-5",
    Api:      llm.Api(llm.KnownApiAnthropicMessages),
    Provider: llm.Provider(llm.KnownProviderAnthropic),
}

stream, _ := llm.StreamSimple(model, &llm.Context{
    SystemPrompt: "你是助手",
    Messages:     []llm.Message{llm.UserMessage{Role: "user", Content: "你好"}},
}, &llm.SimpleStreamOptions{})

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

| Provider | Import Path |
|----------|-------------|
| Anthropic (Claude) | `pkg/llm/providers/anthropic` |
| OpenAI (GPT / o-series) | `pkg/llm/providers/openai` |
| Google (Gemini) | `pkg/llm/providers/google` |
| Ollama（本地） | `pkg/llm/providers/ollama` |
| DeepSeek | `pkg/llm/providers/deepseek` |
| Amazon Bedrock | `pkg/llm/providers/amazon_bedrock` |
| Azure OpenAI | `pkg/llm/providers/azure_openai_responses` |
| Google Vertex | `pkg/llm/providers/google_vertex` |
| Mistral（via openai_completions） | `pkg/llm/providers/openai_completions` |

用 `_` 导入注册所需 Provider：
```go
import _ "github.com/crosszan/modu/pkg/llm/providers/anthropic"
```

## 📄 License

MIT License
