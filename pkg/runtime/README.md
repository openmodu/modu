# pkg/runtime

A rewindable, resumable, re-entrant execution layer over `pkg/agent`.

Agents are stateful: when a run fails midway, the work done so far is usually
lost. This package makes a run **durable** by journaling a checkpoint at every
commit boundary, so a session can be recovered, replayed, or branched.

## How it works

The agent loop is already event-sourced — committed messages accumulate in
agent state on each `message_end` event. `runtime` subscribes to those events and
appends a `Checkpoint` (a serialized snapshot of the conversation) to an
append-only `Store`. The loop itself is untouched.

- **Resumable** — after a crash or provider error, `Resume` loads the latest
  checkpoint, rebuilds the conversation, and continues the loop. It strips the
  synthetic failure marker and repairs any tool call that was interrupted before
  its result was committed, so the next model turn is always well-formed.
- **Rewindable** — `Rewind(seq)` forks a new head from an earlier checkpoint.
  Storage is append-only, so history is never lost.
- **Re-entrant** — `Run`/`Resume` can be called repeatedly. Continuing from
  committed messages means already-executed tools are not run again, and resuming
  a completed session is a no-op.

## Usage

```go
store, _ := runtime.NewFileStore("/var/lib/modu/checkpoints")
rt := runtime.New(agent.NewAgent(cfg), store, "session-123")

// Fresh run; checkpoints are written automatically.
err := rt.Run(ctx, "do the thing")

// In a later process, recover from wherever it stopped.
resumed, err := rt.Resume(ctx)

// Branch from an earlier point.
head, err := rt.Rewind(ctx, 3)
```

## Stores

- `NewMemoryStore()` — in-process, for tests and ephemeral sessions.
- `NewFileStore(dir)` — one append-only JSONL file per session. A torn trailing
  line from a crash is skipped on read; earlier checkpoints stay intact.

Implement `Store` to back checkpoints with a database or object store.

## Notes / tradeoffs

- Checkpoints hold the full message history (not deltas). Simple and robust;
  fine for normal conversation lengths.
- A checkpoint is written per committed message. Lower the granularity by
  filtering events in `checkpointListener` if write volume matters.
