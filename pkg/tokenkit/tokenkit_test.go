package tokenkit

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestScanCodexSessionFile(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	home := t.TempDir()
	sessionFile := filepath.Join(home, "sessions", "2026", "05", "04", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"session_meta","payload":{"id":"sess-1","source":"vscode","cwd":"/repo","originator":"Codex Desktop","model_provider":"openai"}}
{"type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.3-codex"}}
{"timestamp":"2026-05-04T10:00:00+08:00","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000,"output_tokens":200,"cached_input_tokens":300,"reasoning_output_tokens":50,"total_tokens":1550},"model_context_window":200000}}}
`
	if err := os.WriteFile(sessionFile, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := ScanCodex(ctx, store, home, time.FixedZone("CST", 8*60*60))
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 1 || stats.RecordsSeen != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	rows, err := store.Summaries(ctx, SummaryFilter{App: AppCodex})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.Source != "codex:vscode" || row.Model != "gpt-5.3-codex" || row.TotalTokens != 1550 {
		t.Fatalf("unexpected row: %+v", row)
	}
	records, err := store.UsageRecords(ctx, UsageRecordFilter{App: AppCodex, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Workspace != "/repo" || records[0].Metadata["originator"] != "Codex Desktop" {
		t.Fatalf("unexpected records: %+v", records)
	}
	totals, err := store.Totals(ctx, SummaryFilter{App: AppCodex})
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalTokens != 1550 || totals.Records != 1 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
}

func TestScanCodexTotalOnlyUsageAsUnsplit(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	home := t.TempDir()
	sessionFile := filepath.Join(home, "sessions", "2026", "05", "04", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"session_meta","payload":{"id":"sess-total","source":"cli"}}
{"type":"turn_context","payload":{"model":"gpt-5.2-codex"}}
{"timestamp":"2026-05-04T10:00:00+08:00","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":12345}}}}
`
	if err := os.WriteFile(sessionFile, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ScanCodex(ctx, store, home, time.FixedZone("CST", 8*60*60)); err != nil {
		t.Fatal(err)
	}
	totals, err := store.Totals(ctx, SummaryFilter{App: AppCodex})
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalTokens != 12345 || totals.UnsplitTokens != 12345 || totals.InputTokens != 0 || totals.OutputTokens != 0 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
}

func TestScanClaudeCodeSessionFile(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	home := t.TempDir()
	sessionFile := filepath.Join(home, "projects", "demo", "session-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","timestamp":"2026-05-04T09:30:00+08:00","uuid":"event-1","cwd":"/repo","entrypoint":"cli","message":{"id":"msg-1","type":"message","model":"claude-opus-4-7-20260416","usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":40}}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := ScanClaudeCode(ctx, store, home, time.FixedZone("CST", 8*60*60))
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 1 || stats.RecordsSeen != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	rows, err := store.Summaries(ctx, SummaryFilter{App: AppClaudeCode})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.Source != "claude-code:cli" || row.InputTokens != 120 || row.CachedInputTokens != 30 || row.OutputTokens != 40 || row.TotalTokens != 190 {
		t.Fatalf("unexpected row: %+v", row)
	}
	if row.EstimatedCostUSD == nil || *row.EstimatedCostUSD <= 0 {
		t.Fatalf("expected cost estimate, got %+v", row.EstimatedCostUSD)
	}
}

func TestScanGeminiTelemetryLog(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	logFile := filepath.Join(t.TempDir(), "telemetry.log")
	lines := `{"time":"2026-05-04T08:00:00+08:00","name":"gemini_cli.token.usage","attributes":{"model":"gemini-2.5-flash","type":"input","sessionId":"s1"},"value":1234}
{"time":"2026-05-04T08:00:01+08:00","name":"gemini_cli.token.usage","attributes":{"model":"gemini-2.5-flash","type":"output","sessionId":"s1"},"value":234}
`
	if err := os.WriteFile(logFile, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := ScanGeminiTelemetry(ctx, store, logFile, time.FixedZone("CST", 8*60*60))
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 1 || stats.RecordsSeen != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	rows, err := store.Summaries(ctx, SummaryFilter{App: AppGemini})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.InputTokens != 1234 || row.OutputTokens != 234 || row.TotalTokens != 1468 {
		t.Fatalf("unexpected row: %+v", row)
	}
}

func TestScanGeminiChatFile(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	home := t.TempDir()
	chatFile := filepath.Join(home, "tmp", "modu", "chats", "session-2026-05-04T13-19-demo.jsonl")
	if err := os.MkdirAll(filepath.Dir(chatFile), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"id":"user-1","timestamp":"2026-05-04T13:20:08.315Z","type":"user","content":[{"text":"hi"}]}
{"id":"gemini-1","timestamp":"2026-05-04T13:20:13.379Z","type":"gemini","content":"","tokens":{"input":100,"output":20,"cached":30,"thoughts":4,"tool":2,"total":126},"model":"gemini-3-flash-preview"}
{"id":"gemini-1","timestamp":"2026-05-04T13:20:13.379Z","type":"gemini","content":"","tokens":{"input":100,"output":20,"cached":30,"thoughts":4,"tool":2,"total":126},"model":"gemini-3-flash-preview","toolCalls":[]}
`
	if err := os.WriteFile(chatFile, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	stats, err := ScanGemini(ctx, store, home, "", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned != 1 || stats.RecordsSeen != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	totals, err := store.Totals(ctx, SummaryFilter{App: AppGemini})
	if err != nil {
		t.Fatal(err)
	}
	if totals.Records != 1 || totals.TotalTokens != 126 || totals.InputTokens != 100 || totals.OutputTokens != 20 || totals.CachedInputTokens != 30 || totals.ReasoningTokens != 4 || totals.ToolTokens != 2 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
	records, err := store.UsageRecords(ctx, UsageRecordFilter{App: AppGemini, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Source != "gemini:chat" || records[0].Metadata["session_id"] != "session-2026-05-04T13-19-demo" {
		t.Fatalf("unexpected records: %+v", records)
	}
}

func TestParseGeminiOTLPMetricShape(t *testing.T) {
	records := parseGeminiTelemetryLine(`{
		"name":"gemini_cli.token.usage",
		"sum":{
			"dataPoints":[{
				"timeUnixNano":"1777843200000000000",
				"attributes":{
					"model":{"stringValue":"gemini-2.5-flash"},
					"type":{"stringValue":"thought"},
					"sessionId":{"stringValue":"s1"}
				},
				"asInt":"345"
			}]
		}
	}`, "/tmp/telemetry.log", 12, time.UTC)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ReasoningTokens != 345 || records[0].TotalTokens != 345 || records[0].Model != "gemini-2.5-flash" {
		t.Fatalf("unexpected record: %+v", records[0])
	}
}

func TestNormalizeAndEstimateCost(t *testing.T) {
	if got := NormalizeModelDisplay("gpt-5.3-codex"); got != "GPT-5.3 Codex" {
		t.Fatalf("unexpected gpt normalization: %q", got)
	}
	if got := NormalizeModelDisplay("claude-opus-4-7-20260416"); got != "Claude Opus 4.7" {
		t.Fatalf("unexpected claude normalization: %q", got)
	}
	if got := NormalizeModelDisplay("gemini-2.5-flash"); got != "Gemini 2.5 Flash" {
		t.Fatalf("unexpected gemini normalization: %q", got)
	}
	cost := EstimateCostUSD(CostInput{
		Model:             "claude-opus-4-7-20260416",
		Provider:          "anthropic",
		MeasurementMethod: MethodExact,
		InputTokens:       1_000_000,
		CachedInputTokens: 1_000_000,
		OutputTokens:      1_000_000,
	})
	if cost == nil || *cost != 30.5 {
		t.Fatalf("unexpected cost: %v", cost)
	}
}

func TestDailyUsageGroupsByDateAndApp(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	records := []UsageRecord{
		{
			Source:            "codex:cli",
			App:               AppCodex,
			ExternalID:        "codex-1",
			StartedAt:         time.Date(2026, 5, 4, 10, 0, 0, 0, time.UTC),
			LocalDate:         "2026-05-04",
			MeasurementMethod: MethodExact,
			InputTokens:       10,
			OutputTokens:      5,
			TotalTokens:       15,
		},
		{
			Source:            "codex:cli",
			App:               AppCodex,
			ExternalID:        "codex-2",
			StartedAt:         time.Date(2026, 5, 4, 11, 0, 0, 0, time.UTC),
			LocalDate:         "2026-05-04",
			MeasurementMethod: MethodExact,
			InputTokens:       20,
			OutputTokens:      7,
			TotalTokens:       27,
		},
		{
			Source:            "claude-code:cli",
			App:               AppClaudeCode,
			ExternalID:        "claude-1",
			StartedAt:         time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC),
			LocalDate:         "2026-05-03",
			MeasurementMethod: MethodExact,
			InputTokens:       3,
			OutputTokens:      4,
			TotalTokens:       7,
		},
	}
	for _, record := range records {
		if err := store.UpsertUsageRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.DailyUsage(ctx, SummaryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].LocalDate != "2026-05-04" || rows[0].App != AppCodex || rows[0].TotalTokens != 42 || rows[0].Records != 2 {
		t.Fatalf("unexpected first row: %+v", rows[0])
	}
	if rows[1].LocalDate != "2026-05-03" || rows[1].App != AppClaudeCode || rows[1].TotalTokens != 7 || rows[1].Records != 1 {
		t.Fatalf("unexpected second row: %+v", rows[1])
	}
}

func TestParseAndStoreCodexStatus(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	raw := `>_ OpenAI Codex (v0.128.0)                                                    │
│ Visit https://chatgpt.com/codex/settings/usage for up-to-date                  │
│ information on rate limits and credits                                         │
│  Model:                       gpt-5.5 (reasoning medium, summaries auto)       │
│  Directory:                   ~/Code/go/src/github.com/openmodu/modu           │
│  Permissions:                 Workspace (on-request)                           │
│  Agents.md:                   AGENTS.md                                        │
│  Account:                     yuanfeng634@gmail.com (Pro Lite)                 │
│  Collaboration mode:          Default                                          │
│  Session:                     019df1df-b5c9-7aa3-8447-012ae88e2fbf             │
│  Context window:              66% left (95.1K used / 258K)                     │
│  5h limit:                    [████████████████████] 99% left (resets 20:26)   │
│  Weekly limit:                [████████████████████] 98% left                  │
│                               (resets 23:39 on 5 May)                          │
│  GPT-5.3-Codex-Spark limit:                                                    │
│  5h limit:                    [████████████████████] 100% left (resets 20:24)  │
│  Weekly limit:                [████████████████████] 100% left                 │
│                               (resets 15:24 on 11 May)                         │
│  Warning:                     limits may be stale - run /status again shortly. │`

	snapshot := ParseCodexStatus(raw, time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC))
	if snapshot.Version != "0.128.0" || snapshot.Model != "gpt-5.5" || snapshot.Reasoning != "medium" || snapshot.Summaries != "auto" {
		t.Fatalf("unexpected model metadata: %+v", snapshot)
	}
	if snapshot.AccountEmail != "yuanfeng634@gmail.com" || snapshot.AccountPlan != "Pro Lite" {
		t.Fatalf("unexpected account: %+v", snapshot)
	}
	if snapshot.ContextWindow.PercentLeft != 66 || snapshot.ContextWindow.UsedTokens != 95100 || snapshot.ContextWindow.MaxTokens != 258000 {
		t.Fatalf("unexpected context window: %+v", snapshot.ContextWindow)
	}
	if len(snapshot.Limits) != 4 {
		t.Fatalf("expected 4 limits, got %d: %+v", len(snapshot.Limits), snapshot.Limits)
	}
	if snapshot.Limits[0].Model != "gpt-5.5" || snapshot.Limits[0].Window != "5h limit" || snapshot.Limits[0].PercentLeft != 99 {
		t.Fatalf("unexpected first limit: %+v", snapshot.Limits[0])
	}
	if snapshot.Limits[2].Model != "GPT-5.3-Codex-Spark" || snapshot.Limits[2].PercentLeft != 100 {
		t.Fatalf("unexpected spark limit: %+v", snapshot.Limits[2])
	}
	if err := store.SaveCodexStatus(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	latest, err := store.LatestCodexStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.SessionID != snapshot.SessionID || latest.ContextWindow.UsedTokens != 95100 {
		t.Fatalf("unexpected latest snapshot: %+v", latest)
	}
}
