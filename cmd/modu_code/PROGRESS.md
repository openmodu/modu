# modu_code product progress

This file tracks product-experience work for `modu_code`. Keep each item small
enough to implement, verify, and commit independently.

## Done

- Status line moved above the input separator, with animated running state,
  persisted completed state, and duration formatting that supports `min`.
- Terminal resize handling keeps the user prompt visible and avoids duplicate
  completed-status lines.
- Model configuration moved into `~/.coding_agent/config.json` with support for
  multiple configured models and an active model.
- `/model` supports listing, switching by configured name, and a TUI picker;
  selected model is persisted back to config.
- Switching models and `/clear` clear in-memory and persisted conversation
  context, then refresh the dynamic system prompt.
- Dynamic prompt environment now includes the actual connected provider/model
  without hardcoding a vendor identity.
- OpenAI-compatible provider retries Xiaomi MiMo-style `reasoning_content`
  failures by dropping assistant history entries that cannot satisfy the API.
- modu_code-owned comments, harness hint tags, and context discovery no longer
  use Claude-specific naming.

## Next

1. Add `/context` to show active prompt/context sources:
   model, cwd, loaded project context files, memory status, skills, plan mode,
   and worktree mode.
2. Add `/doctor` for config and runtime checks:
   config path, active model, provider registration, base URL reachability,
   API key presence, and current context files.
3. Improve model-switch feedback:
   show that the old context was cleared and which config entry became active.
4. Improve API failure UX:
   collapse repeated timeout errors and offer retry, switch model, edit config,
   or abort.
5. Add config commands:
   initialize, validate, and print examples for multi-model config.

## Validation Log

- 2026-05-16: `go test ./pkg/coding_agent ./pkg/tui ./cmd/modu_code ./pkg/providers/openai ./pkg/agent`
  passed for the completed model/status/provider fixes.
