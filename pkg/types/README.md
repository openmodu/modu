# Shared Agent contracts

`pkg/types` defines the data and interface contracts shared by Providers, the Agent loop, tools, events, and host applications. It contains no Agent control flow or concrete tool implementation; runtime behavior belongs in `pkg/agent`.

Import this package when two components must share the same Go type identity for `Config`, `AgentContext`, `State`, `Event`, `Tool`, `ToolResult`, `ToolApprovalDecision`, `RuntimeHooks`, or loop input and output values.

## File ownership

| File | Contracts |
|---|---|
| `messages.go` | Conversation messages and roles |
| `content.go` | Text, thinking, Tool Call, and other content blocks |
| `usage.go` | Token and cost accounting |
| `model.go` | Models, Providers, reasoning settings, and compatibility metadata |
| `stream.go` | Assistant stream events and `EventStream` |
| `agent_config.go` | Agent configuration, stream callback, and `RuntimeHooks` |
| `agent_loop.go` | LLM and tool execution interfaces plus loop input and output |
| `tool.go` | Tool contracts, execution values, JSON Schema, and approval |
| `agent_state.go` | State, status, interrupts, and resume decisions |
| `agent_event.go` | Runtime events, `EventSink`, and event stream |

Keep behavior out of this package unless it is required to preserve or interpret one of these contracts. `pkg/agent` supplies `Agent`, `Loop`, `DefaultLLM`, and `DefaultTools`.
