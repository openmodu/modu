# Coding Agent Progress

This file tracks the parity work for `pkg/coding_agent` against the coding-agent reference tree.

## Current Assessment

High-priority gaps identified before this round:

- Session persistence was split across two systems:
  `session.Manager` only captured user prompts, model changes, and compaction records.
  `persistence.go` could save full message snapshots, but was not wired into the main prompt flow.
- Skills were loaded and injected as plain prompt text into the main session.
  They were not executed in an isolated sub-agent context.
- Context file discovery existed in `resource.Loader`, but the discovered files were not fed into `SystemPromptBuilder`.
- Test coverage around `skills`, `subagent`, `resource`, and persistence behavior was thin.

## Completed In This Round

- Managed session worktrees now use `<agentDir>/worktrees/<uuid>/<repo>` paths
  and a `modu-code/<repo>-<id>` branch so active editing can live in an
  isolated checkout instead of a detached temporary tree.
- Moved runtime state persistence from `runtime/<project>/state.json` into
  `runtime_state` sidecar entries in the current session JSONL, and made agent
  runtime directories lazy so startup no longer pre-creates empty feature trees.
- Moved approved plan persistence from `plans/latest.md` plus revision files into
  `plan_snapshot` sidecar entries in the current session JSONL.
- Removed the session-level `/state` and `/settings` built-in slash commands
  that exposed runtime/config snapshots containing harness details. TUI-local
  `/settings` remains owned by the UI layer.
- Added file-backed multi-model configuration for `cmd/modu_code`, runtime `/model` switching, and active-model persistence.
- Added a TUI model picker for `cmd/modu_code` so `/model` opens an arrow-key selection flow.
- Wired discovered context files into the system prompt build path.
- Wired agent `message_end` events into persistence so assistant and tool-result messages are recorded.
- Hooked `SaveMessages()` into the live session flow so `messages.jsonl` is generated from real prompts.
- Replaced the split message snapshot/session timeline with a pi-style append-only JSONL session manager: versioned session header, timestamped session files, `leafId` branch navigation, display-name entries, labels, recent-session loading, and session listing metadata.
- Added explicit `/skill` handling that pins the named skill instructions onto the main agent turn.
- Recorded thinking-level changes into the session timeline.
- Ensured `~/.coding_agent/agents` is created alongside skills and sessions.
- Fixed the failing `read` tool offset formatting test by using tab-separated line numbers.
- Expanded subagent frontmatter support with `disallowed_tools`, `thinking`, and `max_turns` parsing.
- Applied `disallowed_tools` and `thinking` during subagent execution.
- Expanded subagent frontmatter support with `skills` and `memory`.
- Added prompt augmentation for subagents so referenced skills and selected memory scopes are injected into the effective system prompt.
- Added minimal `permission_mode` support for subagents.
- Implemented `read-only` permission mode to strip mutating tools from subagent execution.
- Added a session-scoped `todo_write` tool and integrated it into default session tool registration.
- Applied `max_turns` during subagent execution by enforcing a per-subagent assistant-turn cap.
- Added `effort` support for subagents and mapped it onto the available thinking levels when `thinking` is not explicitly set.
- Added `background` support for subagents through asynchronous execution in `spawn_subagent`.
- Added a session-scoped `task_output` tool for inspecting background task results.
- Added minimal plan-mode tools: `enter_plan_mode` and `exit_plan_mode`.
- Added minimal worktree-mode tools: `enter_worktree` and `exit_worktree`.
- Added `isolation: worktree` support for subagents by running them inside temporary git worktrees with cwd-bound tools.
- Refreshed the session system prompt dynamically when plan mode or worktree mode changes.
- Added exported session accessors for discovered subagents, current todos, background tasks, plan mode, and active worktree so external frontends can inspect agent state.
- Added manual validation slash commands to `cmd/modu_code` for `/agents`, `/todos`, `/tasks`, `/plan`, and `/worktree`.
- Optimized context loading so project instruction files are discovered hierarchically from repo root to the active working directory.
- Added prompt-level context deduplication and size budgeting to reduce token waste from repeated or oversized instruction files.
- Flattened prior conversation summaries during compaction so repeated compaction does not recursively summarize old summary envelopes.
- Added dynamic nested-context injection triggered by file/tool access so deeper path-specific instructions can be loaded on demand during a turn.
- Extended dynamic context triggers beyond `read/edit/write` to include `grep`, `find`, and `ls` path discovery.
- Aligned `/goal` UI interactions: hidden follow-up messages are transient, TUI confirms goal replacement, and paused goals ask before resuming once the UI is ready.
- Hardened `/goal` parity with objective length limits, goal-store schema validation, shutdown-time accounting flush, local-time summaries, hashed no-session store keys, and compact user-facing token counts.
- Split `/goal` continuation queue decisions into pure idle/after-agent-end helpers, expanded store edge-case coverage, and preserved goal tool `isError` details for model-readable correction paths.
- Marked dynamic nested-context messages as transient so they do not persist into long-term session history or saved transcripts.
- Added a lightweight session-scoped harness hook layer around tool execution.
- Added a harness-only hint side channel by stripping `<modu-code-hint .../>` tags from tool-visible text output while recording them for the host runtime.
- Removed the agent-facing `harness_paths` tool; harness runtime paths remain available through session/runtime state APIs.
- Persisted the latest recorded plan to a harness-managed plan file under the runtime `plans/` tree.
- Extended harness hooks with compaction lifecycle callbacks (`PreCompact` / `PostCompact`).
- Extended harness hooks with subagent lifecycle callbacks (`SubagentStart` / `SubagentStop`).
- Persisted per-tool text result artifacts under the harness-managed `tool-results/` tree.
- Added `settings.json`-driven harness policy for blocking tools before execution.
- Added `settings.json`-driven toggles for harness hint capture and tool-result artifact persistence.
- Added `settings.json`-driven JSONL event logging for tool-use, compaction, and subagent lifecycle events.
- Added `settings.json`-driven latest-artifact fan-out for tool-use, compaction, and subagent lifecycle snapshots.
- Added `settings.json`-driven bridge directories that emit one structured JSON file per tool-use, compaction, and subagent lifecycle event.
- Added controlled `settings.json`-driven host action dispatch for harness lifecycle events via explicit `exec` actions.
- Added a runtime index file under the harness runtime tree that records resolved output targets and the latest event per category.
- Added explicit `enableActions` permission gating so host actions stay disabled unless opt-in is set.
- Added template expansion for harness action command arguments and working directory fields.
- Added harness-managed action status artifacts under the runtime tree so action failures are observable.
- Added `timeoutMs` support for harness `exec` actions.
- Added stdout/stderr capture into harness action status artifacts.
- Added working-directory annotations to subagent prompts so delegated agents see the same cwd that their bound tools use.
- Removed the `modu_code` local mailbox runtime path so user-facing delegation is centered on `spawn_subagent`.
- Added README usage documentation for harness runtime outputs and action configuration.
- Added safe harness output defaults so logs/artifacts/bridge work without manual settings.
- Added automatic global `settings.json` bootstrap when no config exists.
- Changed harness actions to auto-enable by default, while still allowing explicit `enableActions: false` opt-out.
- Added config validation for harness action policy, including default absolute-command enforcement.
- Extended harness action policy with directory-prefix checks and max-timeout limits.
- Added effective merged-config export for sessions and surfaced it in `cmd/modu_code`.
- Added a default config template exporter so frontends can show the generated baseline configuration.
- Split harness action status output into explicit `stdout` and `stderr` fields while keeping merged `output`.
- Added per-action retry semantics with configurable attempt count and delay.
- Added per-action `onFailure` handling so failed actions can stop later actions in the same event batch.
- Added unified runtime state snapshots under `runtime/<project>/state.json`.
- Added top-level feature gates for core runtime capabilities such as memory, todos, task output, plan mode, worktree mode, subagents, and harness actions.
- Added top-level permission rules for tool allow/deny and bash command prefix allow/deny.
- Scoped interactive bash always-allow/deny approvals to the exact command, and made dangerous bash writes bypass broad allow rules before execution.
- Extended harness events and outputs with `session` and `permission` categories.
- Added a dashboard view in `cmd/modu_code` that summarizes runtime state, latest events, and action statuses.
- Added subagent frontmatter support for `harness_block_tools` and merged it into effective tool blocking.
- Added `cmd/modu_code` inspection commands for harness hints and runtime paths.
- Added `cmd/modu_code` inspection commands for configured harness logs, latest artifacts, and bridge directories.
- Added `cmd/modu_code` command-level tests that exercise `/runtime`, `/logs`, `/artifacts`, and `/bridge` through the real slash-command path.
- Added `cmd/modu_code` smoke tests for print-mode output and rpc-mode request/response flow.
- Added an integration-style regression that runs a real prompt through tool execution and verifies harness artifact emission end-to-end.
- Fixed explicit `/skill prompt` execution so skill-pinned turns emit normal agent events for TUI, print, and RPC subscribers.
- Added working-directory context to explicit slash-skill prompts so git-oriented skills inspect the active project instead of guessing the home directory.
- Added focused tests for:
  session persistence after prompt/tool execution
  explicit slash-skill execution
  subagent tool filtering and thinking behavior
  subagent skill and memory prompt augmentation
  subagent read-only permission mode filtering
  todo_write tool behavior
  subagent max_turns enforcement
  subagent effort mapping
  background subagent execution with task_output
  plan mode tool toggling
  worktree enter/exit
  subagent worktree isolation
  harness hook execution
  harness hint stripping/storage
  harness runtime paths and plan file access
  harness compaction lifecycle hooks
  harness subagent lifecycle hooks
  harness tool-result artifact persistence
  config-driven harness tool blocking
  config-driven disabling of harness hint capture and tool-result persistence
  config-driven harness JSONL event logging
  config-driven latest artifact snapshot files
  config-driven event bridge directories
  config-driven host action dispatch
  runtime index generation
  prompt -> tool -> harness artifact integration
  action permission gating
  action template expansion
  action failure status artifacts
  action timeout handling
  action output capture
  action directory policy validation
  action retry success flow
  action onFailure validation
  action stop-on-failure flow
  runtime state snapshot persistence
  feature-gated tool registration
  permission-rule-driven harness artifacts
  automatic safe harness defaults
  automatic settings bootstrap
  automatic action enablement with explicit opt-out
  config validation for harness action policy
  effective config export
  default config template export
  subagent frontmatter parsing for `harness_block_tools`
  default core tool alignment with upstream coding-agent (`read`, `bash`, `edit`, `write`)
  full `AllTools` restoration including `ls` for explicit opt-in use
  extension API cleanup for first-class hook registration, command descriptions, event dispatch, and removal of the unused `ToolDefinition` wrapper
  unified resource discovery for context files, skills, prompt templates, and local resource packages
  prompt-template slash commands with `{{input}}` / `{{args}}` expansion
  `/context` and `/prompts` visibility for resource packages and prompt templates
  pi-style session JSONL shape and session-manager behavior for local persistence/resume/listing
  reusable session list-all, fork-from-session, branch extraction, and safe delete APIs, plus `/session`, `/sessions`, `/resume`, `/fork-session`, and `/branch-session`
  RPC commands and client helpers for listing, deleting, forking, and extracting sessions
  TUI slash routing for `/tree` and `/fork <entry-id>` so session tree operations are reachable interactively
  session APIs for all-model listing, session-scoped model ranges, leaf-id cloning, and dynamic resource reloads used by the TUI parity work
  interactive session-tree nodes, branch-summary restoration during tree navigation, and TUI tree search/branch controls
  TUI resource/settings selector polish for consistent page navigation and visible resource source/path metadata
  TUI session-tree row polish with short entry IDs, stable type labels, labels, and branch counts
  TUI idle status line polish with model, token, plan, and worktree state ahead of common hints
  TUI hotkey help alignment for selector paging, tree branch/summary controls, and resource commands
  Worktree lifecycle status API and richer `/worktree status` output for active path, original cwd, current cwd, and path existence
  Plan lifecycle status API and richer `/plan status` output for latest plan artifact and todo counters
  `/plan show` command for reading the latest approved plan artifact from the CLI/TUI slash path
  Managed worktree listing API and `/worktree list` output with active/idle and existence markers
  `/plan clear` command for removing the latest plan artifact and clearing approved-plan todos
  `/worktree cleanup` command for removing inactive managed worktrees while preserving the active worktree
  Session tree branch-summary fallback labels that show the historical source entry when no explicit label exists
  Plan revision snapshots plus `/plan history` for tracing approved plan iterations
  Active worktree diff API and `/worktree diff` for reviewing isolated changes before handoff
  Shared TUI selector headers with current position, filtered/total counts, search query, and mode
  Persistent `/goal` extension parity for session-scoped goal files, `create_goal`/`get_goal`/`update_goal` tools, hidden follow-up continuation, token/time accounting, and `budgetLimited` wrap-up behavior
  `/goal` subcommand parity for show, set/replace objective, pause, resume, and clear while keeping the earlier `/goal-*` aliases
  Goal runtime-state exposure plus TUI idle-status indicators for pursuing, paused, unmet, and achieved goals
  Extension notification events so goal command output is visible inside TUI scrollback instead of only stderr
  pi-goal command/tool/accounting parity for `/goal` parsing, replacement confirmation, clear feedback, completion accounting, resume-only paused prompts, and compact user-facing goal formatting
  pi-goal protocol parity for goal tool top-level `isError`, select-style extension prompts, hidden follow-up message metadata, and seconds-based continuation budget prompts
  Migrated subagent parity work onto the first-class `extension/subagent` tool path, including standard `.coding_agent/agents` discovery, fork lifecycle harness events, runtime-state exposure, and updated tests away from legacy `spawn_subagent` expectations
  Reintroduced legacy `spawn_subagent` as an `extension/subagent` compatibility alias backed by `ExtensionAPI.ForkSession`, so the old tool surface no longer depends on direct `CodingSession` registration
  Added first-pass `subagent` management actions for `list`, runtime background `status`, and read-only `doctor` diagnostics
  Added extension-owned `/run`, `/parallel`, `/chain`, and `/subagents-doctor` slash commands backed by the same subagent fork path
  Persisted background task snapshots to the project runtime so async subagent results survive session recreation and remain readable through `task_output` / `subagent status`
  Added single-call `subagent({async:true})` / `async:false` background override support to move closer to pi-subagents' caller-controlled async launch model
  Added first-pass `subagent` async control actions: `interrupt` cancels live background tasks in-process, and `resume` restarts completed/failed/interrupted tasks as background follow-ups using persisted agent/task metadata
  Added per-run async subagent directories with `status.json` under the project runtime, plus recovery from those status files when the aggregate background task list is missing
  Added child `session.jsonl` persistence for async subagent runs and wired `resume` to seed follow-up runs with the previous child transcript
  Rendered `subagent status` as a parent/child run tree using persisted `parentId`, so resumed follow-up runs are visible under their source run
  Added `output` / `outputMode` support for execution-mode `subagent` calls, including per-step output files in parallel and chain runs plus `file-only` compact references
  Extended async subagent completion to write configured `output` files and expose `output_file` through task output, status, and runtime snapshots
  Added first-pass `reads` / `progress` support for execution-mode `subagent` calls and profile defaults, including shared `chainDir` progress files for parallel and chain runs
  Enforced `extension/subagent` `max_depth` at runtime by carrying nested subagent depth through forked child contexts; `max_depth: 0` now disables execution calls
  Added execution-mode `context: "fresh"|"fork"` plus `default_context` profile support, and per-call `model` / `skill` overrides for single, parallel, and chain subagent calls
  Added pi-style top-level `tasks` parallel execution, per-item `count` expansion, automatic mode selection for `tasks`/`parallel`/`chain`, and top-level `concurrency` limiting
  Added mixed chain parallel groups (`chain: [{parallel: [...]}]`) where group children receive `{previous}` and the aggregate group output feeds the next chain step
  Added per-call `cwd` support for execution-mode subagent calls, including child environment prompts, file/shell tool rebinding, and `WithCwd` passthrough for wrapped custom tools
  Recorded pi-subagents parity status under `pkg/coding_agent/extension/subagent/PARITY.md` covering done items, prioritised gaps, and deferred areas (intercom, clarify, packaged agents)
  Added subagent tool-API agent CRUD: `get` returns a profile's full detail, `create` writes a new `.md` profile (kebab-case name, `cfg.AgentsDir` or `scope` target dir) and reloads the loader, `update` merges frontmatter + optional body and reloads, `delete` removes the file and reloads
  Extended chain/parallel/single task substitution with pi-style `{task}` (chain's first sequential step's raw task) and `{chain_dir}` (resolved shared chain dir) on top of the existing `{previous}` flow
  Added `chain[].failFast` on parallel groups: cancels in-flight siblings via ctx on the first failure and aborts the surrounding chain at that step
  Added `force_top_level_async` extension config: defaults a top-level single-mode call's `async` to true when the caller omits it; explicit `async:false` still wins
  Enriched subagent `doctor` output with profile source breakdown, subagents runtime dir + existence check, background subagent task count, and the active `force_top_level_async` flag
  Added per-call `worktree: true` for top-level parallel/tasks and chain[].parallel groups: forces every affected child's `ForkOptions.Isolation` to "worktree", overriding the profile's own isolation
  Added `agentScope: "user"|"project"|"both"` filter on subagent `list` / `get` so callers can scope discovery results by `SubagentDefinition.Source`; default "both" preserves prior behavior
  Removed dead inline `pkg/coding_agent/tools/spawn_subagent.go` plus the deprecated `FeatureConfig.SpawnSubagentTool` field and accessor; the active `spawn_subagent` tool surface is the extension-owned compatibility alias
  Added per-call `thinking` override on single mode, parallel items, and chain steps; empty inherits the profile's ThinkingLevel via the new `effectiveThinking` helper
  Added init-time stale-run reconciler in the subagent extension: any recovered subagent task still reporting `running` from a prior session is rewritten on disk to `status: stale`, decorated as `[stale]` in `status` output, and counted in `doctor` (which downgrades to warning when stale runs exist)
  Added top-level batch async for `mode:parallel|chain + async:true` (and the omitted-async `force_top_level_async` path): the extension reserves a synthetic `subagent-batch-N` task id, runs the dispatch in a goroutine using a background-rooted context, and merges the batch task into `status` output so callers can poll the aggregated result
  Added pi-style `includeProgress: true` so the subagent tool result appends the `progress.md` body after a `## Progress` marker; works in single, parallel, chain, and batch async modes
  Added pi-style `artifacts: true` per-run debug bundle (input/output/metadata JSON under tool-results/<project>/subagents/artifacts/<runID>/); for batch async the runID matches the synthetic batch task id so the on-disk bundle and the caller-visible id agree
  Added pi-style `sessionDir` override for background children's session.jsonl/status.json via a new `ForkOptions.SessionDir` field, plumbed through `backgroundTaskManager.CreateWithMetadataInDir`
  Added pi-style `control: { activeNoticeAfterMs }` skeleton for batch async runs (one-shot timer that emits api.Notify on threshold); remaining ControlOverrides fields are accepted in schema but not yet honored at runtime
  Added pi-style `clarify: true` non-TUI gate: builds a structured preview of the planned dispatch and gates it on `api.Confirm`; denial returns the preview as the tool result without dispatching
  Added pi-style file-based intercom MVP: new `subagent_intercom_send` tool writes to `tool-results/<project>/subagents/intercom/<taskID>.jsonl`, and `action: "intercom"` reads the inbox; full publisher/subscriber pipeline + auto-attach left deferred per PARITY.md
  Expanded control overrides: added `needsAttentionAfterMs` second timer, `notifyOn` event-type filter, and `notifyChannels` routing including a new `intercom` route that appends the notice into the batch task's JSONL inbox; turn/token/tool-attempt-driven triggers stay deferred until host exposes the counters
  Added intercom auto-attach for batch async children: each child gets an `# Intercom` system-prompt section naming its batch task id and pointing at `subagent_intercom_send`, gated by a new `intercom_mode` config (`off`/`fork-only`/`always`, default `always`)
  Added the first Lua `extension/workflow` implementation: a builtin `workflow` tool with safe Lua runtime setup, `meta` / `phase` / `log`, JSON helpers, budget view, `agent()` mapped to `ExtensionAPI.ForkSession`, `parallel()` with concurrency limiting and ordered results, and a `pipeline()` runner with per-item scheduling, serialized Lua state access, stage failure isolation, and nested-pipeline protection. Focused tests cover runtime validation, sandbox-hidden libraries, ForkOptions mapping, branch failure as JSON null, tool updates/details, pipeline order/failure semantics, and cmd/modu_code builtin registration.

## Still Missing

- Lua workflow orchestration still needs the later acceptance work from
  `docs/lua-workflow-orchestration-plan.md`: real `modu_code -p` workflow
  cases against an actual configured model and final M6 compatibility/progress
  records.
- Deeper plan-mode revision flows beyond the current approval/rejection gate
- Advanced worktree flows such as diff/merge handoff from isolated worktrees back to the original checkout
- Full pi-compatible TypeScript extension/package ecosystem, including remote npm/git package install, theme resources, rich UI extension context, provider hooks, and hot reload
- Remaining pi TUI polish outside the now-covered selector/status/session-tree basics

## Suggested Next Steps

1. Continue Lua workflow orchestration from the implemented M1-M4 slice toward
   the remaining M5-M6 validation gates in `docs/lua-workflow-orchestration-plan.md`.
2. Improve plan/worktree semantics beyond the current minimal implementation.
3. Expand integration coverage around background tasks, tool replacement, and session switching.
4. Add richer host action policies such as backoff variants, command/dir allowlist presets, and per-action failure handling.
5. Keep refining the runtime state/control plane so more session resources are represented as first-class harness-managed artifacts instead of ad hoc prompt/session state.
