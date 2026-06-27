# pkg/modu-tui

`modutui` packages the Bubble Tea v2 fullscreen transcript viewport used by
`cmd/tuipoc2`.

It owns only the reusable UI shell:

- rendered transcript viewport with fixed bottom input
- mouse wheel and PageUp/PageDown scrolling
- drag selection with local clipboard plus OSC52 copy
- independent input, text, markdown, collapsible, tool-call, and code blocks
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
- `code_block.go` owns fenced-code rendering and syntax highlighting via Glamour.
- `block_factory.go` maps `Message` values to block structs.
- `Options.BlockFactories` lets callers register custom `Message -> Block`
  mapping before the default mapping runs.
- `Model` owns spacing between transcript blocks; individual blocks do not add
  their own trailing blank lines. The default block gap is one blank line.

This package intentionally does not know about coding-agent sessions, tools, or
approval flows. Those stay in higher-level packages such as `pkg/tui`.
