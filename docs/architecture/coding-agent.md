# Coding Agent Architecture

`pkg/coding_agent` is the session layer between the generic `pkg/agent` loop and a host such as a CLI or RPC server. Its architecture has one enforceable rule: dependencies move from hosts and plugins toward tools, services, the engine, and foundation code—not back toward a host.

The current tree mostly follows this rule. The root Go package still contains both the internal `engine` and the public `CodingSession` facade, so that boundary is a code-review rule rather than a compiler-enforced package boundary.

## Scope

This package owns:

- coding-session state and turn orchestration;
- coding tools and their working-directory binding;
- context-window management, compaction, retry, and persistence;
- extension lifecycle and host-facing runtime state;
- public methods used by CLI, RPC, and SDK callers.

It does not own provider implementations or the raw model/tool loop. Those belong to `pkg/providers` and `pkg/agent`. It is also not a complete application: terminal rendering, process startup, and transport-specific behavior belong to a host.

## Layers

Dependencies may point downward in this table. A lower layer must not import a higher layer to call back into it; define a narrow interface at the lower layer instead.

| Layer | Responsibility | Current location |
|---|---|---|
| L5 host | Drives sessions and exposes them through a process or protocol | `cmd/*`, `modes/`, public `CodingSession` methods |
| L4 plugins | Adds optional tools, commands, hooks, and prompts | `plugins/` |
| L3 tools | Implements actions the model can call | `tools/` |
| L2 services | Owns one stateful session capability behind a narrow API | `services/` |
| L1 engine | Holds session state and orchestrates turns | root `engine` methods |
| L0 foundation | Provides configuration, paths, discovery, events, and shared contracts without session policy | `foundation/`, `pkg/types`, `pkg/providers` |

The practical test is removal: if removing a component prevents any turn from running, it belongs to the engine; if the engine can run without that capability, it is a service or plugin; if it only lets an external process drive the engine, it belongs to the host.

## Turn path

```text
host
  │ Prompt / Steer / FollowUp
  ▼
CodingSession facade
  ▼
engine
  ├─ refresh system prompt and context
  ├─ call pkg/agent
  ├─ execute approved tools
  ├─ persist messages and runtime sidecars
  └─ compact or retry when the relevant service requests it
       │
       ▼
session events and runtime state returned to the host
```

The engine owns sequencing. Services provide capabilities; they do not start turns or render host UI. Tools execute one model-visible action and should not take over session lifecycle.

## Layer responsibilities

### Foundation

Foundation code has no host or plugin policy. It contains configuration loading, API-key storage, event transport, runtime paths, resource discovery, and shared task-output contracts.

Foundation must not import `services`, `tools`, `plugins`, `modes`, or a command package. If a shared type starts depending on session behavior, it no longer belongs here.

### Engine

The internal `engine` owns mutable session state, the `pkg/agent.Agent` instance, service wiring, event emission, and turn orchestration. `CodingSession` embeds the engine and exposes the API used by callers.

Use the receiver as the local boundary:

- `func (s *engine)` is internal orchestration or state;
- `func (s *CodingSession)` is a host-facing operation.

Because both receivers live in the same Go package, an accidental facade-to-internal shortcut still compiles. Review changes by responsibility, not only by import graph.

### Services

Each directory under `services/` owns one stateful capability:

- `contextmgr` and `compaction` manage the model context;
- `session` persists and restores the conversation tree;
- `systemprompt` assembles prompt inputs;
- `approval`, `retry`, and `bash` apply execution policy;
- `memory`, `todo`, `plan`, and `worktree` manage opt-in session features;
- `bgtask` and `mcpclient` integrate longer-running or external capabilities.

When a service needs the engine, the service declares the smallest interface that expresses that need. For example, a service should ask for `Cwd()` or `SwitchCwd(path)`, not receive the whole session just to read fields.

### Tools

Tools are the model's action surface. They parse arguments, call a focused capability, and return a `ToolResult`. Their behavior is bound to a `types.ToolContext`, including the current working directory.

Filesystem tools share read-state tracking to reject writes based on stale file contents. Rebinding the working directory must update the active tools; a tool that retains an old directory can act on the wrong checkout even when the session prompt shows the new one.

### Plugins

Plugins add optional behavior through public extension contracts. They may register tools, commands, hooks, or prompts, but the engine and services must not import a concrete plugin.

Plugin failure must stay local where possible. Initialization can reject the plugin, a hook can reject a tool call, and a background extension can report failure through runtime state; none of those paths should silently rewrite core session state.

### Hosts

A host selects models, supplies API keys, collects approvals, renders events, and controls process or transport lifecycle. The engine reports semantic state; the host decides how that state appears in a terminal, RPC response, or SDK integration.

Host-only concepts must not leak into services. If a service needs confirmation, progress reporting, or a path change, express that as a capability interface and let the host or engine implement it.

## Known boundaries and failure cases

- `engine` and `CodingSession` share a package. The receiver convention is not enforced by Go.
- Some services are always constructed while only their tools are feature-gated. Do not add nil-based optionality unless the service lifecycle itself becomes optional.
- Runtime paths are session and project scoped. Code that writes directly under the agent directory can bypass cleanup and collision rules.
- A worktree or cwd switch is a coordinated operation: update state, rebind tools, rebuild the prompt, and emit the change. Updating only the string leaves the session inconsistent.
- Events and persisted sidecars are host integration surfaces. Changing their shape requires checking resume behavior and every host that consumes runtime state.

## Change checklist

Before adding or moving code, answer these questions:

1. Which single layer owns this responsibility?
2. Does any new import point upward?
3. Can the dependency be expressed as a smaller interface in the lower layer?
4. Does the change affect cwd rebinding, persistence, resume, or runtime-state consumers?
5. Is the behavior optional? If yes, should it be a service feature gate or a plugin?

If the answer to the second question is yes, stop and change the boundary before adding the import.
