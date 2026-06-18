# Lua Workflow Orchestration Plan

目标：在 `modu code` 中实现一个 Lua 版 `workflow` tool，用脚本完成动态多 agent 编排。功能目标对齐 `pi-dynamic-workflows` 的 workflow runtime，但脚本语言从 JavaScript 换成 Lua，执行底座复用 modu 已有的 `ExtensionAPI.ForkSession`、subagent child event、worktree isolation、工具权限和 runtime snapshot。

这不是临时 MVP。实现必须按阶段落地，每个阶段都要有可重复的单元测试和真实 case 验证，确认后再进入下一阶段。

## Reference Behavior

对齐基准来自 `pi-dynamic-workflows` 和 Claude Code 官方 dynamic workflows 文档：

- `workflow` tool 接收一段 inline 脚本、一个 `script_path`，或一个 saved workflow `name`，并接收可选 `args`。
- 脚本声明 `meta`，运行时动态调用 `phase()` 产生活动阶段。
- `agent()` 启动隔离子 agent session。
- `parallel()` 对多个子任务并发执行，失败分支返回空结果并记录 log，非取消错误不拖垮整个 workflow。
- `pipeline()` 让每个 item 顺序经过多个 stage，不同 item 之间可以并发。
- `log()`、phase、agent start/end 通过 tool update 形成可见进度。
- workflow 必须至少执行一次 agent 调用，最终返回 JSON 可序列化结果。
- Runtime 需要限制单次运行资源：并发上限 16，agent 总量上限 1000，避免 runaway script。

modu 的实现不复制 Node `vm`，而是用 Go 内嵌 Lua runtime 承载脚本，同时把真正的 agent 执行交给 host API。

Claude Code 官方 dynamic workflows 的完整基准还包括：

- Claude 可按任务即时编写 workflow，也可运行已保存或内置 workflow。
- workflow 在后台执行，主会话保持响应；运行过程可在 `/workflows` 或 task panel 查看。
- 每次运行会把脚本写入 session 目录，支持查看、diff、编辑后 relaunch。
- 停止后可在同一 session 内 resume，已完成 agent 结果可复用。
- agent 级 token / elapsed 进度可见；成本受并发、总 agent 上限和模型路由约束。
- 可通过 `/config`、settings 或环境变量关闭 dynamic workflows。

这些能力不是 `pi-dynamic-workflows` prototype 已完整实现的范围。`pi-dynamic-workflows` README 也明确其当前只实现 core primitive（script、subagents、parallel/pipeline、phases、abort、structured output），尚未实现 persisted/resumable runs 或 `/workflows` manager。

## Proposed Lua Surface

Lua 脚本可使用以下全局函数和对象：

- `meta(table)`：声明 workflow 元信息，必须在任何 `phase` / `agent` / `parallel` / `pipeline` / `log` 调用前执行。
- `phase(title)`：切换当前运行阶段，阶段可由条件或循环动态产生。
- `log(message)`：记录运行日志并触发进度更新。
- `agent(prompt, opts)`：同步执行一个子 agent，并返回子 agent 最终文本或结构化结果。
- `workflow(nameOrRef, args)`：同步执行一个 saved workflow 名称或 Lua 脚本路径，最多一层嵌套，并与父 workflow 共享 budget、并发、取消信号和 agent 总量上限。
- `parallel(tasks, opts)`：并发执行 task table，按输入顺序返回结果数组。
- `pipeline(items, stages, opts)`：每个 item 顺序经过 stage，不同 item 可按并发限制调度。
- `json.encode(value)` / `json.decode(text)`：JSON 编解码。
- `args`：tool 调用传入的 JSON 值。
- `cwd` / `process.cwd()`：当前工作目录。
- `budget.total` / `budget.spent()` / `budget.remaining()`：workflow 预算视图。`workflow` tool 传入 `budget` 时 `budget.total` 为该整数，`budget.spent()` 优先使用 child usage 事件中的真实 token，用不到时回退到最终文本估算，`budget.remaining()` 返回剩余预算；未传预算时两者为 nil。

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
| `tools` | `AllowedTools` | 空值继承当前主 agent 可见 tool allowlist；非空时从父 session 工具目录中按名筛选，因此 session-connected/custom/MCP 风格工具可被显式请求，并允许显式补齐 `grep` / `find` / `ls` 这类只读发现工具，以及 `web_search` / `web_fetch` 这类网络研究工具。 |
| `disallowed_tools` | `DisallowedTools` | 后置过滤。 |
| `permission_mode` | `PermissionMode` | 先支持现有 `read-only`。 |
| `max_turns` | `MaxTurns` | 复用子 agent turn cap；未设置表示默认行为，显式设置必须为正整数。 |
| `thinking` | `ThinkingLevel` | 透传到 forked session。 |
| `skills` | `Skills` | 复用 skill prompt augmentation。 |
| `memory_scope` | `MemoryScope` | 复用 memory 注入；仅接受 `none`, `user`/`global`, `project`/`local`, `both`/`all`。 |

## Snapshot Contract

tool update 和最终 `ToolResult.Details` 使用同一类 snapshot：

```json
{
  "name": "repo_review",
  "description": "Inspect modu code areas in parallel and synthesize risks",
  "phases": ["Inventory", "Parallel review", "Synthesis"],
  "phaseSummaries": [
    {
      "title": "Inventory",
      "agentCount": 1,
      "doneCount": 1,
      "estimatedTokens": 42,
      "durationMs": 2000
    }
  ],
  "currentPhase": "Parallel review",
  "logs": ["agent risk review failed: ..."],
  "agents": [
    {
      "id": 1,
      "label": "repo inventory",
      "phase": "Inventory",
      "prompt": "Inspect the repository structure...",
      "status": "done",
      "resultPreview": "pkg/coding_agent...",
      "startedAt": "2026-06-18T01:00:00Z",
      "endedAt": "2026-06-18T01:00:02Z",
      "durationMs": 2000,
      "estimatedTokens": 42,
      "turnTokens": 1200,
      "failedToolCalls": 1,
      "recentToolCalls": [
        {
          "toolName": "read",
          "argsPreview": "{\"path\":\"go.mod\"}",
          "resultPreview": "module github.com/openmodu/modu"
        }
      ]
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
- Lua scripts support `meta`, `phase`, `log`, `agent`, `workflow`, `parallel`, `pipeline`, `json.encode`, `json.decode`, `json.null`, `args`, `cwd`, `process.cwd()`, and `budget`; the tool-level `budget` parameter drives `budget.total` / `budget.remaining()`, and exhausted budget stops later `agent()` calls from forking.
- `agent()` maps to `ExtensionAPI.ForkSession` and supports label, phase, model, cwd, worktree isolation, tools, disallowed tools, permission mode, max turns, thinking, skills, memory scope, and a JSON Schema subset through `schema`.
- `schema` currently injects a final JSON output contract into the child task, extracts JSON from the returned text, validates `type` / `required` / `properties` / `items` / `enum`, returns a Lua table on success, and performs one corrective retry before returning `json.null` plus log entries on validation failure.
- `parallel()` runs child tasks with a bounded concurrency limit, preserves input order, and returns stable JSON null for failed branches.
- `pipeline()` runs each item through ordered Lua stage functions, schedules items with a bounded concurrency limit, serializes access to the shared Lua VM, isolates per-item stage failures, and rejects nested `pipeline()` calls to avoid self-deadlock.
- Runtime resource guards enforce Claude-aligned caps: default concurrency 4, max concurrency 16, and max 1000 forked agents per run.
- Inline workflow scripts are persisted under the current session directory at `extensions/workflow/runs/<run-id>/script.lua` when `SessionDir()` is available; completed runs also write `snapshot.json` next to the script, and background runs write `status.json` so stopped/failed/completed status survives process recreation. Final snapshot details expose `scriptPath` and `runDir`, and the tool text includes the `Script:` path so the parent assistant can report it.
- The workflow tool accepts exactly one source: `script`, `script_path`, or `name`. `script_path` supports relaunching a persisted script by path. `name` resolves saved workflows by walking Claude-compatible project `.claude/workflows/<name>.lua` directories from the active cwd up to the git root, then legacy `.coding_agent/workflows/<name>.lua` directories as compatibility fallback, nearest first. Personal workflows resolve from sibling `~/.claude/workflows/<name>.lua` before agent-dir `workflows/<name>.lua`.
- The workflow tool accepts `async: true` to start a background run and return a run id immediately; the in-memory run registry tracks running/stopped/failed/completed status for the current process.
- Saved workflows present at extension init are registered as Claude-style `/<name> [json-args]` slash commands when the name does not conflict with a built-in/extension command, and always as compatibility `/workflow:<name> [json-args]` commands. Project commands take precedence by nearest directory, then parent project directories, then Claude-compatible personal workflows and legacy user/agent-dir workflows with the same name.
- `/deep-research <question>` is registered as a bundled workflow command. It starts a background Lua workflow with scope, parallel research, cross-check, and synthesis phases. It requests the built-in opt-in `web_search` / `web_fetch` tools for child agents; cited web-research quality still depends on network permission, reachable search endpoints, and fetched source quality.
- When the workflow tool is active, the main agent's system prompt includes workflow authoring guidance: explicit user requests such as `workflow`, `dynamic workflow`, or `ultracode`, plus large fan-out/fan-in tasks, should be handled by writing a Lua workflow script and calling the `workflow` tool. `/effort ultracode` enables a session-local workflow-first mode on xhigh-capable models and appends an Ultracode prompt block instructing the agent to consider dynamic workflows for every substantive task. This gives modu the minimal "Claude can write a workflow for the task" path. It does not yet implement Claude's input keyword highlight or `Option/Alt+W` dismissal.
- Workflow starts now pass through a pre-run approval gate when the host provides `Select`: the prompt shows workflow name, description, planned phases inferred from the script, script path, resource limits, and a raw Lua preview. The user can run once, always allow the same workflow script in the same project, view the full raw script, or cancel. Denial cancels the run before any child agent forks. Always-allow entries are stored under the agent dir in `workflow_approvals.json`, keyed by project root, workflow name/source, and script hash. `permissions.defaultMode: "auto"` remembers a run-once approval for the same project/workflow/script, and `permissions.defaultMode: "bypassPermissions"` skips this workflow launch prompt. Claude-style editor integration, ultracode-specific skip rules, and Desktop approval-card rendering remain follow-ups.
- Nested `workflow(nameOrRef, args)` is supported for one level and resolves saved names / script paths with the same rules as the tool-level loader. Nested child agents share the parent budget counter, concurrency default, cancellation context, and max-agent cap.
- `/workflows` provides the first management slice: list live and persisted runs for the current session, display live/completed metadata when present, show a run's `script.lua` by id/prefix or `latest`, inspect one agent's prompt/status/result preview plus recent child tool names/args/results/errors with `/workflows agent <run-id|latest> <agent-id>`, stop or restart one running agent with `/workflows agent-stop <run-id|latest> <agent-id>` / `/workflows agent-restart <run-id|latest> <agent-id>`, pause or stop a running workflow with `/workflows pause <run-id>` / `/workflows stop <run-id>`, resume a stopped in-memory run with `/workflows resume <run-id|latest>` while reusing completed agent results, relaunch a saved run script as a new background run with `/workflows restart <run-id|latest>`, and save a run script for reuse with `/workflows save <run-id|latest> <name> [project|user]`. Project saves use the nearest existing project `.claude/workflows` directory, or git root `.claude/workflows` when none exists; user saves write sibling `~/.claude/workflows`.
- Workflow-specific config can disable the tool and slash commands via `extensions.yaml` `config.disabled: true`, `~/.coding_agent/settings.json` or project `.coding_agent/settings.json` with `disableWorkflows: true`, env-level switches `MODU_CODE_DISABLE_WORKFLOWS=1` / `CLAUDE_CODE_DISABLE_WORKFLOWS=1`, and `/config` -> `Dynamic workflows` for the global settings toggle. Disabling through `/config` removes the workflow tool, `/workflows`, `/deep-research`, and saved workflow commands (`/<name>` plus `/workflow:<name>`) from the current session; re-enabling requires a new session or restart to register them again.
- Tool updates and final result details expose the workflow snapshot shape described above, including per-agent start/end timestamps, `durationMs`, budget-counted `estimatedTokens`, child-event `turnTokens`, observed `cost`, failed tool-call count, recent child tool names/args/results/errors, captured child transcript, and phase-level `phaseSummaries`; token and observed cost usage use `subagent_child_usage` when present and fall back to text estimation only for token budgets. The runner only aggregates provider-supplied `Usage.Cost.Total`; it does not synthesize model pricing.

Intentional differences from `pi-dynamic-workflows`:

- Script language is Lua rather than JavaScript.
- Lua `parallel()` accepts task tables, not JavaScript thunk functions.
- Lua stage functions run through one embedded VM, so Lua bytecode access is serialized; use `parallel()` for actual multi-agent fan-out.
- `schema` / structured-output capture is prompt-and-validate based with one corrective retry; it is not yet a host-level terminating tool.
- The host forks children from the parent session's tool catalog. When `tools` is omitted, the child inherits the current main-agent visible tool allowlist. When `tools` is non-empty, the child receives only the named tools present in the parent session catalog, which covers session-connected/custom/MCP-style tools; default `modu_code` coding sessions also keep `grep`, `find`, `ls`, `web_search`, and `web_fetch` opt-in for the parent model but can explicitly provide those read-only discovery or network research tools to workflow children.

## Claude Code Parity Backlog

Current parity audit sources:

- Claude Code dynamic workflows docs: `https://code.claude.com/docs/en/workflows`.
- Claude Code subagent docs: `https://code.claude.com/docs/en/sub-agents`.
- `pi-dynamic-workflows` README and source at `Michaelliv/pi-dynamic-workflows`.
- `pi-dynamic-workflows` issue #11, which audits faithfulness gaps against Claude Code's integrated Workflow tool.

Backlog, in implementation order:

| Priority | Gap | Current modu status | Acceptance |
| --- | --- | --- | --- |
| P0 | Resource guardrails | Done for static caps and usage-aware budgets: concurrency clamps to 16, total forked agents defaults to 1000, exhausted `budget` blocks later forks, and `budget.spent()` uses captured child usage when `subagent_child_usage` is available with final-text estimation as fallback. | Unit tests prove budget and max-agent gates prevent extra `ForkSession` calls, including a case where child final text is short but real usage exhausts the budget. |
| P0 | Worktree isolation | Done through host `ForkSession` worktree path, unlike pi prototype where `isolation:"worktree"` is prose-only. | Real workflow case proves child cwd is a linked git worktree and unchanged worktrees are cleaned up. |
| P1 | Structured subagent output via `opts.schema` | Partial. Lua `schema` is accepted, injected into the child prompt, parsed from the child final text, locally validated against a JSON Schema subset, and retried once with corrective context before returning `json.null`. It does not yet use a child-only terminating tool. | A schema-bearing `agent()` returns validated JSON-compatible data; missing/invalid structured output gets a bounded corrective retry before returning `json.null`. |
| P1 | Script persistence and saved workflow loader | Partial. Inline scripts persist under the active session directory and expose visible `scriptPath`/`runDir`; completed runs also persist `snapshot.json`; `script_path` relaunch, saved workflow `name` lookup, nearest-project saved workflow discovery including `.claude/workflows`, startup-time Claude-style `/<name>` commands with compatibility `/workflow:<name>` fallback, `/workflows restart <run-id|latest>`, `/workflows save <run-id|latest> <name> [project|user]`, and the TUI save dialog are supported. Saves now prefer Claude-compatible `.claude/workflows` paths while `.coding_agent/workflows` remains a read/discovery fallback. A TUI relaunch dialog is not done. | Inline scripts are persisted under a workflow run dir; saved user/project workflows can be invoked by name with `args`; relaunch can use the persisted script path. |
| P1 | Workflow authoring trigger and approval | Partial. When the workflow tool is active, the system prompt teaches the main agent to write Lua workflows for explicit `workflow` / `dynamic workflow` / `ultracode` requests and large fan-out/fan-in tasks. `/effort ultracode` is available for xhigh-capable models and activates a session-local workflow-first prompt block; `/effort high|medium|low|off` exits that mode. Workflow tool runs, saved workflow commands, `/deep-research`, and `/workflows restart` ask for confirmation with phase and script preview before forking; run-once, always-allow-this-project, view-raw-script, and cancel choices are supported. `permissions.defaultMode: "auto"` prompts once then remembers the same project/workflow/script, and `permissions.defaultMode: "bypassPermissions"` skips workflow launch approval. There is no input keyword highlight, dismissal shortcut, open-in-editor action, ultracode-specific direct skip, or Desktop approval-card renderer yet. | A user can ask the agent to use a workflow and the model has concrete Lua workflow instructions plus the active tool schema needed to write and run one; ultracode mode tells the model to consider workflows for every substantive task; user denial prevents a workflow from forking child agents; an always-allow choice skips future prompts for the same project/workflow/script; view-raw shows the complete Lua before deciding; configured auto/bypass permission modes affect workflow launch approval. |
| P1 | Workflow run registry and `/workflows` manager | Partial. `async: true` and saved workflow commands start background runs; an in-memory registry tracks live status; background run status is persisted to `status.json`; `/workflows` can list/show live and persisted runs, drill into one agent's prompt/status/result preview plus recent child tool names/args/results/errors, browse the captured child transcript with `/workflows transcript`, stop/restart one running agent, pause/stop running workflows, resume stopped in-memory runs, restart a run script as a fresh run, and save run scripts for reuse. Workflow runtime state exposes live run counts, phase summaries, and per-run agent summaries including a capped prompt payload, result/error, and recent tool-call previews; the TUI statusbar shows a running-workflow indicator; exact `/workflows` in TUI opens a selectable run list with live counts, `j/k` or arrows, `Enter`/right to open the selected run's phase progress view, then the phase's agent list, then an agent detail view with prompt/result/error/tool-call previews. `Esc`/left steps back through agent detail -> agents -> phases -> runs, and `Esc`/`q` closes at the run list. TUI `p` routes to run pause/resume, `x` stops the selected agent when focused on an agent or stops the run otherwise, `r` restarts the selected agent, and `s` opens a save-name dialog with `Tab` scope toggle before calling `/workflows save`. Inline TUI transcript browsing remains a polish follow-up; process-exit behavior intentionally matches Claude's documented fresh-start-after-exit model. | Runtime exposes running/paused/completed workflow tasks, stop/pause/resume actions, selected-agent stop/restart, and a TUI/slash view for list/show/kill/pause. |
| P1 | Resume / memoized replay | Partial. `/workflows pause <run-id>` and `/workflows stop <run-id>` cancel a running background workflow into stopped state; `/workflows resume <run-id|latest>` works within the same process/session for stopped background runs: completed agent results are cached in memory and reused, and incomplete branches run live. TUI `p` routes to pause/resume. Resume after process recreation is intentionally not supported yet, matching Claude's fresh-start behavior after exit; parallel duplicate-key edge cases remain follow-ups. | Stopped runs resume within the same session; completed agent results are reused and only incomplete branches run live. |
| P2 | Per-agent real usage and cost view | Done for observed usage supplied by the host. Snapshot records each workflow agent's `startedAt`, `endedAt`, `durationMs`, budget-counted `estimatedTokens`, child-event `turnTokens`, observed `cost`, failed tool-call count, recent child tool names/args/results/errors, and captured child transcript; `phaseSummaries` aggregate observed tokens, cost, and elapsed time. `/workflows show`, `/workflows agent`, runtime state, and the TUI workflow panel render observed cost when `Usage.Cost.Total` is present. Budget checks use real child usage where available; synthetic model-pricing calculation remains intentionally out of scope. | Snapshot records each workflow agent's real token usage, observed provider cost, and elapsed time, and budget checks use real child usage where available. |
| P2 | Nested `workflow(nameOrRef, args)` | Done for one-level saved/path composition. A parent workflow can invoke one saved child workflow, pass JSON-compatible args, and share budget, concurrency default, abort signal, and agent cap. Multi-level nesting remains out of scope. | A parent workflow can invoke one saved child workflow at one nesting level, sharing budget, concurrency, abort signal, and agent cap. |
| P2 | MCP/session-connected tool forwarding | Done for the current host tool model. Forked children inherit the current main-agent visible tool allowlist when `tools` is omitted, and explicit `tools` requests are filtered from the parent session tool catalog, so session-connected/custom/MCP-style tools registered in the parent can be forwarded by name; focused tests cover custom-tool forwarding and narrowed allowlist inheritance. A future external MCP manager may still need a richer catalog/provenance API. | A workflow child can use an MCP tool connected in the parent session when explicitly allowed. |
| P3 | Disable/config toggle and bundled workflows | Partial. `extensions.yaml` `config.disabled: true`, `settings.json` `disableWorkflows: true`, env vars, and `/config` -> `Dynamic workflows` disable the workflow tool plus workflow slash commands including `/workflows`, `/deep-research`, and saved workflow direct/compatibility commands. `/deep-research <question>` exists as a bundled background workflow and can request built-in `web_search` / `web_fetch`; full Claude-style cited research still depends on runtime network permission and a reachable search endpoint. | Config/env can disable the workflow tool and any bundled workflow commands; `/config` exposes the toggle; `/deep-research` starts a workflow and produces a report when child tools can gather evidence. |

Real configured-model cases run on 2026-06-17:

- `repo_inventory_smoke`: one workflow child inspected the repository and confirmed `pkg/coding_agent/plugins/extension/workflow` exists.
- `parallel_smoke`: two child branches ran through `parallel()` and returned `ok=true`.
- `worktree_smoke`: one child ran with `isolation = "worktree"`, confirmed `go.mod` was visible and the workspace was a linked git worktree, and reported no modifications.
- `partial_failure_smoke`: one good branch returned `GOOD_BRANCH_OK`, one invalid-model branch failed to null, and `out[2] == json.null` returned `true`.
