[English](README.md) | [中文](README_zh.md)

<p align="center">
  <img src="logo.png" alt="Modu Logo" width="200">
</p>

<h1 align="center">Modu</h1>

Modu is a Go toolkit for building agent applications. It provides an agent loop, LLM provider adapters, tool execution, multi-agent coordination, messaging channels, scheduling, and terminal UI components; applications still own their prompts, tools, persistence policy, and deployment.

## Requirements

- Go 1.26.2 or later, as declared in [`go.mod`](go.mod)
- An LLM endpoint such as Ollama, LM Studio, OpenAI, Anthropic, DeepSeek, Gemini, or another OpenAI-compatible API

## Install

```bash
go get github.com/openmodu/modu
```

## Minimal agent

The example below registers an Ollama endpoint, runs one prompt, and prints assistant text:

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

	if err := a.Prompt(context.Background(), "What is the capital of France?"); err != nil {
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

## Run examples

```bash
# Basic agent
go run ./examples/agent_demo

# Coding agent with file tools
go run ./examples/coding_agent

# Coordinator-led multi-agent work
go run ./examples/agent_teams
```

## Collaboration patterns

Modu supports two multi-agent patterns on the same mailbox and task model:

| Pattern | Use it when | Execution rule |
|---|---|---|
| Agent Teams | Work has named roles and one coordinator | The coordinator assigns tasks and combines results |
| Adversarial Validation | A queued result must pass an independent check | A worker submits; a validator accepts or requests a retry |

Run `go run ./examples/agent_teams` for the first pattern. See the [mailbox documentation](pkg/mailbox/README.md) for task queues and validation.

## Modules

| Module | Responsibility | Documentation |
|---|---|---|
| `pkg/agent` | Stateful agent facade and dependency-inverted loop | [README](pkg/agent/README.md) |
| `pkg/coding_agent` | Sessions, context compression, skills, and coding tools | [README](pkg/coding_agent/README.md) |
| `pkg/mailbox` | Agent registration, messaging, projects, tasks, and validation | [README](pkg/mailbox/README.md) |
| `pkg/channels` | Telegram and Feishu channel interfaces and bridges | [README](pkg/channels/README.md) |
| `pkg/providers` | Streaming and non-streaming LLM providers | [README](pkg/providers/README.md) |
| `pkg/runtime` | Checkpoint, resume, and rewind around `pkg/agent` | [README](pkg/runtime/README.md) |
| `pkg/cron` | Scheduled coding-agent runs inside interactive `modu_code` | [README](pkg/cron/README.md) |
| `pkg/modu-tui` | Reusable Bubble Tea v2 agent transcript UI | [README](pkg/modu-tui/README.md) |
| `pkg/env` | `.env` loading and environment access | [README](pkg/env/README.md) |
| `pkg/tokenkit` | Local coding-agent usage and Codex status data | [README](pkg/tokenkit/README.md) |
| `pkg/types` | Shared messages, tools, events, models, and loop contracts | [README](pkg/types/README.md) |

The [documentation index](docs/README.md) covers guides, architecture, references, plans, and articles. README files beside source code remain the shortest entry point for each package.

## LLM providers

Register each provider under an ID that matches `types.Model.ProviderID`:

| Endpoint | Constructor |
|---|---|
| OpenAI-compatible APIs, Ollama, LM Studio | `openai.New(id, openai.WithBaseURL(url), openai.WithAPIKey(key))` |
| Anthropic Messages API | `anthropic.New(apiKey, model)` |
| DeepSeek | `deepseek.New(apiKey)` |
| Gemini | `gemini.New(ctx, apiKey, id, model)` |

See [`pkg/providers`](pkg/providers/README.md) for request, registry, chat, and streaming examples.

## License

MIT
