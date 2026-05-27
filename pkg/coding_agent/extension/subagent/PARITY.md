# Subagent extension — pi-subagents parity tracker

This file tracks how `pkg/coding_agent/extension/subagent` lines up against the
TypeScript reference at `github.com/openmodu/pi-subagents`. The high-level
project log lives in `pkg/coding_agent/PROGRESS.md`; this file is the
subagent-specific subset, organized by capability area with file pointers into
pi-subagents so the next iteration has an unambiguous source of truth.

The reference snapshot is the `pi-subagents` tree at this repo's local clone
(`/Users/ityike/Code/go/src/github.com/openmodu/pi-subagents`), aligned to pi
`SubagentParams` in `src/extension/schemas.ts` and `src/shared/types.ts`.

## What's done

### Execution modes
- `single` (default), `parallel`, `chain`, top-level `tasks` (alias). Auto-mode
  selection when only `chain` / `parallel` / `tasks` is provided.
- `count` expansion (parallel/tasks items only — chain steps reject `count`).
- Top-level and per-group `concurrency` for parallel/tasks and chain parallel
  groups.
- Mixed chain (`chain: [{agent, task}, {parallel: [...]}, ...]`): parallel
  groups receive `{previous}`, and the aggregated group output feeds the next
  chain step.
- `chain[].failFast: true` on a parallel group cancels in-flight siblings via
  ctx on the first failure and aborts the surrounding chain.
- `worktree: true` per-call (top-level parallel/tasks and on a chain[].parallel
  group) forces every affected child's `ForkOptions.Isolation` to "worktree",
  overriding the profile's own isolation.
- Template substitution: `{previous}` (prior step), `{task}` (chain's first
  sequential step's raw task), `{chain_dir}` (resolved shared chain dir).
- `context: "fresh" | "fork"` per call plus profile-level `default_context`.
- Per-call overrides: `model`, `skill`, `cwd`, `reads`, `progress`, `output`,
  `outputMode`, `chainDir`, `async`.
- Profile-level `Background`, `Isolation`, `Skills`, `MemoryScope`,
  `MaxTurns`, `PermissionMode`, `ThinkingLevel`, `DisallowedTools`,
  `DefaultReads`, `DefaultProgress` propagated through `ForkOptions`.
- `force_top_level_async` extension config: defaults a top-level single-mode
  call's `async` to true when the caller omits it. Explicit `async:false`
  still wins.
- Top-level batch async (`mode:parallel|chain + async:true`, or any top-level
  parallel/chain when `force_top_level_async` is set and `async` is omitted):
  reserves a synthetic `subagent-batch-N` task id, dispatches the rest in a
  goroutine, and returns the id immediately. `subagent status` overlays
  these batch tasks on top of the host's per-child snapshot list.
- Per-call `thinking` override on single, parallel item, and chain step;
  empty inherits the profile's ThinkingLevel.
- Init-time stale-run reconciler: any recovered subagent task whose
  `status.json` still says `running` is rewritten to `status: stale` (it
  belongs to a goroutine that died with the previous session) and shown
  with a `[stale]` label in the current session's status output.
- `includeProgress: true` appends the call's `progress.md` body to the
  tool result after a `## Progress` marker. Works in single, parallel,
  chain, and batch async modes.

### Async / background runs
- `async: true` single override forces background; `async: false` overrides a
  profile's `Background: true`.
- Per-run directory under the project runtime with `status.json` and child
  `session.jsonl`.
- Recovery from on-disk status when the aggregate background task list is
  empty.
- Background task tree rendering with `parentId` (`subagent status`).
- `resume` revives completed/failed/interrupted tasks by replaying child
  metadata as a new background follow-up.
- `interrupt` cancels an in-process running background task.

### Output / progress / reads
- `output` writes the child's final reply to a file. `outputMode: "file-only"`
  collapses the reply to a saved-file reference.
- `reads: [...]` prepends an `[Read from: ...]` instruction; `reads: false`
  disables profile defaults.
- `progress: true` instructs the child to maintain `progress.md` in
  `chainDir` (or the default runtime `subagents/` dir). First call writes the
  initial template; later calls update it.
- `chainDir` shared across a chain or parallel batch for `progress.md` plus
  relative `reads` resolution.

### Discovery / depth
- Loader scans the host's standard agent paths or an explicit `agents_dir`
  config.
- `max_depth` enforced via context propagation; `max_depth: 0` disables
  execution entirely.

### Management / control actions
- `action: "list"` — sorted profile list with description + source. Accepts
  `agentScope: "user" | "project" | "both"` to filter by `SubagentDefinition.Source`.
- `action: "get"` — full detail for one profile, including frontmatter +
  system prompt body. Honors the same `agentScope` filter — out-of-scope
  matches return a scope-aware not-found error.
- `action: "create"` — writes a new `.md` profile, sanitises the name to
  kebab-case, picks the target dir from `cfg.AgentsDir` or `scope`
  (`user`/`project`), then reloads the loader.
- `action: "update"` — merges `config` keys into an existing profile's
  frontmatter, optionally replaces the body via `systemPrompt`; rejects
  rename and scope changes (delete + recreate to migrate).
- `action: "delete"` — removes the profile file and reloads.
- `action: "status"` — background task tree or one-task detail by id/prefix.
- `action: "interrupt"` — cancel a live in-process background task.
- `action: "resume"` — restart a finished background task with a follow-up
  message.
- `action: "doctor"` — read-only setup diagnostics. Reports profile count,
  per-source breakdown, agents_dir / host agent dir / host cwd, the
  subagents runtime dir + existence check, background subagent task count,
  default_model, max_depth, timeout_seconds, force_top_level_async.

### Compatibility / commands
- Legacy `spawn_subagent` tool still works as a thin alias backed by
  `ExtensionAPI.ForkSession`.
- Slash commands: `/run`, `/parallel`, `/chain`, `/subagents-doctor`.

### Tests
- `pkg/coding_agent/extension/subagent/subagent_test.go` covers all of the
  above (per-call overrides, parallel concurrency, chain `{previous}` flow,
  chain parallel groups, `count`, `concurrency` validation, `cwd` forwarding
  across modes, output file-only, reads/progress placement, async overrides,
  max_depth, profile field propagation, etc.).
- End-to-end coverage in `pkg/coding_agent/coding_agent_test.go`:
  `TestSubagentContextForkSeedsChildMessages`,
  `TestSubagentCwdBindsChildWorkingDirectory`,
  `TestSpawnSubagentBackgroundAndTaskOutput`.

## What's missing

The remaining items are not "small patches" — each one needs host- or
infrastructure-level work that the extension can't ship alone. They stay
parked here rather than getting half-implemented inside the extension.

### G. `control` overrides (long-running / needs-attention notifications)
**Pi ref:** `src/runs/shared/long-running-guard.ts`,
`src/runs/shared/completion-guard.ts`,
`src/runs/shared/subagent-control.ts`,
`src/extension/control-notices.ts`, schema `ControlOverrides`.

Pi tracks per-run heuristics (`needsAttentionAfterMs`, `activeNoticeAfterMs`,
`failedToolAttemptsBeforeAttention`, etc.) and pushes notices through events
and intercom back into the parent agent's flow. Implementing the
configuration surface alone would be misleading: without a notification
loop that the parent's LLM context actually consumes, the notices are
write-only and don't change behavior. Needs a host design for "feed
subagent control notices back as parent context" before the schema is
worth wiring.

### H. Intercom bridge
**Pi ref:** `src/intercom/*` (~750 LOC).

Agent-to-agent message bus between parent and background children. New
infrastructure addition, not a patch — there is no analogue in the current
host. Stays deferred until there is a concrete use case (currently the
only motivation is pi parity for its own sake).

### I. `clarify` TUI preview/edit
**Pi ref:** `src/runs/foreground/chain-clarify.ts`,
`src/slash/slash-commands.ts` (`/clarify` flow).

TUI-driven preview/edit before execution. The execution side could be
sketched in the extension, but the TUI half is the point of the feature.
Wait for the matching TUI scaffolding.

### K. Misc params on `SubagentParams` (partial)
- `includeProgress` — **done** (see "What's done" above).
- `sessionDir` — needs a new `ForkOptions.SessionDir` field plus
  per-task plumbing in the host's `backgroundTaskManager`. Pure
  extension-layer change cannot redirect where child session logs land.
- `share` (upload session to Gist) — needs a host-level session sharing
  pipeline. Out of scope.
- `artifacts` — pi-subagents writes per-run debug artifacts (input.json,
  output.json, metadata.json) under its own results dir. We don't have a
  matching artifact pipeline; the parameter currently has no meaningful
  effect.

## Explicitly deferred / out of scope

- pi's `npm install`-style package distribution of agents.
- pi's GitHub Gist `share` upload pipeline.
- pi's TUI rendering layers (`src/tui/render*.ts`, slash live state).
- Detailed intercom bridge wire format.

## Quick reference — pi source tree

```
src/extension/index.ts            tool registration + lifecycle wiring
src/extension/schemas.ts          SubagentParams shape (single source of truth)
src/extension/config.ts           ExtensionConfig loader
src/extension/doctor.ts           doctor implementation
src/agents/agents.ts              agent discovery
src/agents/agent-management.ts    CRUD actions
src/agents/skills.ts              skill discovery / merging
src/runs/foreground/             chain + parallel + single sync execution
src/runs/background/             async dispatch, status, resume, watcher
src/runs/shared/                  worktree, intercom helpers, prompt runtime
src/intercom/                     intercom bridge transport
src/slash/                        slash-command + prompt-template bridges
src/shared/types.ts               ExtensionConfig, SUBAGENT_ACTIONS, events
```
