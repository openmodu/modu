package tokenkit

import (
	"context"
	"strings"
)

func (s *Store) Summaries(ctx context.Context, filter SummaryFilter) ([]SummaryRow, error) {
	query := `
SELECT
	local_date,
	app,
	source,
	COALESCE(model, '') AS model,
	measurement_method,
	SUM(input_tokens) AS input_tokens,
	SUM(output_tokens) AS output_tokens,
	SUM(cached_input_tokens) AS cached_input_tokens,
	SUM(reasoning_tokens) AS reasoning_tokens,
	SUM(tool_tokens) AS tool_tokens,
	SUM(unsplit_tokens) AS unsplit_tokens,
	SUM(total_tokens) AS total_tokens,
	SUM(credits) AS credits,
	COUNT(*) AS records
FROM tokenkit_usage_records
WHERE 1=1`
	args := []any{}
	if filter.StartDate != "" {
		query += " AND local_date >= ?"
		args = append(args, filter.StartDate)
	}
	if filter.EndDate != "" {
		query += " AND local_date <= ?"
		args = append(args, filter.EndDate)
	}
	if filter.App != "" {
		query += " AND app = ?"
		args = append(args, filter.App)
	}
	if filter.Source != "" {
		query += " AND source = ?"
		args = append(args, filter.Source)
	}
	if filter.Model != "" {
		query += " AND model = ?"
		args = append(args, filter.Model)
	}
	query += `
GROUP BY local_date, app, source, COALESCE(model, ''), measurement_method
ORDER BY local_date DESC, app, source, model`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SummaryRow
	for rows.Next() {
		var row SummaryRow
		if err := rows.Scan(
			&row.LocalDate,
			&row.App,
			&row.Source,
			&row.Model,
			&row.MeasurementMethod,
			&row.InputTokens,
			&row.OutputTokens,
			&row.CachedInputTokens,
			&row.ReasoningTokens,
			&row.ToolTokens,
			&row.UnsplitTokens,
			&row.TotalTokens,
			&row.Credits,
			&row.Records,
		); err != nil {
			return nil, err
		}
		cost := EstimateCostUSD(CostInput{
			Model:             row.Model,
			Provider:          providerForApp(row.App),
			MeasurementMethod: row.MeasurementMethod,
			InputTokens:       row.InputTokens,
			CachedInputTokens: row.CachedInputTokens,
			OutputTokens:      row.OutputTokens,
		})
		row.EstimatedCostUSD = cost
		out = append(out, row)
	}
	return out, rows.Err()
}

func providerForApp(app string) string {
	switch strings.TrimSpace(strings.ToLower(app)) {
	case AppClaudeCode:
		return "anthropic"
	case AppGemini:
		return "google"
	case AppCodex:
		return "openai"
	default:
		return ""
	}
}
