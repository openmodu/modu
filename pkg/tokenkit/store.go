package tokenkit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.Init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Init(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS tokenkit_usage_records (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	source TEXT NOT NULL,
	app TEXT NOT NULL,
	external_id TEXT NOT NULL,
	started_at TEXT NOT NULL,
	local_date TEXT NOT NULL,
	measurement_method TEXT NOT NULL DEFAULT 'exact',
	model TEXT,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cached_input_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	tool_tokens INTEGER NOT NULL DEFAULT 0,
	unsplit_tokens INTEGER NOT NULL DEFAULT 0,
	total_tokens INTEGER NOT NULL DEFAULT 0,
	credits REAL NOT NULL DEFAULT 0,
	category TEXT,
	workspace TEXT,
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(source, external_id)
);

CREATE INDEX IF NOT EXISTS idx_tokenkit_usage_records_local_date
	ON tokenkit_usage_records(local_date);

CREATE INDEX IF NOT EXISTS idx_tokenkit_usage_records_app_source
	ON tokenkit_usage_records(app, source);

CREATE TABLE IF NOT EXISTS tokenkit_file_scan_state (
	state_key TEXT PRIMARY KEY,
	app TEXT NOT NULL,
	file_path TEXT NOT NULL,
	offset INTEGER NOT NULL DEFAULT 0,
	file_size INTEGER NOT NULL DEFAULT 0,
	mtime_ns INTEGER NOT NULL DEFAULT 0,
	last_scanned_at TEXT NOT NULL,
	metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_tokenkit_file_scan_state_app
	ON tokenkit_file_scan_state(app);

CREATE TABLE IF NOT EXISTS tokenkit_codex_status_snapshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	captured_at TEXT NOT NULL,
	account_email TEXT,
	account_plan TEXT,
	model TEXT,
	session_id TEXT,
	directory TEXT,
	context_percent_left REAL NOT NULL DEFAULT 0,
	context_used_tokens INTEGER NOT NULL DEFAULT 0,
	context_max_tokens INTEGER NOT NULL DEFAULT 0,
	status_json TEXT NOT NULL,
	raw_text TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tokenkit_codex_status_snapshots_captured_at
	ON tokenkit_codex_status_snapshots(captured_at);
`)
	if err != nil {
		return err
	}
	return s.ensureUsageRecordColumns(ctx)
}

func (s *Store) ensureUsageRecordColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info(tokenkit_usage_records)")
	if err != nil {
		return err
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !columns["unsplit_tokens"] {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE tokenkit_usage_records ADD COLUMN unsplit_tokens INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	return err
}

func (s *Store) UpsertUsageRecord(ctx context.Context, record UsageRecord) error {
	if record.MeasurementMethod == "" {
		record.MeasurementMethod = MethodExact
	}
	if record.LocalDate == "" && !record.StartedAt.IsZero() {
		record.LocalDate = localDate(record.StartedAt, time.Local)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tokenkit_usage_records (
	source, app, external_id, started_at, local_date, measurement_method, model,
	input_tokens, output_tokens, cached_input_tokens, reasoning_tokens, tool_tokens,
	unsplit_tokens, total_tokens, credits, category, workspace, metadata_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source, external_id) DO UPDATE SET
	app = excluded.app,
	started_at = excluded.started_at,
	local_date = excluded.local_date,
	measurement_method = excluded.measurement_method,
	model = excluded.model,
	input_tokens = excluded.input_tokens,
	output_tokens = excluded.output_tokens,
	cached_input_tokens = excluded.cached_input_tokens,
	reasoning_tokens = excluded.reasoning_tokens,
	tool_tokens = excluded.tool_tokens,
	unsplit_tokens = excluded.unsplit_tokens,
	total_tokens = excluded.total_tokens,
	credits = excluded.credits,
	category = excluded.category,
	workspace = excluded.workspace,
	metadata_json = excluded.metadata_json
`,
		record.Source,
		record.App,
		record.ExternalID,
		record.StartedAt.Format(time.RFC3339Nano),
		record.LocalDate,
		record.MeasurementMethod,
		record.Model,
		record.InputTokens,
		record.OutputTokens,
		record.CachedInputTokens,
		record.ReasoningTokens,
		record.ToolTokens,
		record.UnsplitTokens,
		record.TotalTokens,
		record.Credits,
		record.Category,
		record.Workspace,
		jsonObject(record.Metadata),
	)
	return err
}

func (s *Store) DeleteUsageRecordsForFile(ctx context.Context, app, filePath string) error {
	pattern := fmt.Sprintf("%%\"session_file\":\"%s\"%%", escapeLike(resolvePath(filePath)))
	_, err := s.db.ExecContext(ctx, `
DELETE FROM tokenkit_usage_records
WHERE app = ? AND metadata_json LIKE ? ESCAPE '\'
`, app, pattern)
	return err
}

func (s *Store) GetFileScanState(ctx context.Context, stateKey string) (*FileScanState, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT state_key, app, file_path, offset, file_size, mtime_ns, last_scanned_at, metadata_json
FROM tokenkit_file_scan_state
WHERE state_key = ?
`, stateKey)
	var state FileScanState
	var lastScannedAt, metadataJSON string
	err := row.Scan(
		&state.StateKey,
		&state.App,
		&state.FilePath,
		&state.Offset,
		&state.FileSize,
		&state.ModTimeUnixNS,
		&lastScannedAt,
		&metadataJSON,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t, ok := parseTime(lastScannedAt); ok {
		state.LastScannedAt = t
	}
	state.Metadata = parseJSONObject(metadataJSON)
	return &state, nil
}

func (s *Store) UpsertFileScanState(ctx context.Context, state FileScanState) error {
	if state.LastScannedAt.IsZero() {
		state.LastScannedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tokenkit_file_scan_state (
	state_key, app, file_path, offset, file_size, mtime_ns, last_scanned_at, metadata_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(state_key) DO UPDATE SET
	app = excluded.app,
	file_path = excluded.file_path,
	offset = excluded.offset,
	file_size = excluded.file_size,
	mtime_ns = excluded.mtime_ns,
	last_scanned_at = excluded.last_scanned_at,
	metadata_json = excluded.metadata_json
`,
		state.StateKey,
		state.App,
		state.FilePath,
		state.Offset,
		state.FileSize,
		state.ModTimeUnixNS,
		state.LastScannedAt.Format(time.RFC3339Nano),
		jsonObject(state.Metadata),
	)
	return err
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}
