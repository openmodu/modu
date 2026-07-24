# Agent transcript TUI

`pkg/modu-tui` provides the Bubble Tea v2 full-screen transcript and input shell used by `cmd/tuipoc2`. It renders messages and host-owned state, but it does not create coding-agent sessions, execute commands, or choose Agent policy; those responsibilities stay in higher-level packages such as `cmd/modu_code`.

The import path contains `modu-tui`; the Go package name is `modutui`.

The target encapsulation boundary and staged migration plan are documented in
[`docs/architecture/modu-tui.md`](../../docs/architecture/modu-tui.md). New
features must keep CodingSession and product-specific command/workflow types out
of this package.

## Create a model

```go
model := modutui.NewModel(modutui.Options{
	Width:  120,
	Height: 35,
	IntentHandler: func(intent modutui.Intent) {
		switch intent := intent.(type) {
		case modutui.SubmitIntent:
			// Route prompt, follow-up, or steering input to the host runtime.
		case modutui.InterruptIntent:
			// Cancel the host's active run.
		}
	},
})
```

`NewModel()` also works without options and uses a 120×35 initial size. Bubble Tea window-size messages replace those dimensions at runtime.

`IntentHandler` runs from a Bubble Tea command after `Model.Update` returns.
Host callbacks therefore do not block the event-loop goroutine. Hosts use
entries and updates for inbound data, intents for outbound actions, and
`Options.Services` for clipboard, pasted-image, slash-command, permission, and
artifact-loading queries.

## What the model owns

### Transcript and scrolling

- A scrollable transcript above a fixed input area; mouse wheel and PageUp/PageDown move the viewport.
- A single jump-to-bottom hint appears in the Agent status row when new content arrives off-screen.
- Every rendered row is padded to the current width, preventing shorter frames from leaving stale terminal content.
- Drag selection copies through the local clipboard and OSC52. SSH, tmux, and screen sessions use passthrough OSC52.
- `DisableMouse` turns off terminal mouse reporting. `ArrowKeysScroll` lets Up/Down scroll when input and history navigation are both empty.

### Input and host intents

- Input grows from one to five rows and keeps up to 100 history entries. Up/Down traverses history without discarding the current draft.
- `SubmitIntent` contains text plus referenced `ImageAttachment` values with `SubmitKindPrompt`, `SubmitKindFollowUp`, or `SubmitKindSteer`.
- Ctrl+V calls `Services.ReadClipboardImages` asynchronously. `Services.ResolvePastedImages` lets the host turn pasted or dragged file paths into attachments. The input renders `[Image #N]` tokens; Backspace/Delete removes the referenced attachment before submission.
- `InterruptIntent` reports Esc while the model is busy or streaming. Ctrl+C remains the quit path.
- `Options.SlashCommands` or `Services.SlashCommands` supplies command suggestions. Tab completes; Enter emits `SlashCommandIntent`.
- `Options.InputHistory` seeds history; `InputHistoryChangedIntent` lets the host persist the normalized list.

### Transcript entries

- Ordinary product output can use `Entry` with standard `TextNode`,
  `MarkdownNode`, `CodeNode`, `ThinkingNode`, `ToolNode`, `TableNode`,
  `ListNode`, `KeyValueNode`, and `ProgressNode` values. One entry can compose
  multiple nodes.
- A stable `Entry.ID` supports upsert and removal without rebuilding or parsing
  rendered text.
- Text, Markdown, tables, fenced code, thinking, Tool Calls, and host-defined blocks render independently.
- Tool entries with the same `ToolCall.ID` merge into one block. Expanded blocks wrap input, output, code, and diffs instead of truncating them.
- `ToolNode` and `ThinkingNode` carry their lifecycle and expansion state
  directly; the transcript stores only `Entry` values.
- Tool permission and artifact services run through `tea.Cmd`; Render never invokes a host callback or reads an artifact file.
- Read-style results show `Read N lines` rather than dumping file contents.
- `ToolNoCollapse`, `ToolCode`, and `ToolLanguage` keep code or diffs expanded with syntax highlighting.
- Numbered write previews keep the line-number gutter outside the syntax lexer and use a four-column outer indent; selection metadata excludes the indent, line number, and separator from copied source. Idempotent existing-file writes infer the source language from the file path instead of treating full-file content as a diff.
- New-file numbered previews paint every source row with the added green background while preserving syntax foreground colors. Existing unchanged files remain untinted, and existing-file diffs keep green/red/gray per-row backgrounds.
- Numbered Update diff rows also mark their layout indent, change marker, and line number as non-copyable gutter, so dragging across changed rows copies source without `+`/`-` or file line numbers.
- `TextNode` preserves line layout. `Entry.Plain` omits user and Assistant markers.
- `Options.BlockFactories` can map an `Entry` to a custom `Block` before the built-in factory runs.

### Fixed panels and cards

- `CardBlock` provides one border style for approval, slash-command, todo, and human-input cards.
- `Client.AskToolApproval` opens a blocking approval card and returns allow/deny decisions. `ToolApprovalDecisionIntent` reports the result to the host.
- `Client.AskChoice` and `Client.AskText` collect host-requested choices or text without exposing response channels to business flows.
- `SetTodoListUpdate` updates the current run's active todos. Empty, completed-only, idle, and previous-run lists remain hidden; approval cards take precedence when vertical space is limited.
- `ShowPanelUpdate`, `RefreshPanelUpdate`, and `ClosePanelUpdate` manage a host-owned main-view panel. Rows and shortcuts emit `PanelActionIntent`; closing emits `PanelClosedIntent`.

### Host updates

The host feeds runtime state into the model with the closed `Update` protocol:

| Update | Effect |
|---|---|
| `AppendEntryUpdate` | Append one standard transcript entry |
| `UpsertEntryUpdate` / `RemoveEntryUpdate` | Replace or remove one stable entry |
| `ReplaceEntriesUpdate` / `ClearEntriesUpdate` | Replace or clear the transcript |
| `SetStatusUpdate` | Update Agent status; `TTL` clears a temporary notice |
| `SetFooterUpdate` | Update model, context, or working-directory metadata below input |
| `SetBusyUpdate` | Mark the host runtime busy or idle |
| `SetTodoListUpdate` | Replace current todo state |
| `ShowPanelUpdate` / `RefreshPanelUpdate` / `ClosePanelUpdate` | Open, update, or close a host panel |

Wrap the running program in a `Client`. All semantic Client methods call
`Client.Apply`, defensively copy the standard data, and send one `UpdateMsg`
transport envelope to Bubble Tea:

```go
client := modutui.NewClient(func(msg any) { program.Send(msg) })
entry := modutui.Entry{
	ID:   "job-42",
	Role: modutui.RoleAssistant,
	Nodes: []modutui.Node{
		modutui.ProgressNode{Label: "Index", Current: 2, Total: 5},
	},
}
client.AppendEntry(entry)
client.UpsertEntry(entry)
client.SetStatus("running", 0)
client.Apply(modutui.SetTodoListUpdate{Items: todos})
choice, err := client.AskChoice(ctx, request)
```

Business packages must not construct `UpdateMsg`, call `tea.Program.Send`, or
send the deprecated concrete message types directly. The single adapter that
constructs `Client` owns that Bubble Tea boundary. Dialog request/response
methods remain a separate protocol because they wait for a typed result and
honor context cancellation.

Panel rows and shortcuts can carry `Action{ID, Payload}`. The payload is opaque
to the kernel and returns unchanged in `PanelActionIntent`; use immutable value
payloads. `Command` remains available during migration.

## Component map

| Component | Responsibility |
|---|---|
| `InputBlock` | Editing, cursor placement, pasted-text/image tokens, and IME replacement |
| `TextBlock` / `MarkdownBlock` / `TableBlock` | Transcript text and tables |
| `ThinkingBlock` | Independently collapsible reasoning content |
| `ToolCallBlock` / `ToolGroupBlock` | Tool state, output, code, diffs, and batching |
| `CodeBlock` | Fenced code and syntax highlighting |
| `CardBlock` | Shared fixed-card rendering |
| `TodoBlock` / `SlashCommandBlock` / `ApprovalBlock` | Fixed interaction cards |
| `Block` / `BlockRender` | Extension contract for custom message rendering |
| `Entry` / `Node` / `Update` | Data-only host presentation and incremental state protocol |

`Model` owns spacing between transcript blocks. Individual blocks should not add trailing blank lines; the default gap is one blank row.
