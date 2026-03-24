# providers

Unified multi-provider streaming LLM interface.

## Overview

`pkg/providers` provides a standardized interface for interacting with various LLM providers (OpenAI, Anthropic, DeepSeek, Ollama, etc.). It handles the complexities of different API formats and provides a consistent streaming event interface.

## Core Interface: Provider

```go
type Provider interface {
	ID() string
	Stream(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (stream.Stream, error)
}
```

## Quick Start

### Register and Use

```go
import (
    "github.com/openmodu/modu/pkg/providers"
    "github.com/openmodu/modu/pkg/types"
)

// Register a provider
providers.Register(providers.NewOpenAIChatCompletionsProvider("openai",
    providers.WithBaseURL("https://api.openai.com/v1"),
    providers.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
))

// Use the default registry to stream
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

## Supported Providers

| Provider | Constructor | Description |
|----------|-------------|-------------|
| OpenAI | `NewOpenAIChatCompletionsProvider` | Supports any OpenAI-compatible API |
| Anthropic | `NewOpenAIChatCompletionsProvider` | Via compatibility layer |
| DeepSeek | `NewDeepSeekProvider` | Dedicated DeepSeek support |
| Ollama | `NewOpenAIChatCompletionsProvider` | Local models via Ollama |

## Built-in Providers

### OpenAI-compatible
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

## Registry

The global registry allows you to manage multiple providers and select them by ID:

```go
providers.Register(p)
provider, err := providers.Get("openai")
```

## Package Structure

```
pkg/providers/
├── provider.go         # Core interface
├── registry.go         # Global provider registry
├── provider_types.go   # Options and types
├── models.go           # Model definitions and helpers
├── openai/             # OpenAI-compatible implementation
└── deepseek/           # DeepSeek implementation
```
