# acp_e2e — end-to-end smoke test for `cmd/acp-gateway`

Runs the gateway against a scripted mock ACP agent — no LLM API key, no
network. Good enough to catch regressions across the full chain: HTTP →
mailbox-style task queue → `pkg/acp` (manager → client → process) → ACP
subprocess → bridge → SSE.

## Run

```bash
bash examples/acp_e2e/test_e2e.sh
```

The script builds two binaries into a tempdir, starts the gateway on
`127.0.0.1:17080` (override via `PORT=...`), and drives it with `curl`.

## What it covers

| Case | What it proves |
|---|---|
| `/healthz` | gateway boots, no auth required |
| `/api/agents` | manager sees the configured mock agent |
| POST + stream (case 1) | text chunks land as SSE `text_delta`; final status=completed; `result` concatenation works |
| permission flow (case 2) | mock agent fires `session/request_permission` → gateway surfaces a `permission` SSE frame → `POST /approve` forwards the `optionId` back → prompt resumes |
| auth enforcement (case 3) | `/api/agents` without Bearer → 401 |

## Mock agent

`mock_acp_agent/` is a small Go binary that speaks the same LDJSON
JSON-RPC 2.0 stdio protocol as `@zed-industries/claude-code-acp`, but
with deterministic behavior:

- prompt containing `"permission"` → reverse `session/request_permission`
  before replying; result text reflects the user's outcome
- prompt containing `"error"` → `session/prompt` returns `-32603`
- any other prompt → three text chunks (`Hello`, `, `, `world`) + `end_turn`

This lets us exercise the full client stack without depending on Anthropic
credentials or `npx`.

## Manual verification with the real agent

The real `@zed-industries/claude-code-acp` requires `ANTHROPIC_API_KEY`.
To smoke-test against it:

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

go run ./cmd/acp-gateway -config /tmp/acp.config.json &

curl -sf -H "Authorization: Bearer $MODU_ACP_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"agent":"claude","prompt":"list files in cwd","cwd":"/tmp"}' \
  http://localhost:7080/api/tasks
```

Stream the response via `curl -N -H "Authorization: Bearer $MODU_ACP_TOKEN"
http://localhost:7080/api/tasks/<id>/stream`.
