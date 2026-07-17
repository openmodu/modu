# ACP Gateway mock Agent

`examples/acp_e2e` contains a deterministic ACP subprocess for testing `cmd/acp-gateway` without an LLM key or external network access. The mock Agent is reusable, but the repository does not currently provide an automated end-to-end harness for the Gateway's Project / Session / Turn API.

## Current API boundary

The current Gateway exposes projects, sessions, and turns:

```text
POST /api/projects
POST /api/sessions
POST /api/sessions/{sessionId}/turns
GET  /api/sessions/{sessionId}/turns/{turnId}/stream
POST /api/sessions/{sessionId}/turns/{turnId}/approve
```

Package and Handler tests cover the individual components. Do not describe the current repository as having automated ACP Gateway E2E coverage until a harness drives these routes through a real Gateway process.

## Required end-to-end coverage

| Case | Required assertion |
|---|---|
| `/healthz` | Gateway starts and health checks require no authentication |
| `/api/agents` | Manager lists the configured mock Agent |
| Prompt and stream | Text chunks arrive as SSE `text_delta` events and the final text is `Hello, world` |
| Permission | `session/request_permission` becomes an SSE `permission` event; approving `optionId=allow` resumes the turn |
| Authentication | `/api/agents` without a Bearer token returns HTTP 401 |

## Mock Agent behavior

`mock_acp_agent/` is a Go binary that speaks the LDJSON JSON-RPC 2.0 protocol used by `@zed-industries/claude-code-acp`:

- A Prompt containing `permission` sends `session/request_permission` before completing; the response reflects the selected option.
- A Prompt containing `error` returns JSON-RPC error `-32603` from `session/prompt`.
- Any other Prompt emits three text chunks—`Hello`, `, `, and `world`—then `end_turn`.

This isolates Gateway, ACP process, Bridge, permission, and SSE behavior from Anthropic credentials and `npx`. It does not test a real Agent package, model response, or network service.

## Start the Gateway with a real Claude ACP Agent

```bash
export ANTHROPIC_API_KEY=sk-...
export MODU_ACP_TOKEN=dev-token

cat > /tmp/acp.config.json <<'JSON'
{
  "version": 1,
  "agents": [
    {
      "id": "claude",
      "command": "npx",
      "args": ["-y", "@zed-industries/claude-code-acp"]
    }
  ],
  "defaultAgent": "claude"
}
JSON

go run ./cmd/acp-gateway -config /tmp/acp.config.json
```

The real Agent path requires network access, `npx`, and a valid `ANTHROPIC_API_KEY`. All API routes except `/` and `/healthz` require `Authorization: Bearer $MODU_ACP_TOKEN`.
