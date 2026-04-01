# Coding Agent Progress

This file tracks the parity work for `pkg/coding_agent` against the Claude Code reference tree.

## Current Assessment

High-priority gaps identified before this round:

- Session persistence was split across two systems:
  `session.Manager` only captured user prompts, model changes, and compaction records.
  `persistence.go` could save full message snapshots, but was not wired into the main prompt flow.
- Skills were loaded and injected as plain prompt text into the main session.
  They were not executed in an isolated sub-agent context.
- Context file discovery existed in `resource.Loader`, but the discovered files were not fed into `SystemPromptBuilder`.
- `spawn_agent_tool.go` existed, but was not integrated into `NewCodingSession`.
- Test coverage around `skills`, `subagent`, `resource`, and persistence behavior was thin.

## Completed In This Round

- Wired discovered context files into the system prompt build path.
- Wired agent `message_end` events into persistence so assistant and tool-result messages are recorded.
- Hooked `SaveMessages()` into the live session flow so `messages.jsonl` is generated from real prompts.
- Added isolated skill execution for explicit `/skill` invocations using `subagent.Run`.
- Recorded thinking-level changes into the session timeline.
- Ensured `~/.coding_agent/agents` is created alongside skills/prompts/sessions.
- Fixed the failing `read` tool offset formatting test by using tab-separated line numbers.
- Integrated `spawn_agent` into `CodingSession` and the SDK factory when a mailbox client is supplied.
- Expanded subagent frontmatter support with `disallowed_tools`, `thinking`, and `max_turns` parsing.
- Applied `disallowed_tools` and `thinking` during subagent execution.
- Expanded subagent frontmatter support with `skills` and `memory`.
- Added prompt augmentation for subagents so referenced skills and selected memory scopes are injected into the effective system prompt.
- Added minimal `permission_mode` support for subagents.
- Implemented `read-only` permission mode to strip mutating tools from subagent execution.
- Added a session-scoped `todo_write` tool and integrated it into default session tool registration.
- Applied `max_turns` during subagent execution by enforcing a per-subagent assistant-turn cap.
- Added `effort` support for subagents and mapped it onto the available thinking levels when `thinking` is not explicitly set.
- Added `background` support for subagents through asynchronous execution in `spawn_subagent`.
- Added a session-scoped `task_output` tool for inspecting background task results.
- Added minimal plan-mode tools: `enter_plan_mode` and `exit_plan_mode`.
- Added minimal worktree-mode tools: `enter_worktree` and `exit_worktree`.
- Added `isolation: worktree` support for subagents by running them inside temporary git worktrees with cwd-bound tools.
- Refreshed the session system prompt dynamically when plan mode or worktree mode changes.
- Added exported session accessors for discovered subagents, current todos, background tasks, plan mode, and active worktree so external frontends can inspect agent state.
- Wired `examples/modu_code` to use its local `agents/` directory by default.
- Added a local in-process mailbox runtime to `examples/modu_code` so `spawn_agent` is exercised during example runs instead of remaining disconnected.
- Added manual validation slash commands to `examples/modu_code` for `/agents`, `/todos`, `/tasks`, `/plan`, and `/worktree`.
- Added focused tests for:
  session persistence after prompt/tool execution
  isolated slash-skill execution
  spawn_agent tool registration
  subagent tool filtering and thinking behavior
  subagent skill and memory prompt augmentation
  subagent read-only permission mode filtering
  todo_write tool behavior
  subagent max_turns enforcement
  subagent effort mapping
  background subagent execution with task_output
  plan mode tool toggling
  worktree enter/exit
  subagent worktree isolation

## Still Missing

- Broader end-to-end coverage for the `examples/modu_code` interactive path
- Deeper plan-mode semantics beyond the current state/prompt toggle
- Richer worktree lifecycle controls and cleanup introspection

## Suggested Next Steps

1. Integrate `spawn_agent` behind an option so orchestration can be enabled without coupling every session to mailbox.
2. Improve plan/worktree semantics beyond the current minimal implementation.
3. Expand integration coverage around background tasks, tool replacement, and session switching.
