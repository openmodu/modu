# acp-gateway HTTP API

Reference for iOS / any HTTP client dispatching prompts to ACP agents
(Claude Code, Codex, Gemini, Modu) running on a home machine.

- Base URL: whatever the gateway is bound to (default `:7080`).
- All request/response bodies are `application/json`.
- **Error responses are `text/plain`** — a one-line message, no JSON wrapper.
- All timestamps are RFC 3339 UTC (`2026-04-25T14:00:00.000Z`).

---

## Auth

Every route except `GET /healthz` and `GET /` requires:

```
Authorization: Bearer <MODU_ACP_TOKEN>
```

`MODU_ACP_TOKEN` is set as an environment variable on the gateway process.
If unset, auth is disabled (dev mode). Missing or wrong token → `401`.

---

## Concepts

```
Project ──► Session ──► Turn
  (workdir)   (agent+context)  (one prompt/response)
```

- **Project** — a named working directory. File-system operations are jailed to the project path.
- **Session** — a persistent conversation between a user and one agent inside a project. The agent subprocess stays alive across turns so conversation context accumulates naturally.
- **Turn** — one round-trip: user prompt → agent response. Each turn streams events via SSE while running.

---

## Endpoint summary

| Method   | Path                                                | Auth | Purpose |
|----------|-----------------------------------------------------|------|---------|
| GET      | `/healthz`                                          | no   | liveness probe |
| GET      | `/`                                                 | no   | web console (HTML) |
| GET      | `/api/info`                                         | yes  | gateway version, uptime, connections |
| GET      | `/api/system`                                       | yes  | memory & disk usage |
| GET      | `/api/agents`                                       | yes  | list agents with status |
| POST     | `/api/agents`                                       | yes  | add agent (persists to config) |
| GET      | `/api/agents/{id}`                                  | yes  | agent detail + stats |
| PUT      | `/api/agents/{id}`                                  | yes  | update agent config |
| DELETE   | `/api/agents/{id}`                                  | yes  | remove agent |
| POST     | `/api/agents/{id}/restart`                          | yes  | restart agent subprocess |
| GET      | `/api/workdir`                                      | yes  | default working directory |
| GET      | `/api/files?path=`                                  | yes  | list files in workdir |
| GET      | `/api/browse?path=&dirs=true`                       | yes  | browse any absolute path (project picker) |
| GET      | `/api/projects`                                     | yes  | list all projects |
| POST     | `/api/projects`                                     | yes  | create project |
| GET      | `/api/projects/{id}`                                | yes  | get project |
| DELETE   | `/api/projects/{id}`                                | yes  | delete project |
| GET      | `/api/projects/{id}/files?path=`                    | yes  | list files in project |
| GET      | `/api/sessions?projectId=`                          | yes  | list sessions |
| POST     | `/api/sessions`                                     | yes  | create session |
| GET      | `/api/sessions/{id}`                                | yes  | session detail + turns |
| DELETE   | `/api/sessions/{id}`                                | yes  | delete session |
| POST     | `/api/sessions/{id}/cancel`                         | yes  | cancel running turn (session-level) |
| GET      | `/api/turns?status=&agent=&sessionId=&limit=`       | yes  | global turn list with filtering |
| POST     | `/api/sessions/{id}/turns`                          | yes  | add a turn (send prompt) |
| GET      | `/api/sessions/{id}/turns/{turnId}/stream`          | yes  | SSE event stream for turn |
| POST     | `/api/sessions/{id}/turns/{turnId}/approve`         | yes  | approve a tool call |
| POST     | `/api/sessions/{id}/turns/{turnId}/cancel`          | yes  | cancel individual turn |
| GET      | `/api/sessions/{id}/turns/{turnId}/events`          | yes  | timestamped event history |

---

## Models

### GatewayInfo

```json
{
  "version":     "1.2.0",
  "startTime":   "2026-04-28T10:00:00Z",
  "uptimeSec":   3600.5,
  "connections": 3,
  "agents":      4
}
```

### SystemInfo

```json
{
  "goroutines":  42,
  "heapMB":      128,
  "allocMB":     64,
  "diskTotalGB": 500.1,
  "diskFreeGB":  120.3,
  "diskUsedPct": 75.9
}
```

### AgentDetail

```json
{
  "id":            "claude",
  "name":          "Claude Code",
  "command":       "npx",
  "args":          ["-y", "@zed-industries/claude-code-acp"],
  "status":        "running | idle | offline",
  "activeTurns":   1,
  "totalSessions": 8,
  "totalTurns":    23
}
```

`status` values:
- `running` — at least one turn is actively executing
- `idle` — subprocess is alive but no active turn
- `offline` — no subprocess spawned yet (first prompt will launch it)

### Project

```json
{
  "id":        "proj-1",
  "name":      "my-app",
  "path":      "/Users/me/Code/my-app",
  "createdAt": "2026-04-25T14:00:00Z"
}
```

### Session

```json
{
  "id":        "sess-3",
  "projectId": "proj-1",
  "agent":     "claude",
  "title":     "Add dark mode toggle",
  "status":    "idle | running | cancelled",
  "cwd":       "/Users/me/Code/my-app",
  "createdAt": "2026-04-25T14:01:00Z",
  "updatedAt": "2026-04-25T14:05:30Z"
}
```

`title` is auto-set to the first 80 characters of the first prompt if not provided at creation time.

### SessionDetail

```json
{
  /* all Session fields */
  "turns": [ /* Turn[] */ ]
}
```

### Turn

```json
{
  "id":        "turn-7",
  "sessionId": "sess-3",
  "agent":     "claude",
  "cwd":       "/Users/me/Code/my-app",
  "prompt":    "Add a dark mode toggle to the settings page.",
  "result":    "Done! I added a toggle in ...",
  "error":     "",
  "status":    "pending | running | completed | failed",
  "createdAt": "2026-04-25T14:04:00Z",
  "updatedAt": "2026-04-25T14:05:30Z"
}
```

Turn status lifecycle:

```
pending ──► running ──► completed
                    └──► failed   (also used for cancel)
```

- `result` populated only when `status=completed`.
- `error` populated only when `status=failed`.
- A session can have only one `running` turn at a time. Posting while one runs → `409`.

### BufferedEvent (events history)

```json
{
  "time": "2026-04-28T14:30:06.123Z",
  "type": "status | event | permission",
  "data": { /* same shape as the SSE data field */ }
}
```

### PermissionPrompt (SSE frame)

```json
{
  "toolCallId": "call-abc",
  "title":      "Run `rm -rf /tmp/foo`",
  "kind":       "execute",
  "options": [
    {"optionId": "allow_once",   "name": "Allow once",   "kind": "allow_once"},
    {"optionId": "allow_always", "name": "Always allow", "kind": "allow_always"},
    {"optionId": "reject_once",  "name": "Reject once",  "kind": "reject_once"}
  ]
}
```

---

## Endpoints

### `GET /healthz`

No auth. Returns `{"status": "ok"}`.

---

### `GET /api/info`

Gateway status for the overview dashboard.

```json
{
  "version": "dev",
  "startTime": "2026-04-28T10:00:00Z",
  "uptimeSec": 3600.5,
  "connections": 3,
  "agents": 4
}
```

`version` is set at build time via `-ldflags "-X main.Version=1.2.0"`.

---

### `GET /api/system`

Process and disk metrics. `diskTotalGB` / `diskFreeGB` are measured on the
gateway's workdir filesystem.

---

### `GET /api/agents`

Returns an array of `AgentDetail` (not just IDs).

```json
{"agents": [ /* AgentDetail[] */ ]}
```

### `POST /api/agents`

Adds an agent at runtime and persists it to `acp.config.json`.

```json
{
  "id":      "my-agent",
  "name":    "My Agent",
  "command": "/usr/local/bin/my-agent",
  "args":    ["--acp"]
}
```

Response `201`: `AgentDetail`. `409` if ID already exists.

### `GET /api/agents/{id}`

Response `200`: `AgentDetail`. `404` if not found.

### `PUT /api/agents/{id}`

Replaces agent config and restarts its subprocess. Persists to config.

```json
{"id": "my-agent", "name": "My Agent v2", "command": "...", "args": [...]}
```

Response `200`: updated `AgentDetail`.

### `DELETE /api/agents/{id}`

Stops the subprocess and removes the agent from config. Response `204`.

### `POST /api/agents/{id}/restart`

Stops all running subprocesses for the agent. The next prompt will lazily
re-launch the process.

Response `200`: `{"status": "restarted"}`.

---

### `GET /api/workdir`

```json
{"workdir": "/Users/me/Code"}
```

---

### `GET /api/files?path=subdir`

Lists files relative to workdir. `path` is a relative path.

---

### `GET /api/browse?path=/Users/me&dirs=true`

Browses **any absolute path** on the gateway machine — designed as a remote
filesystem picker for project directory selection.

Query params:
- `path` — absolute path (default: home directory)
- `dirs=true` — return only directories

Hidden entries (dot-files) are omitted.

```json
{
  "path":   "/Users/me/Code",
  "parent": "/Users/me",
  "files": [
    {"name": "my-app", "path": "/Users/me/Code/my-app", "isDir": true, "modTime": "..."}
  ]
}
```

Picker flow: `GET /api/browse` → navigate → user picks → `POST /api/projects`.

---

### `GET /api/projects`

```json
{"projects": [ /* Project[] */ ]}
```

### `POST /api/projects`

```json
{"name": "my-app", "path": "/Users/me/Code/my-app"}
```

Response `201`: `Project`.

### `GET /api/projects/{id}`

Response `200`: `Project`. `404` if not found.

### `DELETE /api/projects/{id}`

Response `204`.

### `GET /api/projects/{id}/files?path=subdir`

Same as `GET /api/files` but rooted at the project path.

---

### `GET /api/sessions?projectId=proj-1`

`projectId` is optional. Returns `{"sessions": [ /* Session[] */ ]}`.

### `POST /api/sessions`

```json
{"projectId": "proj-1", "agent": "claude", "title": "optional"}
```

Response `201`: `Session`. Errors: `400` bad fields / unknown agent / project.

### `GET /api/sessions/{id}`

Response `200`: `SessionDetail` (session + all turns). `404` if not found.

### `DELETE /api/sessions/{id}`

Removes session and all its turns. Response `204`.

### `POST /api/sessions/{id}/cancel`

Cancels the currently running turn. Response `200`: `{"status": "cancel requested"}`.

---

### `GET /api/turns?status=running&agent=claude&sessionId=sess-3&limit=50`

Global turn list across all sessions, newest first.

Query params (all optional):
- `status` — `pending | running | completed | failed`
- `agent` — filter by agent ID
- `sessionId` — filter by session
- `limit` — max results (default 50)

```json
{"turns": [ /* Turn[] */ ]}
```

---

### `POST /api/sessions/{id}/turns`

Adds a turn and queues it. Returns immediately.

```json
{"prompt": "Add a dark mode toggle."}
```

Response `202`: `Turn` (status=pending). `409` if session already has a running turn.

---

### `GET /api/sessions/{id}/turns/{turnId}/stream`

**Server-Sent Events.** Stays open until turn reaches a terminal status.
Replays up to 256 buffered past events on connect.

Frame format:
```
event: <type>
data: <JSON>

```

#### SSE event types

| `event:`     | `data:` shape      | Meaning |
|--------------|--------------------|---------|
| `status`     | `Turn`             | Status transition |
| `event`      | `StreamEvent`      | Token stream from agent |
| `permission` | `PermissionPrompt` | Agent requests tool approval |

`StreamEvent` payload:
```json
{
  "type":   "start | text_delta | thinking_delta | done | error | ...",
  "delta":  "string",
  "reason": "end_turn",
  "error":  "message"
}
```

---

### `POST /api/sessions/{id}/turns/{turnId}/approve`

```json
{"toolCallId": "call-abc", "optionId": "allow_once"}
```

Response `200`: `{"status": "approved"}`. `409` if no pending permission.

---

### `POST /api/sessions/{id}/turns/{turnId}/cancel`

Cancels a specific running turn (more granular than session cancel).

Response `200`: `{"status": "cancel requested"}`. `409` if turn not running.

---

### `GET /api/sessions/{id}/turns/{turnId}/events`

Returns the full timestamped event history for a turn (in-memory replay
buffer, up to 256 entries).

```json
{
  "events": [
    {"time": "2026-04-28T14:30:06.123Z", "type": "status", "data": {...}},
    {"time": "2026-04-28T14:30:07.001Z", "type": "event",  "data": {"type": "text_delta", "delta": "Hello"}},
    {"time": "2026-04-28T14:30:09.500Z", "type": "status", "data": {...}}
  ]
}
```

Note: the buffer lives in memory and is lost on gateway restart. For
completed turns this provides an execution log; for running turns it
provides a snapshot of events so far.

---

## End-to-end example

```bash
BASE=http://localhost:7080
H="Authorization: Bearer $TOKEN"

# Gateway info
curl -H "$H" $BASE/api/info | jq

# Create project + session + turn
proj=$(curl -s -H "$H" -H "Content-Type: application/json" \
  -d '{"name":"my-app","path":"/Users/me/Code/my-app"}' \
  $BASE/api/projects | jq -r .id)

sess=$(curl -s -H "$H" -H "Content-Type: application/json" \
  -d "{\"projectId\":\"$proj\",\"agent\":\"claude\"}" \
  $BASE/api/sessions | jq -r .id)

turn=$(curl -s -H "$H" -H "Content-Type: application/json" \
  -d '{"prompt":"List files in the project."}' \
  $BASE/api/sessions/$sess/turns | jq -r .id)

# Stream live output
curl -N -H "$H" $BASE/api/sessions/$sess/turns/$turn/stream

# Multi-turn follow-up (wait for first turn to complete first)
turn2=$(curl -s -H "$H" -H "Content-Type: application/json" \
  -d '{"prompt":"Now add a README.md."}' \
  $BASE/api/sessions/$sess/turns | jq -r .id)
curl -N -H "$H" $BASE/api/sessions/$sess/turns/$turn2/stream

# View execution log
curl -H "$H" $BASE/api/sessions/$sess/turns/$turn/events | jq

# Global task view
curl -H "$H" "$BASE/api/turns?status=completed&agent=claude" | jq

# Add a custom agent at runtime
curl -X POST -H "$H" -H "Content-Type: application/json" \
  -d '{"id":"my-bot","name":"My Bot","command":"/usr/local/bin/my-bot","args":["--acp"]}' \
  $BASE/api/agents | jq
```

---

## Multi-turn conversation

Turns within the same session share an agent subprocess, so each successive
prompt sees the full conversation history. Sequential execution is enforced:
posting a turn while one is running returns `409` — wait for
`status=completed` on the stream before sending the next prompt.

---

## iOS integration notes

### Streaming

```swift
var req = URLRequest(url: URL(string: "\(base)/api/sessions/\(sid)/turns/\(tid)/stream")!)
req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
req.timeoutInterval = .infinity

let (bytes, _) = try await URLSession.shared.bytes(for: req)
var eventName = "message"; var dataBuf = ""
for try await line in bytes.lines {
    if line.isEmpty {
        handleFrame(event: eventName, data: dataBuf)
        eventName = "message"; dataBuf = ""
    } else if line.hasPrefix("event:") {
        eventName = String(line.dropFirst(6)).trimmingCharacters(in: .whitespaces)
    } else if line.hasPrefix("data:") {
        dataBuf = String(line.dropFirst(5)).trimmingCharacters(in: .whitespaces)
    }
}
```

### Backgrounding

SSE does not survive backgrounding. On return to foreground:
1. `GET /api/sessions/{id}` for current state
2. If a turn is still running, reopen `/stream` (replay buffer catches you up)
3. If completed, `GET /api/sessions/{id}/turns/{turnId}/events` for the full log

### Cancel

- Turn-level: `POST /api/sessions/{id}/turns/{turnId}/cancel`
- Session-level: `POST /api/sessions/{id}/cancel` (cancels current running turn)

Both mark the turn `failed` with `error="cancelled"` within one scheduler tick.

---

## Persistence

Projects, sessions, and turns are written to SQLite (`acp-gateway.db` by
default, override with `-db`). Turns that were `pending` or `running` at
shutdown are marked `failed` on next startup. Agent config changes (add /
update / delete) are persisted to `acp.config.json`.

The event buffer (turn execution log) lives in memory only and is not
persisted across restarts.

---

## Build flags

```bash
go build -ldflags "-X main.Version=1.2.0" ./cmd/acp-gateway
```

---

## Versioning

Pre-1.0; unversioned. Breaking changes noted in commit messages.
Routes will move under `/v1/` when the API stabilizes.
