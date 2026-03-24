# providers

统一的多提供商流式 LLM 接口。

## 概述

`pkg/providers` 提供了一个标准化的接口，用于与各种 LLM 提供商（OpenAI、Anthropic、DeepSeek、Ollama 等）进行交互。它处理了不同 API 格式的复杂性，并提供了一致的流式事件接口。

## 核心接口: Provider

```go
type Provider interface {
	ID() string
	Stream(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (stream.Stream, error)
}
```

## 快速开始

### 注册并使用

```go
import (
    "github.com/openmodu/modu/pkg/providers"
    "github.com/openmodu/modu/pkg/types"
)

// 注册一个提供商
providers.Register(providers.NewOpenAIChatCompletionsProvider("openai",
    providers.WithBaseURL("https://api.openai.com/v1"),
    providers.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
))

// 使用默认注册表进行流式传输
model := &types.Model{ID: "gpt-4o", ProviderID: "openai"}
llmCtx := &types.LLMContext{
    Messages: []types.AgentMessage{types.UserMessage{Role: "user", Content: "Hello"}},
}

stream, err := providers.StreamDefault(ctx, model, llmCtx, nil)
if err != nil {
    log.Fatal(err)
}

for ev := range stream.Events() {
    if ev.Type == "text_delta" {
        fmt.Print(ev.Delta)
    }
}
```

## 支持的提供商

| 提供商 | 构造函数 | 说明 |
|----------|-------------|-------------|
| OpenAI | `NewOpenAIChatCompletionsProvider` | 支持任何兼容 OpenAI 的 API |
| Anthropic | `NewOpenAIChatCompletionsProvider` | 通过兼容层支持 |
| DeepSeek | `NewDeepSeekProvider` | 专门的 DeepSeek 支持 |
| Ollama | `NewOpenAIChatCompletionsProvider` | 通过 Ollama 支持本地模型 |

## 内置提供商

### 兼容 OpenAI 的提供商
```go
p := providers.NewOpenAIChatCompletionsProvider("id",
    providers.WithBaseURL("..."),
    providers.WithAPIKey("..."),
)
```

### DeepSeek
```go
p := providers.NewDeepSeekProvider(apiKey)
```

## 注册表

全局注册表允许您管理多个提供商并按 ID 进行选择：

```go
providers.Register(p)
provider, err := providers.Get("openai")
```

## 目录结构

```
pkg/providers/
├── provider.go         # 核心接口
├── registry.go         # 全局提供商注册表
├── provider_types.go   # 选项和类型
├── models.go           # 模型定义和辅助函数
├── openai/             # 兼容 OpenAI 的实现
└── deepseek/           # DeepSeek 实现
```
