[English](README.md) | [中文](README_zh.md)

<p align="center">
  <img src="logo.png" alt="Modu Logo" width="200">
</p>

<h1 align="center">Modu（毛肚）</h1>

Modu 是用于构建 Agent 应用的 Go 工具库，提供 Agent 循环、LLM Provider 适配、工具执行、多 Agent 协作、消息通道、定时调度和终端 UI 组件。应用仍需自行定义 Prompt、工具、持久化策略和部署方式。

## 环境要求

- [`go.mod`](go.mod) 声明的 Go 1.26.2 或更高版本
- LLM 服务，例如 Ollama、LM Studio、OpenAI、Anthropic、DeepSeek、Gemini 或其他 OpenAI 兼容 API

## 安装

```bash
go get github.com/openmodu/modu
```

## 最简 Agent

下面的示例注册一个 Ollama 服务，执行一次 Prompt，并输出 Assistant 文本：

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

func main() {
	providers.Register(openai.New(
		"ollama",
		openai.WithBaseURL("http://localhost:11434/v1"),
	))

	model := &types.Model{
		ID:         "llama3.2",
		Name:       "Llama 3.2",
		ProviderID: "ollama",
	}

	a := agent.NewAgent(types.Config{
		InitialState: &types.State{
			SystemPrompt: "You are a helpful assistant.",
			Model:        model,
		},
	})

	if err := a.Prompt(context.Background(), "法国的首都是哪里？"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	for _, message := range a.GetState().Messages {
		assistant, ok := message.(types.AssistantMessage)
		if !ok {
			continue
		}
		for _, content := range assistant.Content {
			if text, ok := content.(*types.TextContent); ok {
				fmt.Println(text.Text)
			}
		}
	}
}
```

## 运行示例

```bash
# 基础 Agent
go run ./examples/agent_demo

# 带文件工具的编程 Agent
go run ./examples/coding_agent

# 由协调者组织的多 Agent 协作
go run ./examples/agent_teams
```

## 协作模式

Modu 在同一套 mailbox 和 task 模型上提供两种多 Agent 模式：

| 模式 | 适用场景 | 执行规则 |
|---|---|---|
| Agent Teams | 工作包含明确角色，并由一个协调者负责 | 协调者分派任务并汇总结果 |
| Adversarial Validation | 队列任务的结果必须经过独立检查 | Worker 提交结果，Validator 决定通过或重试 |

运行 `go run ./examples/agent_teams` 可查看第一种模式；任务队列和验证机制见 [mailbox 文档](pkg/mailbox/README_zh.md)。

## 模块

| 模块 | 职责 | 文档 |
|---|---|---|
| `pkg/agent` | 有状态 Agent 门面和依赖倒置的执行循环 | [README](pkg/agent/README_zh.md) |
| `pkg/coding_agent` | 会话、上下文压缩、Skill 和编程工具 | [README](pkg/coding_agent/README_zh.md) |
| `pkg/mailbox` | Agent 注册、消息、项目、任务和结果验证 | [README](pkg/mailbox/README_zh.md) |
| `pkg/channels` | Telegram、飞书通道接口及 Bridge | [README](pkg/channels/README_zh.md) |
| `pkg/providers` | 流式和非流式 LLM Provider | [README](pkg/providers/README_zh.md) |
| `pkg/runtime` | `pkg/agent` 的检查点、恢复和回退 | [README](pkg/runtime/README.md) |
| `pkg/cron` | 在交互式 `modu_code` 中运行定时 Agent 任务 | [README](pkg/cron/README.md) |
| `pkg/modu-tui` | 可复用的 Bubble Tea v2 Agent 对话界面 | [README](pkg/modu-tui/README.md) |
| `pkg/env` | `.env` 文件加载和环境变量读取 | [README](pkg/env/README_zh.md) |
| `pkg/tokenkit` | 本地编程 Agent 用量和 Codex 状态数据 | [README](pkg/tokenkit/README.md) |
| `pkg/types` | 消息、工具、事件、模型和循环的公共契约 | [README](pkg/types/README.md) |

[文档索引](docs/README.md)收录使用指南、架构、参考、计划和文章。源码旁的 README 仍作为各包的最短入口。

## LLM Provider

注册 Provider 时，其 ID 必须与 `types.Model.ProviderID` 一致：

| 服务 | 构造函数 |
|---|---|
| OpenAI 兼容 API、Ollama、LM Studio | `openai.New(id, openai.WithBaseURL(url), openai.WithAPIKey(key))` |
| Anthropic Messages API | `anthropic.New(apiKey, model)` |
| DeepSeek | `deepseek.New(apiKey)` |
| Gemini | `gemini.New(ctx, apiKey, id, model)` |

请求结构、注册表、Chat 和流式调用示例见 [`pkg/providers`](pkg/providers/README_zh.md)。

## 许可证

MIT
