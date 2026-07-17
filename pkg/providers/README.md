[English](README.md) | [中文](README_zh.md)

# LLM providers

`pkg/providers` defines common Chat and streaming request contracts, plus a process-wide Provider registry. Protocol implementations live in subpackages; this package does not normalize every vendor feature into one lowest-common-denominator option set.

## Provider contract

```go
type Provider interface {
	ID() string
	Stream(ctx context.Context, req *ChatRequest) (types.EventStream, error)
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}
```

`Chat` returns one completed response. `Stream` returns an event stream that the caller must consume until it closes.

## Register and call a Provider

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

Registering the same ID again replaces the previous Provider. `List` returns all registered Providers in unspecified order.

## Implementations

| Endpoint | Constructor | Boundary |
|---|---|---|
| OpenAI-compatible APIs | `openai.New(id, options...)` | Requires `WithBaseURL`; supports API key, headers, and extra request fields |
| Anthropic Messages API | `anthropic.New(apiKey, model)` | Uses the dedicated Anthropic protocol |
| DeepSeek | `deepseek.New(apiKey)` | Uses DeepSeek's OpenAI-compatible endpoint; empty key falls back to `DEEPSEEK_API_KEY` |
| Gemini | `gemini.New(ctx, apiKey, id, model)` | Uses the Google Gen AI SDK and returns an error when the key is empty |

Ollama and LM Studio use `openai.New` with their local OpenAI-compatible base URL.

## Stream a response

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

## Layout

```text
pkg/providers/
├── provider.go       # Provider interface
├── provider_types.go # Requests, responses, messages, tools, and usage
├── registry.go       # Process-wide registry
├── models.go         # Model metadata registry
├── sse.go            # Shared SSE scanner
├── openai/           # OpenAI-compatible implementation
├── anthropic/        # Anthropic Messages implementation
├── deepseek/         # DeepSeek constructor
└── gemini/           # Gemini implementation
```
