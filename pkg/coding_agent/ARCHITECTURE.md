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
- `coding_agent/config.go`, `paths.go` — config load, default agent dir

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
| tool approval policy | `approval.go`, `approval_risk.go` |
| persistent memory | `memory.go` |
| background async tasks | `task_output.go`, `task_output_adapter.go` |
| plan mode | `plan.go` |
| isolated git worktree | `worktree.go` |
| todo list | `todo.go` |
| inline `!command` execution | `bash_exec.go` |
| retry policy | `retry.go` |
| runtime-state snapshot + git cache | `runtime_state.go`, `git_runtime.go` |
| host confirm/select prompt registry | `extension_confirm.go` |

### L3 — tools
Single responsibility: the agent's action surface. Each tool is a self-contained
capability with a uniform `agent.Tool` contract. This layer is already the
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

1. **Flat namespace.** L1–L5 all live in `package coding_agent` (34 files, ~6.3k
   LOC). The compiler enforces *no* boundary, so the layering above is only a
   convention until the directories reflect it.
2. **`CodingSession` spans L1 + L5.** It is simultaneously the kernel (state +
   turn loop) and the host API (the `*_api.go` methods hang off the same struct).
   A reader can't tell engine from façade. Target: a small kernel `Engine` + a
   thin host façade composed over it.
3. **`harness.go` has split personality.** It mixes `RuntimePaths` (foundation),
   the tool wrapper (kernel↔tools glue), and session-event emitters (kernel).
   Three layers in one file.
4. **Services reach the kernel by back-reference.** `planController`,
   `worktreeController`, `bashRunner` hold `*CodingSession`. Acceptable for now,
   but it means "service" and "kernel" are not yet separable packages. The
   `contextmgr.Host` interface is the model to generalize toward.
5. **No service registry.** The kernel holds ~7 concrete component fields plus
   ~25 other fields. A registry keyed by contract would make the kernel agnostic
   to which services are present (and make features truly optional).

## 5. What is already aligned

The recent refactors moved real, stateful subsystems into self-contained
components/subpackages with narrow APIs: `contextmgr`, `systemprompt`,
`session.render`, `mdloader`, and the in-package `todoStore` / `planController` /
`worktreeController` / `bashRunner` / `extensionPrompts`. These are L2 services in
all but directory location. The next step the directory layout would make
explicit — not new logic.

## 6. How to use this document

- When adding a file, decide its layer first; put it with its peers; respect the
  downward dependency rule.
- When a change needs an upward dependency, that is a smell — introduce a
  contract (interface) at the lower layer instead.
- This doc is the blueprint for any future directory reorg (`kernel/`,
  `services/`, `host/`, `plugins/`, `foundation/`) or kernel extraction. Those
  are deliberately *not* done here; this fixes the design intent first.
