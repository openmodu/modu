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
- `SummaryRow.Accumulate(UsageRecord)` for in-memory aggregation without a database round-trip.
- `UsageRecord.SessionID()` for extracting correlated session IDs from record metadata.
- acp-gateway integration for automatic background sync, manual sync, web-console token usage display, and project/session scoped overview output.

## Not Implemented Yet

- Automatic non-interactive Codex status capture.
- Standalone HTML/TUI dashboard outside acp-gateway.
- Hosted sync or remote usage API integration.

## CodexBar Gap Notes

CodexBar tracks live quota windows in addition to local cost usage. tokenkit
currently has the local ledger path and acp-gateway API surface, but these
CodexBar-style sources are still missing:

- Codex OAuth `auth.json` usage API and CLI RPC `account/rateLimits/read`.
- Codex PTY `/status` probe as an automatic collector; only pasted status text is parsed today.
- Claude OAuth/browser-cookie/CLI fallback quota windows.
- Gemini OAuth quota API via Gemini CLI credentials.
- Credits, reset timestamps, stale/error status, and per-account source selection.

## Next Step

Find a reliable source for live Codex status data. If the CLI exposes it later, wire that command into a collector. Until then, callers can parse `/status` text with `ParseCodexStatus`.
