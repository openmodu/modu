# modu_code product progress

This file tracks product-experience work for `modu_code`. Keep each item small
enough to implement, verify, and commit independently.

## Done

- Status line moved above the input separator, with animated running state,
  persisted completed state, and duration formatting that supports `min`.
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
