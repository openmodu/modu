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

- **Project** — a named working directory. All file-system operations are
  jailed to the project path.
- **Session** — a persistent conversation between a user and one agent
  inside a project. The agent subprocess (or modu's CodingSession) stays
  alive across turns, so conversation context accumulates naturally.
- **Turn** — one round-trip: user prompt → agent response. Each turn
  streams events via SSE while the agent is running.

---

## Endpoint summary

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| GET  | `/healthz`                                          | no  | liveness probe |
| GET  | `/`                                                 | no  | web console (HTML) |
| GET  | `/api/agents`                                       | yes | list configured agents |
| GET  | `/api/workdir`                                      | yes | default working directory |
| GET  | `/api/files?path=`                                  | yes | list files in workdir |
| GET  | `/api/browse?path=&dirs=true`                       | yes | browse any absolute path (project picker) |
| GET  | `/api/projects`                                     | yes | list all projects |
| POST | `/api/projects`                                     | yes | create project |
| GET  | `/api/projects/{id}`                                | yes | get project |
| DELETE | `/api/projects/{id}`                              | yes | delete project |
| GET  | `/api/projects/{id}/files?path=`                    | yes | list files in project |
| GET  | `/api/sessions?projectId=`                          | yes | list sessions |
| POST | `/api/sessions`                                     | yes | create session |
| GET  | `/api/sessions/{id}`                                | yes | session detail + turns |
| DELETE | `/api/sessions/{id}`                              | yes | delete session |
| POST | `/api/sessions/{id}/cancel`                         | yes | cancel running turn |
| POST | `/api/sessions/{id}/turns`                          | yes | add a turn (send prompt) |
| GET  | `/api/sessions/{id}/turns/{turnId}/stream`          | yes | SSE event stream for turn |
| POST | `/api/sessions/{id}/turns/{turnId}/approve`         | yes | approve a tool call |

---

## Models

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

`title` is auto-set to the first 80 characters of the first prompt if not
provided at creation time.

### SessionDetail (GET /api/sessions/{id})

```json
{
  /* all Session fields */
  "turns": [ /* array of Turn */ ]
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
  "result":    "Done! I added a toggle in src/settings/page.tsx ...",
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

- `result` is present only when `status=completed`.
- `error` is present only when `status=failed`.
- A session can only have one `running` turn at a time. Posting a new turn
  while one is running returns `409 Conflict`.

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

### `GET /api/agents`

Lists configured agent IDs.

```json
{"agents": ["codex", "claude", "gemini", "modu"]}
```

---

### `GET /api/browse?path=/Users/me&dirs=true`

Browses **any absolute path** on the gateway machine. Not restricted to
workdir — designed as a remote filesystem picker so clients can discover
project directories without prior knowledge of the machine layout.

Query params:
- `path` — absolute path to list (default: user home directory)
- `dirs=true` — return only directories (ideal for a project folder picker)

Hidden entries (dot-files/dot-dirs) are omitted unless the caller explicitly
navigates into a hidden directory.

```json
{
  "path":   "/Users/me/Code",
  "parent": "/Users/me",
  "files": [
    {"name": "my-app",  "path": "/Users/me/Code/my-app",  "isDir": true, "modTime": "..."},
    {"name": "modu",    "path": "/Users/me/Code/modu",    "isDir": true, "modTime": "..."}
  ]
}
```

Typical project-picker flow:
1. `GET /api/browse` → home dir contents
2. Navigate with `GET /api/browse?path=/Users/me/Code&dirs=true`
3. User picks a folder → `POST /api/projects` with that path

---

### `GET /api/workdir`

Returns the default working directory used when a project path is not
specified.

```json
{"workdir": "/Users/me/Code"}
```

---

### `GET /api/files?path=subdir`

Lists files in the default workdir. `path` is relative to the workdir.

```json
{
  "root":  "/Users/me/Code",
  "path":  "go/src",
  "files": [
    {"name": "main.go", "path": "go/src/main.go", "isDir": false, "size": 1234, "modTime": "..."},
    {"name": "pkg",     "path": "go/src/pkg",     "isDir": true,  "modTime": "..."}
  ]
}
```

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

Response `204`. `404` if not found.

### `GET /api/projects/{id}/files?path=subdir`

Same shape as `GET /api/files` but rooted at the project's path.

---

### `GET /api/sessions?projectId=proj-1`

`projectId` is optional; omit to list all sessions.

```json
{"sessions": [ /* Session[] */ ]}
```

### `POST /api/sessions`

Creates a new idle session.

```json
{"projectId": "proj-1", "agent": "claude", "title": "optional title"}
```

Response `201`: `Session`.

Errors: `400` bad fields / unknown agent / unknown project.

### `GET /api/sessions/{id}`

Response `200`: `SessionDetail` (session + full turn history). `404` if not found.

### `DELETE /api/sessions/{id}`

Removes the session and all its turns. Response `204`.

### `POST /api/sessions/{id}/cancel`

Cancels the currently running turn (if any). The worker observes ctx
cancellation and marks the turn `failed` with `error="cancelled"`.

Response `200`:
```json
{"status": "cancel requested"}
```

---

### `POST /api/sessions/{id}/turns`

Adds a new turn to the session and queues it for execution. Returns
immediately; subscribe to `/stream` for live output.

```json
{"prompt": "Add a dark mode toggle to the settings page."}
```

Response `202`: `Turn` (status=pending).

Errors:
- `400` — missing prompt or session not found
- `409` — session already has a running turn

---

### `GET /api/sessions/{id}/turns/{turnId}/stream`

**Server-Sent Events.** Stays open until the turn reaches a terminal
status.

The server replays events buffered since the turn started (up to 256
frames), closing the race between `POST /turns` returning and the client
opening the stream.

Frame format:
```
event: <type>
data: <JSON>

```

#### SSE event types

| `event:` | `data:` shape | Meaning |
|----------|---------------|---------|
| `status`     | `Turn`             | Status transition (running → completed/failed) |
| `event`      | `StreamEvent`      | Token-level stream from the agent |
| `permission` | `PermissionPrompt` | Agent requests tool approval |

`StreamEvent` payload:
```json
{
  "type":  "start | text_delta | thinking_delta | done | error | ...",
  "delta": "string",   // on *_delta
  "reason": "end_turn",// on done
  "error": "message"   // on error
}
```

#### Client loop

1. Open `/stream` immediately after `POST /turns` returns `202`.
2. Parse `event:` / `data:` lines split by blank lines.
3. On `event=status` with `status=completed|failed` → turn is done.
4. On `event=event` + `type=text_delta` → append `delta` to the message bubble.
5. On `event=permission` → render approval UI, POST to `/approve`.
6. On EOF / context cancel → stream ended; re-GET session for final state.

---

### `POST /api/sessions/{id}/turns/{turnId}/approve`

Replies to the most recent `permission` SSE frame.

```json
{"toolCallId": "call-abc", "optionId": "allow_once"}
```

Response `200`: `{"status": "approved"}`.

Errors:
- `400` — missing fields
- `409` — no pending permission for this toolCallId

---

## End-to-end example

```bash
BASE=http://localhost:7080
TOKEN=dev-token
H="Authorization: Bearer $TOKEN"

# 1. Create a project
proj=$(curl -s -H "$H" -H "Content-Type: application/json" \
  -d '{"name":"my-app","path":"/Users/me/Code/my-app"}' \
  $BASE/api/projects | jq -r .id)

# 2. Create a session
sess=$(curl -s -H "$H" -H "Content-Type: application/json" \
  -d "{\"projectId\":\"$proj\",\"agent\":\"claude\"}" \
  $BASE/api/sessions | jq -r .id)

# 3. Send a prompt (turn)
turn=$(curl -s -H "$H" -H "Content-Type: application/json" \
  -d '{"prompt":"List the files in the project."}' \
  $BASE/api/sessions/$sess/turns | jq -r .id)

# 4. Stream the response
curl -N -H "$H" $BASE/api/sessions/$sess/turns/$turn/stream

# 5. Send a follow-up (multi-turn)
turn2=$(curl -s -H "$H" -H "Content-Type: application/json" \
  -d '{"prompt":"Now add a README.md."}' \
  $BASE/api/sessions/$sess/turns | jq -r .id)
curl -N -H "$H" $BASE/api/sessions/$sess/turns/$turn2/stream

# 6. Get full session history
curl -H "$H" $BASE/api/sessions/$sess | jq
```

---

## Multi-turn conversation

Turns within the same session share an agent subprocess (or modu
CodingSession), so each successive prompt sees the full conversation
history. The gateway enforces sequential execution: if you POST a turn
while one is running, you get `409` — wait for the current turn to
complete (watch for `status=completed` on the stream) before sending
the next.

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

SSE over URLSession does not survive backgrounding. On return to
foreground: `GET /api/sessions/{id}` for current state, then reopen
`/stream` on the active turn (replay buffer replays recent events).

### Cancel

`POST /api/sessions/{id}/cancel` — the running turn is marked `failed`
with `error="cancelled"` within one scheduler tick.

---

## Persistence

Tasks survive gateway restarts. Projects and sessions are written to
SQLite (`acp-gateway.db` by default, override with `-db`). Turns that
were `pending` or `running` at shutdown are marked `failed` on next
startup so the UI never shows stale running state.

---

## Versioning

Pre-1.0; unversioned. Breaking changes will be noted in commit messages.
Stable routes will move under `/v1/` when the API solidifies.
