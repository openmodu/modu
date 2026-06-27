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
- drag selection with local clipboard plus OSC52 copy
- independent input, text, markdown, collapsible, tool-call, and code blocks
- fixed bottom cards are rendered through `CardBlock`, so approval and slash
  command popups share one heavy-border card style
- slash commands can be supplied through `Options.SlashCommands`; typing `/`
  opens a bottom card with filtered command matches, `Tab` completes, and
  `Enter` dispatches through `Hooks.SlashCommand`
- tool-call messages with the same `ToolID` are merged into a single block so
  call/start/result updates do not scatter through the transcript
- Read-style tool calls can render as compact `Read(path · lines x-y)` blocks
  with a `Read N lines` result summary instead of dumping file content inline
- expanded tool-call blocks use a faint full-width background without nested
  ANSI styling so the command and output read as one consistent block
- collapsed tool-call summaries are indented, while every rendered line of an
  expanded tool-call block can be clicked to collapse it
- markdown inline code renders without Glamour's default red foreground and
  dark background so status text such as commit hashes stays readable
- assistant messages marked `Preformatted` render through `TextBlock` instead
  of Markdown so command output such as `/help` keeps its line layout
- optional simulated streaming reply for demos and integration experiments

Call `NewModel(Options{...})` to create a Bubble Tea v2 model. The directory is
named `modu-tui` for the import path; the Go package name is `modutui`.

Component layout:

- `block.go` defines the common `Block` interface and render result types.
- `InputBlock` owns text editing and caret positioning.
- `Block` is the extension interface: every block is a struct with
  `Render(RenderContext) BlockRender`.
- `text_block.go` and `markdown_block.go` render user/assistant transcript content;
  markdown tables are rendered by `table_block.go` with bordered table blocks.
- `collapsible_block.go` owns generic expand/collapse rendering.
- `tool_call_block.go` embeds `CollapsibleBlock` for command/tool output and can
  render permission state from `Hooks.ToolPermission`.
- `card_block.go` owns reusable heavy-border card rendering for fixed bottom
  panels and future popup-style blocks.
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
- `Hooks.Submit` lets host applications receive bottom-input submissions.
- `Hooks.SlashCommand` lets host applications route selected or typed slash
  commands without sending them as normal prompts.
- `Hooks.ToolApprovalDecision` lets host applications observe approval decisions.
- `AppendMessageMsg`, `SetStatusMsg`, and `SetBusyMsg` let host applications
  feed external session events into the model without coupling this package to
  a specific agent runtime.
- `Model` owns spacing between transcript blocks; individual blocks do not add
  their own trailing blank lines. The default block gap is one blank line.

This package intentionally does not know about coding-agent sessions or command
execution. Those stay in higher-level packages such as `cmd/modu_code`.
