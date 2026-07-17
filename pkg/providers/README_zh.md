[English](README.md) | [中文](README_zh.md)

# LLM Provider

`pkg/providers` 定义统一的 Chat、流式请求契约和进程级 Provider 注册表。各协议的实现位于子包；本包不会把所有厂商特性压缩成一组最小公共选项。

## Provider 契约

```go
type Provider interface {
	ID() string
	Stream(ctx context.Context, req *ChatRequest) (types.EventStream, error)
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}
```

`Chat` 返回一条完整响应。`Stream` 返回事件流，调用方必须持续消费，直到流关闭。

## 注册并调用 Provider

```go
import (
	"context"
	"fmt"
	"os"

	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/providers/openai"
)

p := openai.New(
	"openai",
	openai.WithBaseURL("https://api.openai.com/v1"),
	openai.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
)
providers.Register(p)

registered, ok := providers.Get("openai")
if !ok {
	return fmt.Errorf("provider not registered")
}

response, err := registered.Chat(context.Background(), &providers.ChatRequest{
	Model: "gpt-4o",
	Messages: []providers.Message{
		{Role: providers.RoleUser, Content: "Hello"},
	},
})
```

重复注册同一 ID 会替换原 Provider。`List` 返回全部已注册 Provider，不保证顺序。

## 实现

| 服务 | 构造函数 | 边界 |
|---|---|---|
| OpenAI 兼容 API | `openai.New(id, options...)` | 必须设置 `WithBaseURL`；支持 API Key、自定义 Header 和额外请求字段 |
| Anthropic Messages API | `anthropic.New(apiKey, model)` | 使用独立的 Anthropic 协议 |
| DeepSeek | `deepseek.New(apiKey)` | 使用 DeepSeek 的 OpenAI 兼容端点；Key 为空时读取 `DEEPSEEK_API_KEY` |
| Gemini | `gemini.New(ctx, apiKey, id, model)` | 使用 Google Gen AI SDK；Key 为空时返回错误 |

Ollama 和 LM Studio 使用 `openai.New`，并传入各自的本地 OpenAI 兼容地址。

## 流式响应

```go
events, err := registered.Stream(ctx, &providers.ChatRequest{
	Model: "gpt-4o",
	Messages: []providers.Message{
		{Role: providers.RoleUser, Content: "Hello"},
	},
})
if err != nil {
	return err
}

for event := range events.Events() {
	if event.Type == "text_delta" {
		fmt.Print(event.Delta)
	}
}
```

## 目录

```text
pkg/providers/
├── provider.go       # Provider 接口
├── provider_types.go # 请求、响应、消息、工具和用量
├── registry.go       # 进程级注册表
├── models.go         # 模型元数据注册表
├── sse.go            # 共用 SSE 扫描器
├── openai/           # OpenAI 兼容实现
├── anthropic/        # Anthropic Messages 实现
├── deepseek/         # DeepSeek 构造函数
└── gemini/           # Gemini 实现
```
