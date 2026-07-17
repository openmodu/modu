# Local AI usage data

`pkg/tokenkit` scans local Codex, Claude Code, and Gemini usage files into SQLite and exposes query APIs for reports or dashboards. It is a library, not a CLI or UI, and its totals are limited by the data each local client records.

## Scan local records

```go
store, err := tokenkit.OpenStore("usage.sqlite")
if err != nil {
	return err
}
defer store.Close()

scanner := tokenkit.NewScanner(store, tokenkit.ScannerOptions{})
stats, err := scanner.ScanAll(ctx)
if err != nil {
	return err
}
```

With empty options, the scanner reads:

- Codex session JSONL below `~/.codex/sessions` and `~/.codex/archived_sessions`.
- Claude Code session JSONL below `~/.claude/projects`.
- Gemini CLI telemetry containing `gemini_cli.token.usage` metrics.

Use `CodexHome`, `ClaudeHome`, `GeminiTelemetryLog`, or `Location` in `ScannerOptions` to override discovery and local-date calculation. Paths are passed to the filesystem as provided; `~` is not expanded by the package.

Each scanner persists file offsets and metadata, so subsequent scans can continue from known files instead of importing every record again.

## Query records and totals

```go
records, err := store.UsageRecords(ctx, tokenkit.UsageRecordFilter{
	App:       tokenkit.AppCodex,
	StartDate: "2026-05-04",
	EndDate:   "2026-05-04",
	Limit:     100,
})

totals, err := store.Totals(ctx, tokenkit.SummaryFilter{
	App:       tokenkit.AppCodex,
	StartDate: "2026-05-04",
	EndDate:   "2026-05-04",
})
```

Dates use `YYYY-MM-DD`. Filters can also select source, model, and—for raw records—workspace, offset, and sort direction.

To aggregate records already in memory, avoid another database query:

```go
var totals tokenkit.SummaryRow
for _, record := range records {
	totals.Accumulate(record)
}
```

`UsageRecord.SessionID()` returns `gateway_session_id` or `session_id` from metadata when present. An empty result means the source record did not carry either key.

## Parse Codex `/status`

The package parses captured `/status` text; it does not invoke Codex or capture the screen:

```go
snapshot := tokenkit.ParseCodexStatus(statusText, time.Now())
if err := store.SaveCodexStatus(ctx, snapshot); err != nil {
	return err
}

latest, err := store.LatestCodexStatus(ctx)
```

The snapshot can include account, model, reasoning mode, session, directory, permissions, context-window usage, rate-limit percentages, and warnings. Missing fields remain empty; callers should not treat an absent value as zero usage or an unlimited quota.
