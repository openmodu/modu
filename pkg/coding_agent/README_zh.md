# coding_agent

[English](README.md) | [中文](README_zh.md)

`coding_agent` 把通用的 `pkg/agent` 循环组装成编码会话，负责文件与 Shell 工具、会话持久化、上下文压缩、扩展系统，以及供宿主读取的运行时状态。

需要开发编码 Agent 宿主时使用这个包；如果只需要带自定义工具的 LLM 循环，直接使用 `pkg/agent`。`coding_agent` 还会接管文件访问、会话文件、配置发现和扩展生命周期，这些行为不属于通用 Agent 内核。

## 最短示例

```go
package main

import (
	"context"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
	"github.com/openmodu/modu/pkg/types"
)

func main() {
	providers.Register(openai.New(
		"ollama",
		openai.WithBaseURL("http://localhost:11434/v1"),
	))

	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd: "/path/to/project",
		Model: &types.Model{
			ID:            "qwen3-coder-next",
			ProviderID:    "ollama",
			ContextWindow: 32768,
			MaxTokens:     4096,
		},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		panic(err)
	}

	if err := session.Prompt(context.Background(), "解释 main.go"); err != nil {
		panic(err)
	}
	session.WaitForIdle()
}
```

创建会话前必须注册模型 Provider。`Cwd` 决定工具解析文件的基准目录，也决定项目配置和资源的发现范围。工具可能修改该工作区，宿主必须根据运行环境配置审批策略。

## 文档

- [详细参考](../../docs/reference/coding-agent.md)：功能、工具、配置、运行时文件和请求流程。
- [架构说明](../../docs/architecture/coding-agent.md)：分层边界、依赖规则和已知违规。
- [Subagent 兼容进度](../../docs/reference/subagent-parity.md)：与 `pi-subagents` 对齐的已实现、部分实现和暂缓项。
