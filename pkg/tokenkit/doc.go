// Package tokenkit collects local AI coding token usage into a small SQLite
// ledger.
//
// The package follows the same core shape as TokKit: discover local usage
// files, scan them incrementally with file checkpoints, normalize provider
// events into usage records, and query aggregate summaries for dashboards or
// reports. It intentionally stays library-only; callers can build their own CLI,
// TUI, or web dashboard on top.
//
// Supported first-party scanners:
//   - Codex session JSONL under ~/.codex/sessions and ~/.codex/archived_sessions
//   - Claude Code session JSONL under ~/.claude/projects
//   - Gemini CLI local telemetry logs containing gemini_cli.token.usage metrics
package tokenkit
