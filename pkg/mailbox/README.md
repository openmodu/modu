# Mailbox

[English](README.md) | [中文](README_zh.md)

Mailbox coordinates agents through registration, per-agent inboxes, task and project state, capability-based queues, validation, pipelines, and conversation logs. Use it in process through `Hub`, or expose the same state machine over the package's Redis-compatible command server.

Mailbox is coordination infrastructure, not an agent runtime. It does not call an LLM, choose a worker for manually assigned tasks, or provide durable delivery with the default store.

## Minimal embedded use

```go
package main

import (
	"fmt"

	"github.com/openmodu/modu/pkg/mailbox"
)

func main() {
	hub := mailbox.NewHub()
	hub.Register("director")
	hub.Register("writer")

	msg, err := mailbox.NewTaskAssignMessage("director", "task-1", "Write product copy")
	if err != nil {
		panic(err)
	}
	if err := hub.Send("writer", msg); err != nil {
		panic(err)
	}

	raw, ok := hub.Recv("writer")
	if !ok {
		return
	}
	fmt.Println(raw)
}
```

`Recv` is non-blocking. `Send` fails when the target is missing or its inbox is full, so callers must decide whether to retry, drop, or apply backpressure. `NewHub()` uses an in-memory no-op store; choose `mailbox/sqlitestore` when tasks, projects, roles, and conversations must survive a restart.

## Documentation

- [English reference](../../docs/reference/mailbox.md)
- [中文参考](../../docs/reference/mailbox.zh-CN.md)
- [System architecture](../../docs/architecture/mailbox-agent-system.md) — process boundaries, state transitions, persistence, events, and failure cases.
