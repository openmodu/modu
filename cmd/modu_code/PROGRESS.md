# modu_code product progress

This file tracks product-experience work for `modu_code`. Keep each item small
enough to implement, verify, and commit independently.

## Done

- 2026-07-16: converted Feishu channel replies and standalone notifications
  from raw Markdown text messages into Feishu `post` rich text. The conversion
  preserves readable headings, lists, quotes, code blocks, task items, tables,
  and clickable HTTP(S)/email links inside `pkg/channels/feishu`; the generic
  channel bridge and `coding_agent` remain unchanged.
- 2026-07-16: added a single interactive `/channel` configuration flow for
  Telegram and Feishu. Telegram tokens and Feishu app secrets use masked TUI
  input, Feishu chat allowlists accept comma/space-separated IDs or `-` for all
  authorized chats, legacy `/telegram` and `/feishu` slash entry points were
  removed, and configured Telegram bots start again in the default modu TUI.
- 2026-07-15: added Streamable HTTP MCP servers through a Codex-compatible
  `url` configuration, including bearer-token, static-header, and
  environment-backed-header support. This uses the current Streamable HTTP
  transport and does not enable the deprecated legacy HTTP+SSE client.
- 2026-07-15: connected configured stdio MCP tool servers to every new
  `modu_code` coding session. Discovered tools are available to the main agent,
  can be explicitly forwarded to workflow/subagent children, and are counted
  by `/doctor`; required-server startup failures stop session creation while
  optional failures remain diagnostic warnings.
- 2026-07-02: added Feishu bot support for `modu_code`: configuration is stored
  under `~/.modu/channels/feishu/config.toml` or read from
  `MODU_FEISHU_APP_ID` / `MODU_FEISHU_APP_SECRET`, and the default `modu-tui`
  runner starts a Feishu `pkg/channels.Channel` through the generic
  channel/CodingSession bridge.
- 2026-07-03: deduplicated inbound channel events in the generic
  channel/CodingSession bridge by channel, chat ID, and platform message ID, and
  dispatch Feishu message handlers asynchronously so the WebSocket event
  callback can acknowledge delivery before the agent run finishes.
- 2026-06-30: moved the default `modu_code` runtime/config root from
  `~/.coding_agent` to `~/.modu`, and changed the main model config from
  `config.json` JSON to `config.toml` TOML across config init/show/list/validate
  and save paths. Telegram bot config under `channels/telegram/` now uses
  `config.toml` as well.
- 2026-06-30: limited the `modu-tui` todo card to current active runs. A todo
  snapshot restored from the session or left uncompleted after a finished goal
  no longer appears in the fixed bottom area; the card only renders while the
  model is busy/streaming after a current-run `SetTodosMsg`.
- 2026-06-30: restored session token usage after `--resume` by rebuilding
  context-manager usage from persisted assistant message usage, so the fixed
  footer ctx counter and `/session` stats no longer reset to zero on restart.
- 2026-06-30: routed queued continuation turns through `CodingSession.Continue`
  in the TUI/Telegram hosts, so follow-up, steer, and goal continuation rounds
  run the same post-turn auto-compaction check as normal prompts.
- 2026-06-30: `modu-tui` now writes a persistent
  `------------- context compact ------------------` transcript divider when
  context compaction completes, and restores the divider from saved compaction
  entries when resuming a session.
- Started the `pkg/modu-tui` backed `modu_code` runner on branch
  `codex/modu-code-modu-tui`: the default interactive entry no longer imports
  `pkg/tui`, core session events are converted into `modu-tui` messages, and
  config hook types are owned by `cmd/modu_code`.
- The `modu_code` `modu-tui` runner now seeds Bubble Tea with the current
  terminal size, matching `tuipoc2` startup sizing and avoiding a wrong-size
  first frame on small mobile terminals.
- `pkg/modu-tui` render output now pads every terminal row to the current
  width, so scrolling from a longer previous frame to shorter content clears
  stale tail cells on mobile terminals.
- `pkg/modu-tui` now renders `Jump to bottom` once in a fixed row above the
  input instead of as a viewport overlay, preserving tap-to-bottom behavior
  while avoiding duplicated jump hints during mobile terminal redraws.
- `modu_code` tool calls now merge assistant call, execution start/end, and
  tool result updates by `ToolID` into one `pkg/modu-tui` block. Collapsed
  blocks render only a two-space indented summary, while expanded blocks render
  `⏺ ToolName(input args)`, wrap long args with `  │ ` continuation lines, then
  show a two-space `└ output` line; long output and code continue with
  four-space indentation instead of truncating.
- `modu_code` Read tool calls now render with a compact `Read N lines` result
  summary instead of dumping file contents into the tool block.
- `modu_code` write/edit tool calls now render as explicit non-collapsible
  blocks with a short write/diff summary and syntax-highlighted content or
  diff. Existing-file `write` and `edit` previews include line numbers plus
  nearby context when the target file can be read locally.
- Expanded `pkg/modu-tui` tool blocks now avoid a dark container background;
  command details stay dimmed while code/diff lines preserve their own syntax
  highlighting. Diff blocks now use red/green/gray per-line backgrounds with
  syntax highlighting applied only to the code portion of each line, and
  `toolDiffSourceLanguage` infers the highlighting language from the file
  extension in the diff header.
- Collapsed `pkg/modu-tui` tool summaries stay compact, while clicking any
  rendered line inside an expanded tool block collapses it.
- Tool approval cards now use compact command previews instead of JSON args,
  with clearer grouped allow/deny shortcuts.
- Tool approval cards now use a heavier, higher-contrast border so the fixed
  approval panel reads as a distinct card on mobile terminals.
- `pkg/modu-tui` now has a reusable `CardBlock` heavy-border card renderer,
  and approval/slash popups share that card style instead of hardcoding panel
  borders in the model.
- Fresh `modu-tui` sessions now open with a `CardBlock` startup information
  card showing app, model, cwd, session id, and basic `/` command guidance;
  the card is UI-only and stays at the top after transcript messages exist.
- The `modu-tui` runner now passes session todos into a fixed todo card above
  the input and refreshes it after tool/slash state changes. Cwd-aware `WithCwd`
  variants resolve relative file paths for local preview and update detection;
  writes to existing files render as "update" with a contextual diff preview
  including line numbers and added/removed counts; edit end events extract the
  final diff from tool output for `ToolCode` and `ToolLanguage: "diff"`.
- `pkg/modu-tui` input now collapses large pasted text into a `[Pasted text ...]`
  token in the bottom input while submitting and rendering the full expanded
  content in the conversation transcript.
- The `modu-tui` runner now supports slash command suggestions: typing `/`
  opens a fixed bottom card, `Tab` completes the selected command, and `Enter`
  routes slash commands through `pkg/slash` or the session slash/prompt/skill
  path instead of sending them to the model as plain text.
- Slash command input highlights the leading command token, and the running
  status now names the command being executed, such as `running /goal`.
- Slash command output in the `modu-tui` runner is now marked preformatted, so
  multiline outputs such as `/help` keep line breaks and column alignment
  instead of being reflowed as a Markdown paragraph.
- `pkg/modu-tui` markdown rendering now disables Glamour's heavy inline-code
  red/background styling while preserving fenced-code highlighting and table
  rendering, keeping status text such as commit summaries readable.
- `pkg/modu-tui` now owns reusable tool approval UI primitives:
  `ApprovalBlock`, `RequestToolApprovalMsg`, approval decision constants, and
  keyboard handling for allow/deny decisions.
- Git-backed `modu_code` startup can enter a managed branch-backed worktree via
  `--worktree`; the default startup path stays in the current checkout.
- Default `modu_code` startup now creates a fresh session id; previous context
  is restored only when the caller passes `--resume <session-id>`.
- Interactive TUI exit now prints the current session id plus a copyable
  `modu_code --resume <session-id>` command, making the saved history path
  explicit after Ctrl+C or `/quit`; empty sessions are flushed so printed ids
  can be resumed immediately.
- SSH sessions keep `pkg/modu-tui` mouse reporting on by default again, with
  `MODU_TUI_MOUSE=off` as an opt-out for JuiceSSH/mobile clients that flood
  touch-motion events and make the interface appear frozen.
- SSH sessions and explicit mouse-disabled sessions route empty-input Up/Down
  keys to transcript scrolling only when no input history is available, so
  prompt history remains usable while mobile swipe gestures translated into
  arrow keys can still reach earlier conversation content.
- The `modu-tui` runner now restores per-agent-run elapsed summaries by
  tracking `AgentStart`/`AgentEnd` events and appending `✓ Completed (...)`
  after each finished conversation round.
- Status line moved above the input separator for compact agent running state
  and recent completion duration; the bottom footer now shows short
  context/window, model, and cwd, and `Esc` interrupts the active prompt plus
  running bash process.
- Terminal resize handling keeps the user prompt visible and avoids duplicate
  completed-status lines.
- Model configuration moved into `~/.coding_agent/config.json` with support for
  multiple configured models and an active model.
- `/model` supports listing, switching by configured name, and a TUI picker;
  selected model is persisted back to config.
- Switching models and `/clear` clear in-memory and persisted conversation
  context, then refresh the dynamic system prompt.
- Dynamic prompt environment now includes the actual connected provider/model
  without hardcoding a vendor identity.
- OpenAI-compatible provider retries Xiaomi MiMo-style `reasoning_content`
  failures by dropping assistant history entries that cannot satisfy the API.
- modu_code-owned comments, harness hint tags, and context discovery no longer
  use Claude-specific naming.
- `/context` shows the current prompt/context sources without changing session
  state, including model, cwd, messages, prompt size, memory, context files,
  skills, plan mode, and worktree mode.
- `/doctor` shows runtime diagnostics without changing session state, including
  config path, model, baseUrl reachability, provider registration, API key
  status, context file count, and detected problems.
- Model switching feedback now shows the active entry/name and explicitly says
  whether the conversation context was cleared.
- API failure messages in the TUI now collapse repeated identical errors into a
  counter, compact long multiline errors, and show recovery actions.
- `modu_code config example|init|validate` provides CLI helpers for creating
  and checking multi-model config files.
- `/retry` retries the last failed prompt in the interactive TUI and clears the
  failed prompt marker after a successful retry.
- `/sessions` opens a real TUI session picker, with keyboard actions to resume,
  fork, and delete persisted sessions.
- TUI session picker now supports search, current/all scope, sort modes,
  named-only filtering, path display, rename, and delete confirmation.
- TUI model selector now supports search, scoped/all selection, `/model <query>`,
  `/scoped-models`, and `Ctrl+P` / `Ctrl+N` model cycling.
- Added interactive `/settings`, `/hotkeys`, `/reload`, `/new`, `/name`, and
  `/clone` handling for the TUI path.
- `/tree` now opens an interactive session-tree selector with search, current-path
  markers, branch-summary preview, Enter navigation, and Ctrl+F branched-session
  creation.
- TUI editor now supports `@file` fuzzy references, Tab/Enter reference completion,
  prompt-time referenced-file expansion, and Tab completion for path-like tokens.
- TUI shell shortcuts now align with pi semantics: `!cmd` sends command output to
  the model, while `!!cmd` only displays command output.
- Added `/export [file]` for HTML session export from slash/TUI paths.
- `/session` now shows a richer pi-style runtime summary: cwd, model, messages,
  tokens, duration, plan/worktree state, and resource counts.
- Added `/copy` to copy the last assistant message to the system clipboard when
  `pbcopy` is available.
- Added `/changelog` to show recent git commits from the active working directory.
- Added TUI `/config example|init|validate` routing through a command hook so
  `cmd/modu_code` can reuse its internal provider config helpers without moving packages.
- `/skills` and `/prompts` now open searchable TUI resource pickers and insert
  the selected slash command back into the input.
- TUI tool-output display mode selected from `/settings` is persisted in
  `~/.coding_agent/tui_settings.json` and restored on startup.
- TUI now exposes the agent steer/follow-up queues: Enter while running queues
  a follow-up, Shift+Enter or `/steer <message>` interrupts and steers, and
  `/followup <message>` queues explicitly. `/s` and `/f` provide terminal-safe
  short aliases when Shift+Enter is not distinguishable from Enter.
- `pkg/modu-tui` now reports bottom-input submit intent as prompt, follow-up,
  or steer events so `cmd/modu_code` can route running-task input without
  coupling the reusable UI package to coding-agent sessions.
- `pkg/modu-tui` now renders assistant thinking as one collapsed block at the
  top of each assistant turn, with separate marker colors for assistant replies,
  streaming replies, and expanded tool calls.
- `modu-tui` input history is wired into `modu_code`: Up/Down navigates the
  last 100 submitted inputs, shows a `History n/total` hint on the top input
  rule, restores the current draft, and persists through the session input
  history file.
- Added `/queue` to inspect pending steer/follow-up messages, clear all or one
  queue type, and drop the last pending message after accidental input.
- Telegram input now mirrors the TUI queue semantics: plain messages become
  follow-ups while a task is active, `/f` queues explicitly, and `/s` steers and
  cancels the current Telegram-driven turn so queued work can continue.
- Default interactive TUI entry migrated to Bubble Tea. The old legacy runtime
  and comparison path have been removed; the Bubble Tea path covers full-screen
  rendering, prompt submission, slash-command selection, approval prompts, shell
  shortcuts, queue handling, Telegram bridge output, and agent/session event
  streaming.
- Bubble Tea TUI now includes the interactive `/model` selector,
  `/scoped-models` scope editor, and `Ctrl+P` / `Ctrl+N` model cycling.
- Bubble Tea view chrome now follows the Agenvoy-style visual structure:
  top header, bounded transcript, bordered input, and popup-styled selectors.
- Default TUI path moved to Bubble Tea inline runtime: Bubble Tea renders the
  bottom input/selector/approval widget, while completed transcript blocks are
  printed above the program into terminal scrollback for selection/copy.
- Bubble Tea inline runtime now prints the Agenvoy-style bordered multi-line header
  information into scrollback on startup and after model switches, with Telegram
  shown as `channel` instead of `mode`, without keeping a persistent header row
  in the live renderer.
- Bubble Tea tool and plan approval prompts now use the Agenvoy-style prompt
  card: `⏺` title, compact tool/input detail, and colored `actions:` choices.
- `/config` now opens a configuration page instead of exposing list/validate/example
  slash subcommands. The page is intentionally limited to Active Model and
  Provider setup, with Esc returning from a second-level page.
- `/scoped-models` now supports simple slash arguments: list, set, add, remove,
  clear, and edit.
- Model config now writes a v2 schema with separate provider connection config,
  model metadata, active model, roles, reasoning, and persistent scopedModels;
  legacy per-model baseUrl/apiKey files still load and migrate on save.
- `/config` Provider now uses the same searchable selector pattern as model
  selection, opens an existing, preset, or custom OpenAI-compatible provider
  settings form, and discovers model entries from `<baseUrl>/models` after save.
- Prompt-template slash commands now match Claude Code custom-command argument
  syntax: `$ARGUMENTS` (all args), positional `$1`/`$2`/... (whitespace-split,
  multi-digit safe), and inline ``!`command` `` shell substitution that runs in
  the session cwd and injects the output into the prompt; legacy
  `{{input}}`/`{{args}}` still work. The `/` picker shows a template's
  `argument-hint` frontmatter next to its description.
- Subagent token spend now counts toward the active `/goal` budget. When a
  ForkSession child finishes (single, worktree, or background), the host emits
  a `subagent_child_usage` event carrying the child transcript; the goal
  extension folds that token usage into the current turn's accounting so a
  goal's budget no longer undercounts what its subagents consumed. The same
  host signal unlocks post-hoc per-child token totals for subagent control
  (see subagent `PARITY.md` section G).
- Background subagent children now stream their live activity up to extensions
  (subagent control parity, G group). The host subscribes to a background
  child's agent events and re-emits `turn_end`/`tool_execution_end`/`agent_end`
  as `subagent_child_event` tagged with the child `TaskID`
  (`types.Event.TaskID`, original type in `Reason`, `IsError` + per-turn usage
  preserved). The subagent extension tallies per-task turns / failed tools /
  tokens in a `childActivityRegistry` and shows them as an `activity:` line in
  `subagent action=status id=<taskID>`.
- Subagent control activity-counter thresholds are now wired end to end for
  batch async runs (`activeNoticeAfterTurns`, `activeNoticeAfterTokens`,
  `failedToolAttemptsBeforeAttention`). `ForkOptions.BubbleTaskID` carries the
  batch id into every child fork (sync or background), so the host bubbles all
  of a batch's children under one id; a per-batch `controlCounterRegistry`
  (registered in `dispatchBatchAsync`) latches each threshold once and
  delivers an `active_long_running` / `needs_attention` notice through the
  existing `notifyOn` / `notifyChannels` routing. Single-mode background runs
  still lack a `control` entry point (see subagent `PARITY.md`).
- `modu_code` now registers the builtin Lua `workflow` extension by default.
  The tool supports scripted `meta` / `phase` / `log` / `agent` / `parallel`
  / `pipeline` orchestration, with child execution routed through the existing
  `ExtensionAPI.ForkSession` path. Pipeline item scheduling now honors
  `concurrency` while serializing access to the shared Lua VM. Workflow failure
  branches return a stable `json.null` sentinel so Lua scripts can compare
  failed branch outputs against `json.null`.
- Follow-up workflow review findings are fixed: `workflow` exposes a real
  optional `budget` parameter instead of a fake infinite remaining budget,
  validates `memory_scope`, hardens pipeline stage locking, and documents that
  child `tools` are filtered from the parent active tool set. `modu_code`
  config validation now writes problem diagnostics to stderr for CLI callers
  while `/config` still merges them into TUI output; config show distinguishes
  invalid JSON from unreadable config files. ACP JSON marshal failures now log
  and return protocol error frames instead of silently dropping responses,
  invalid active model names no longer fall back to the first configured model,
  and print/RPC/ACP modes share SIGINT/SIGTERM cancellation wiring.
- Claude Code dynamic-workflow parity was re-audited against the official docs,
  `pi-dynamic-workflows` source, and the upstream issue audit. The plan now
  records gaps for structured output, persisted scripts, `/workflows`,
  resume/replay, real token usage, nested workflows, MCP forwarding, and config
  disable toggles. The implemented runtime now enforces the static resource
  guardrails that can be done without widening host APIs: max concurrency 16,
  max 1000 forked agents per run by default, and budget exhaustion preventing
  later `agent()` forks.
- Workflow inline scripts are now persisted under the active session directory
  at `extensions/workflow/runs/<run-id>/script.lua` when `SessionDir()` is
  available. Final workflow details include `scriptPath` and `runDir`, giving
  users a concrete script artifact to inspect or diff before the later saved
  workflow/relaunch/resume work.
- The workflow tool now also loads scripts from `script_path` and saved
  workflow `name`, with an exactly-one source rule across `script`,
  `script_path`, and `name`. Saved names resolve project
  Claude-compatible `.claude/workflows/<name>.lua` and legacy
  `.coding_agent/workflows/<name>.lua` before sibling `~/.claude/workflows/<name>.lua`
  and user/agent-dir `workflows/<name>.lua`, giving `modu_code` a minimal
  Claude-style saved workflow/relaunch primitive before adding a registry or
  `/workflows` manager.
- `/workflows` now provides the first read-only management surface for persisted
  workflow runs: list current-session run scripts and show a run by id/prefix or
  `latest`. This intentionally does not expose pause/kill/resume until workflow
  runs become host-managed background tasks.
- Workflow can now be disabled without removing code: workflow `config.disabled:
  true` in `extensions.yaml`, `disableWorkflows: true` in global/project
  `settings.json`, `/config` -> `Dynamic workflows`,
  `MODU_CODE_DISABLE_WORKFLOWS=1`, or
  `CLAUDE_CODE_DISABLE_WORKFLOWS=1` skips registration of the workflow tool and
  `/workflows`. Disabling from `/config` also removes the current session's
  workflow tool and slash commands; re-enabling requires a new session or
  restart to register them again.
- Workflow structured output now has a first local implementation: `schema` on
  `agent()` / `parallel()` tasks injects a final JSON contract, parses and
  validates the child result, retries once with corrective context, and returns
  Lua tables or `json.null` on final failure. Host-level terminating tools
  remain future work.
- Workflow scripts can now compose one saved/path workflow via
  `workflow(nameOrRef, args)`. Nested child agents share the parent workflow's
  budget, cancellation context, concurrency default, and max-agent cap; deeper
  nesting and saved-command registry integration remain future work.
- Saved workflow files present at startup are now registered as
  `/workflow:<name> [json-args]` commands. Project workflows take precedence
  over user workflows with the same name; live registry metadata remained
  future work for a later slice.
- `/deep-research <question>` is now registered as a bundled workflow command.
  It starts a background Lua workflow with scope, parallel research,
  cross-check, and synthesis phases. It can now request built-in opt-in
  `web_search` / `web_fetch` tools from forked children; true cited
  web-research parity still depends on runtime network permission and a
  reachable search endpoint.
- The main agent system prompt now includes workflow authoring guidance when
  the `workflow` tool is active. Explicit `workflow` / `dynamic workflow` /
  `ultracode` requests and large fan-out/fan-in tasks instruct the model to
  write Lua and call the `workflow` tool. Keyword highlighting and approval
  cards remain follow-ups.
- `/effort ultracode` now enables a session-local workflow-first mode when the
  workflow tool is active and the current model supports xhigh reasoning. It
  sets thinking to `xhigh`, appends an Ultracode prompt block, and
  `/effort high|medium|low|off` exits the mode. Keyword highlighting,
  dismissal shortcuts, and approval cards remain follow-ups.
- Workflow starts now have a pre-run approval gate through host `Select`.
  The workflow tool, saved `/workflow:<name>` commands, `/deep-research`, and
  `/workflows restart` show workflow metadata, inferred phases, resource
  limits, and a Lua script preview before any child agent forks; denial cancels
  the run. Run-once, always-allow-this-project, and cancel choices are
  supported, with always-allow records stored in `workflow_approvals.json`.
  View-raw-script is also available and returns to the approval choice after
  showing the full Lua script. `permissions.defaultMode: "auto"` now remembers
  a run-once workflow approval for the same project/workflow/script, and
  `permissions.defaultMode: "bypassPermissions"` skips workflow launch
  approval; editor actions, ultracode-specific direct skips, and Desktop card
  rendering remain follow-ups.
- Completed workflow runs now persist `snapshot.json` next to `script.lua`.
  `/workflows list/show` can display completed run metadata, including workflow
  name, agent/error counts, duration/result preview, and script path. This is
  still a completed-run registry slice, not live background pause/kill/resume.
- Workflow now has the first live management slice: `workflow` accepts
  `async:true` to return a run id immediately, saved workflow slash commands
  start in the background, `/workflows list/show` overlays live registry state
  with persisted scripts/snapshots, and `/workflows stop <run-id>` cancels a
  running workflow. Pause/resume, memoized replay, cross-process registry
  recovery, and TUI workflow panel remain future work.
- `/workflows save <run-id|latest> <name> [project|user]` now saves a run's
  persisted `script.lua` into project or user workflows for reuse in future
  sessions. The command validates saved workflow names and asks before
  overwriting existing files. Claude-style TUI save dialog remains future work.
- Saved project workflow discovery now walks from the active cwd up to the git
  root with nearest-directory precedence before falling back to user workflows.
  Project saves use the nearest existing project `.claude/workflows` directory,
  or the git root `.claude/workflows` when no project workflows directory exists.
- `/workflows restart <run-id|latest>` now relaunches a persisted run script as
  a fresh background workflow run. This is a relaunch path, not memoized
  resume/replay.
- Background workflow runs now persist `status.json` beside `script.lua` and
  `snapshot.json`; `/workflows` reads it so stopped/failed/completed state
  survives extension/process recreation. This is persisted status recovery; a
  run found only from persisted files still restarts fresh, matching Claude's
  documented process-exit behavior.
- Workflow snapshots now track each agent's `startedAt`, `endedAt`, and
  `durationMs`, and `/workflows show` renders per-agent durations. Real
  token/cost accounting is still pending a `ForkSession` result/API that
  exposes child usage.
- `/workflows resume <run-id|latest>` now resumes a stopped in-memory workflow
  in the same session. Completed agent results are returned from cache without
  another `ForkSession`, while incomplete branches run live. Runs recovered only
  from persisted files still require fresh `/workflows restart`, matching
  Claude's documented fresh-start behavior after process exit.
- `/workflows pause <run-id>` now provides the slash-level pause counterpart to
  Claude's workflow progress controls. It cancels the live run into stopped
  state with `pause requested`, and `/workflows resume <run-id|latest>` can
  continue that same in-memory run with completed agent results cached.
- Workflow snapshots now include phase progress summaries: each agent records
  text-based `estimatedTokens`, and `phaseSummaries` aggregate agent counts,
  done/running/error counts, estimated tokens, and elapsed duration per phase.
  `/workflows show` renders phase progress before agent details; real token/cost
  accounting still needs a richer `ForkSession` result or usage correlation.
- `/workflows agent <run-id|latest> <agent-id>` now provides a slash drill-down
  for one workflow agent, showing run status, agent status, label, phase,
  estimated tokens, timing, error/result preview, and prompt. Child events now
  add `turnTokens`, failed tool-call count, and recent child tool names/errors
  to the snapshot and drill-down.
- Recent workflow child tool calls now retain short `argsPreview` and
  `resultPreview` fields in the workflow snapshot, and `/workflows agent
  <run-id|latest> <agent-id>` renders them under `RecentToolCalls`. This
  closes the slash-level Claude drill-down requirement for recent tool-call
  args/results; full child transcript browsing is handled by the later
  `/workflows transcript` slice.
- Workflow live run state is now exposed through
  `RuntimeState().Extensions["workflow"]`: running/stopped/completed/failed
  counts, latest run metadata, and a running-workflow indicator. The TUI
  statusbar renders that indicator while background workflows are active, giving
  users a visible progress-view entry point before the later navigable workflow
  panel and key-binding layer.
- Exact `/workflows` in the TUI now renders a local workflow overview panel
  from workflow runtime state. It shows live run counts, recent runs, and the
  existing `/workflows show/agent/pause/stop/resume/restart/agent-stop/
  agent-restart` command paths. Selectable Enter/Esc/p/x/r navigation remains a
  later slice.
- Workflow runtime state now includes per-run agent summaries (`id`, `label`,
  `phase`, `status`, token/tool counters, and duration), and the TUI
  `/workflows` overview renders the latest run's agent list. This moves the TUI
  progress data closer to Claude's workflow view while leaving selectable
  drill-down and p/x/r/s key bindings as a later slice.
- Agent summaries now also carry result/error previews and recent child
  tool-call previews (`toolName`, error flag, args preview, result preview).
  The TUI `/workflows` overview renders those short previews under each latest
  run agent, narrowing the gap to Claude's agent drill-down while still keeping
  selectable navigation for a later slice.
- Workflow runtime state and the TUI `/workflows` overview now include
  phase-level summaries for the latest run: agent counts, done/running/error
  counts, estimated tokens, and elapsed duration. This covers another visible
  piece of Claude's progress view before implementing a selectable panel.
- Workflow runtime-state agent summaries now include capped `promptPreview`,
  and the TUI `/workflows` overview renders that prompt preview under each
  latest-run agent. Full prompts remain available through `/workflows agent
  <run-id|latest> <agent-id>`.
- 2026-06-18 validation for the workflow overview prompt-preview slice:
  `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./...`, and
  `git diff --check` all passed.
- 2026-06-18 validation for the workflow overview phase-summary slice:
  `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./...`, and
  `git diff --check` all passed.
- 2026-06-18 validation for the workflow overview agent-preview slice:
  `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./...`, and
  `git diff --check` all passed.
- 2026-06-18 validation for the workflow runtime-state agent-summary slice:
  `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./...`, and
  `git diff --check` all passed.
- 2026-06-18 validation for the TUI workflow overview panel slice:
  `gofmt -w pkg/tui/workflow_panel.go pkg/tui/workflow_panel_test.go pkg/tui/bubble.go pkg/tui/model.go`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/tui`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./...`, and
  `git diff --check` all passed.
- 2026-06-18 validation for the workflow runtime-state/statusbar slice:
  `gofmt -w pkg/coding_agent/plugins/extension/workflow/runtime_state.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go pkg/tui/statusbar.go pkg/tui/statusbar_test.go pkg/tui/bubble.go`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent ./cmd/modu_code`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./...`, and
  `git diff --check` all passed.
- 2026-06-18 validation for the child tool args/results drill-down slice:
  `gofmt -w pkg/coding_agent/fork_session.go pkg/coding_agent/plugins/extension/workflow/activity.go pkg/coding_agent/plugins/extension/workflow/commands.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code`,
  `env GOCACHE=/private/tmp/modu-go-build go test ./...`, and
  `git diff --check` all passed.
- `/workflows agent-stop <run-id|latest> <agent-id>` and `/workflows
  agent-restart <run-id|latest> <agent-id>` now provide selected-agent control
  from the slash layer. Stop cancels one running child and lets the workflow
  continue with nil for that branch; restart cancels the selected child and
  reruns the same prompt/options as a new agent snapshot. TUI `x`/`r`
  keybindings remain a UI follow-up.

## Next

1. Migrate the remaining rich selectors to Bubble Tea: sessions/tree,
   settings, skills/prompts, and file-reference completion.
2. Add real keybindings.json remapping if custom keyboard shortcuts become a priority.

## Validation Log

- 2026-05-16: `go test ./pkg/coding_agent ./pkg/tui ./cmd/modu_code ./pkg/providers/openai ./pkg/agent`
  passed for the completed model/status/provider fixes.
- 2026-05-16: `go test ./cmd/modu_code ./pkg/coding_agent ./pkg/tui ./pkg/slash ./pkg/providers/openai ./pkg/agent`
  passed for `/context`.
- 2026-05-16: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/coding_agent ./pkg/tui ./pkg/slash ./pkg/providers/openai ./pkg/agent`
  passed for basic `/doctor`.
- 2026-05-16: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/coding_agent ./pkg/tui ./pkg/slash ./pkg/providers/openai ./pkg/agent`
  passed for `/doctor` baseUrl reachability checks.
- 2026-05-16: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/coding_agent ./pkg/tui ./pkg/slash ./pkg/providers/openai ./pkg/agent`
  passed for model-switch feedback.
- 2026-05-16: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/coding_agent ./pkg/tui ./pkg/slash ./pkg/providers/openai ./pkg/agent`
  passed for collapsed API failure messages.
- 2026-05-16: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/coding_agent ./pkg/tui ./pkg/slash ./pkg/providers/openai ./pkg/agent`
  passed for config helper commands.
- 2026-05-16: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/coding_agent ./pkg/tui ./pkg/slash ./pkg/providers/openai ./pkg/agent`
  passed for `/retry`.
- 2026-05-19: `go test -count=1 ./cmd/modu_code ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the TUI session picker and cmd/modu_code session-flow coverage.
- 2026-05-19: `go test -count=1 ./pkg/tui ./pkg/slash ./pkg/coding_agent ./cmd/modu_code`
  passed for expanded TUI parity: slash commands, session selector, model selector,
  settings, hotkeys, and reload.
- 2026-05-19: `go test ./pkg/coding_agent ./pkg/tui ./pkg/slash`
  passed for interactive session-tree navigation and branch-summary restoration.
- 2026-05-19: `go test ./pkg/tui`
  passed for TUI file-reference and path-token completion coverage.
- 2026-05-19: `go test ./pkg/tui ./cmd/modu_code`
  passed for single-bang and double-bang shell shortcut behavior.
- 2026-05-19: `go test ./pkg/slash ./pkg/coding_agent`
  passed for slash-driven session HTML export.
- 2026-05-19: `go test ./pkg/slash ./cmd/modu_code`
  passed for the expanded `/session` runtime summary.
- 2026-05-19: `go test ./pkg/slash ./cmd/modu_code`
  passed for slash-driven last-assistant copy behavior.
- 2026-05-19: `go test ./pkg/slash ./cmd/modu_code ./pkg/tui`
  passed for slash/TUI changelog command coverage.
- 2026-05-20: `go test ./cmd/modu_code ./pkg/tui`
  passed for the TUI `/config` command hook.
- 2026-05-20: `go test ./pkg/tui`
  passed for searchable skill/prompt resource picker behavior.
- 2026-05-20: `go test ./pkg/tui`
  passed for persisted TUI settings round-trip behavior.
- 2026-05-30: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the first Bubble Tea migration slice.
- 2026-05-30: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the Bubble Tea `/model`, `/scoped-models`, and model cycling migration.
- 2026-05-30: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the Agenvoy-style Bubble Tea chrome pass.
- 2026-05-30: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed after restoring the default inline selectable TUI path.
- 2026-05-30: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the Bubble Tea inline selectable-scrollback runtime.
- 2026-05-30: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the non-persistent multi-line inline header, channel labeling, and
  input prompt marker update.
- 2026-05-30: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the Agenvoy-style Bubble Tea approval prompt cards.
- 2026-05-31: `GOCACHE=/private/tmp/modu-go-build go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for removing the legacy go-tui runtime and dependency.
- 2026-05-31: `GOCACHE=/private/tmp/modu-go-build go test ./...`
  passed after removing the legacy go-tui runtime and dependency.
- 2026-05-31: `go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the expanded `/config` management commands.
- 2026-05-31: `env GOCACHE=/private/tmp/modu-go-build go test ./cmd/modu_code ./cmd/modu_code/internal/provider ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the `/config` configuration page and provider/model/scoped-model flows.
- 2026-05-31: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/tui ./cmd/modu_code`
  passed for `/scoped-models` slash-argument configuration.
- 2026-05-31: `env GOCACHE=/private/tmp/modu-go-build go test ./cmd/modu_code/internal/provider ./cmd/modu_code ./pkg/tui`
  passed for v2 config schema loading, migration, scopedModels persistence, and
  config command coverage.
- 2026-05-31: `env GOCACHE=/private/tmp/modu-go-build go test ./cmd/modu_code/internal/provider ./cmd/modu_code ./pkg/tui`
  passed for the searchable `/config` provider selector and OpenAI-compatible
  model discovery.
- 2026-06-05: `env GOCACHE=/private/tmp/modu-go-build go test ./cmd/modu_code ./pkg/coding_agent ./pkg/coding_agent/plugins/prompts ./pkg/tui ./pkg/slash`
  passed for Claude Code-style prompt-template argument substitution
  (`$ARGUMENTS`, positional `$N`), inline ``!`command` `` shell substitution, and
  `argument-hint` surfacing in the slash picker.
- 2026-06-05: `env GOCACHE=/private/tmp/modu-go-build go test -count=1 ./cmd/modu_code ./pkg/coding_agent ./pkg/coding_agent/plugins/extension/goal ./pkg/coding_agent/plugins/extension/subagent ./pkg/tui`
  plus `go test -race` on the new tests passed for the `subagent_child_usage`
  host event and goal-budget folding (`TestSubagentForkEmitsChildUsage`,
  `TestSubagentChildUsageCountsTowardGoalBudget`,
  `TestSubagentChildUsageIgnoredWithoutActiveGoal`).
- 2026-06-05: `env GOCACHE=/private/tmp/modu-go-build go test -count=1 ./pkg/agent ./pkg/coding_agent ./pkg/coding_agent/plugins/subagent ./pkg/coding_agent/plugins/extension/subagent ./pkg/coding_agent/plugins/extension/goal ./cmd/modu_code`
  plus `go test -race` passed for bubbling background subagent child events
  (`subagent_child_event` + `types.Event.TaskID` + `RunWithMessagesObserved`)
  and the child-activity tally (`TestSubagentBackgroundBubblesChildEvents`,
  `TestChildActivityRegistryTallies`,
  `TestChildActivityRegistryIsolatesTasksAndIgnoresUntagged`).
- 2026-06-05: `env GOCACHE=/private/tmp/modu-go-build go test -count=1` (+ `-race`)
  over the same packages passed for wiring activity-counter control thresholds:
  `ForkOptions.BubbleTaskID` batch→child id mapping and the per-batch
  `controlCounterRegistry` (`TestBatchAsyncTagsChildrenWithBatchID`,
  `TestBatchAsyncBubblesChildEventsUnderBatchID`,
  `TestControlCounterFailedToolThresholdFiresOnce`,
  `TestControlCounterTurnsAndTokens`, `TestControlCounterRespectsNotifyOn`,
  `TestControlCounterUnregisterStopsNotices`).
- 2026-06-16: `env GOCACHE=/private/tmp/modu-go-build go test ./cmd/modu_code ./pkg/tui`
  passed for the Bubble Tea v2 active-region resize behavior and fixed-width
  inline turn separator (`TestBubbleInlineResizeReflowsActiveRegionAndKeepsScrollback`,
  `TestBubbleInlineTurnSeparatorStaysBelowMinimumTerminalWidth`).
- 2026-06-17: `env GOCACHE=/private/tmp/modu-go-build go test ./cmd/modu_code ./pkg/coding_agent ./pkg/coding_agent/plugins/extension/workflow`
  passed for the first Lua workflow extension slice: builtin registration,
  safe runtime basics, ForkOptions mapping, parallel concurrency/order/failure
  handling, pipeline order/failure semantics, and workflow tool result details.
- 2026-06-17: `go run ./cmd/modu_code -p '<repo_inventory_smoke workflow prompt>'`
  passed as a real configured-model workflow smoke case on `deepseek-v4-flash`.
  The model called the `workflow` tool, the Lua script ran `meta` / `phase` /
  `agent`, and the child returned `ok=true` while confirming
  `pkg/coding_agent/plugins/extension/workflow` exists. The run also showed
  that `grep`, `find`, and `ls` are skipped when they are requested in
  workflow child options but are not active in the parent tool set; later real
  cases should either enable those tools or extend the host API to expose the
  intended full coding tool set to workflow children.
- 2026-06-17: `go run ./cmd/modu_code -p '<parallel_smoke workflow prompt>'`
  passed as a real configured-model `parallel()` case. Two child branches ran
  through workflow fan-out/fan-in and returned `ok=true`, confirming the
  `cmd/modu_code` entrypoint and workflow extension directory.
- 2026-06-17: `go run ./cmd/modu_code -p '<worktree_smoke workflow prompt>'`
  passed as a real configured-model `isolation="worktree"` case. The child
  confirmed `go.mod` was visible, the workspace was a linked git worktree, and
  no files were modified.
- 2026-06-17: `go run ./cmd/modu_code -p '<partial_failure_smoke workflow prompt>'`
  passed as a real configured-model partial-failure case after making
  `json.null` stable per Lua state. The good branch returned
  `GOOD_BRANCH_OK`, the invalid-model branch returned null, and the script's
  `out[2] == json.null` check produced `ok=true`.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./cmd/modu_code/internal/provider ./cmd/modu_code/internal/acp ./cmd/modu_code`
  passed for the workflow hardening and `modu_code` follow-up fixes: real
  optional workflow budget semantics, memory-scope and explicit max-turns
  validation, preview bounds, ACP marshal-error handling, invalid active-model
  rejection, config stderr diagnostics, and signal-aware print mode wiring.
  Forked workflow/subagent children also receive explicitly requested
  `grep` / `find` / `ls` read-only discovery tools while the parent coding
  session keeps those tools opt-in by default, resolving the real workflow
  review case where child repository-inspection tasks had those tools skipped.
  Coverage includes a host-level `forkSession` test proving the child LLM
  context receives those requested read-only tools.
  Config target matching is now shared through `provider.ModelMatchesTarget`
  instead of duplicating active-model matching logic in `cmd/modu_code`.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the Claude Code parity guardrail slice. Coverage includes
  workflow budget exhaustion preventing extra forks, the default max-agent cap,
  the existing workflow/subagent host integration tests, and full-repo tests.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow saved-loader and `/workflows` read-only manager
  slices. Focused tests cover `script_path`, saved `name` resolution with
  project precedence, exactly-one source validation, and `/workflows list/show`.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go run ./cmd/modu_code --no-approve -p '<budget workflow smoke prompt>'`
  passed as a real configured-model workflow budget smoke case on
  `deepseek-v4-flash`: the model called `workflow` with `budget=20`, Lua
  observed `budget_total=20` and `budget_before=20`, and the child agent output
  drove `budget_after=0`. The child did not strictly limit itself to the
  requested fixed string, so future deterministic child-output tests should use
  stronger system/profile constraints.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go run ./cmd/modu_code --no-approve -p '<workflow child ls smoke prompt>'`
  passed as a real configured-model workflow child-tool case. The main agent
  called `workflow` directly, the Lua script spawned a child with
  `tools={"ls"}`, and the child returned `{"ok":true,"checked":"cmd/modu_code"}`
  without the previous `unknown tool "ls"` skip.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go run ./cmd/modu_code --no-approve -p '<budget stop workflow smoke prompt>'`
  passed as a real configured-model workflow guardrail case. The main agent
  called `workflow` with `budget=1`; the first child returned `12345678`, Lua
  reported `spent=2` and `remaining=0`, and the second child result was
  absent/nil, confirming budget exhaustion prevented the later `agent()` fork.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go run ./cmd/modu_code --no-approve -p '<persist visible workflow smoke prompt>'`
  passed as a real configured-model workflow script-persistence case. The
  workflow tool returned a visible `Script:` path under
  `~/.coding_agent/sessions/.../extensions/workflow/runs/<run-id>/script.lua`,
  and reading that file confirmed it contained the `persist_visible_smoke`
  inline Lua script.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go run ./cmd/modu_code --no-approve -p '<script_path workflow relaunch prompt>'`
  passed as a real configured-model workflow relaunch case. The workflow tool
  loaded the previously persisted `persist_visible_smoke` `script.lua` via
  `script_path`, returned `PERSIST_VISIBLE_OK`, and persisted a new run script
  under a later `extensions/workflow/runs/<run-id>/script.lua` path.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow disable/config slice. Focused coverage includes
  workflow `config.disabled`, `MODU_CODE_DISABLE_WORKFLOWS`, invalid config
  key/type handling, and preserving normal registration when disable switches
  are unset.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow structured-output slice. Focused coverage includes
  `schema` prompt contracts, validated Lua table returns, schema validation
  failures returning `json.null`, one corrective retry before success, and
  invalid schema definition errors.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the one-level nested workflow slice. Focused coverage includes
  saved workflow name resolution from project workflow directories, nested args,
  shared budget accounting, shared max-agent enforcement, and rejecting
  second-level nesting.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the saved workflow slash-command slice. Focused coverage includes
  `/workflow:<name>` registration at extension init, project-over-user
  precedence, JSON args passed into Lua, command result notification, persisted
  run script paths, and invalid JSON args being rejected before execution.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the completed workflow snapshot metadata slice. Focused coverage
  includes persisting `snapshot.json` next to `script.lua`, `/workflows`
  list/show metadata rendering, and saved workflow slash-command runs writing
  completed metadata.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the live workflow management slice. Focused coverage includes
  `workflow(async=true)` returning a run id immediately, `/workflows` listing a
  running workflow, `/workflows stop <run-id>` cancelling it, stopped status in
  `/workflows show`, and saved workflow slash commands starting background runs
  while still persisting completed metadata.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow save-from-run slash slice. Focused coverage
  includes `/workflows save latest <name>` writing project workflows,
  `/workflows save latest <name> user` writing user workflows, and invalid
  saved workflow names being rejected without writing files.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the nearest project workflow directory slice. Focused coverage
  includes saved workflow name resolution preferring the nearest project
  directory over parent/user workflows, startup command discovery using the same
  precedence, project saves choosing the nearest existing `.claude/workflows`
  directory, and fallback saves writing to git root `.claude/workflows`.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the `/workflows restart` relaunch slice. Focused coverage
  includes restarting a persisted run script as a new background workflow,
  emitting the new run id, executing one new child agent, and listing the
  restarted run metadata.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow status persistence slice. Focused coverage includes
  writing `status.json` for a stopped background workflow and showing the
  stopped status plus error from a fresh extension instance that only reads
  persisted run files.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow per-agent timing slice. Focused coverage includes
  completed agent `startedAt`/`endedAt`/`durationMs` metadata and
  `/workflows show` rendering per-agent duration.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow same-session resume slice. Focused coverage
  includes stopping a two-agent background workflow, resuming it, proving the
  completed first agent is not forked again, running only the incomplete second
  agent live, and showing the cached marker in `/workflows show`.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow phase-progress summary slice. Focused coverage
  includes per-agent `estimatedTokens`, generated `phaseSummaries`, and
  `/workflows show` rendering phase-level agent counts, estimated tokens, and
  duration before individual agent details.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow agent-detail drill-down slice. Focused coverage
  includes `/workflows agent latest 1` rendering workflow name, agent status,
  phase, result preview, prompt, and invalid agent-id validation.
- 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/{activity.go,workflow.go,runtime.go,snapshot.go,registry.go,tool.go,commands.go,workflow_test.go} && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow child-event activity slice. Focused coverage
  includes `subagent_child_event` correlation through `BubbleTaskID`,
  per-agent `turnTokens`, failed tool-call count, recent child tool names/errors
  in the workflow snapshot, and `/workflows agent latest 1` rendering those
  fields.
- 2026-06-18: `gofmt -w pkg/coding_agent/foundation/config/config.go pkg/coding_agent/coding_agent.go pkg/coding_agent/coding_agent_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow settings-disable slice. Focused coverage includes
  reading `disableWorkflows` from `settings.json` and proving a session created
  with the real workflow extension does not register the `workflow` tool or
  `/workflows` command when that setting is true.
- 2026-06-18: `gofmt -w pkg/tui/run.go pkg/tui/config_interactive.go pkg/tui/bubble_test.go pkg/coding_agent/config_api.go pkg/coding_agent/coding_agent_test.go cmd/modu_code/main.go cmd/modu_code/config_command.go cmd/modu_code/main_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the `/config` dynamic-workflows toggle slice. Focused coverage
  includes the TUI `Dynamic workflows` config row, writing
  `disableWorkflows` to global `settings.json`, removing the current session's
  `workflow` tool and workflow slash commands when disabled, and full-repo
  regression tests.
- 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/tool.go pkg/coding_agent/plugins/extension/workflow/saved_commands.go pkg/coding_agent/plugins/extension/workflow/commands.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the Claude-compatible saved workflow directory slice. Focused
  coverage includes loading nested workflows from project `.claude/workflows`,
  discovering `/workflow:<name>` commands from nearest `.claude/workflows`,
  discovering sibling `~/.claude/workflows`, and preserving `.coding_agent`
  discovery fallback behavior.
  Updated saved workflow save/load precedence for Claude parity: `.claude/workflows`
  wins over legacy `.coding_agent/workflows`, personal `~/.claude/workflows`
  wins over agent-dir workflows, project saves write the nearest existing
  project `.claude/workflows` or git root `.claude/workflows`, and user saves
  write `~/.claude/workflows`.
- 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/bundled.go pkg/coding_agent/plugins/extension/workflow/workflow.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the bundled `/deep-research` workflow slice. Focused coverage
  includes command registration, empty-question usage output, background run
  startup, six child-agent phases/calls, and final workflow completion output
  containing the question and report.
- 2026-06-18: `gofmt -w pkg/coding_agent/services/systemprompt/builder.go pkg/coding_agent/services/systemprompt/builder_test.go pkg/coding_agent/prompt_refresh.go pkg/coding_agent/resources_api.go pkg/coding_agent/config_api.go pkg/coding_agent/coding_agent.go pkg/coding_agent/coding_agent_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/services/systemprompt ./pkg/coding_agent ./cmd/modu_code ./pkg/coding_agent/plugins/extension/workflow && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow authoring-prompt slice. Focused coverage includes
  builder-level workflow guidance only when the `workflow` tool is present,
  real sessions with the workflow extension exposing Lua workflow instructions
  in the system prompt, and disabled workflow sessions removing both the tool
  and authoring guidance.
- 2026-06-18: `gofmt -w pkg/coding_agent/services/systemprompt/modes.go pkg/coding_agent/services/systemprompt/builder_test.go pkg/coding_agent/coding_agent.go pkg/coding_agent/config_api.go pkg/coding_agent/prompt_refresh.go pkg/coding_agent/runtime_state.go pkg/coding_agent/slash_commands.go pkg/coding_agent/coding_agent_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/services/systemprompt ./pkg/coding_agent ./cmd/modu_code ./pkg/coding_agent/plugins/extension/workflow && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the `/effort ultracode` slice. Focused coverage includes
  requiring both the workflow tool and an xhigh-capable model, setting thinking
  to `xhigh`, adding/removing the Ultracode system-prompt block, recording
  `modes.ultracode` in runtime state, and ordinary effort levels exiting
  ultracode mode.
- 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/approval.go pkg/coding_agent/plugins/extension/workflow/tool.go pkg/coding_agent/plugins/extension/workflow/saved_commands.go pkg/coding_agent/plugins/extension/workflow/bundled.go pkg/coding_agent/plugins/extension/workflow/commands.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow pre-run approval slice. Focused coverage includes
  confirmation prompts containing workflow name, description, inferred phases,
  source, script preview, and resource limits; denial for direct tool and saved
  workflow command paths prevents any `ForkSession` calls; `/deep-research`
  still starts when approval allows it.
- 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/approval.go pkg/coding_agent/plugins/extension/workflow/approval_store.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow always-allow approval slice. Focused coverage
  includes run-once / always-allow / cancel choices, writing
  `workflow_approvals.json`, and skipping future approval prompts for the same
  project/workflow/script while still executing the workflow.
- 2026-06-18: `gofmt -w pkg/coding_agent/plugins/extension/workflow/approval.go pkg/coding_agent/plugins/extension/workflow/workflow_test.go && env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the workflow view-raw approval slice. Focused coverage includes
  selecting `View raw script`, receiving the full Lua script in a workflow
  notification, returning to the approval choice, and then running once without
  skipping execution.
- 2026-06-18: exact `/workflows` in the Bubble Tea TUI now opens a selectable
  workflow run panel instead of only writing a static overview block. The panel
  uses workflow runtime state, supports `j/k` or arrows, `Enter`/right to route
  through the existing `/workflows show <run-id>` command, and `Esc`/`q` close.
  Phase/agent nested drill-down and `p`/`x`/`r`/`s` controls remain follow-up
  slices.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the selectable TUI `/workflows` run-list slice.
- 2026-06-18: exact `/workflows` in the Bubble Tea TUI now has a second
  progress-view layer. Selecting a run with phase summaries opens a phase list
  with per-phase agent counts, done/running/error counts, estimated tokens, and
  elapsed duration; `Esc`/left returns to the run list. Agent-level selection
  and `p`/`x`/`r`/`s` controls remain follow-up slices.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the selectable TUI `/workflows` phase-view slice.
- 2026-06-18: exact `/workflows` in the Bubble Tea TUI now supports the next
  progress-view layer: phase -> agent list -> agent detail. Agent detail shows
  status, phase, token/tool counters, prompt preview, result/error preview, and
  recent tool-call args/results from runtime state. Full prompt/transcript
  rendering and `p`/`x`/`r`/`s` controls remain follow-up slices.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the selectable TUI `/workflows` agent drill-down slice.
- 2026-06-18: exact `/workflows` in the Bubble Tea TUI now wires progress-view
  controls to existing slash commands: `p` pauses/resumes the selected run,
  `x` stops the selected agent or whole run depending on focus, and `r`
  restarts the selected agent. `s` currently shows the explicit
  `/workflows save <run-id> <name> [project|user]` command until the save-name
  input UI is implemented.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the TUI `/workflows` p/x/r control-key slice.
- 2026-06-18: exact `/workflows` in the Bubble Tea TUI now implements the `s`
  save dialog. It captures a safe workflow name, toggles project/user scope with
  `Tab`, returns to the previous panel layer with `Esc`, and delegates the
  actual write to the existing `/workflows save <run-id> <name> <scope>` slash
  command.
- 2026-06-18: `env GOCACHE=/private/tmp/modu-go-build go test ./pkg/coding_agent/plugins/extension/workflow ./pkg/tui ./pkg/coding_agent ./cmd/modu_code && env GOCACHE=/private/tmp/modu-go-build go test ./... && git diff --check`
  passed after the TUI `/workflows` save dialog slice.
- 2026-06-18: workflow runtime state now includes a capped per-agent prompt
  payload, and the TUI `/workflows` agent detail view renders multi-line
  prompts instead of only the preview. Runtime state and the slash drill-down
  both keep a 4000-byte display boundary.
- 2026-06-18: fixed stopped workflow status persistence so a user-facing
  stop/pause reason is not overwritten by the background runner's later
  `context canceled` error. The focused async stop regression now checks the
  persisted `status.json` after extension reload.
- 2026-06-18: workflow child transcript capture is now wired into the progress
  view. Host `subagent_child_usage` events include the workflow bubble task id,
  workflow snapshots persist normalized child transcript entries, and
  `/workflows transcript <run-id|latest> <agent-id>` renders the captured
  user/assistant/tool-result messages, tool-call args, and usage while keeping
  heavy transcript payloads out of lightweight runtime state.
- 2026-06-18: validation includes a real `CodingSession` + workflow extension
  case (`TestWorkflowToolCapturesRealForkTranscript`) where the workflow tool
  runs a Lua script, forks a child through the host, receives a fake child model
  reply, and verifies the child transcript plus usage in the workflow snapshot.
- 2026-06-18: workflow budget accounting now uses captured child token usage
  from `subagent_child_usage` when available, falling back to final-text
  estimation only when child usage is absent. New workflow tests cover both
  visible `budget.spent()` / `budget.remaining()` values and budget exhaustion
  when the child final text is short but the real child usage exceeds the
  budget.
- 2026-06-18: workflow/subagent child tool forwarding now matches the intended
  allowlist boundary more closely: omitted `tools` inherits the current
  main-agent visible tools, while explicit `tools` requests are filtered from
  the parent session tool catalog so session-connected/custom/MCP-style tools
  can be forwarded by name. Focused host tests cover explicit custom-tool
  forwarding, narrowed allowlist inheritance, and the existing read-only
  discovery-tool opt-in behavior.
- 2026-06-18: saved workflows now register Claude-style direct slash commands
  such as `/review [json-args]` when the name is not reserved, while preserving
  the compatibility `/workflow:review` command. Reserved names like
  `workflows.lua` do not override built-in/extension commands, and disabling
  workflows removes both direct and compatibility saved workflow commands.
- 2026-06-18: workflow observed cost now flows through the child usage path.
  When `subagent_child_usage` or live child turn events include
  `Usage.Cost.Total`, snapshots aggregate it at agent/phase/workflow level and
  `/workflows show`, `/workflows agent`, runtime state, and the TUI workflow
  panel render it. The runner does not synthesize cost from model pricing.
- 2026-06-18: hardened stopped workflow persistence against async completion
  races. Once `status.json` records a stopped run, later background
  completed/failed writes cannot overwrite it unless the user explicitly
  resumes the run back to running.
- 2026-06-18: workflow budget admission is now concurrency-aware. Each live
  child fork atomically reserves one budget slot alongside the agent-count
  check, releases it on failed/stopped attempts, and caps committed
  `budget.spent()` at `budget.total` while retaining real per-agent observed
  token counts in snapshots. Parallel budget tests cover the previous
  oversubscription window, and docs now state default concurrency 4 with a
  hard cap of 16.
- 2026-06-18: clarified the workflow tool/manager boundary after a real TUI
  misuse case tried `workflow(action=status id=...)`. Tool descriptions,
  dynamic-workflow prompt guidance, Ultracode guidance, and README docs now say
  the `workflow` tool only starts runs and `/workflows ...` commands handle
  status, agent details, stop/resume, restart, save, and the TUI panel.
- 2026-06-30: first-run `modu_code` now opens the interactive TUI even when no
  model provider is configured. The session starts with an explicit
  unconfigured placeholder model and shows a startup notice directing the user
  to `/config`; non-interactive `-p`, `--rpc`, and `--acp` still fail fast with
  the CLI quick-start guidance. `/config add` and provider setup now sync the
  current session to the resolved active model after config writes.
- 2026-06-30: `env GOCACHE=/private/tmp/modu-go-build go test ./cmd/modu_code ./pkg/tui ./pkg/slash ./pkg/coding_agent`
  passed for the first-run `/config` TUI setup slice.
- 2026-06-30: fixed the newer `modu-tui` runner so `/config` is handled as a
  local slash command instead of falling through to `unknown command`. The
  config status output now lists usable slash subcommands for add/use/list and
  validate.
- 2026-06-30: `/config` in the newer `modu-tui` runner now opens an
  interactive configuration wizard. While the wizard is active, ordinary input
  is routed to the wizard instead of the model. The flow supports provider
  setup/model discovery, manual model add, active model selection, and status
  display. API keys are configured via environment variable names in the wizard
  so secrets are not echoed into transcript history.
- 2026-06-30: exact `/config` now opens the wizard even if it reaches the
  generic slash runner fallback. `/config validate` and other argument-bearing
  config commands still use the textual command output path.
- 2026-06-30: config wizard choices now use the `modu-tui` fixed human-prompt
  card instead of printing menu text into the transcript. The shared prompt card
  renders vertical options, supports up/down and j/k navigation, Enter to
  choose, numeric quick selection, and Esc cancel. `/config` action, provider,
  and active-model choices use this card; free-text fields still use the normal
  input line.
- 2026-06-30: provider setup now asks how to configure authentication before
  requesting any key material. Choosing "Paste API key" opens a masked fixed
  input card backed by `RequestHumanTextMsg`, so the key is not echoed into the
  transcript or input history; choosing env-var still records an env var name,
  and local providers can skip keys.
- 2026-06-30: exact `/model` in the newer `modu-tui` runner now opens the same
  fixed selection card used by `/config`. The card lists available models with
  the current model marked, supports keyboard selection, and switches through
  `SetModelByID`; argument-bearing commands such as `/model list` and
  `/model <provider> <modelId>` still use the existing slash output path.
- 2026-06-30: registered `/config` in the newer `modu-tui` base slash command
  list so it appears in slash completion/discovery instead of only existing as
  a runtime intercept. `/help` now also documents `/config` and
  `/config validate`, with test coverage guarding `/config` and `/model` in
  the base command registry.
- 2026-06-30: simplified the `/config` wizard card hierarchy. The top card now
  offers only "Setup with provider or add model manually" and "Show config
  status"; choosing setup opens a fixed provider card ordered as DeepSeek,
  LMStudio, Ollama, and Custom OpenAI-Compatible. Non-top card cancellation
  returns to the previous layer, and text prompts accept `back` to go back.
- 2026-06-30: improved the empty `/prompts` slash-command state. Instead of
  only saying no templates were found, it now prints a concrete
  `.coding_agent/prompts/review.md` example, shows `$ARGUMENTS`, and gives the
  follow-up `/reload`, `/prompts`, and `/review cmd/modu_code` commands.
- 2026-06-30: merged global coding-agent settings into `~/.modu/config.toml`
  under `[settings]` while keeping project `.coding_agent/settings.json` and
  legacy global `settings.json` readable. Global saves now write only
  non-default settings, so empty `retrySettings`, `harness`, `permissions`, and
  default feature flags are omitted. Legacy global JSON is migrated into
  `config.toml` when no `[settings]` table exists, and model config saves
  preserve the `[settings]` table.
- 2026-06-30: added panic containment around the newer `modu-tui` runner. The
  top-level TUI now restores terminal state, exits alternate screen/mouse
  tracking, prints a clean panic stack to stderr, and returns a UI error;
  background TUI goroutines for agent runs, slash commands, config/model
  panels, queueing, interrupts, and startup events now recover and report an
  internal panic in the TUI instead of crashing the whole process.
- 2026-06-30: fixed the workflow panic shown from
  `snapshotTracker.finishAgent -> emitSnapshot`. Workflow progress `onUpdate`
  callbacks are now best-effort and recover locally, so a registry/TUI progress
  sink panic cannot crash the workflow goroutine or leave the terminal in a raw
  control-sequence state. Added focused snapshot tracker coverage for a
  panicking update callback during agent finish and completion.
- 2026-06-30: reworked workflow completion output so users can see the
  orchestration before reading the payload. Completed workflows now render an
  `Execution flow` section grouped by phase with each agent's status,
  duration/token/tool/result preview, followed by a separate `Final result`
  section. This prevents dynamic workflow results from arriving as one
  undifferentiated transcript blob.
- 2026-06-30: exact `/workflows` in the newer `modu-tui` runner now renders a
  local `Workflow Cockpit` instead of falling through to the transcript-style
  session slash command. The cockpit opens with run counts and the live
  indicator, then shows an orchestration map grouped by phase with agent
  status, prompt/result/tool previews, followed by latest-run metadata and
  drill-down commands.
- 2026-06-30: `/workflows show <run-id|latest>` no longer truncates the final
  workflow `Result` at 600 characters or the persisted workflow script at 4000
  characters. Structured results render as indented JSON, plain text results
  keep their original text, and the persisted-run coverage now asserts long
  result/script tails are present.
- 2026-06-30: started the Claude-Code-style dynamic workflow TUI surface for the
  newer `modu-tui` runner. `pkg/modu-tui` now has a host-owned scrollable
  `SetPanelMsg`/`ClearPanelMsg` main-view panel, exact `/workflows` opens the
  workflow cockpit in that panel instead of appending transcript text, and
  workflow `RuntimeState()` now merges persisted runs with live registry state
  so the cockpit and `/workflows list` no longer disagree after a restart.
- 2026-06-30: made the `modu-tui` panel selectable. Panels can now render
  host-owned rows, use ↑/↓ to move selection, and emit `Hooks.PanelAction` on
  Enter. The `/workflows` cockpit uses this to list recent workflow runs as
  selectable rows; pressing Enter closes the panel and opens
  `/workflows show <run-id>` for the selected run.
- 2026-06-30: moved `/workflows` run selection one step further into the TUI.
  Selecting a run in the workflow cockpit now opens a `Workflow Run` detail
  panel instead of dumping `/workflows show` output into the transcript. The
  detail panel shows summary metadata, phase/agent orchestration, agent result
  previews, and a selectable Back row that returns to the cockpit.
- 2026-06-30: added workflow detail subviews inside the newer `modu-tui` panel.
  The `Workflow Run` panel now exposes selectable `Result` and `Script` rows;
  choosing them opens `Workflow Result` or `Workflow Script` panels in-place,
  reading full result data from `snapshot.json` and full workflow source from
  `script.js`, with Back rows returning to the run detail or cockpit.
- 2026-06-30: added workflow agent subviews to the newer `modu-tui` panel. The
  `Workflow Run` detail panel now has an `Agents` row, which opens a selectable
  `Workflow Agents` list. Selecting an agent opens a `Workflow Agent` panel with
  summary metadata, prompt/result/error previews, and recent tool-call previews,
  plus Back rows to the agent list or run detail.
- 2026-06-30: added full workflow agent transcript browsing inside the
  `modu-tui` panel. `Workflow Agent` now exposes a `Transcript` row, which reads
  the selected agent's full child transcript from `snapshot.json` and renders
  user/assistant/tool entries, tool calls, and usage in a `Workflow Transcript`
  panel with Back rows to the agent or agent list.
- 2026-06-30: added workflow control actions to the newer `modu-tui` workflow
  detail panel. Running workflows expose `Pause` and `Stop`; stopped workflows
  expose `Resume` and `Restart`; completed/failed workflows expose `Restart`,
  with each row routed through the existing `/workflows` control commands and
  returning to the refreshed run detail panel.
- 2026-06-30: made workflow panels live-refresh while they are open. The
  `modu_code` runner now tracks the current workflow cockpit/detail/agent/result
  panel, polls the workflow runtime-state fingerprint, and sends
  `RefreshPanelMsg` when the state changes. `pkg/modu-tui` preserves the
  selected row and scroll offset on same-panel refreshes, and `Hooks.PanelClosed`
  stops refreshing when the user closes the panel.
- 2026-06-30: added phase-level workflow drill-down. `Workflow Run` panels now
  expose each orchestration phase as a selectable row before the global
  Agents/Result/Script actions; selecting a phase opens a live-refreshing
  `Workflow Phase` panel showing only that stage's progress, agent rows,
  prompt/result/error previews, and tool summaries, with navigation back to the
  run detail, all agents, or cockpit.
- 2026-06-30: routed read-only workflow slash subcommands into the new panel
  surface. `/workflows list` opens the cockpit, `/workflows show <run|latest>`
  opens the run detail panel, and `/workflows agent|transcript <run|latest>
  <agent-id>` opens the corresponding agent/transcript panel. Control commands
  such as pause/stop/resume/restart still execute through the workflow slash
  command path.
- 2026-07-01: added per-agent workflow controls to the `Workflow Agent` panel.
  Running agents now expose `Stop agent` and `Restart agent` rows that route to
  the existing `/workflows agent-stop` and `/workflows agent-restart` commands,
  then return to the refreshed agent detail panel. Completed agents keep the
  read-only Transcript/Back navigation.
- 2026-07-01: routed workflow lifecycle notifications into the panel surface.
  Workflow extension `Run:`/`New run:` start messages now open the matching
  `Workflow Run` detail panel, completion messages open the latest run detail,
  and control/error notifications update the status line instead of appending a
  large workflow block to the transcript.
- 2026-07-01: routed workflow tool results into the panel surface. Async
  `workflow` tool results with `Details.runID` now open the matching run detail
  panel, synchronous completion results open the latest run detail, and the
  transcript tool block keeps only a compact "Opened workflow run panel" summary
  instead of rendering the full workflow report inline.
- 2026-07-01: workflow tool starts now open the `Workflow Cockpit` immediately.
  This gives synchronous workflow runs a live panel surface before a run id is
  returned; the existing runtime-state refresh loop then keeps the cockpit
  updated while the workflow registry receives progress snapshots, and final
  tool results still switch to the run detail panel.
- 2026-07-01: tuned workflow panel default focus. The cockpit now initially
  selects the latest running run when one exists, run detail panels select the
  current/running phase instead of a control row, phase and agent-list panels
  select the running agent, and running agent detail panels focus Transcript
  instead of Stop/Restart controls. Live refresh still preserves the user's
  current row selection.
- 2026-07-01: added a compact live flow summary to `Workflow Run` panels. The
  detail view now shows a short `flow` block before the full orchestration map,
  including phase progression, the current phase, active agents, attention/error
  agents, and the next waiting phase so users can understand the workflow shape
  without reading the serialized transcript output.
- 2026-07-01: added a phase timeline to `Workflow Run` panels. The new
  `timeline` block expands the live flow into one short row per phase with
  status, progress, token/duration hints, and at most two active/error agent
  callouts, giving a Claude-Code-style step feed without dumping full child
  transcripts into the main conversation.
- 2026-07-01: routed workflow `log(...)` progress messages into dynamic TUI
  panels. Workflow runtime state now exposes capped recent logs for each run,
  and `Workflow Run` panels render them as a short `updates` feed between the
  live flow and phase timeline, so script-authored progress messages are visible
  without opening `snapshot.json`.
- 2026-07-01: promoted the latest run's live flow, updates, and phase timeline
  into the `Workflow Cockpit` first screen. Exact `/workflows` now shows the
  same compact execution feed that run detail uses, so users can understand the
  active orchestration shape before drilling into a run, phase, or agent.
- 2026-07-01: added workflow quick-entry rows and a dedicated `Workflow Feed`
  panel. Run detail now starts with `Execution feed`, `Current phase`, and
  `Active agent` rows before control actions, and the feed panel shows only
  flow/updates/timeline with focused navigation into the current phase or active
  agent. This makes the dynamic workflow panel behave more like an operations
  cockpit than a long command list.
- 2026-07-01: added panel shortcuts for workflow controls. `pkg/modu-tui`
  panels now support `Panel.Shortcuts`, and workflow run/feed panels map `p` to
  pause/resume, `x` to stop, and `r` to restart where valid; running agent
  detail panels map `x` to stop-agent and `r` to restart-agent. All shortcuts
  reuse the existing `PanelAction` slash-command control path.
- 2026-07-01: surfaced workflow shortcut hints in panel footers. Run, feed, and
  running-agent panels now append context-specific `[p]`, `[x]`, and `[r]`
  control hints to the footer whenever those shortcuts are active, so the TUI
  exposes controls without requiring users to discover them from row labels.
- 2026-07-01: added a workflow `board` section to the cockpit, run detail, and
  feed panels. The board renders each phase as a numbered step with done,
  running, error, or waiting state, then highlights attention and active agents
  under the relevant phase so users can see the orchestration shape before
  opening detailed timelines or transcripts.
- 2026-07-01: split the full workflow orchestration dump into a dedicated
  `Workflow Map` panel. `/workflows map <run-id|latest>` and the new `Map` rows
  show the complete phase/agent tree, while run detail stays focused on
  summary, board, flow, updates, timeline, and actions to avoid turning
  `/workflows show latest` into a long truncated transcript block.
- 2026-07-01: made `Workflow Feed` a first-class slash route. In the modu TUI,
  `/workflows feed <run-id|latest>` opens the live feed panel directly; in the
  workflow extension command it prints the same short execution-feed shape
  without full result/script expansion. This gives users an explicit dynamic
  follow mode instead of forcing `/workflows show latest` to carry both summary
  and full inspection duties.
- 2026-07-01: routed workflow started/restarted events into `Workflow Feed` by
  default when the run is still active. Explicit `/workflows show` continues to
  open run detail, and completed workflow events still open detail, but live
  background runs now land in the dynamic follow view immediately.
- 2026-07-01: added direct view-switch shortcuts across workflow panels. Run
  detail exposes `[f] Feed`, `[m] Map`, and `[a] Agents`; feed exposes `[d]
  Detail`, `[m] Map`, and `[a] Agents`; map exposes `[f] Feed`, `[d] Detail`,
  and `[a] Agents`. The shortcuts reuse the existing panel action routing, so
  users can move between follow, summary, tree, and agent views without
  scrolling to navigation rows.
- 2026-07-01: added compact agent `lanes` to `Workflow Feed`. Each phase now
  gets a single scan line such as `Research: run #3 verify | err #4 risk`,
  making the active/done/error agent distribution visible without opening the
  full map or per-agent list.
- 2026-07-01: added a lane legend to `Workflow Feed` so the compact agent
  markers are self-explanatory: `run active`, `done complete`, `err attention`,
  and `wait queued`.
- 2026-07-01: added `Attention agent` quick rows to workflow run detail and
  feed panels. When any agent has an error/failed state, users can jump directly
  to that agent from the top navigation rows, before drilling through the full
  agent list or map.
- 2026-07-01: added `[!] Attention` shortcuts to workflow run detail and feed
  panels when an error/failed agent exists. The shortcut opens the first
  attention agent directly through the existing panel action route, matching the
  `err attention` lane marker.
- 2026-07-01: made `Workflow Map` interactive instead of a read-only tree. The
  map panel now includes current/attention/active quick rows, phase rows, and
  feed/detail/agents navigation rows, and its panel actions can drill directly
  into phase and agent panels.
- 2026-07-01: added lightweight cards to the top of `Workflow Feed`. The feed
  now starts with stable `Status`, `Attention`, `Active`, and `Next` cards when
  the runtime snapshot has matching data, giving active workflow runs a more
  scannable Claude-style status surface before the board, lanes, updates, and
  timeline sections.
- 2026-07-01: changed the `/workflows` cockpit entry action for running runs to
  open `Workflow Feed` instead of run detail. Completed, failed, and stopped
  rows still open detail, but active runs now follow the dynamic status surface
  by default when selected from the cockpit.
- 2026-07-01: added a run-scoped `Workflow Guide` panel behind the `[?] Guide`
  shortcut from Feed, Detail, and Map. The guide shows how Feed, Map, Phase,
  Agent, and Transcript views fit together for the current run, plus direct rows
  back into the live feed, structure map, detail panel, current phase, active
  agent, and attention agent when those snapshots exist.
- 2026-07-01: added the TUI slash route `/workflows guide <run-id|latest>` so
  users can open the run-scoped guide directly from the prompt, not only through
  `[?] Guide` inside an existing workflow panel.
- 2026-07-01: slimmed the `/workflows` cockpit so it no longer embeds the full
  orchestration map for the latest run. The cockpit now stays as a dashboard
  with board/flow/updates/timeline plus explicit Guide, Feed, Map, and Detail
  next actions; the complete tree remains in `Workflow Map`.
- 2026-07-01: promoted `Workflow Guide` to a selectable row in run detail,
  feed, and map panels, not only a `[?] Guide` shortcut. The quick current
  phase/attention/active rows stay first, then the navigation group starts with
  Guide so users can discover the view map without memorizing shortcuts.
- 2026-07-01: added run-level navigation to `Workflow Phase` panels. A phase now
  keeps its agent rows first, then exposes Guide, Feed, Map, Detail, Agents, and
  Back rows plus `[?]`, `[f]`, `[m]`, `[d]`, and `[a]` shortcuts, so drilling
  into one orchestration stage does not strand the user away from the live feed
  or structure map.
- 2026-07-01: added the same run-level navigation to `Workflow Agent` detail
  panels. Transcript and running-agent control rows keep priority, then Guide,
  Feed, Map, Agents, and Detail let users jump back to the run-level dynamic
  views after inspecting an active or attention agent.
- 2026-07-01: added run-level navigation to `Workflow Agents` list panels. The
  agent rows still stay first for quick selection, followed by Guide, Feed, Map,
  Detail, and Back rows plus `[?]`, `[f]`, `[m]`, and `[d]` shortcuts, so the
  all-agents list also connects back to the live workflow surfaces.
- 2026-07-01: added run-level navigation to `Workflow Transcript` panels. The
  transcript keeps `Back to agent` first, then exposes Guide, Feed, Map, Agents,
  and Detail rows plus matching shortcuts, so a deep transcript drill-down can
  return directly to the dynamic workflow surfaces.
- 2026-07-01: added run-level navigation to `Workflow Result` and `Workflow
  Script` panels. Final artifact views now expose Guide, Feed, Map, Agents,
  Detail, and Back rows plus `[?]`, `[f]`, `[m]`, `[d]`, and `[a]` shortcuts, so
  reading the result or generated script no longer traps the user away from the
  live workflow surfaces.
- 2026-07-01: added `Result` and `Script` to `Workflow Guide` as first-class
  artifact views. The guide now explains where final output and generated
  scripts fit in the run map, and its rows can jump directly into those panels
  without returning through run detail first.
- 2026-07-01: added latest-run shortcuts to the `Workflow Cockpit`. The cockpit
  footer now exposes `[?] Guide`, `[f] Feed`, `[m] Map`, and `[d] Detail` for
  the latest run, and its copy says `open` instead of `details` because running
  rows intentionally enter the live Feed.
- 2026-07-01: capped `Workflow Result` and `Workflow Script` panel previews at
  a fixed line budget and added snapshot/script path headers. Oversized
  artifacts now show a truncation line with the full artifact path, keeping the
  dynamic TUI responsive while preserving a clear route to the complete data.
- 2026-07-01: aligned workflow guidance copy with the dynamic TUI design. Tool
  descriptions and README text now direct users to the `/workflows` cockpit,
  Feed, Guide, Map, and artifact panels first instead of treating
  `/workflows show` as the primary inspection surface.
- 2026-07-01: unified async workflow start notifications around the same
  cockpit-first guidance. The workflow tool, `/deep-research`, saved workflow
  commands, and `/workflows restart` now tell users to open `/workflows` for the
  cockpit before listing direct feed/guide/show/stop command fallbacks.
- 2026-07-01: updated `/workflows list` terminal guidance to match the TUI entry
  model. The list footer now starts with `Open /workflows for the cockpit`
  before listing feed/guide/map/show command fallbacks.
- 2026-07-01: added a `Metrics` card to the `Workflow Feed`. The live feed now
  surfaces run-level agent totals, phase state counts, aggregated estimated
  tokens, and elapsed duration directly in the card stack, so the dynamic TUI
  gives a quick execution health read before users drill into phases or agents.
- 2026-07-01: added a `Path` card to the `Workflow Feed`. The feed card stack
  now shows the phase route, current node, and next queued phase before the
  agent-specific cards, making the workflow orchestration visible without
  forcing users to read the serialized timeline or open the map panel first.
- 2026-07-01: added an `Outcome` card to terminal `Workflow Feed` views. When a
  run completes, fails, or stops, the feed now surfaces final artifact entry
  points, snapshot/script paths, and Result/Script rows plus `[o]` and `[s]`
  shortcuts, so final data is reachable as structured TUI artifacts instead of
  being buried at the end of serialized workflow output.
- 2026-07-01: wired observed workflow cost into the Feed `Metrics` card. The TUI
  now reads run/phase/agent cost from workflow runtime state and renders the
  real observed total when available, alongside agent totals, phase counts,
  estimated tokens, and elapsed duration.
- 2026-07-01: added a latest-run preview to the `Workflow Cockpit` panel. The
  `/workflows` entry view now embeds the same Status, Metrics, Path, and
  terminal Outcome cards used by the Feed, and terminal latest runs expose
  `[o] Result` and `[s] Script` shortcuts directly from the cockpit.
- 2026-07-01: added a `recent runs` board to the `Workflow Cockpit` panel. The
  `/workflows` entry view now shows the latest run cards plus a compact list of
  recent workflow runs with status, progress, phase, duration, and error counts,
  so users can see both the active workflow and nearby run history before
  selecting a row.
- 2026-07-01: changed `Workflow Cockpit` run rows to open the Feed for every
  run status. Running rows still enter the live surface, while completed,
  failed, or stopped rows now land on the Feed Outcome cards and can use `[d]`
  for metadata detail instead of bypassing the dynamic workflow view.
- 2026-07-01: made terminal `Workflow Feed` panels select the `Result` row by
  default. Completed, failed, or stopped runs now land on the Outcome cards with
  Enter ready to open the final artifact, while non-terminal feeds still select
  the current phase or attention/active agent first.
- 2026-07-01: routed workflow completion notifications and workflow tool
  completion events back to the Feed instead of the metadata detail panel. The
  dynamic TUI now keeps lifecycle follow-up on the same live surface and lets
  terminal feeds expose Outcome/Result/Script as the primary completion path.
- 2026-07-01: routed workflow tool update events back to the Feed. Runtime
  snapshot updates now resolve their run from `runID`, `runDir`,
  `snapshotPath`, or `scriptPath`, so active workflow progress can refresh the
  dynamic panel instead of disappearing until final completion.
- 2026-07-01: added a `Plan` card to workflow run cards. The Cockpit preview
  and Feed now show the numbered phase route, current orchestration point, next
  blocked/queued stage, and compact per-stage labels before lower-level metrics
  and logs, making the workflow arrangement visible without reading a serialized
  transcript.
- 2026-07-01: made workflow tool/session updates refresh the current workflow
  panel in place when possible. If the user is looking at Cockpit, Map, Phase,
  Agent, Result, Script, or Feed for the same run, incoming live updates now
  send `RefreshPanelMsg` for that view instead of forcing the UI back to Feed.
- 2026-07-01: added a topology section to `Workflow Map`. The map now starts
  with numbered phase nodes, phase-to-phase path edges, and compact per-phase
  agent lanes before the detailed tree, so users can read the workflow
  arrangement before drilling into each agent's output.
- 2026-07-01: removed duplicate phase rows from `Workflow Map` navigation. The
  current phase remains the first focused row, while the full phase list skips
  commands already present in that focus area, making map navigation less
  repetitive without changing Feed or Run Detail quick rows.
- 2026-07-01: added phase position context to `Workflow Phase`. A phase drill
  down now shows its stage number, previous/current/next path, and neighboring
  phase names before the agent list, so users keep the orchestration context
  after opening one stage from the Map or Feed.
- 2026-07-01: added orchestration context to `Workflow Agent`. Agent detail now
  shows the parent phase position, phase path, agent index inside that phase,
  and compact peer lanes before result/tool details, keeping agent drill-downs
  tied to the workflow plan.
- 2026-07-01: added run context to `Workflow Result` and `Workflow Script`.
  Artifact panels now show workflow name, run id, status, progress, current
  phase, and the phase plan route before the payload or script body, so final
  outputs remain tied to the dynamic workflow execution view.
- 2026-07-01: refreshed `Workflow Guide` copy to match the dynamic TUI surface.
  The guide now describes Feed Plan/Metrics cards, Map topology, Phase/Agent
  context, artifact run context, and return paths between artifact views and the
  execution surfaces.
- 2026-07-01: added a panel-derived run-id fallback for workflow refresh events.
  Completion notifications that do not carry `runID` details can now reuse the
  run id embedded in the returned Feed/Result/Script panel, so current workflow
  subviews can refresh in place instead of being replaced unnecessarily.
- 2026-07-01: added phase lanes to `Workflow Agents`. The all-agent view now
  starts with phase-grouped agent lanes before the selectable flat list, so
  users can compare worker distribution across stages without opening the Map.
- 2026-07-01: exposed Result/Script shortcuts from `Workflow Guide`. The guide
  footer now includes `[o] Result` and `[s] Script`, matching its updated
  description of artifact views as first-class workflow surfaces.
- 2026-07-01: added parent-phase navigation to `Workflow Agent` and
  `Workflow Transcript`. Agent drill-downs now offer a direct return to the
  orchestration stage they belong to, preserving the Phase -> Agent ->
  Transcript hierarchy during inspection.
- 2026-07-01: fixed TUI resource hot loading and multiline input history.
  The modu-tui slash picker can now refresh commands from the active session on
  each `/` match, so newly added skills and prompt-template slash commands show
  without restarting. Input history is now stored as JSON lines while still
  reading the legacy one-line format, so multiline prompts restore as one
  history entry.
- 2026-07-01: `go test ./pkg/modu-tui`, `go test ./cmd/modu_code`, and
  `go test ./pkg/coding_agent ./pkg/slash` passed for the hot-load/history fix.
- 2026-07-01: improved workflow panel readability. `pkg/modu-tui` panel titles
  now use a distinct title color, section headings use a secondary color,
  result panels can opt into Markdown rendering for artifact text, and workflow
  panel footers use ASCII `up/down` hints instead of arrow glyphs that can
  display poorly in some terminals. Script/code panels remain plain text so
  markdown-looking content inside source files is not accidentally reformatted.
- 2026-07-07: migrated cron task identity from legacy `id` to `uuid` + `name`.
  Legacy `id:` loads as `name:` and gets a stable generated UUID so list/remove
  works before the migrated file is saved; new saves write `uuid/name`,
  scheduler/logs/notifications use UUID identity, and notifications also include
  `task_name`. The cron extension now registers `/cron`: `list` and
  `rm <uuid>` run directly through cron tools and notify formatted output, while
  `add` and `update` inject a natural-language cron management turn. Verified
  with `go test ./pkg/cron/... ./pkg/coding_agent/plugins/extension/cron` and
  `go test ./pkg/slash ./pkg/coding_agent ./cmd/modu_code ./pkg/tui`.
- 2026-07-16: `go test ./cmd/modu_code ./pkg/slash ./pkg/tui
  ./pkg/channels/... ./pkg/tgbot` and `go run ./cmd/modu_code config path`
  passed for interactive `/channel` configuration and Telegram startup
  restoration. `go test ./...` still has the unrelated existing
  `TestDefaultSystemPromptAllowsNonCodingTasks` failure because its expected
  weather guidance is absent from the current default system prompt.
