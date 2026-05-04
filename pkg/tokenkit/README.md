# tokenkit

`pkg/tokenkit` provides data access for local AI coding usage and Codex status data.

Current scope:

- Scan Codex, Claude Code, and Gemini local token usage into SQLite.
- Query raw usage records with filters.
- Query totals and grouped summaries.
- Parse and persist Codex `/status` output, including account, model, context window, and limit percentages.

## Usage Records

```go
store, err := tokenkit.OpenStore("usage.sqlite")
if err != nil {
    return err
}
defer store.Close()

scanner := tokenkit.NewScanner(store, tokenkit.ScannerOptions{
    CodexHome: "~/.codex",
})
_, err = scanner.ScanCodex(ctx)
if err != nil {
    return err
}

records, err := store.UsageRecords(ctx, tokenkit.UsageRecordFilter{
    App:       tokenkit.AppCodex,
    StartDate: "2026-05-04",
    EndDate:   "2026-05-04",
    Limit:     100,
})
```

## Totals

```go
totals, err := store.Totals(ctx, tokenkit.SummaryFilter{
    App:       tokenkit.AppCodex,
    StartDate: "2026-05-04",
    EndDate:   "2026-05-04",
})
```

## Aggregating Records

`SummaryRow.Accumulate` accumulates a single `UsageRecord` into an in-memory summary without a database round-trip:

```go
var totals tokenkit.SummaryRow
for _, record := range records {
    totals.Accumulate(record)
}
```

## Extracting Metadata

`UsageRecord.SessionID` reads the `gateway_session_id` or `session_id` key from a record's metadata map:

```go
if id := record.SessionID(); id != "" {
    // correlate record to a known session
}
```

## Codex Status

Codex does not currently expose a non-interactive `codex status` command in the local CLI. The first supported path is parsing the text shown by `/status`:

```go
snapshot := tokenkit.ParseCodexStatus(statusText, time.Now())
err := store.SaveCodexStatus(ctx, snapshot)

latest, err := store.LatestCodexStatus(ctx)
```

The parsed snapshot includes:

- `AccountEmail`, `AccountPlan`
- `Model`, `Reasoning`, `Summaries`
- `SessionID`, `Directory`, `Permissions`
- `ContextWindow.PercentLeft`, `UsedTokens`, `MaxTokens`
- `Limits`, including 5h and weekly limit percentages
- `Warning`

