[English](README.md) | [中文](README_zh.md)

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
go get github.com/openmodu/modu
```

## 🗂 项目结构

```
modu/
├── pkg/                    # 核心工具库
│   ├── agent/              # 通用 Agent 引擎（事件驱动、工具）
│   ├── coding_agent/       # 高级编程 Agent（会话、技能、压缩）
│   ├── mailbox/            # Agent Teams 通信基础设施
│   ├── moms/               # Telegram 智能机器人（mom 的 Go/TG 移植版）
│   ├── channels/           # 消息通道接口（Telegram、飞书）
│   ├── providers/          # 多提供商 LLM 流式接口抽象
│   ├── skills/             # Agent 技能加载与注册表
│   ├── tui/                # 终端用户界面库
│   ├── types/              # 共享类型定义
│   ├── env/                # 环境变量加载器
│   ├── playwright/         # Playwright 浏览器自动化包装
│   ├── stream/             # 流处理工具
│   └── utils/              # 通用工具函数
├── repos/                  # 业务仓库层
│   ├── gen_image_repo/     # 图像生成（Gemini 等）
│   ├── notebooklm/         # Google NotebookLM 非官方 SDK
│   └── scraper/            # 网页爬虫
```

## 📚 核心模块

### pkg/mailbox — Agent Teams 通信基础设施

用于多 Agent 协作的完整通信层：消息传递、任务注册、状态跟踪和实时仪表盘。

📖 [详细文档](pkg/mailbox/README_zh.md)

---

### pkg/agent — Agent 引擎

通用的有状态 Agent 核心，支持工具调用和事件流。

📖 [详细文档](pkg/agent/README_zh.md)

---

### pkg/coding_agent — 编程 Agent

在 `pkg/agent` 基础上增加了会话管理、技能加载、上下文压缩。内置工具：bash、read、write、edit、grep、find、ls。

📖 [详细文档](pkg/coding_agent/README_zh.md)

---

### pkg/moms — Telegram 机器人

基于 `pkg/agent` 构建的 Telegram 智能机器人，是 pi-mono mom Slack 机器人的 Go/Telegram 移植版本。支持 bash 执行、文件操作、技能、定时事件和跨会话记忆。

📖 [详细文档](pkg/moms/README_zh.md) | 📦 [示例代码](examples/moms/main.go)

---

### pkg/channels — 消息通道

Telegram、飞书等消息平台的统一接口。

📖 [详细文档](pkg/channels/README_zh.md)

---

### pkg/providers — LLM Provider 层

统一的多提供商流式 LLM 接口。

📖 [详细文档](pkg/providers/README_zh.md)

---

### pkg/tui — 终端用户界面

终端用户界面渲染和输入处理库。

📖 [详细文档](pkg/tui/README_zh.md)

---

### pkg/env — 环境变量加载器

📖 [详细文档](pkg/env/README_zh.md)

---

### repos/ — 业务仓库

| 模块 | 描述 |
|------|------|
| [`repos/notebooklm`](repos/notebooklm/README_zh.md) | Google NotebookLM 非官方 SDK，支持 Notebooks、Sources、Chat 和音频生成 |
| [`repos/gen_image_repo`](repos/gen_image_repo/README_zh.md) | 图像生成抽象层，支持 Gemini 和其他提供商 |
| [`repos/scraper`](repos/scraper/README_zh.md) | 网页爬虫，支持 Hacker News 等 |

## 🔧 支持的 LLM Providers

通过 `providers.NewOpenAIChatCompletionsProvider` 或专用构造函数注册。

| Provider | 注册方法 |
|----------|----------|
| Anthropic (Claude) | `providers.NewOpenAIChatCompletionsProvider("anthropic", providers.WithBaseURL("https://api.anthropic.com"))` |
| OpenAI (GPT / o-series) | `providers.NewOpenAIChatCompletionsProvider("openai", providers.WithBaseURL("https://api.openai.com/v1"))` |
| DeepSeek | `providers.NewDeepSeekProvider(apiKey)` |
| Ollama（本地） | `providers.NewOpenAIChatCompletionsProvider("ollama", providers.WithBaseURL("http://localhost:11434/v1"))` |
| LM Studio（本地） | `providers.NewOpenAIChatCompletionsProvider("lmstudio", providers.WithBaseURL("http://localhost:1234/v1"))` |
| 任何兼容 OpenAI 的接口 | `providers.NewOpenAIChatCompletionsProvider(id, providers.WithBaseURL(url))` |

## 📄 许可证

MIT License
