# Agent runtime checkpoints

`pkg/runtime` records `pkg/agent` state at each committed message, then uses that journal to resume, rewind, or inspect one Agent session. It does not make external tool effects transactional: if a tool changes another system before its result is committed, recovery can repair the conversation but cannot undo that external change.

## Run and recover

```go
store, err := runtime.NewFileStore("/var/lib/modu/checkpoints")
if err != nil {
	return err
}

rt := runtime.New(agent.NewAgent(cfg), store, "session-123")

// Start a new prompt. Committed messages are checkpointed automatically.
if err := rt.Run(ctx, "do the thing"); err != nil {
	return err
}

// A later process can continue an unfinished session.
resumed, err := rt.Resume(ctx)

// Restore checkpoint 3 as a new append-only branch head.
head, err := rt.Rewind(ctx, 3)
```

`Resume` loads the latest checkpoint, removes the synthetic trailing failure marker, repairs an interrupted Tool Call without a committed result, and continues the Agent when work remains. It returns `false` without running the Agent when the session is already complete.

`Rewind` does not delete newer history. It appends a paused checkpoint whose `ParentSeq` points to the selected sequence, restores that state into the Agent, and lets the caller continue along the new branch.

Use `History`, `Latest`, or the `Store` interface to inspect checkpoints.

## Stores

| Store | Use it for | Persistence behavior |
|---|---|---|
| `NewMemoryStore()` | Tests and process-local sessions | Lost when the process exits |
| `NewFileStore(dir)` | Sessions that must survive restarts | One append-only JSONL file per session |

`FileStore` calls `fsync` after each append. On read, it skips malformed lines, including a partial trailing line left by a crash, and keeps earlier checkpoints available. Implement `Store` to use a database or object store.

## Boundaries and cost

- A checkpoint contains the full message history, not a delta. Storage grows with both message count and conversation length.
- The default listener writes once per committed message, plus a terminal checkpoint.
- `Run` and `Resume` serialize through the underlying Agent, so repeated calls do not rerun already committed tools.
- Conversation recovery and external side-effect recovery are different problems. Tools that require exactly-once behavior need their own idempotency key or transaction boundary.
