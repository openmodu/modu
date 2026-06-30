# pkg/modu-tui

`modutui` packages the Bubble Tea v2 fullscreen transcript viewport used by
`cmd/tuipoc2`.

It owns only the reusable UI shell:

- rendered transcript viewport with fixed bottom input
- mouse wheel and PageUp/PageDown scrolling
- every rendered terminal row is padded to the current width so shorter frames
  clear content left by previous scroll frames
- `Jump to bottom` is shown once in a fixed row above the input when scrolled
  away from the bottom, avoiding repeated viewport overlays on mobile terminals
- drag selection with local clipboard plus OSC52 copy; SSH/tmux/screen sessions
  emit passthrough OSC52 so the local terminal can update the user's clipboard
- independent input, text, markdown, collapsible, tool-call, and code blocks
- bottom input history supports Up/Down navigation, keeps a temporary draft,
  and caps retained entries at 100 with a `History n/total` hint on the top
  input rule
- fixed bottom cards are rendered through `CardBlock`, so approval and slash
  command popups share one heavy-border card style
- bottom popups are height-budgeted during terminal resize so the fixed input
  row and cursor remain visible before optional slash/jump detail rows
- active todos can be supplied through `Options.Todos` and updated with
  `SetTodosMsg`; `normalizeTodos` filters empty content and validates status
  before rendering; outstanding items render as a fixed card above the input;
  the todo panel respects resize budget (`todoPanelHeight/todoPanelLines`) and
  is hidden when an approval panel is active
- `Options.DisableMouse` disables terminal mouse reporting for SSH/mobile
  clients that can flood the event loop with touch-motion sequences
- `Options.ArrowKeysScroll` lets Up/Down scroll the transcript when the input is
  empty and there is no input history to navigate, matching mobile SSH clients
  that translate swipe gestures into arrows without breaking prompt history
- selection auto-scroll has a missing-release guard so mobile SSH clients that
  drop mouse release events cannot leave a permanent 30ms redraw loop running
- slash commands can be supplied through `Options.SlashCommands`; typing `/`
  opens a bottom card with filtered command matches, `Tab` completes, and
  `Enter` dispatches through `Hooks.SlashCommand`; the leading slash command
  token in the input is highlighted separately from its arguments, and the
  slash picker does not trigger the away-from-bottom jump hint unless the user
  was already scrolled away from the bottom
- tool-call messages with the same `ToolID` are merged into a single block so
  call/start/result updates do not scatter through the transcript
- Read-style tool calls render with a compact `Read N lines` result summary
  instead of dumping file content inline
- expanded tool-call blocks use a green leading tool marker without a container
  background and render as `ToolName(input args)` followed by a two-space
  indented `└ output` line; empty output renders as `no content data`, and
  long input args wrap onto `  │ ` continuation lines before the output branch;
  wrapped output/code/diff lines use four-space indentation instead of
  truncating long content
- collapsed tool-call summaries render as two-space indented summary text only,
  while every rendered line of an expanded tool-call block can be clicked to collapse it
- tool-call messages can set `ToolNoCollapse`, `ToolCode`, and `ToolLanguage`
  to render a permanently expanded syntax-highlighted code/diff block; callers
  can include line numbers and nearby context in `ToolCode`; diff blocks render
  after the `└ output` line, indent their body by four spaces, use
  red/green/gray per-line backgrounds with syntax highlighting applied to the
  code portion of each line, and infer the highlighting language from the tool
  input file extension
- markdown inline code renders without Glamour's default red foreground and
  dark background so status text such as commit hashes stays readable
- assistant messages marked `Preformatted` render through `TextBlock` instead
  of Markdown so command output such as `/help` keeps its line layout
- messages marked `Plain` render without the usual user/assistant marker, for
  status rows such as `✓ Completed (...)`
- assistant thinking messages render through `ThinkingBlock` as one collapsed
  block that can be expanded independently from the final assistant reply
- optional simulated streaming reply for demos and integration experiments
- the fixed bottom area separates agent status above the input from a caller
  supplied `Options.Footer` below the input for context/model/cwd metadata, with
  a blank row separating transcript/panels from the agent status line
- the away-from-bottom jump hint renders in the agent status row, and the input
  area grows up to five rows as long text soft-wraps or hard newlines are typed

Call `NewModel(Options{...})` to create a Bubble Tea v2 model. The directory is
named `modu-tui` for the import path; the Go package name is `modutui`.

Component layout:

- `block.go` defines the common `Block` interface and render result types.
- `InputBlock` owns text editing, caret positioning, collapsed paste tokens, and
  cursor-local replacement used by mobile SSH IME preedit coalescing.
- `Block` is the extension interface: every block is a struct with
  `Render(RenderContext) BlockRender`.
- `text_block.go` and `markdown_block.go` render user/assistant transcript content;
  markdown tables are rendered by `table_block.go` with bordered table blocks.
- `thinking_block.go` renders assistant thinking as a collapsed block.
- `collapsible_block.go` owns generic expand/collapse rendering.
- `tool_call_block.go` embeds `CollapsibleBlock` for command/tool output and can
  render permission state from `Hooks.ToolPermission`.
- `card_block.go` owns reusable heavy-border card rendering for fixed bottom
  panels and future popup-style blocks.
- `todo_block.go` renders active todo items from `Options.Todos` as a fixed
  card above the input; completed-only or empty lists are hidden, and
  `SetTodosMsg` refreshes the card after host state changes; `normalizeTodos`
  filters empty content and validates status before rendering.
- `slash_command_block.go` renders slash command suggestions as an independent
  bottom card.
- `approval_block.go` renders a pending tool approval request as a fixed panel
  above the bottom input area. Hosts send `RequestToolApprovalMsg` with a
  response channel; the model handles `y/a/n/d/esc` and returns a
  `ToolApprovalDecision`. Approval panels show a compact command preview and
  grouped allow/deny shortcuts inside a high-contrast heavy border.
- `code_block.go` owns fenced-code rendering and syntax highlighting via Glamour.
- `block_factory.go` maps `Message` values to block structs.
- `Options.BlockFactories` lets callers register custom `Message -> Block`
  mapping before the default mapping runs.
- `Options.InfoCardLines` lets callers provide a non-message startup card for
  model/session/context information on a fresh screen.
- `Options.Footer` and `SetFooterMsg` render a fixed bottom metadata row below
  the input, separate from the agent status shown above the input.
- `Hooks.Interrupt` lets callers handle `Esc` while the model is busy or
  streaming; approval panels keep their own `Esc` deny behavior.
- `Esc` and `Ctrl+C` are matched by normalized key code and raw control text so
  SSH/mobile clients can interrupt or quit even when their key names differ.
- `Hooks.SubmitMessage` lets host applications receive typed submissions with
  prompt, follow-up, or steer intent. `Hooks.Submit` remains as a simple text
  fallback for callers that do not need submit kinds.
- `Options.InputHistory` seeds input history and `Hooks.InputHistoryChanged`
  lets hosts persist the trimmed history list after each submission.
- `Options.Todos` seeds the internal todo snapshot; the fixed todo card is only
  shown while the model is busy/streaming after a current-run `SetTodosMsg`.
  Completed-only, empty, idle, or previous-run todo lists are hidden;
  `normalizeTodos` validates and normalizes the provided items before use.
- `Hooks.SlashCommand` lets host applications route selected or typed slash
  commands without sending them as normal prompts.
- `Hooks.ToolApprovalDecision` lets host applications observe approval decisions.
- `AppendMessageMsg`, `SetStatusMsg`, `SetFooterMsg`, and `SetBusyMsg` let host
  applications feed external session events into the model without coupling
  this package to a specific agent runtime. `SetStatusMsg.TransientFor` can be
  set for completion/error notices that should disappear automatically.
- `SetPanelMsg` opens a host-owned, scrollable main-view panel for richer TUI
  surfaces such as workflow cockpits. The panel replaces the transcript
  viewport until the user closes it with Esc, q, or Ctrl+C; `ClearPanelMsg`
  can close a matching panel programmatically. Panels may include selectable
  rows; ↑/↓ changes selection and Enter emits `Hooks.PanelAction`.
- `RequestHumanPromptMsg` renders a blocking human-in-the-loop choice card for
  host prompts such as confirm/select/plan approval; numeric keys choose
  options and Enter/Esc use the configured default.
- `Model` owns spacing between transcript blocks; individual blocks do not add
  their own trailing blank lines. The default block gap is one blank line.

This package intentionally does not know about coding-agent sessions or command
execution. Those stay in higher-level packages such as `cmd/modu_code`.
