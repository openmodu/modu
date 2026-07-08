# Tool Call Output and Parallel Execution Plan

## V1 Scope

- Model context receives a bounded preview, not full large tool output.
- Full raw output is stored under the canonical runtime tool-results tree:
  `RuntimePaths().ToolResultsDir/sessions/<session-id>/`.
- Tool output metadata is carried in `ToolResult.Details.output` using the
  existing Details field.
- TUI collapsed state shows the preview; expanded tool output reads the local
  artifact path and does not send the raw artifact back to the model.
- Parallel tool execution remains opt-in through `types.ParallelTool`.
- `DefaultTools.MaxConcurrency` bounds parallel execution, defaulting to 4.
- `DefaultTools.MaxTurnToolResultBytes` can bound aggregate text returned by
  all tool calls in one turn.
- Parallel tool results are written back in original tool-call order.
- Slow parallel batches respect context cancellation while waiting for the
  concurrency semaphore.

## V1 Tool Coverage

- `bash`: stores full combined command output when the preview is truncated.
- `grep`: stores the complete formatted result artifact while preserving the
  requested `offset`/`limit` window in the model-visible preview.
- `find`: stores the complete sorted match list when the preview is truncated.
- `ls`: stores the complete directory listing when the preview is truncated.
- `web_fetch`: stores the complete formatted fetched page output when the
  preview is truncated, and avoids putting full page content in Details.
- `web_search`: intentionally unchanged in v1 because it is an opt-in research
  tool with bounded search-result snippets rather than full fetched bodies.
- `read`: intentionally unchanged. Large files already require `offset` and
  `limit`; the source file itself is the artifact.
- `read_tool_result`: reads a required `offset`/`limit` page from a previous
  truncated tool-result artifact by original `call_id`.

## TUI Coverage

- Collapsed tool output shows the model-visible preview.
- Expanded tool output and `/tool-output <call-id>` read local artifact files
  when `ToolResult.Details.output.artifactPath` is available.
- TUI displays parallel batch size using `Event.BatchSize`.

## Explicitly out of scope (v1)

- `middle` and LLM `summary` preview strategies.
- A separate `ToolScheduler` abstraction.
- `BatchIndex`; current TUI needs only existing `Parallel` plus `BatchSize`.
- Cross-batch scheduling, priority queues, and resource locks.
