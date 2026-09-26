# Model router progress

Work is split into independently verifiable functions as required by the repository's `AGENTS.md`.

## Done

- Added `pkg/modelrouter` configuration load, validation, atomic save, model catalog, and a loopback CLI gateway. Unit tests cover config permissions, model routing, key isolation, unknown models, and a missing key environment variable.
- Added Chat Completions forwarding and basic Responses and Anthropic Messages conversion for text and function calls. Unit tests cover both translation directions and the SSE event structure emitted for translated responses.
- Added Codex and Claude Code config switching with saved previous values and restore commands. Tests verify that unrelated TOML and JSON settings survive the switch.
- Added `cmd/modu_models` provider, models, use, restore, and serve commands. A CLI test covers provider add, list, and removal.
- Ran opt-in local end-to-end tests with installed Codex CLI and Claude Code CLI, temporary homes, and a fake upstream. Both completed a turn through the gateway. Codex initially rejected an incomplete model catalog (`support_verbosity` missing); the catalog was corrected and the test passed. A `go run` smoke test also exercised provider add, model listing, server startup, and `/v1/models`.
- Ran opt-in tool round trips: Codex executed `exec_command` and Claude Code read a temporary file, then both sent the tool result through the gateway to a second upstream Chat request and completed the turn.
- 2026-09-26: repeated the opt-in local integration suite three times. An initial run exposed a Codex CLI background plugin sync that could race with temporary-home cleanup after the model turn completed. The test now passes `--disable plugins` to Codex, and all three repeats passed. Added a verification path to the library README.

## Next

- Confirm whether reusing Codex and Claude Code logins means making subscription accounts available as cross-agent providers or only reading existing API key configuration.
- Complete protocol coverage for reasoning and custom tools where required by live integration tests. Image input is mapped from Responses and Messages into Chat image URLs; output images are not supported.
- Validate an opt-in live provider only after a suitable key and model are configured. Record any compatibility failures here.
