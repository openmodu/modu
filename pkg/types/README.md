# Modu Shared Types

`pkg/types` owns shared data contracts used across providers, agent runtime,
tools, events, and host applications.

Agent-facing definitions such as `Config`, `AgentContext`, `State`, `Event`,
`Tool`, `ToolResult`, `ToolApprovalDecision`, `RuntimeHooks`, and loop input /
output structs live here so every package can reuse the same Go type identity.
`pkg/agent` owns runtime behaviour such as `Agent`, `Loop`, `DefaultLLM`, and
`DefaultTools`; callers should import this package directly for shared
contracts.

File ownership:

- `messages.go`: conversation message shapes and roles.
- `content.go`: message content blocks.
- `usage.go`: token/cost usage accounting.
- `model.go`: model, provider, reasoning, and provider-compat metadata.
- `stream.go`: assistant streaming event contracts.
- `agent_config.go`: agent configuration, stream callback, and runtime hooks.
- `agent_loop.go`: LLM/tool execution interfaces and loop input/output.
- `tool.go`: tool interfaces, tool execution input/output, and approval.
- `agent_state.go`: agent state, status, interrupts, and resume decisions.
- `agent_event.go`: agent runtime events and event stream.
