# Model router

`pkg/modelrouter` is a Go library for routing locally installed coding agents through one model catalog. `cmd/modu_models` is its CLI.

Providers can use `chat` (the default) or `responses` as their upstream protocol. The local gateway accepts:

| Local endpoint | Upstream behavior |
| --- | --- |
| `/v1/chat/completions` | Forward to a Chat provider, including upstream SSE |
| `/v1/responses` | Forward natively to a Responses provider, or translate for a Chat provider |
| `/v1/responses/input_tokens`, `/v1/responses/compact` | Forward to a Responses provider |
| `/v1/responses/{id}` | Retrieve or delete a native response |
| `/v1/responses/{id}/cancel`, `/v1/responses/{id}/input_items` | Cancel a native response or list its input items |
| `/v1/messages` | Translate request and response to Chat Completions |
| `/v1/models` | List configured `provider/model` IDs |

Native Responses requests preserve request fields such as reasoning, built-in and custom tools, structured output, background mode, and `previous_response_id`. Native SSE events are forwarded as they arrive; event types and content remain intact. The gateway prefixes the public response ID with provider routing information and restores the upstream ID on later requests. The `model` field in response objects uses the local `provider/model` name. These routes cover the Responses HTTP API; its separate WebSocket transport is not implemented. A request must include a configured `provider/model` to select its upstream. A native Responses provider currently serves Codex and direct Responses clients; `use claude` requires a Chat provider.

For Chat providers, Responses and Messages use the existing conversion. Their streaming requests receive SSE only after the upstream response completes. This conversion handles text, base64 image input, and function tool calls; it cannot preserve reasoning items, custom tools, or incremental deltas. Use a native Responses provider when those features are required.

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
go run ./cmd/modu_models provider add openai --url https://api.openai.com/v1 --models YOUR_MODEL --protocol responses --key-env OPENAI_API_KEY
go run ./cmd/modu_models models
go run ./cmd/modu_models serve
go run ./cmd/modu_models use codex deepseek/deepseek-chat
go run ./cmd/modu_models use codex openai/YOUR_MODEL
go run ./cmd/modu_models use claude deepseek/deepseek-chat
go run ./cmd/modu_models restore codex
```

Configuration defaults to `~/.modu/modelrouter/config.json`. `MODU_MODELROUTER_CONFIG` overrides that path. `MODU_MODELROUTER_URL` changes the loopback gateway URL (default `http://127.0.0.1:3425`). The gateway uses the provider's configured key or `APIKeyEnv` value, never the client token. Keys are not printed by list commands.

## Verification

First run the local integration tests. They use temporary Codex and Claude Code homes and a fake upstream, so they need no provider key and do not change your agent settings:

```sh
MODELROUTER_AGENT_INTEGRATION=1 go test ./pkg/modelrouter ./cmd/modu_models -count=1
```

For a live provider test, set `DEEPSEEK_API_KEY` in the shell that will run `serve`, then add the provider and start the gateway:

```sh
go run ./cmd/modu_models provider add deepseek --url https://api.deepseek.com/v1 --models deepseek-chat --key-env DEEPSEEK_API_KEY
go run ./cmd/modu_models models
go run ./cmd/modu_models serve
```

In another terminal, inspect the catalog and make one Chat Completions request:

```sh
curl -fsS http://127.0.0.1:3425/v1/models
curl -fsS http://127.0.0.1:3425/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"Reply with OK"}]}'
```

Only after the gateway request works, switch an agent with `use codex` or `use claude` and start a new agent session. `use` changes that agent's configuration; `restore codex` or `restore claude` restores the previous model and endpoint settings. Subscription logins are not available as cross-agent providers yet.
