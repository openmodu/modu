package tokenkit

import (
	"context"
	"strings"
)

func (s *Store) UsageRecords(ctx context.Context, filter UsageRecordFilter) ([]UsageRecord, error) {
	query := `
SELECT
	id,
	source,
	app,
	external_id,
	started_at,
	local_date,
	measurement_method,
	COALESCE(model, '') AS model,
	input_tokens,
	output_tokens,
	cached_input_tokens,
	reasoning_tokens,
	tool_tokens,
	unsplit_tokens,
	total_tokens,
	credits,
	COALESCE(category, '') AS category,
	COALESCE(workspace, '') AS workspace,
	metadata_json,
	created_at
FROM tokenkit_usage_records
WHERE 1=1`
	args := []any{}
	query, args = appendUsageRecordFilter(query, args, filter.StartDate, filter.EndDate, filter.App, filter.Source, filter.Model, filter.Workspace)
	if filter.Ascending {
		query += " ORDER BY started_at ASC, id ASC"
	} else {
		query += " ORDER BY started_at DESC, id DESC"
	}
	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		if filter.Limit <= 0 {
			query += " LIMIT -1"
		}
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UsageRecord
	for rows.Next() {
		record, err := scanUsageRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) Totals(ctx context.Context, filter SummaryFilter) (SummaryRow, error) {
	query := `
SELECT
	COALESCE(SUM(input_tokens), 0) AS input_tokens,
	COALESCE(SUM(output_tokens), 0) AS output_tokens,
	COALESCE(SUM(cached_input_tokens), 0) AS cached_input_tokens,
	COALESCE(SUM(reasoning_tokens), 0) AS reasoning_tokens,
	COALESCE(SUM(tool_tokens), 0) AS tool_tokens,
	COALESCE(SUM(unsplit_tokens), 0) AS unsplit_tokens,
	COALESCE(SUM(total_tokens), 0) AS total_tokens,
	COALESCE(SUM(credits), 0) AS credits,
	COUNT(*) AS records
FROM tokenkit_usage_records
WHERE 1=1`
	args := []any{}
	query, args = appendUsageRecordFilter(query, args, filter.StartDate, filter.EndDate, filter.App, filter.Source, filter.Model, "")

	row := SummaryRow{
		App:    filter.App,
		Source: filter.Source,
		Model:  filter.Model,
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&row.InputTokens,
		&row.OutputTokens,
		&row.CachedInputTokens,
		&row.ReasoningTokens,
		&row.ToolTokens,
		&row.UnsplitTokens,
		&row.TotalTokens,
		&row.Credits,
		&row.Records,
	)
	if err != nil {
		return SummaryRow{}, err
	}
	cost := EstimateCostUSD(CostInput{
		Model:             row.Model,
		Provider:          providerForApp(row.App),
		MeasurementMethod: MethodExact,
		InputTokens:       row.InputTokens,
		CachedInputTokens: row.CachedInputTokens,
		OutputTokens:      row.OutputTokens,
	})
	row.EstimatedCostUSD = cost
	return row, nil
}

type usageRecordScanner interface {
	Scan(dest ...any) error
}

func scanUsageRecord(scanner usageRecordScanner) (UsageRecord, error) {
	var record UsageRecord
	var startedAtRaw, createdAtRaw, metadataJSON string
	err := scanner.Scan(
		&record.ID,
		&record.Source,
		&record.App,
		&record.ExternalID,
		&startedAtRaw,
		&record.LocalDate,
		&record.MeasurementMethod,
		&record.Model,
		&record.InputTokens,
		&record.OutputTokens,
		&record.CachedInputTokens,
		&record.ReasoningTokens,
		&record.ToolTokens,
		&record.UnsplitTokens,
		&record.TotalTokens,
		&record.Credits,
		&record.Category,
		&record.Workspace,
		&metadataJSON,
		&createdAtRaw,
	)
	if err != nil {
		return UsageRecord{}, err
	}
	if t, ok := parseTime(startedAtRaw); ok {
		record.StartedAt = t
	}
	if t, ok := parseTime(createdAtRaw); ok {
		record.CreatedAt = t
	}
	record.Metadata = parseJSONObject(metadataJSON)
	return record, nil
}

func appendUsageRecordFilter(query string, args []any, startDate, endDate, app, source, model, workspace string) (string, []any) {
	if strings.TrimSpace(startDate) != "" {
		query += " AND local_date >= ?"
		args = append(args, strings.TrimSpace(startDate))
	}
	if strings.TrimSpace(endDate) != "" {
		query += " AND local_date <= ?"
		args = append(args, strings.TrimSpace(endDate))
	}
	if strings.TrimSpace(app) != "" {
		query += " AND app = ?"
		args = append(args, strings.TrimSpace(app))
	}
	if strings.TrimSpace(source) != "" {
		query += " AND source = ?"
		args = append(args, strings.TrimSpace(source))
	}
	if strings.TrimSpace(model) != "" {
		query += " AND model = ?"
		args = append(args, strings.TrimSpace(model))
	}
	if strings.TrimSpace(workspace) != "" {
		query += " AND workspace = ?"
		args = append(args, strings.TrimSpace(workspace))
	}
	return query, args
}
