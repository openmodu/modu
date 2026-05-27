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
- `artifacts: true` writes per-run input/output/metadata JSON under
  `tool-results/<project>/subagents/artifacts/<runID>/`; the tool reply
  advertises the directory in an `[artifacts: <path>]` tail. For batch
  async the runID is the synthetic batch task id so the on-disk bundle
  and the caller-visible task id agree.
- `sessionDir` overrides where background children's session.jsonl /
  status.json land (relative to parent session cwd). Propagated through
  `ForkOptions.SessionDir` and a new `taskManager.CreateWithMetadataInDir`
  host method.
- `control: { activeNoticeAfterMs, ... }` skeleton: when set, batch async
  runs schedule a one-shot timer that calls `api.Notify` if the run is
  still in flight past the threshold; the rest of pi's ControlOverrides
  fields are accepted but not yet honored.
- `clarify: true` invokes the host's `api.Confirm` with a structured
  preview of the dispatch (mode + agent/task lists + flags) before any
  fork runs. Denial returns the preview as the tool result without
  dispatching. No in-line editor — see deferred section.
- File-based intercom MVP: every task can receive messages in
  `tool-results/<project>/subagents/intercom/<taskID>.jsonl`. Writers use
  the new `subagent_intercom_send` tool (registered alongside the
  subagent tool); readers use `action: "intercom"` with the same task id.

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

Most of pi's surface now has an analog in the extension. The remaining
pieces are deliberate omissions where pi's design depends on infrastructure
we have not built yet.

### G (partial). Control overrides — `needsAttention*` / `failedToolAttempts*`
**Pi ref:** `src/runs/shared/long-running-guard.ts`,
`src/runs/shared/completion-guard.ts`, `src/runs/shared/subagent-control.ts`,
`src/extension/control-notices.ts`, schema `ControlOverrides`.

`activeNoticeAfterMs` is wired through `api.Notify`. The pi sibling fields
(`needsAttentionAfterMs`, `activeNoticeAfterTurns`, `activeNoticeAfterTokens`,
`failedToolAttemptsBeforeAttention`, `notifyOn`, `notifyChannels` beyond
"event") are accepted by the schema but ignored at runtime — they require
per-run activity tracking (token/turn accounting, tool failure callbacks)
that the extension has no visibility into today. Filling them in cleanly
needs a host design for feeding subagent control notices back into the
parent's LLM context.

### H (partial). Full intercom bridge
**Pi ref:** `src/intercom/*` (~750 LOC).

The MVP file-based inbox + send tool is in place; pi additionally has a
publisher/subscriber pipeline with retry, mode toggles
(`off`/`fork-only`/`always`), and intercom-aware tool delivery (children
get the bridge auto-attached). Those layers stay deferred until a real
use case justifies the design work.

### I (partial). `clarify` in-line edit
**Pi ref:** `src/runs/foreground/chain-clarify.ts`,
`src/slash/slash-commands.ts` (`/clarify` flow).

We have preview + yes/no confirmation through `api.Confirm`. Pi's full
flow lets the user *edit* the dispatch (rewrite task strings, swap agents)
before approving. That needs a TUI editor; revisit when matching UI
scaffolding lands.

### K. `share` (Gist upload)
**Pi ref:** `src/runs/background/subagent-runner.ts` (share path).

Needs a host-level session sharing pipeline (Gist API client + auth). Out
of scope until that infrastructure exists.

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
