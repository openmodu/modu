# coding_agent — Architecture

Status: **design intent**. This document defines the layering the package should
follow. The current tree only partly matches it; the gaps are listed under
[Known violations](#known-violations). No behaviour is described here — this is
about *where code belongs and why*.

## 1. What this package is

`coding_agent` is an **embeddable agent engine**: a host process (CLI, RPC
server, SDK caller) creates a session, feeds it user input, and the engine runs
agent turns — driving the LLM loop, tools, context, and persistence.

It is **not** a framework and **not** an application. It sits on top of the
lower execution kernel in `pkg/agent` (the `Agent` type that runs the raw
LLM/tool loop) and adds the *session* layer around it: conversation state,
context-window management, tools wiring, persistence, plan/worktree modes,
plugins, and a host-facing API.

Design north star: **a reader should be able to point at any file and name its
layer and its single responsibility.** Today they cannot — that is the problem
this document exists to fix.

## 2. The layer model

Six layers. **Dependencies may only point downward.** A higher layer may import
a lower one; never the reverse.

```
L5  host        How an external process embeds and drives the engine.
L4  plugins     Userland: optional capabilities loaded at runtime.
L3  tools       The agent's action surface (read/write/edit/grep/bash/…).
L2  services    Stateful subsystems the kernel orchestrates, each behind a contract.
L1  kernel      The irreducible engine: session state, turn loop, events, service registry.
L0  foundation  No agent semantics: types, config, paths, discovery, pub/sub.
```

Rule of thumb: **if removing a piece would stop the engine from running a turn,
it is kernel (L1). If it adds a capability the kernel orchestrates, it is a
service (L2). If it only exists so an outside process can talk to the engine, it
is host (L5).**

### L0 — foundation
Single responsibility: primitives with no knowledge of agents or sessions.
Pure, widely depended upon, depends on nothing in this package.

- `pkg/types` — message/model types
- `pkg/providers` — model registry
- `pkg/mdloader` — markdown resource discovery skeleton
- `coding_agent/eventbus` — pub/sub
- `coding_agent/resource` — agent-dir layout, path resolution, context-file & package discovery
- `coding_agent/taskoutput` — the background-task contract (breaks an import cycle)
- `foundation/config` — session settings (`config.Config`, `config.Load`, `config.Default`)
- `foundation/apikeys` — per-provider API key storage under the agent dir
- `coding_agent/paths.go` — a one-line `DefaultAgentDir` re-export of
  `foundation/resource` (kept as a public convenience for cmd callers)

### L1 — kernel
Single responsibility: hold the session and run turns. The smallest thing that
can execute the agent loop. Depends only on `pkg/agent` + foundation + service
*contracts* (not service implementations directly, ideally).

- the session core: state, `Prompt`/`Steer`/`FollowUp`, the agent event
  subscription, `emitSessionEvent`, the service registry/wiring
- `messages.go` — in-conversation message types + transient detection
- `events.go` — `SessionEvent` types + emission
- `persistence.go` — message save/restore/migrate
- `prompt_refresh.go` — rebuilds the system prompt each turn (drives the
  systemprompt service)

### L2 — services
Single responsibility: one cohesive, stateful capability the kernel orchestrates
through a narrow API. Each should be replaceable/testable in isolation.

| Service | Today lives in |
|---|---|
| conversation window (tokens, compaction trigger, nested context, pruning) | `contextmgr/` |
| transcript summarization | `compaction/` |
| system-prompt assembly | `systemprompt/` |
| session persistence + tree/branching | `session/` |
| tool approval policy | `approval/` (session keeps only the `approval.go` wiring + Observer impl) |
| persistent memory | `memory/` |
| background async tasks | `services/bgtask` (kernel keeps `task_output.go` aliases + accessor, and `task_output_adapter.go`) |
| plan mode | `plan/` (kernel keeps `plan.go` wiring + tool registration) |
| isolated git worktree | `worktree/` (kernel keeps `worktree.go` wiring + tool registration) |
| todo list | `todo/` (kernel keeps `todo.go` alias + tool adapter) |
| inline `!command` execution | `bash/` (kernel keeps `bash_exec.go` wiring) |
| retry policy | `retry/` |
| runtime-state snapshot + git cache | `runtime_state.go`, `git_runtime.go` |
| host confirm/select prompt registry | `extension_confirm.go` |

### L3 — tools
Single responsibility: the agent's action surface. Each tool is a self-contained
capability with a uniform `types.Tool` contract. This layer is already the
cleanest (`tools/` subtree).

- `tools/{read,write,edit,grep,find,ls,bash,memory,planning,worktree,backend_task,common}`

### L4 — plugins (userland)
Single responsibility: optional capabilities discovered/loaded at runtime, not
required for the engine to run.

- `extension/` (+ `agents/`, `goal/`, `subagent/`) — the extension runtime
- `subagent/`, `subagent_session.go`, `fork_session.go` — spawning/forking sub-sessions
- `prompts/` — slash-command prompt templates
- `pkg/skills` — skills

### L5 — host
Single responsibility: adapt the engine to an external driver. Nothing here is
needed to run a turn; it exists for embedding, IO, and introspection.

- `sdk.go` — `CreateSession` facade
- `modes/`, `modes/rpc/` — RPC driver
- `rpc_domain.go` — host-facing DTOs
- `slash_commands.go` — slash-command registry
- `doctor_info.go`, `context_info.go` — diagnostics/introspection
- `export_html.go` — transcript export
- `model_api.go`, `config_api.go`, `session_api.go`, `resources_api.go` — the
  broad session API surface external callers use

## 3. The dependency rule

```
host ──▶ plugins ──▶ tools ──▶ services ──▶ kernel ──▶ foundation
```

- A service must not know about a specific host or plugin. It exposes a contract;
  the kernel wires it.
- The kernel must not import a host type. When a service needs something only the
  host can provide (e.g. emitting a UI event, building a transient message), it
  asks through an interface the host/kernel implements — see `contextmgr.Host`
  and `context_host.go`. That pattern is correct and should be the norm.
- Plugins and tools depend on services/kernel, never the other way around.

## 4. Known violations (the gap between this doc and the tree)

These are why the package "doesn't read like an engine" today. Listed so the
intent is honest, not aspirational.

1. ~~**Flat namespace.**~~ **Largely resolved.** The directory tree now encodes
   the layers, so each package's layer is self-evident from its path:

   ```
   pkg/coding_agent/
     *.go            L1 kernel + L5 host API (the CodingSession facade)
     foundation/     config, resource, eventbus, taskoutput
     services/       contextmgr, compaction, systemprompt, session, approval,
                     memory, retry, bash, todo, plan, worktree
     plugins/        extension, subagent, prompts
     tools/          L3 (already conventionally named)
     modes/          L5 host drivers (rpc)
   ```

   What remains in the root `package coding_agent` is the kernel itself plus the
   host-API methods that hang off `CodingSession` — that is violation #2 (the
   L1/L5 split), which directory moves cannot fix.
2. ~~**`CodingSession` spans L1 + L5.**~~ **Resolved.** The kernel is now an
   internal `engine` struct (state + turn loop + service wiring + the capability
   surface the services depend on; ~126 methods). `CodingSession` is a thin host
   façade — `struct { *engine }` — carrying only the ~37 host-facing API methods
   (model/session/config management, introspection, export). Same package, so
   the façade reaches engine internals directly (no accessors). A reader can now
   tell engine from façade by the receiver: `func (s *engine)` is L1, `func (s
   *CodingSession)` is L5. The engine keeps a `self *CodingSession` back-pointer
   for the few callbacks (slash-command handlers) whose signature needs the
   façade.
3. ~~**`harness.go` has split personality.**~~ **Resolved.** Split by concern
   into `runtime_paths.go` (the on-disk layout), `tool_gate.go` (the kernel↔tools
   pre-execution gate), and the session-event emitters folded into `events.go`.
4. ~~**Services reach the kernel by back-reference.**~~ **Resolved.** Every L2
   service is now a package reaching the kernel through a narrow Host interface
   (`bash.Host`, `plan.Host`, `worktree.Host`, `contextmgr.Host`); `todo` needs
   none. The kernel exposes a small capability surface (`kernel.go`:
   `WriteRuntimeState`, `RefreshSystemPrompt`, `SwitchCwd`, `Cwd`, `AgentDir`,
   `PlanFile`/`PlansDir`, `*ModeEnabled`, `EmitWorktree*`). Tool registration
   (`replace*Tools`) stays kernel-side, with the service controller supplied as
   the tool's manager.
5. **No service registry — and that is the right call (not a violation).** The
   kernel holds ~7 concrete, typed component fields (`ctxMgr`, `bash`, `todos`,
   `taskManager`, `plan`, `worktree`, `extPrompts`). A registry keyed by contract
   was considered and rejected: the services are *always* present (only their
   *tools* are feature-gated, via `replace*Tools`), so a registry would trade
   type-safe direct access (`s.plan.X`) for untyped map lookups plus nil checks —
   more ceremony for optionality the code does not need. Direct fields stay.

## 5. What is already aligned

The recent refactors moved real, stateful subsystems into self-contained
components/subpackages with narrow APIs: `contextmgr`, `systemprompt`,
`session.render`, `mdloader`, and the in-package `todoStore` / `planController` /
`worktreeController` / `bashRunner` / `extensionPrompts`. These are L2 services in
all but directory location. The next step the directory layout would make
explicit — not new logic.

## 6. Kernel contract (the design for unblocking the back-ref services)

`plan`, `worktree`, `bash` hold `*CodingSession`, so they cannot become packages
(import cycle). The fix is the `contextmgr.Host` pattern, generalized: **each
service subpackage declares a narrow interface of the kernel capabilities it
needs; the kernel (CodingSession) implements all of them.** Two principles keep
the interfaces small:

1. **Expose capabilities, not fields.** The kernel offers `SwitchCwd(path)`, not
   raw `cwd`/`activeTools`/`agent`. The cwd-change choreography (set cwd → rebind
   tools → refresh prompt → emit cwd-changed) is one kernel capability.
2. **Keep kernel-owned jobs in the kernel.** Tool *registration*
   (`replace*Tools`) stays kernel-side; the service only supplies the controller
   that implements the tool's manager interface (e.g. `worktreetool.WorktreeManager`).

Capability inventory (what each back-ref service actually uses):

| service | needs from kernel |
|---|---|
| `bash` | `Cwd()` — that's all |
| `todo` | *nothing* — already uses an `onChange` callback; extractable today, like `memory` |
| `plan` | plan-file paths, get/set todos, allow-tool-always, refresh prompt, write runtime state, feature flag |
| `worktree` | `Cwd`/`AgentDir`, `SwitchCwd`, emit worktree created/removed, write runtime state, feature flag |

Proposed minimal interfaces (segregated role interfaces — no single fat
`Kernel`; CodingSession satisfies each structurally):

```go
// package bash
type Host interface{ Cwd() string }

// package plan
type Host interface {
    PlanFile() string
    PlansDir() string
    Todos() []TodoItem
    SetTodos([]TodoItem)
    AllowToolAlways(tool string)
    RefreshSystemPrompt()
    WriteRuntimeState()
    Enabled() bool // FeaturePlanMode
}

// package worktree
type Host interface {
    Cwd() string
    AgentDir() string
    SwitchCwd(newCwd string) // set cwd + rebind tools + refresh prompt + emit cwd-changed
    EmitWorktreeCreated(path string)
    EmitWorktreeRemoved(path string)
    WriteRuntimeState()
    Enabled() bool // FeatureWorktreeMode
}
```

Open design decisions (to settle before coding):
- **Shared method overlap.** `WriteRuntimeState`/`RefreshSystemPrompt`/feature
  flags recur. Options: accept the small duplication across role interfaces
  (clearer), or factor a tiny `kernel.Core` the role interfaces embed (DRY-er).
  Leaning toward the former — duplication of three signatures beats an embedding
  hierarchy.
- **Type homes.** `TodoItem` and the runtime-paths shape need a stable home once
  `plan`/`todo` are packages. Likely: extract `todo` first (`todo.Item`,
  `todo.Store`); `plan` then depends on `todo` (same-layer, no cycle).
- **`writeLatestPlan`** is plan logic that currently lives kernel-side; it moves
  *into* the `plan` package (needs only paths + write-state from Host).

Sequencing (easiest → hardest): `todo` (no interface) → `bash` (1-method Host) →
`plan` → `worktree` (needs `SwitchCwd` + kernel-side tool registration).

*Progress:* `todo` and `bash` are done. `todo` is now a package (`todo.Item` /
`todo.Store`, aliased as `TodoItem` for zero ripple); `bash` is the first real
contract — `bash.Host{ Cwd() string }`, with the kernel exposing `Cwd()`.

Refinement learned while implementing: because Go interface methods are exported
and a single `CodingSession` must satisfy *all* role interfaces, **capability
method names must be specific** (`PlanModeEnabled()`, not a generic `Enabled()`)
and the kernel must expose **exported capability methods** (e.g.
`WriteRuntimeState()`, `RefreshSystemPrompt()`) wrapping its unexported internals.
These exported wrappers are the kernel's capability surface — `plan`/`worktree`
build on them.

## 7. How to use this document

- When adding a file, decide its layer first; put it with its peers; respect the
  downward dependency rule.
- When a change needs an upward dependency, that is a smell — introduce a
  contract (interface) at the lower layer instead.
- This doc is the blueprint for any future directory reorg (`kernel/`,
  `services/`, `host/`, `plugins/`, `foundation/`) or kernel extraction.
