# acp-gateway HTTP API

Reference for iOS / any HTTP client that wants to dispatch prompts at an
ACP agent (Claude Code, Codex, Gemini) running on the user's home
computer.

- Base URL: whatever the gateway is bound to (default `:7080`).
- Content-Type: `application/json` on all non-SSE success responses and
  requests.
- **Error response bodies are `text/plain`**, not JSON. Every non-2xx
  response (400 / 401 / 404 / 409 / 503 / 500) has a one-line message in
  the body and no `Content-Type: application/json`.
- All times are RFC 3339 / ISO 8601 UTC (`2026-04-18T15:13:45.142Z`).

---

## Auth

Every route except `GET /healthz` and `GET /` requires:

```
Authorization: Bearer <MODU_ACP_TOKEN>
```

`MODU_ACP_TOKEN` is set on the gateway process. If unset, auth is
disabled (dev only). A missing or mismatching token returns
`401 unauthorized` with a plain-text body.

---

## Endpoint summary

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET  | `/healthz`                          | no  | liveness probe |
| GET  | `/`                                 | no  | bundled web console (HTML) |
| GET  | `/api/agents`                       | yes | list configured agents |
| POST | `/api/tasks`                        | yes | submit a prompt |
| GET  | `/api/tasks/{id}`                   | yes | fetch task snapshot |
| GET  | `/api/tasks/{id}/stream`            | yes | live SSE of one task |
| POST | `/api/tasks/{id}/approve`           | yes | reply to a permission prompt |

---

## Models

### Task

```json
{
  "id":        "task-42",
  "agent":     "codex",
  "prompt":    "What is 2 + 2?",
  "cwd":       "/Users/me/proj",
  "status":    "pending | running | completed | failed",
  "result":    "Four",                        // only when completed
  "error":     "simulated prompt failure",    // only when failed
  "createdAt": "2026-04-18T15:13:45.142Z",
  "updatedAt": "2026-04-18T15:13:49.215Z"
}
```

Status lifecycle:

```
pending ──► running ──► completed
                    └──► failed      // also used for cancel / ctx.Done
```

Notes:

- `pending` only appears in the HTTP response body of `POST /api/tasks`
  — it is **not** emitted as an SSE `status` frame. The first frame
  clients see on `/stream` is always `status=running`.
- `cancelled` is reserved for a future explicit-cancel endpoint. Today,
  any cancellation (ctx close, server shutdown) surfaces as
  `status=failed` with `error="cancelled"`.
- Result/error are omitted (via `omitempty`) when empty.

### PermissionPrompt (delivered over SSE)

```json
{
  "toolCallId": "call-abc",
  "title":      "Run `rm -rf /tmp/foo`",
  "kind":       "execute",
  "options": [
    {"optionId": "allow_once",  "name": "Allow once",  "kind": "allow_once"},
    {"optionId": "allow_always","name": "Always allow","kind": "allow_always"},
    {"optionId": "reject_once", "name": "Reject once", "kind": "reject_once"}
  ]
}
```

`toolCall.kind` is forwarded unchanged from the agent (typical ACP
values: `execute`, `read`, `edit`, `fetch`, `search`, `think`, `move`,
`delete`, `other`). Do not hardcode an exhaustive enum on the client.

`option.kind` is one of `allow_once`, `allow_always`, `reject_once`,
`reject_always`. The gateway treats `reject_once` / `reject_always` as
the default decision when no client is listening.

---

## Endpoints

### `GET /healthz`

Liveness check. No auth.

Response 200:
```json
{"status": "ok"}
```

---

### `GET /api/agents`

Lists the agent IDs the gateway is configured to launch.

Response 200:
```json
{"agents": ["codex", "claude"]}
```

---

### `POST /api/tasks`

Submit a prompt. Returns immediately with the created `Task`; the agent
runs in the background. Subscribe to `/stream` to see output.

Request body:
```json
{
  "agent":  "codex",         // required; must be one of /api/agents
  "prompt": "...",           // required; the user turn
  "cwd":    "/path/to/dir"   // optional at the protocol level — omit or
                             // "" is accepted. File-aware agents (codex,
                             // claude-code) need a real path. The gateway
                             // treats (agent, cwd) as session identity:
                             // same pair reuses the same subprocess.
}
```

Response 202 (Accepted):
```json
{
  "id":"task-42","agent":"codex","prompt":"...","cwd":"/tmp",
  "status":"pending",
  "createdAt":"...","updatedAt":"..."
}
```

Errors:
- `400` — missing `agent` or `prompt`, or unknown agent id
- `503` — worker queue full (retry later)

---

### `GET /api/tasks/{id}`

Snapshot of the task's current state. Safe to poll, but prefer `/stream`.

Response 200: `Task`. 404 if unknown id.

---

### `GET /api/tasks/{id}/stream`

**Server-Sent Events.** One HTTP response the client keeps open; the
server writes frames as the agent runs, then closes when the task
reaches a terminal status.

Response headers:
```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
```

The server **replays any events emitted before subscribe** (bounded
buffer: 256 frames). This closes the race between `POST /tasks`
returning and the client opening the stream — so even if a permission
prompt fires before you connect, you'll still see it. The buffer is
per-task.

Frame wire format (blank line terminates a frame):
```
event: <type>
data: <one line of JSON>

```

#### SSE event types

| `event:` | `data:` shape | Meaning |
|---|---|---|
| `status`     | `Task`             | Status transition. At minimum: first running, final terminal. |
| `event`      | `StreamEvent` (see below) | Provider-level token stream — text deltas, tool calls, etc. |
| `permission` | `PermissionPrompt` | Agent asked to run a tool. Reply via `POST /approve`. |

`StreamEvent` payload (flattened to a JSON map — omitted keys mean the
value was empty/nil for this event type):
```json
{
  "type":   "start | text_start | text_delta | text_end
           | thinking_start | thinking_delta | thinking_end
           | toolcall_start | toolcall_delta | toolcall_end
           | done | error",
  "delta":    "string",           // on *_delta and toolcall_delta
  "content":  "string",           // on *_end
  "reason":   "end_turn | ...",   // on "done"
  "error":    "message",          // on "error"
  "toolCall": {                   // on toolcall_start / toolcall_end
    "type":             "string",
    "id":               "string",
    "name":             "string",
    "arguments":        { /* map[string]any — tool-specific JSON */ },
    "thoughtSignature": "string"  // optional
  }
}
```

What ACP backends actually emit today (as of this writing, confirmed by
reading `pkg/acp/provider` + `pkg/acp/bridge`):

| `type` | When | Fields present |
|---|---|---|
| `start`         | once, at the first frame | — |
| `text_delta`    | per `agent_message_chunk` | `delta` |
| `thinking_delta`| per `agent_thought_chunk` | `delta` |
| `done`          | once, at stopReason      | `reason` |
| `error`         | on provider/transport error | `error` |

The other types in the enum above (`text_start` / `text_end` /
`thinking_start` / `thinking_end` / `toolcall_start` / `toolcall_delta`
/ `toolcall_end`) are **not** produced by ACP providers today — tool
activity on ACP surfaces instead through the `permission` SSE event
(and, for Claude, the bridge swallows tool-call updates rather than
forwarding them). The enum is the forward-compatible contract; handle
it defensively.

#### Client loop (what iOS should do)

1. Open the stream as soon as `POST /tasks` returns `202` (don't wait).
2. Parse framed blocks split by `\n\n`. Within a frame, concatenate all
   `data:` lines (server only emits one today; be defensive).
3. Switch on the `event:` header:
   - `status` → update UI, pop the view when status is terminal.
   - `event` + `type=text_delta` → append `delta` to the assistant
     bubble.
   - `event` + `type=done` → the current turn is finished; the server
     will also send a final `status` frame, then close the response.
   - `permission` → render the approve sheet; call `/approve`.
4. On `URLSessionTask` completion / EOF, consider the stream done. The
   task's final state is whatever the last `status` frame said — or
   re-`GET /api/tasks/{id}` to confirm.

Reconnecting: open `/stream` again with the same `id`. The replay
buffer lets you catch back up to the last 256 frames without gaps
(unless you were gone long enough for a streaming turn to flood past
that cap — for pathological cases, fall back to the snapshot).

---

### `POST /api/tasks/{id}/approve`

Reply to the most recent `permission` SSE frame for this task.

Request body:
```json
{
  "toolCallId": "call-abc",   // from the permission prompt
  "optionId":   "allow_once"  // one of PermissionPrompt.options[].optionId
}
```

Response 200:
```json
{"status":"approved"}
```

Errors:
- `400` — missing `toolCallId` or `optionId`
- `404` — no such task
- `409` — no pending permission for this `toolCallId` (already
  answered, or the task moved on / was cancelled)

The agent resumes within one network round trip of the 200 landing.
You'll see new `event` or `status` frames stream in immediately.

---

## End-to-end example

```bash
TOKEN=dev-token
BASE=http://home.local:7080

# 1. submit
id=$(curl -s -H "Authorization: Bearer $TOKEN" \
          -H "Content-Type: application/json" \
          -d '{"agent":"codex","prompt":"list files in cwd","cwd":"/tmp"}' \
          $BASE/api/tasks | jq -r .id)

# 2. stream (blocks until terminal)
curl -N -H "Authorization: Bearer $TOKEN" "$BASE/api/tasks/$id/stream"

# 3. if you saw a `permission` frame, approve it
curl -X POST -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"toolCallId":"call-abc","optionId":"allow_once"}' \
     "$BASE/api/tasks/$id/approve"

# 4. final state (redundant if you streamed to end)
curl -H "Authorization: Bearer $TOKEN" "$BASE/api/tasks/$id" | jq
```

---

## iOS notes

### URLSession streaming

`EventSource` doesn't exist on Apple platforms, and `URLSession`'s
`NSURLSessionDataDelegate` + buffered parsing is the standard approach.
A minimal sketch:

```swift
var req = URLRequest(url: URL(string: "\(base)/api/tasks/\(id)/stream")!)
req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
req.timeoutInterval = .infinity          // don't time out a live stream

let (bytes, response) = try await URLSession.shared.bytes(for: req)
guard (response as? HTTPURLResponse)?.statusCode == 200 else { /* err */ }

var eventName = "message"
var dataBuf = ""
for try await line in bytes.lines {
    if line.isEmpty {
        // frame terminator
        handleFrame(event: eventName, data: dataBuf)
        eventName = "message"; dataBuf = ""
    } else if line.hasPrefix("event:") {
        eventName = line.dropFirst(6).trimmingCharacters(in: .whitespaces)
    } else if line.hasPrefix("data:") {
        dataBuf += line.dropFirst(5).trimmingCharacters(in: .whitespaces)
    }
    // ignore `id:`, `retry:` — gateway doesn't emit them
}
```

### Backgrounding

SSE over `URLSession` does **not** survive the app being backgrounded.
Strategies that work:

1. **Foreground-only streaming.** When the app backgrounds, drop the
   stream. On return, `GET /api/tasks/{id}` for state, then reopen
   `/stream` (replay buffer fills the recent past).
2. **APNs push from the gateway** (not implemented yet — tell me if
   you want this).

### Cancel on the iOS side

There is no `DELETE /api/tasks/{id}` yet — the server cancels only on
the provider side via process teardown. If you need cancel, say so and
I'll wire it up (the plumbing is in place in `Store.Cancel`).

### Timeouts / retries

- `POST /api/tasks` should return within ~1 s. Retry on network error.
- `GET /stream` is a long-lived request; do not apply a read timeout.
  Gateway never idles the connection — if the agent goes silent for
  minutes, nothing is written.
- `POST /approve` returns within ~100 ms; retry on network error only
  (idempotent: a second 409 just means the first landed).

---

## Versioning

This API is unversioned and pre-1.0. Shape changes will be called out
in the commit changelog. When we stabilize, routes will move under
`/v1/...`.
