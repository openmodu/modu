# coding_agent

[English](README.md) | [中文](README_zh.md)

`coding_agent` turns the generic `pkg/agent` loop into a coding session with file and shell tools, persistent conversations, context compaction, extensions, and host-facing runtime state.

Use this package when you are building a coding-agent host. If you only need an LLM loop with custom tools, use `pkg/agent` directly; `coding_agent` also owns filesystem access, session files, configuration lookup, and extension lifecycle.

## Minimal session

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

	if err := session.Prompt(context.Background(), "Explain main.go"); err != nil {
		panic(err)
	}
	session.WaitForIdle()
}
```

The caller must register the model provider before creating the session. `Cwd` determines which files tools can resolve and which project-level configuration and resources are discovered. Tool calls can modify that working tree, so the host must apply an approval policy appropriate to its environment.

## Documentation

- [Detailed reference](../../docs/reference/coding-agent.md) — features, tools, configuration, runtime files, and request flow. This document is currently maintained in Chinese.
- [Architecture](../../docs/architecture/coding-agent.md) — layer boundaries, dependency rules, and known violations.
- [Subagent parity](../../docs/reference/subagent-parity.md) — implemented, partial, and deferred `pi-subagents` compatibility.
