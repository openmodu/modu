# Agent transcript TUI

`pkg/modu-tui` provides the Bubble Tea v2 full-screen transcript and input shell used by `cmd/tuipoc2`. It renders messages and host-owned state, but it does not create coding-agent sessions, execute commands, or choose Agent policy; those responsibilities stay in higher-level packages such as `cmd/modu_code`.

The import path contains `modu-tui`; the Go package name is `modutui`.

## Create a model

```go
model := modutui.NewModel(modutui.Options{
	Width:  120,
	Height: 35,
	Hooks: modutui.Hooks{
		SubmitMessage: func(event modutui.SubmitEvent) {
			// Route prompt, follow-up, or steering input to the host runtime.
		},
		Interrupt: func() {
			// Cancel the host's active run.
		},
	},
})
```

`NewModel()` also works without options and uses a 120×35 initial size. Bubble Tea window-size messages replace those dimensions at runtime.

## What the model owns

### Transcript and scrolling

- A scrollable transcript above a fixed input area; mouse wheel and PageUp/PageDown move the viewport.
- A single jump-to-bottom hint appears in the Agent status row when new content arrives off-screen.
- Every rendered row is padded to the current width, preventing shorter frames from leaving stale terminal content.
- Drag selection copies through the local clipboard and OSC52. SSH, tmux, and screen sessions use passthrough OSC52.
- `DisableMouse` turns off terminal mouse reporting. `ArrowKeysScroll` lets Up/Down scroll when input and history navigation are both empty.

### Input and host hooks

- Input grows from one to five rows and keeps up to 100 history entries. Up/Down traverses history without discarding the current draft.
- `Hooks.SubmitMessage` receives `SubmitKindPrompt`, `SubmitKindFollowUp`, or `SubmitKindSteer`. `Hooks.Submit` is the text-only fallback.
- `Hooks.Interrupt` receives Esc while the model is busy or streaming. Ctrl+C remains the quit path.
- `Options.SlashCommands` or `SlashCommandsProvider` supplies command suggestions. Tab completes; Enter dispatches through `Hooks.SlashCommand`.
- `Options.InputHistory` seeds history; `Hooks.InputHistoryChanged` lets the host persist the normalized list.

### Message blocks

- Text, Markdown, tables, fenced code, thinking, Tool Calls, and host-defined blocks render independently.
- Tool messages with the same `ToolID` merge into one block. Expanded blocks wrap input, output, code, and diffs instead of truncating them.
- Read-style results show `Read N lines` rather than dumping file contents.
- `ToolNoCollapse`, `ToolCode`, and `ToolLanguage` keep code or diffs expanded with syntax highlighting.
- `Preformatted` messages preserve line layout. `Plain` messages omit user and Assistant markers.
- `Options.BlockFactories` can map a `Message` to a custom `Block` before the built-in factory runs.

### Fixed panels and cards

- `CardBlock` provides one border style for approval, slash-command, todo, and human-input cards.
- `RequestToolApprovalMsg` opens a blocking approval card and returns allow/deny decisions through its response channel. `Hooks.ToolApprovalDecision` observes the result.
- `RequestHumanPromptMsg` and `RequestHumanTextMsg` collect host-requested choices or text.
- `SetTodosMsg` updates the current run's active todos. Empty, completed-only, idle, and previous-run lists remain hidden; approval cards take precedence when vertical space is limited.
- `SetPanelMsg`, `RefreshPanelMsg`, and `ClearPanelMsg` manage a host-owned main-view panel. Rows, shortcuts, `Hooks.PanelAction`, and `Hooks.PanelClosed` handle interaction.

### Host updates

The host feeds runtime state into the model with Bubble Tea messages:

| Message | Effect |
|---|---|
| `AppendMessageMsg` | Append one transcript message |
| `SetMessagesMsg` | Replace the transcript, for example after session resume |
| `ClearMessagesMsg` | Clear the transcript |
| `SetStatusMsg` | Update Agent status; `TransientFor` clears a temporary notice |
| `SetFooterMsg` | Update model, context, or working-directory metadata below input |
| `SetBusyMsg` | Mark the host runtime busy or idle |
| `SetTodosMsg` | Replace current todo state |
| `SetPanelMsg` / `RefreshPanelMsg` / `ClearPanelMsg` | Open, update, or close a host panel |

## Component map

| Component | Responsibility |
|---|---|
| `InputBlock` | Editing, cursor placement, paste tokens, and IME replacement |
| `TextBlock` / `MarkdownBlock` / `TableBlock` | Transcript text and tables |
| `ThinkingBlock` | Independently collapsible reasoning content |
| `ToolCallBlock` / `ToolGroupBlock` | Tool state, output, code, diffs, and batching |
| `CodeBlock` | Fenced code and syntax highlighting |
| `CardBlock` | Shared fixed-card rendering |
| `TodoBlock` / `SlashCommandBlock` / `ApprovalBlock` | Fixed interaction cards |
| `Block` / `BlockRender` | Extension contract for custom message rendering |

`Model` owns spacing between transcript blocks. Individual blocks should not add trailing blank lines; the default gap is one blank row.
