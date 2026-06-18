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
  Added the first Lua `extension/workflow` implementation: a builtin `workflow` tool with safe Lua runtime setup, `meta` / `phase` / `log`, JSON helpers, stable `json.null`, budget view, `agent()` mapped to `ExtensionAPI.ForkSession`, `parallel()` with concurrency limiting and ordered results, and a `pipeline()` runner with per-item scheduling, serialized Lua state access, stage failure isolation, and nested-pipeline protection. Focused tests cover runtime validation, sandbox-hidden libraries, ForkOptions mapping, branch failure as JSON null, tool updates/details, pipeline order/failure semantics, and cmd/modu_code builtin registration. Real configured-model smoke cases covered inventory, parallel fan-out, worktree isolation, and partial-failure handling; compatibility status and intentional differences are recorded in `docs/lua-workflow-orchestration-plan.md`.
  Hardened the Lua workflow follow-up findings: `workflow` now accepts a positive `budget` tool parameter, exposes nil budget totals when unset instead of a fake infinite remaining value, validates `memory_scope` and explicit `max_turns`, guards preview truncation for non-positive limits, and uses a defer-based helper for serialized pipeline stage execution. Tests cover budget tracking/unset semantics, invalid memory scopes, non-positive max turns, invalid budget input, and preview bounds.
  Forked workflow/subagent children now receive explicitly requested `grep`, `find`, and `ls` tools even though those read-only discovery tools remain opt-in for the parent coding session. This fixes real workflow review cases where child tasks requested repository discovery tools and the host previously skipped them as unknown.
  Added a Claude Code dynamic-workflow parity audit to the orchestration plan, comparing the official workflow docs, `pi-dynamic-workflows` source, and the upstream issue audit. Runtime guardrails now align with the important static limits: workflow concurrency is capped at 16, each run defaults to at most 1000 forked agents, and exhausted workflow budgets stop later `agent()` calls from forking. Real token accounting was a follow-up at this point; later `subagent_child_usage` work below wires child usage into workflow budget accounting.
  Added the first script-persistence slice for Claude Code parity: when a session directory is available, each inline workflow script is written to `extensions/workflow/runs/<run-id>/script.lua`, and workflow snapshot/details expose `scriptPath` plus `runDir`. Saved workflow lookup, relaunch-by-path, and resume/replay remain separate backlog items.
  Added tool-level workflow script loading: callers must provide exactly one of `script`, `script_path`, or `name`; `script_path` runs a Lua file relative to the active cwd or by absolute path; `name` resolves saved workflow files from project and user workflow directories. This completes the minimal loader/relaunch primitive while leaving registry metadata and resume/replay for later.
  Added a read-only `/workflows` command for the workflow extension. It lists persisted workflow run scripts for the current session and supports `/workflows show <run-id|latest>` to inspect the saved `script.lua`. Live workflow registry state, pause/kill/resume controls, and a TUI workflow panel remain backlog items.
  Added the workflow disable/config slice for Claude Code parity: `extensions.yaml` can set workflow `config.disabled: true`, `settings.json` can set `disableWorkflows: true`, `/config` exposes a `Dynamic workflows` toggle, and `MODU_CODE_DISABLE_WORKFLOWS=1` or `CLAUDE_CODE_DISABLE_WORKFLOWS=1` prevents registration of both the workflow tool and `/workflows`. Disabling from `/config` removes the current session's workflow tool and slash commands; re-enabling requires a new session or restart to register them again.
  Added the first structured-output workflow slice: `agent()` and `parallel()` task options accept `schema`, inject a final JSON output contract into the child task, parse returned JSON, validate a JSON Schema subset, retry once with validation error context, and return Lua tables on success or `json.null` on final validation failure. This narrows the pi/Claude parity gap while leaving host-level terminating tools for a later API slice.
  Added one-level nested workflow composition: Lua `workflow(nameOrRef, args)` loads a saved workflow name or script path with the same resolver as the top-level tool, passes JSON-compatible args, and shares budget accounting, cancellation, concurrency defaults, and max-agent caps with the parent run. Multi-level nesting and registry-backed saved workflow commands remain backlog items.
  Added startup-time saved workflow slash commands: existing project/user `.lua` workflows are registered as `/workflow:<name> [json-args]`, with project commands taking precedence over user/agent-dir commands. This completed the minimal command invocation path while live registry metadata remained a later slice.
  Added Claude-compatible saved workflow discovery: project `.claude/workflows` directories are searched alongside `.coding_agent/workflows`, and sibling `~/.claude/workflows` is searched alongside agent-dir user workflows.
  Added the bundled `/deep-research <question>` workflow command. It starts a background Lua workflow with scope, parallel research, cross-check, and synthesis phases and uses `/workflows` for management; true Claude-style cited web research still requires host `web_search` / `web_fetch` tools.
  Added workflow authoring guidance to the main agent system prompt when the `workflow` tool is active. Explicit `workflow` / `dynamic workflow` / `ultracode` requests and large fan-out/fan-in tasks now have concrete Lua workflow instructions available to the model; keyword highlighting and approval cards remain follow-ups.
  Added the session-local `/effort ultracode` command for Claude workflow authoring parity. It requires the workflow tool and an xhigh-capable model, sets thinking to `xhigh`, and appends an Ultracode prompt block that asks the model to consider dynamic workflows for every substantive task; `/effort high|medium|low|off` exits the mode. Input highlighting, dismissal shortcuts, and approval cards remain follow-ups.
  Added a pre-run workflow approval gate for Claude safety parity. The workflow tool, saved `/workflow:<name>` commands, `/deep-research`, and `/workflows restart` now call host `Select` with workflow metadata, inferred phases, resource limits, and a Lua script preview before any child agent forks; denial cancels the run. The gate supports run-once, always-allow-this-project, view-raw-script, and cancel choices, with always-allow entries stored in `workflow_approvals.json`; editor actions, permission-mode-specific skips, and Desktop card rendering remain follow-ups.
  Added the first permission-mode-specific workflow approval slice: `permissions.defaultMode: "auto"` treats a run-once workflow approval as remembered for the same project/workflow/script, while `permissions.defaultMode: "bypassPermissions"` skips the workflow launch approval prompt. This narrows Claude Code workflow launch parity while leaving ultracode-specific direct-skip behavior and Desktop approval-card rendering for later host/UI work.
  Added completed workflow run metadata persistence: successful tool and saved-command executions now write `snapshot.json` next to the persisted `script.lua`, and `/workflows list/show` display completed workflow name, agent/error counts, duration/result preview, and script path when metadata is available. Live background task registry, pause/kill/resume, and TUI workflow panel remain backlog items.
  Added the first live workflow management slice for Claude Code parity: `workflow` accepts `async:true` to return a run id immediately, saved workflow slash commands start in the background, an in-memory registry tracks running/stopped/failed/completed runs for the current process, `/workflows list/show` overlays live state with persisted scripts/snapshots, and `/workflows stop <run-id>` cancels running workflows. Pause/resume, cross-process registry recovery, memoized replay, and TUI workflow panel remain backlog items.
  Added a save-from-run slash command for workflow reuse: `/workflows save <run-id|latest> <name> [project|user]` copies a persisted run script into a saved workflow directory with name validation and overwrite confirmation. This closes the non-TUI save path; Claude-style TUI save dialog and host slash-command live refresh remain follow-ups.
  Added nearest-project workflow directory semantics for saved workflows: project workflow load/registration walks from the active cwd up to the git root with nearest directory precedence, user workflows remain the fallback, and project saves now prefer Claude-compatible `.claude/workflows` paths.
  Updated saved workflow save/load precedence for Claude parity: project `.claude/workflows` is preferred over legacy `.coding_agent/workflows`, personal `~/.claude/workflows` is preferred over agent-dir `workflows`, project saves use the nearest existing project `.claude/workflows` or git root `.claude/workflows`, and user saves write `~/.claude/workflows`.
  Added `/workflows restart <run-id|latest>` to relaunch a persisted run script as a fresh background workflow run. This closes the non-TUI relaunch command path while memoized resume/replay remains separate work.
  Added persisted workflow run status metadata: background runs now write `status.json` beside `script.lua` and `snapshot.json` when they start and finish, and `/workflows` reads it so stopped/failed/completed state survives extension/process recreation. This does not recover live running work after process exit or implement memoized resume.
  Added per-agent workflow timing metadata: snapshots now record `startedAt`, `endedAt`, and `durationMs` for each agent, and `/workflows show` renders agent durations. Real per-agent token/cost accounting remains pending because `ForkSession` still returns only child final text.
  Added same-session workflow resume: `/workflows resume <run-id|latest>` restarts a stopped in-memory background workflow against the same script and args, reuses cached completed agent results without another `ForkSession`, and runs only incomplete branches live. Process-recreated runs still require fresh `/workflows restart`, matching Claude's documented fresh-start behavior after exit.
  Added `/workflows pause <run-id>` as the slash-level pause counterpart to Claude's workflow progress controls. It cancels the live run into stopped state with a distinct `pause requested` status reason, and `/workflows resume <run-id|latest>` continues the same in-memory run with completed agent results served from cache.
  Added workflow phase progress summaries for Claude progress-view parity: snapshots now include per-agent `estimatedTokens` plus `phaseSummaries` with agent counts, done/running/error counts, estimated tokens, and elapsed duration; `/workflows show` renders both phase and agent progress. Real token/cost accounting remains pending a richer `ForkSession` result or per-call usage correlation.
  Added opt-in network research tools for workflow/deep-research parity. `web_fetch` fetches HTTP/HTTPS pages and extracts readable HTML text; `web_search` queries a configurable search endpoint (`MODU_WEB_SEARCH_ENDPOINT` or a default public search URL) and returns parsed result titles/URLs/snippets. These tools are not part of the default coding tool set, but forked workflow/subagent children receive them when `tools` explicitly includes `web_search` or `web_fetch`. Tests use local `httptest` servers so CI does not depend on external network permission.
  Added a slash drill-down for workflow agent detail: `/workflows agent <run-id|latest> <agent-id>` shows one agent's workflow, run status, agent status, label, phase, estimated tokens, timing, error/result preview, and prompt. Child events now add `turnTokens`, failed tool-call count, and recent child tool names/errors to the snapshot and drill-down.
  Extended workflow child-event drill-down to retain short `argsPreview` and `resultPreview` fields for recent child tool calls. `/workflows agent <run-id|latest> <agent-id>` now shows those previews under `RecentToolCalls`, which closes the Claude progress-view requirement for recent tool-call args/results at the slash-command layer; full child transcript browsing is handled by the later `/workflows transcript` slice.
  Validation 2026-06-18: `gofmt -w pkg/coding_agent/fork_session.go pkg/coding_agent/plugins/extension/workflow/activity.go pkg/coding_agent/plugins/extension/workflow/commands.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the child tool args/results drill-down slice.
  Added workflow runtime-state exposure for host UIs. `RuntimeState().Extensions["workflow"]` now reports live run counts, latest run metadata, and a running-workflow indicator; the TUI statusbar renders that indicator while background workflows are running. This gives modu a visible progress-view entry point while the full navigable workflow panel and key bindings remain follow-ups.
  Validation 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/statusbar.go pkg/tui/statusbar_test.go pkg/tui/bubble.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the workflow runtime-state/statusbar slice.
  Added a TUI-local workflow overview panel for exact `/workflows`. It renders workflow runtime-state counts, recent live runs, and the existing `/workflows show/agent/pause/stop/resume/restart/agent-stop/agent-restart` command paths without yet adding a selectable keyboard panel. This keeps the Claude progress-view work on a smaller, testable step before implementing Enter/Esc/p/x/r navigation.
  Validation 2026-06-18: `gofmt -w pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go pkg/tui/bubble.go pkg/tui/model.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/tui && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the TUI workflow overview panel slice.
  Extended workflow runtime state with per-run agent summaries (`id`, `label`, `phase`, `status`, token/tool counters, and duration), and updated the TUI `/workflows` overview panel to render the latest run's agent list. This moves the panel data closer to Claude's progress view while keeping selectable drill-down and p/x/r/s key bindings for a later slice.
  Validation 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the workflow runtime-state agent-summary slice.
  Expanded those agent summaries with result/error previews and recent child tool-call previews (`toolName`, error flag, args preview, result preview), and rendered the previews in the TUI `/workflows` overview. This brings the read-only overview closer to Claude's agent drill-down without adding the selectable panel yet.
  Validation 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the workflow overview agent-preview slice.
  Added phase summaries to workflow runtime state and the TUI `/workflows` overview. The overview now shows the latest run's phase-level agent counts, done/running/error counts, estimated tokens, and elapsed duration before the agent list, matching another piece of Claude's progress view while still avoiding the larger selectable panel.
  Validation 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the workflow overview phase-summary slice.
  Added capped `promptPreview` to workflow runtime-state agent summaries and rendered it in the TUI `/workflows` overview. Full prompts remain available through `/workflows agent <run-id|latest> <agent-id>`, keeping runtime-state sidecars compact while preparing the data needed for a future selectable drill-down panel.
  Validation 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the workflow overview prompt-preview slice.
  Added selected-agent workflow controls for Claude progress-view parity. `/workflows agent-stop <run-id|latest> <agent-id>` cancels one running child and lets the Lua workflow continue with that `agent()` returning nil; `/workflows agent-restart <run-id|latest> <agent-id>` cancels the selected child and reruns the same prompt/options as a new agent snapshot. This provides the runtime control surface needed for future TUI `x` and `r` key bindings.
  Added the first selectable TUI `/workflows` panel. Exact `/workflows` now opens a popup run list from workflow runtime state; `j/k` or arrows move selection, `Enter`/right invokes the existing `/workflows show <run-id>` command, and `Esc`/`q` closes the panel. Phase/agent nested selection and `p`/`x`/`r`/`s` key controls remain separate slices.
  Validation 2026-06-18: `gofmt -w pkg/tui/model.go pkg/tui/bubble.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the selectable TUI `/workflows` run-list slice.
  Extended the selectable TUI `/workflows` panel with a run-to-phase progress view. Selecting a run with phase summaries now opens a phase list showing per-phase agent counts, done/running/error counts, estimated tokens, and duration; `Esc`/left returns to the run list. Agent-level selection and `p`/`x`/`r`/`s` key controls remain separate slices.
  Validation 2026-06-18: `gofmt -w pkg/tui/bubble.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the selectable TUI `/workflows` phase-view slice.
  Extended the selectable TUI `/workflows` panel with phase-to-agent drill-down. Selecting a phase opens its agent list, selecting an agent opens a detail view with status, phase, token/tool counters, prompt preview, result/error preview, and recent tool-call argument/result previews. `Esc`/left backs out through agent detail, agents, phases, and runs. Full prompt rendering and `p`/`x`/`r`/`s` controls remain follow-up slices.
  Validation 2026-06-18: `gofmt -w pkg/tui/bubble.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the selectable TUI `/workflows` agent drill-down slice.
  Added TUI workflow progress-view control keys backed by existing slash commands: `p` maps to `/workflows pause` or `/workflows resume`, `x` maps to `/workflows agent-stop` on selected agents and `/workflows stop` otherwise, and `r` maps to `/workflows agent-restart` for selected agents. `s` currently displays the explicit `/workflows save <run-id> <name> [project|user]` command because a save-name input UI is still a separate follow-up.
  Validation 2026-06-18: `gofmt -w pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the TUI `/workflows` p/x/r control-key slice.
  Added the TUI workflow save-name dialog for `s`. The save layer captures a safe workflow name, supports `Tab` to toggle `project` / `user` scope, validates the name before running, and delegates to the existing `/workflows save <run-id> <name> <scope>` slash command. `Esc` returns to the previous workflow panel layer without closing the whole panel.
  Validation 2026-06-18: `gofmt -w pkg/tui/bubble.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check` passed after the TUI `/workflows` save dialog slice.
  Added capped prompt payloads to workflow runtime-state agent summaries and taught the TUI agent detail panel to render multi-line prompts instead of only the short preview. Runtime state and `/workflows agent <run-id|latest> <agent-id>` both keep a 4000-byte display boundary; child transcript browsing is handled by the later `/workflows transcript` slice.
  Added workflow child transcript capture for completed forked agents. Host `subagent_child_usage` events now carry the workflow bubble task id, workflow snapshots persist normalized user/assistant/tool-result transcript entries, and `/workflows transcript <run-id|latest> <agent-id>` renders the captured transcript, tool-call args, and usage without putting the heavy transcript into lightweight runtime state.
  Validation 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent -run TestWorkflowToolCapturesRealForkTranscript -count=1 -v` passed for a real `CodingSession` + workflow extension + fake child model case, proving the transcript flows through host `ForkSession` rather than only the workflow fake API.
  Workflow budget accounting now consumes captured child usage from `subagent_child_usage` before falling back to final-text estimation, and avoids double-counting when live `turn_end` events and final transcript usage are both present. Focused workflow tests cover usage-driven `budget.spent()` / `budget.remaining()` and usage-driven budget exhaustion.
  ForkSession child tool forwarding now distinguishes the current visible tool allowlist from the broader session tool catalog: omitted child `tools` inherits the main agent's current visible tools, while explicit `tools` requests are filtered from the session catalog so custom/session-connected/MCP-style tools can be forwarded by name. Focused tests cover custom-tool forwarding, narrowed allowlist inheritance, and the existing read-only discovery-tool opt-in path.
  Saved workflows now register Claude-style direct slash commands (`/<name> [json-args]`) when the workflow name is not reserved, while retaining `/workflow:<name>` as a compatibility path. Reserved saved workflow names no longer override built-in/extension commands, and disabling workflows removes both direct and compatibility saved commands from the live session.
  Fixed a stopped-run persistence race found by the async stop test: when a user stop/pause reason is already recorded, the background goroutine no longer overwrites that persisted reason with a later `context canceled` error.
  Added observed workflow cost aggregation from child usage. When child usage
  contains `Usage.Cost.Total`, workflow snapshots aggregate it across
  agent/phase/workflow levels, runtime state exposes it, and both slash
  commands plus the TUI workflow panel render it without synthesizing model
  pricing.
  Hardened stopped workflow persistence against async completion races. Once
  `status.json` records a stopped run, later background completed/failed writes
  cannot overwrite it unless the user explicitly resumes the run back to
  running.
  Made workflow budget admission concurrency-aware. Each live child fork now
  atomically reserves one budget slot with the agent-count check, releases it
  on failed/stopped attempts, and caps committed `budget.spent()` at
  `budget.total` while snapshots still retain real per-agent observed token
  counts. A parallel budget regression covers the previous oversubscription
  window, and docs now distinguish default concurrency 4 from the hard cap 16.
  Clarified the workflow tool/manager boundary after a real TUI misuse case
  tried `workflow(action=status id=...)`. Tool descriptions, dynamic workflow
  prompt guidance, Ultracode guidance, and README docs now say the `workflow`
  tool only starts runs and `/workflows ...` commands handle status, agent
  details, stop/resume, restart, save, and the TUI panel.

## Still Missing

- Deeper plan-mode revision flows beyond the current approval/rejection gate
- Advanced worktree flows such as diff/merge handoff from isolated worktrees back to the original checkout
- Full pi-compatible TypeScript extension/package ecosystem, including remote npm/git package install, theme resources, rich UI extension context, provider hooks, and hot reload
- Remaining pi TUI polish outside the now-covered selector/status/session-tree basics

## Suggested Next Steps

1. Improve plan/worktree semantics beyond the current minimal implementation.
2. Expand integration coverage around background tasks, tool replacement, and session switching.
3. Add richer host action policies such as backoff variants, command/dir allowlist presets, and per-action failure handling.
4. Keep refining the runtime state/control plane so more session resources are represented as first-class harness-managed artifacts instead of ad hoc prompt/session state.
