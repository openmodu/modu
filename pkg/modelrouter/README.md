# Model router

`pkg/modelrouter` is a Go library for routing locally installed coding agents through one model catalog. `cmd/modu_models` is its CLI.

The current provider format describes OpenAI Chat Completions compatible upstreams. The local gateway accepts:

| Local endpoint | Upstream behavior |
| --- | --- |
| `/v1/chat/completions` | Forward Chat Completions, including upstream SSE |
| `/v1/responses` | Translate request and response to Chat Completions |
| `/v1/messages` | Translate request and response to Chat Completions |
| `/v1/models` | List configured `provider/model` IDs |

Responses and Messages streaming requests receive SSE events after the upstream response completes. The conversion handles text, base64 image input, and function tool calls. It does not yet translate reasoning items, custom tools, or incremental upstream deltas. Configure only models and clients whose requests fit this boundary.

## Library

```go
cfg := modelrouter.Config{Providers: []modelrouter.Provider{{
    ID: "deepseek", BaseURL: "https://api.deepseek.com/v1",
    APIKeyEnv: "DEEPSEEK_API_KEY", Models: []string{"deepseek-chat"},
}}}
router, err := modelrouter.New(cfg)
if err != nil { /* handle error */ }
server := &http.Server{Addr: "127.0.0.1:3425", Handler: router.Handler()}
```

`Load` and `Save` read and atomically write the JSON configuration. Saved configuration and agent stashes use owner-only permissions. The CLI binds the gateway to loopback only.

`AgentManager.Use("codex", "deepseek/deepseek-chat")` writes a Codex model provider and local model catalog. `Use("claude", ...)` writes Claude Code's gateway environment settings. `Restore` returns the settings present before the first switch. Restart either agent after switching.

## CLI

```sh
go run ./cmd/modu_models provider add deepseek --url https://api.deepseek.com/v1 --models deepseek-chat --key-env DEEPSEEK_API_KEY
go run ./cmd/modu_models models
go run ./cmd/modu_models serve
go run ./cmd/modu_models use codex deepseek/deepseek-chat
go run ./cmd/modu_models use claude deepseek/deepseek-chat
go run ./cmd/modu_models restore codex
```

Configuration defaults to `~/.modu/modelrouter/config.json`. `MODU_MODELROUTER_CONFIG` overrides that path. `MODU_MODELROUTER_URL` changes the loopback gateway URL (default `http://127.0.0.1:3425`). The gateway uses the provider's configured key or `APIKeyEnv` value, never the client token. Keys are not printed by list commands.
