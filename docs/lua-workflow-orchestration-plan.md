# Lua Workflow Orchestration Plan

目标：在 `modu code` 中实现一个 Lua 版 `workflow` tool，用脚本完成动态多 agent 编排。功能目标对齐 `pi-dynamic-workflows` 的 workflow runtime，但脚本语言从 JavaScript 换成 Lua，执行底座复用 modu 已有的 `ExtensionAPI.ForkSession`、subagent child event、worktree isolation、工具权限和 runtime snapshot。

这不是临时 MVP。实现必须按阶段落地，每个阶段都要有可重复的单元测试和真实 case 验证，确认后再进入下一阶段。

## Reference Behavior

对齐基准来自 `pi-dynamic-workflows`：

- `workflow` tool 接收一段脚本和可选 `args`。
- 脚本声明 `meta`，运行时动态调用 `phase()` 产生活动阶段。
- `agent()` 启动隔离子 agent session。
- `parallel()` 对多个子任务并发执行，失败分支返回空结果并记录 log，非取消错误不拖垮整个 workflow。
- `pipeline()` 让每个 item 顺序经过多个 stage，不同 item 之间可以并发。
- `log()`、phase、agent start/end 通过 tool update 形成可见进度。
- workflow 必须至少执行一次 agent 调用，最终返回 JSON 可序列化结果。

modu 的实现不复制 Node `vm`，而是用 Go 内嵌 Lua runtime 承载脚本，同时把真正的 agent 执行交给 host API。

## Proposed Lua Surface

Lua 脚本可使用以下全局函数和对象：

- `meta(table)`：声明 workflow 元信息，必须在任何 `phase` / `agent` / `parallel` / `pipeline` / `log` 调用前执行。
- `phase(title)`：切换当前运行阶段，阶段可由条件或循环动态产生。
- `log(message)`：记录运行日志并触发进度更新。
- `agent(prompt, opts)`：同步执行一个子 agent，并返回子 agent 最终文本或结构化结果。
- `parallel(tasks, opts)`：并发执行 task table，按输入顺序返回结果数组。
- `pipeline(items, stages, opts)`：每个 item 顺序经过 stage，不同 item 可按并发限制调度。
- `json.encode(value)` / `json.decode(text)`：JSON 编解码。
- `args`：tool 调用传入的 JSON 值。
- `cwd` / `process.cwd()`：当前工作目录。
- `budget.total` / `budget.spent()` / `budget.remaining()`：workflow 预算视图。

示例：

```lua
meta({
  name = "repo_review",
  description = "Inspect modu code areas in parallel and synthesize risks",
})

phase("Inventory")
local inventory = agent("Inspect the repository structure. Identify key packages and entrypoints.", {
  label = "repo inventory",
  tools = {"read", "grep", "find", "ls"},
  permission_mode = "read-only",
})

phase("Parallel review")
local reviews = parallel({
  {
    label = "coding agent review",
    prompt = "Review pkg/coding_agent based on this inventory:\n" .. inventory,
    cwd = "pkg/coding_agent",
    permission_mode = "read-only",
  },
  {
    label = "modu code review",
    prompt = "Review cmd/modu_code based on this inventory:\n" .. inventory,
    cwd = "cmd/modu_code",
    permission_mode = "read-only",
  },
  {
    label = "tui review",
    prompt = "Review pkg/tui based on this inventory:\n" .. inventory,
    cwd = "pkg/tui",
    permission_mode = "read-only",
  },
}, { concurrency = 2 })

phase("Synthesis")
return agent("Synthesize the review results into risks and next actions:\n" .. json.encode(reviews), {
  label = "final synthesis",
  permission_mode = "read-only",
})
```

## Agent Option Mapping

`agent(prompt, opts)` 和 `parallel` task item 映射到 `extension.ForkOptions`：

| Lua option | ForkOptions field | Notes |
| --- | --- | --- |
| `label` | `Name` | 为空时生成 `workflow-agent-N`。 |
| `prompt` / first arg | `Task` | 会追加 workflow phase / label 上下文。 |
| `model` | `Model` | 复用现有 fork model override。 |
| `cwd` | `Cwd` | 相对路径按父 session cwd 解析。 |
| `isolation = "worktree"` | `Isolation` | 只接受空值或 `worktree`。 |
| `tools` | `AllowedTools` | 空值继承当前 active tools。 |
| `disallowed_tools` | `DisallowedTools` | 后置过滤。 |
| `permission_mode` | `PermissionMode` | 先支持现有 `read-only`。 |
| `max_turns` | `MaxTurns` | 复用子 agent turn cap。 |
| `thinking` | `ThinkingLevel` | 透传到 forked session。 |
| `skills` | `Skills` | 复用 skill prompt augmentation。 |
| `memory_scope` | `MemoryScope` | 复用 memory 注入。 |

## Snapshot Contract

tool update 和最终 `ToolResult.Details` 使用同一类 snapshot：

```json
{
  "name": "repo_review",
  "description": "Inspect modu code areas in parallel and synthesize risks",
  "phases": ["Inventory", "Parallel review", "Synthesis"],
  "currentPhase": "Parallel review",
  "logs": ["agent risk review failed: ..."],
  "agents": [
    {
      "id": 1,
      "label": "repo inventory",
      "phase": "Inventory",
      "prompt": "Inspect the repository structure...",
      "status": "done",
      "resultPreview": "pkg/coding_agent..."
    }
  ],
  "agentCount": 4,
  "runningCount": 0,
  "doneCount": 4,
  "errorCount": 0,
  "durationMs": 12345,
  "result": {}
}
```

Agent status values: `queued`, `running`, `done`, `error`, `skipped`.

Final text result:

```text
Workflow repo_review completed with 4 agent(s).

Result:
...
```

## Sandbox Requirements

Use an embedded Lua runtime, preferably `gopher-lua`, with a strict allowlist:

- Do not open `os`, `io`, `package`, or `debug`.
- Do not expose `require`, `dofile`, `loadfile`, or unrestricted `load`.
- Keep JSON helpers provided by Go.
- Do not allow mutation of registered host globals.
- Disable or make deterministic `math.random`; do not expose time APIs unless deterministic.
- Use context cancellation / timeout hooks so a cancelled tool call stops Lua execution and cancels live child agent calls.
- Convert Lua return values through a JSON-compatible converter and reject unsupported values, functions, cycles, userdata, and channels.

## Implementation Phases

### M1: Lua Runtime Skeleton

Scope:

- Add `pkg/coding_agent/plugins/extension/workflow`.
- Register a builtin `workflow` extension and tool.
- Add safe Lua state setup.
- Implement `meta`, `phase`, `log`, `json.encode`, `json.decode`, `args`, `cwd`, and `budget`.
- Implement snapshot updates through `types.ToolUpdateCallback`.
- Reject scripts that omit `meta` or return non-JSON-compatible values.

Validation:

- Unit tests with no real model:
  - `meta` is required.
  - `phase` can be conditional or loop-generated.
  - `log` appears in snapshot and details.
  - unsafe libraries are unavailable.
  - unsupported return values fail with a clear error.
- No `ForkSession` dependency in this milestone.

### M2: `agent()` via `ExtensionAPI.ForkSession`

Scope:

- Implement `agent(prompt, opts)`.
- Map Lua options to `extension.ForkOptions`.
- Track agent start/end/error in snapshot.
- Add default labels and phase inheritance.
- Treat non-cancel errors as logged branch failures returning `nil`.

Validation:

- Fake `ExtensionAPI.ForkSession` tests:
  - prompt and option mapping are correct.
  - current phase is assigned when `opts.phase` is omitted.
  - explicit `model`, `cwd`, `worktree`, tools, permission, max turns, thinking, skills, and memory scope flow into `ForkOptions`.
  - failed child returns nil and records an error log.
  - context cancellation aborts the workflow.

### M3: `parallel()`

Scope:

- Implement `parallel(tasks, opts)`.
- Accept task tables shaped like `{ prompt = "...", label = "...", ... }`.
- Support `opts.concurrency`, with a global workflow default.
- Return results in input order.
- Mark running agents as skipped when cancelled.

Validation:

- Fake API tests:
  - concurrency limit is honored.
  - results remain input ordered even when child completion order differs.
  - one failed branch returns nil without cancelling siblings.
  - cancellation stops pending and running children.

### M4: `pipeline()`

Scope:

- Implement `pipeline(items, stages, opts)`.
- Each stage receives `(previous, original, index)`.
- Stage may be a Lua function that calls `agent()` or returns transformed data.
- Different items may be scheduled with `opts.concurrency`; access to a single Lua state must remain serialized.

Validation:

- Fake API tests:
  - stages run in order per item.
  - item results are input ordered.
  - stage failures affect only that item unless cancelled.
  - stage functions can compose `agent()` results.

### M5: Real `modu code` Cases

Scope:

- Register the workflow extension in `cmd/modu_code`.
- Run real workflows through `go run ./cmd/modu_code -p ...`.
- Verify normal text mode and JSON event mode where applicable.

Required real cases:

1. Repository inventory:
   - One `agent()` reads current repo structure.
   - Expected: result mentions key packages and entrypoints.
2. Parallel area review:
   - Inventory first, then parallel review of `pkg/coding_agent`, `cmd/modu_code`, and `pkg/tui`.
   - Expected: all branch outputs return, final synthesis handles the combined results.
3. Worktree isolation:
   - One branch uses `isolation = "worktree"` to inspect or propose changes.
   - Expected: original checkout remains clean except intended docs/test artifacts.
4. Partial failure:
   - One branch intentionally references a bad cwd or invalid tool option.
   - Expected: workflow logs branch failure, final synthesis handles nil result.

### M6: Documentation, Progress, and Acceptance Record

Scope:

- Update `pkg/coding_agent/README.md` and `README_zh.md` with workflow usage.
- Update `pkg/coding_agent/PROGRESS.md` and `cmd/modu_code/PROGRESS.md` with completed milestone notes.
- Record exact validation commands and observed results.
- Add a short compatibility note explaining the Lua surface relative to `pi-dynamic-workflows`.

## Acceptance Rule

Do not merge a later milestone before the current milestone has:

- focused unit tests,
- at least one integration-style test when host behavior is involved,
- a real case when the milestone reaches `cmd/modu_code`,
- documentation updates for any public surface added,
- progress log entries with the exact commands used for validation.

## Compatibility Status

Current implementation status on branch `feat/lua-workflow`:

- `workflow` is a builtin `modu_code` extension and tool.
- Lua scripts support `meta`, `phase`, `log`, `agent`, `parallel`, `pipeline`, `json.encode`, `json.decode`, `json.null`, `args`, `cwd`, `process.cwd()`, and `budget`.
- `agent()` maps to `ExtensionAPI.ForkSession` and supports label, phase, model, cwd, worktree isolation, tools, disallowed tools, permission mode, max turns, thinking, skills, and memory scope.
- `parallel()` runs child tasks with a bounded concurrency limit, preserves input order, and returns stable JSON null for failed branches.
- `pipeline()` runs each item through ordered Lua stage functions, schedules items with a bounded concurrency limit, serializes access to the shared Lua VM, isolates per-item stage failures, and rejects nested `pipeline()` calls to avoid self-deadlock.
- Tool updates and final result details expose the workflow snapshot shape described above.

Intentional differences from `pi-dynamic-workflows`:

- Script language is Lua rather than JavaScript.
- Lua `parallel()` accepts task tables, not JavaScript thunk functions.
- Lua stage functions run through one embedded VM, so Lua bytecode access is serialized; use `parallel()` for actual multi-agent fan-out.
- `schema` / structured-output capture is not implemented yet. Child results are text or JSON-compatible Lua values.
- The host currently forks children from the parent active tool set. In default `modu_code` sessions, requested tools such as `grep`, `find`, and `ls` are skipped unless they are active in the parent tool set.

Real configured-model cases run on 2026-06-17:

- `repo_inventory_smoke`: one workflow child inspected the repository and confirmed `pkg/coding_agent/plugins/extension/workflow` exists.
- `parallel_smoke`: two child branches ran through `parallel()` and returned `ok=true`.
- `worktree_smoke`: one child ran with `isolation = "worktree"`, confirmed `go.mod` was visible and the workspace was a linked git worktree, and reported no modifications.
- `partial_failure_smoke`: one good branch returned `GOOD_BRANCH_OK`, one invalid-model branch failed to null, and `out[2] == json.null` returned `true`.
