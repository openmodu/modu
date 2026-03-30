[English](README.md) | [中文](README_zh.md)

<p align="center">
  <img src="logo.png" alt="Modu Logo" width="200">
</p>

<h1 align="center">Modu</h1>

<p align="center">
  <strong>🚀 A Fast and Efficient Go Infrastructure Toolkit for Building Agent Applications</strong>
</p>

<p align="center">
  <em>Modular, Event-Driven Multi-Agent Collaboration Framework</em>
</p>

---

## 📦 Installation

```bash
go get github.com/openmodu/modu
```

## 🗂 Project Structure

```
modu/
├── pkg/                    # Core toolkits
│   ├── agent/              # Generic Agent engine (event-driven, tools)
│   ├── coding_agent/       # Advanced programming Agent (sessions, skills, compression)
│   ├── mailbox/            # Agent Teams communication infrastructure
│   ├── swarm/              # Decentralised task queue with auto-scaling workers
│   ├── moms/               # Telegram smart bot (Go/TG port of mom)
│   ├── channels/           # Messaging channel interfaces (Telegram, Feishu)
│   ├── providers/          # Multi-provider LLM streaming interface abstraction
│   ├── skills/             # Agent skills loading and registry
│   ├── tui/                # Terminal user interface library
│   ├── types/              # Shared type definitions
│   ├── env/                # Environment variable loader
│   ├── playwright/         # Playwright browser automation wrapper
│   ├── stream/             # Streaming utilities
│   └── utils/              # General utility functions
├── repos/                  # Business repository layer
│   ├── gen_image_repo/     # Image generation (Gemini, etc.)
│   ├── notebooklm/         # Google NotebookLM unofficial SDK
│   └── scraper/            # Web scraper
```

## 📚 Core Modules

## 🤝 Collaboration Patterns

Modu currently supports three distinct multi-agent execution patterns on top of the same mailbox/task model:

| Pattern | Best for | Core idea |
|------|------|------|
| Agent Teams | Role-based collaboration with a clear coordinator | One orchestrator assigns work to named agents and aggregates results |
| Agent Swarm | Elastic worker pools and queue-driven execution | Tasks are published to a shared queue and matching agents claim them competitively |
| Adversarial Validation | Quality control for swarm-style execution | A worker submits a result, then a separate validator agent scores it and can trigger retries |

Related examples:

- `go run ./examples/agent_teams`
- `go run ./examples/swarm_demo/`

### pkg/mailbox — Agent Teams Communication Infrastructure

Complete communication layer for multi-agent collaboration: agent registration, point-to-point messaging, task/project lifecycle, swarm queue operations, adversarial validation, and real-time dashboard.

📖 [Detailed Documentation](pkg/mailbox/README.md)

---

### pkg/swarm — Auto-scaling Agent Swarm

Decentralised task execution built on `pkg/mailbox`: no fixed orchestrator, capability-based claiming, and automatic worker scaling based on queue pressure.

📖 [Detailed Documentation](pkg/swarm/README.md)

---

### pkg/agent — Agent Engine

Generic, stateful Agent core with tool calling and event streaming.

📖 [Detailed Documentation](pkg/agent/README.md)

---

### pkg/coding_agent — Programming Agent

Builds on `pkg/agent` with session management, skill loading, context compression. Built-in tools: bash, read, write, edit, grep, find, ls.

📖 [Detailed Documentation](pkg/coding_agent/README.md)

---

### pkg/moms — Telegram Bot

Telegram bot based on `pkg/agent`, a Go/Telegram port of the pi-mono mom Slack bot. Supports bash execution, file operations, skills, scheduled events, and cross-session memory.

📖 [Detailed Documentation](pkg/moms/README.md) | 📦 [Example Code](examples/moms/main.go)

---

### pkg/channels — Messaging Channels

Unified interface for messaging platforms like Telegram and Feishu.

📖 [Detailed Documentation](pkg/channels/README.md)

---

### pkg/providers — LLM Provider Layer

Unified multi-provider streaming LLM interface.

📖 [Detailed Documentation](pkg/providers/README.md)

---

### pkg/tui — Terminal User Interface

Terminal user interface rendering and input handling library.

📖 [Detailed Documentation](pkg/tui/README.md)

---

### pkg/env — Environment Loader

📖 [Detailed Documentation](pkg/env/README.md)

---

### repos/ — Business Repositories

| Module | Description |
|------|------|
| [`repos/notebooklm`](repos/notebooklm/README.md) | Google NotebookLM unofficial SDK supporting Notebooks, Sources, Chat, and Audio generation |
| [`repos/gen_image_repo`](repos/gen_image_repo/README.md) | Image generation abstraction layer supporting Gemini and other providers |
| [`repos/scraper`](repos/scraper/README.md) | Web scraper supporting Hacker News and more |

## 🔧 Supported LLM Providers

Registered via `providers.NewOpenAIChatCompletionsProvider` or dedicated constructors.

| Provider | Register Method |
|----------|----------|
| Anthropic (Claude) | `providers.NewOpenAIChatCompletionsProvider("anthropic", providers.WithBaseURL("https://api.anthropic.com"))` |
| OpenAI (GPT / o-series) | `providers.NewOpenAIChatCompletionsProvider("openai", providers.WithBaseURL("https://api.openai.com/v1"))` |
| DeepSeek | `providers.NewDeepSeekProvider(apiKey)` |
| Ollama (Local) | `providers.NewOpenAIChatCompletionsProvider("ollama", providers.WithBaseURL("http://localhost:11434/v1"))` |
| LM Studio (Local) | `providers.NewOpenAIChatCompletionsProvider("lmstudio", providers.WithBaseURL("http://localhost:1234/v1"))` |
| Any OpenAI-compatible interface | `providers.NewOpenAIChatCompletionsProvider(id, providers.WithBaseURL(url))` |

## 📄 License

MIT License
