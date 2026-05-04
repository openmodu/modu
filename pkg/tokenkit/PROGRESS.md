# tokenkit Progress

## Current Goal

Provide data access first, without building a dashboard.

## Implemented

- SQLite ledger for normalized token usage records.
- File scan state for incremental local log scans.
- Codex token usage scanner from local session JSONL.
- Claude Code token usage scanner from local session JSONL.
- Gemini token usage scanner from local telemetry logs.
- Raw usage record query API with filters and pagination.
- Totals and grouped summary query APIs.
- Codex `/status` text parser for account, model, context window, session, and limit data.
- Codex status snapshot persistence and latest-snapshot retrieval.
- Unit tests for scanners, queries, pricing, Gemini OTLP shape, and Codex status parsing.

## Not Implemented Yet

- Automatic non-interactive Codex status capture.
- HTML/TUI dashboard.
- Hosted sync or remote usage API integration.

## Next Step

Find a reliable source for live Codex status data. If the CLI exposes it later, wire that command into a collector. Until then, callers can parse `/status` text with `ParseCodexStatus`.

