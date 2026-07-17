# modu_code TUI changes, 2026-05-18 to 2026-05-20

This record explains the modu_code TUI changes that landed on `main` through merge commit `611e031` (`Merge pull request #27 from openmodu/refactor/align-tools-extensions`). It connects each user-visible behavior to its implementation and tests.

The review window is from `2026-05-18 00:00` to `2026-05-20 23:59` Asia/Shanghai. In that window, the coding-agent/TUI related surface changed by roughly `55 files`, `8232 insertions`, and `546 deletions` across `pkg/tui`, `pkg/slash`, `pkg/coding_agent`, and `cmd/modu_code`.

## Outcome

The change set completed several previously disconnected interaction paths:

- The input area became closer to a real coding-agent terminal: visible block cursor, `❯` prompt, multiline editing, command/file completion, shell shortcuts, and compact status lines.
- Session management moved from command-only behavior to an interactive flow: pick, search, resume, fork, branch, rename, delete, and inspect session trees.
- Plan mode and worktree mode became inspectable workflows instead of hidden state: `/plan`, `/worktree`, plan revisions, active worktree diff, lifecycle status, and approval gates.
- Skills, prompts, config, settings, and models became selectable resources with consistent headers and search behavior.
- Rendering became denser and easier to scan: highlighted user prompts, structured section rows, collapsed tool output, better approval prompts, and transient status/activity messages.
- The implementation received end-to-end and unit coverage around the new TUI paths, especially selectors, session flows, shell shortcuts, file references, status lines, and approval behavior.

## 1. Terminal layout and input feel

The first layer of improvement is basic terminal ergonomics.

- `fix(tui): repaint scrollback on terminal resize` and `fix(tui): soft reset inline viewport on launch` addressed visual glitches where the inline widget could leave blank areas or stale rows after launch/resize.
- The inline widget now keeps a stable height during normal interaction and resets intentionally on clear. This avoids scrollback gaps while still allowing `Ctrl+L` to recover to a baseline.
- The input cursor was changed toward a Claude Code style cursor: reverse-video while editing characters, then a full block cursor at end-of-line. The full block fix matters because go-tui did not reliably paint a reverse-styled whitespace-only cell.
- CJK cursor handling is covered so wide characters do not shift the visible cursor.
- `Home`/`End`, vertical cursor movement inside multiline drafts, `Ctrl+J` newline, history navigation, and cursor-aware backspace/delete make the prompt behave more like a real editor.

Relevant files:

- `pkg/tui/root.go`
- `pkg/tui/input.go`
- `pkg/tui/ui_test.go`

## 2. Prompt, command, and file-reference workflow

The prompt path became much more capable.

- User prompts are now highlighted as first-class transcript blocks instead of plain text. Local prompts use the inline `❯` marker; external prompts use a separate marker and background so remote/channel input can be distinguished.
- Slash command matching merges static commands with discovered skills and prompt templates.
- `@file` reference completion was added. It scans the current working directory, skips noisy directories like `.git`, `node_modules`, `vendor`, `dist`, `build`, `.next`, and `.cache`, limits match count and byte expansion, and expands selected references into prompt context.
- Path-token completion was added for ordinary path-looking tokens.
- Shell shortcuts were aligned:
  - `!cmd` runs a shell command, prints the output, then sends command/output to the model.
  - `!!cmd` runs and prints output only.
- `/retry` can resubmit the last failed prompt.
- Prompt errors are compacted, deduplicated, and include actionable hints such as `/retry`, `/model`, `/doctor`, and `ctrl+c`.

Relevant files:

- `pkg/tui/prompt.go`
- `pkg/tui/file_refs.go`
- `pkg/tui/suggestions.go`
- `pkg/tui/errors.go`
- `pkg/tui/render.go`

## 3. Selectors became consistent TUI surfaces

A major source of the better feel is that many command-only actions now have interactive selectors.

### Model selector

- `/model` opens a searchable model selector.
- Models are sorted by current model, provider, and id.
- Scoped model editing is supported through `/scoped-models`.
- The selector can show scoped-only mode and updates the session model with context-cleared status when the model changes.

### Session selector

- `/sessions` and `/resume` open a real session picker.
- The picker supports current/all scope, text search, `re:` regex search, quoted exact search, threaded/recent/relevance sorting, named-session filtering, path display, rename, safe delete, resume, and fork.
- Active session rows are marked, delete confirmation is inline, and selected rows show message count and age.

### Tree selector

- `/tree` and `/fork` open a session tree navigator.
- The tree can search nodes, jump to entries, create branched sessions, and toggle summary display.
- Branch summaries and fallback labels make forked histories easier to read.

### Resource selectors

- `/skills` and `/prompts` open searchable resource selectors.
- Selecting a resource inserts the corresponding slash command into the draft.
- Config and resource picker commands were wired into the TUI path.

### Shared selector polish

- Selector headers now show title, selected/visible counts, filtered counts, search query, and mode.
- `PageUp`/`PageDown`, `Home`/`End`, `Esc`, `Ctrl+C`, and search keys are implemented consistently across selectors.

Relevant files:

- `pkg/tui/model_select.go`
- `pkg/tui/session_select.go`
- `pkg/tui/tree_select.go`
- `pkg/tui/resource_select.go`
- `pkg/tui/settings_select.go`
- `pkg/tui/selector_header.go`

## 4. Session model and Pi parity

The TUI work was backed by coding-agent session work, not just UI wrappers.

- Session persistence was aligned with Pi-style session headers and entries.
- Session listing now includes name, cwd, first message, full message text, message count, modified time, parent session, and branch metadata.
- Session commands were added for list/resume/fork/branch/delete/name/clone/tree/export.
- Safe session deletion validates that the target is a real session and prevents deleting the active session.
- Session management was exposed through RPC so non-TUI clients can use the same capabilities.
- `/export` writes a session HTML export.
- `/copy` copies the last assistant message.
- `/changelog` shows recent git commits.
- Session summaries were expanded so picker/search results have better labels.

Relevant files:

- `pkg/coding_agent/session/manager.go`
- `pkg/coding_agent/session/entry.go`
- `pkg/coding_agent/persistence.go`
- `pkg/coding_agent/rpc_domain.go`
- `pkg/coding_agent/modes/rpc/*`
- `pkg/slash/commands.go`
- `cmd/modu_code/main.go`

## 5. Plan mode became a visible workflow

Plan mode was tightened both behaviorally and visually.

- Plan mode blocks write/edit tools while planning.
- Plan exit now has an approval gate with explicit approve, approve-and-auto-accept, and reject paths.
- Rejection can collect free-form feedback and send it back to the waiting plan approval.
- Approval-level blocking was strengthened so plan constraints are enforced even when tool calls reach the approval layer.
- `/plan status` exposes active state, artifact existence, revision count, todo counts, and plan file path.
- `/plan` without a subcommand now renders a dedicated TUI panel with status, recent revisions, and todo progress.
- Latest plan display, clear latest plan, and revision history were added.
- The idle status line indicates when plan mode is active.

Relevant files:

- `pkg/coding_agent/tools/planning/plan_mode.go`
- `pkg/coding_agent/plan_worktree.go`
- `pkg/tui/approval.go`
- `pkg/tui/plan_worktree_panel.go`
- `pkg/slash/commands.go`

## 6. Worktree mode became inspectable

Worktree mode also moved from hidden state to visible lifecycle.

- `/worktree status` exposes active state, existence, cwd, path, and original cwd.
- Managed worktrees can be listed.
- Inactive worktrees can be cleaned up.
- Active worktree diff is available and shown in the TUI panel.
- `/worktree` without a subcommand now renders a dedicated panel with status, managed worktrees, and active diff summary.
- The idle status line indicates when a worktree is active.

Relevant files:

- `pkg/coding_agent/plan_worktree.go`
- `pkg/tui/plan_worktree_panel.go`
- `pkg/slash/commands.go`

## 7. Approval prompts and safety feedback

Approval UI was made more explicit and less ambiguous.

- Normal approval prompts now show a layered structure: title, tool name, args, and action hints.
- Plan approval prompts show step count and auto-accept risk text.
- Approval can be dismissed externally, for example by another channel responding.
- `Ctrl+O` can toggle tool output display while approval is pending.
- Abort now denies pending approval, aborts the agent, and aborts bash.

Relevant files:

- `pkg/tui/approval.go`
- `pkg/coding_agent/approval.go`
- `pkg/coding_agent/coding_agent.go`

## 8. Rendering and visual hierarchy

Rendering changes made the transcript denser and easier to scan.

- User prompt blocks have distinct background styling.
- External user prompts use a separate marker/background from local prompts.
- Assistant replies render with a bullet prefix and preserve markdown styling without duplicated headings.
- Thinking blocks and tool blocks have clearer glyphs and continuation alignment.
- Tool output is collapsed by default, can expand with `Ctrl+O`, and includes `+N more lines` hints.
- Edit output can syntax-highlight diff-like lines when file paths are known.
- Section rendering now recognizes simple `key: value` rows and dims keys while preserving indented lines such as hotkey help.
- The idle status line now shows model, token count, plan/worktree markers, and compact key hints.
- Completed activity lines briefly persist above the input, then expire. Transient status messages also expire instead of cluttering the bottom row.

Relevant files:

- `pkg/tui/render.go`
- `pkg/tui/theme.go`
- `pkg/tui/statusbar.go`
- `pkg/tui/agent_events.go`

## 9. Settings and persistence

The TUI now has a small settings surface.

- `/settings` opens a selector for display/runtime toggles.
- Tool output display mode can be toggled between compact and expanded.
- Plan mode, thinking level, and worktree mode can be changed from settings.
- TUI display settings persist to `.coding_agent/tui_settings.json` under the session agent directory.

Relevant files:

- `pkg/tui/settings_select.go`
- `pkg/tui/settings_store.go`

## 10. Removed or avoided surface area

Several pieces were removed instead of leaving dead code behind.

- Browser automation and Playwright-related integrations were removed from the coding-agent default surface.
- NotebookLM and image-generation module surfaces were removed in this work stream.
- Default tools and extension APIs were aligned so unavailable capabilities are not advertised as usable.

Relevant commits:

- `ef31da2 chore: remove browser automation integrations`
- `b1cf6c2 refactor(image-gen): remove image generation module`
- Earlier cleanup in the same work stream removed NotebookLM-related packages and examples.

## Test coverage added or expanded

The important point is that most UX changes are now covered at the behavior level, not only through render snapshots.

Notable TUI test areas:

- approval key handling and approval widget detail rows
- plan approval step count and risk display
- cursor editing, CJK cursor rendering, multiline input
- ANSI/style preservation
- user/assistant/tool block rendering
- section key/value rendering
- activity duration, transient status expiry, completed activity expiry
- model selector switching and scoped model selection
- session picker resume/fork/delete
- session tree navigation and branch creation
- selector header counts/query/mode
- file reference completion and prompt expansion
- shell shortcut behavior for `!` and `!!`
- config hook output rendering
- resource selector filtering and insertion
- persisted TUI settings
- plan/worktree panel rendering
- compact prompt errors and retry behavior
- viewport wrapping

Additional coverage was added in:

- `pkg/slash/commands_test.go`
- `pkg/coding_agent/session/session_test.go`
- `pkg/coding_agent/coding_agent_test.go`
- `cmd/modu_code/main_test.go`

## Why the changes matter

Before this work, modu_code exposed many capabilities only through slash commands or backend APIs. The change set made them usable from three direct interaction paths:

1. The default input loop is faster: cursor, completion, shell, file refs, retry, and status feedback all happen in place.
2. Long-running coding-agent concepts are visible: sessions, trees, plan state, worktrees, resources, and settings have dedicated UI instead of hidden command output.
3. The transcript is easier to scan: prompt blocks, approval prompts, tool output, section rows, and activity/status lines now have clear hierarchy and bounded noise.

The result comes from these paths working together, not from one rendering change.

## Commit index

Main TUI and workflow commits in the window:

- `59c566b` strengthen plan mode blocking at approval level
- `44c6230` repaint scrollback on terminal resize
- `c49409b` soft reset inline viewport on launch
- `c3b0ca6` align default tools and extension api
- `03f9a79` align resources and sessions with pi
- `6175899` add session management commands
- `119bd0d` support safe session deletion
- `711e360` expose session management over rpc
- `0a4531f` route session tree slash commands
- `edb0acb` add session picker coverage
- `e920695` align pi controls
- `c3a1c4a` add session tree navigator
- `1816b87` add file reference completion
- `5a0ea8e` align shell shortcuts
- `19ba0e7` add session html export
- `3da0142` expand session summary
- `e874029` add copy command
- `019783c` add changelog command
- `d164e31` add config and resource pickers
- `ba031e8` persist display settings
- `481ab39` polish resource selectors
- `9ac44e9` improve session tree rows
- `0ae8d64` enrich idle status line
- `08e96d9` update hotkey help
- `4d9e860` expose worktree lifecycle status
- `4df0b51` expose plan lifecycle status
- `452e964` show latest plan
- `5a7240c` list managed worktrees
- `7d65e8e` clear latest plan
- `0beba1c` cleanup inactive worktrees
- `cc2c2b0` label branch summaries
- `1f688d7` track plan revision history
- `a23fd11` show active worktree diff
- `61a7b22` add selector headers
- `ba08de1` highlight user prompts
- `d7a5f48` replace prompt glyph with `❯` and inline it in user block
- `9196103` switch cursor to reverse-video style
- `a5c069b` use full block cursor instead of reverse-video space
- `e4b6e3a` align model and session headers
- `e04758a` add plan and worktree panels
- `e568b82` refine approval prompts
- `613e7b6` distinguish external user prompts
- `b022e4c` expire transient status messages
- `cd972e4` structure section rendering
