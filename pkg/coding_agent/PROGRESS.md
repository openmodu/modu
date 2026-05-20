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
- Marked dynamic nested-context messages as transient so they do not persist into long-term session history or saved transcripts.
- Added a lightweight session-scoped harness hook layer around tool execution.
- Added a harness-only hint side channel by stripping `<modu-code-hint .../>` tags from tool-visible text output while recording them for the host runtime.
- Added harness-managed runtime path exposure through a new `harness_paths` tool and session API.
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

## Still Missing

- Deeper plan-mode semantics beyond the current state/prompt toggle
- Richer worktree lifecycle controls and cleanup introspection
- Full pi-compatible TypeScript extension/package ecosystem, including remote npm/git package install, theme resources, UI extension context, provider hooks, and hot reload
- Remaining pi TUI polish around session tree rendering and historical branch labels

## Suggested Next Steps

1. Improve plan/worktree semantics beyond the current minimal implementation.
2. Expand integration coverage around background tasks, tool replacement, and session switching.
3. Add richer host action policies such as backoff variants, command/dir allowlist presets, and per-action failure handling.
4. Keep refining the runtime state/control plane so more session resources are represented as first-class harness-managed artifacts instead of ad hoc prompt/session state.
