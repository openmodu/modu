package tokenkit

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

func ScanGeminiTelemetry(ctx context.Context, store *Store, telemetryLog string, loc *time.Location) (ScanStats, error) {
	info, err := os.Stat(telemetryLog)
	if err != nil {
		if isNotExist(err) {
			return ScanStats{}, nil
		}
		return ScanStats{}, err
	}
	stateKey := stateKeyForFile(AppGemini, telemetryLog)
	previous, err := store.GetFileScanState(ctx, stateKey)
	if err != nil {
		return ScanStats{}, err
	}
	if sameFileState(previous, info) {
		return ScanStats{}, nil
	}
	var startOffset int64
	if previous != nil && canContinueFromOffset(previous, info) {
		startOffset = previous.Offset
	} else if previous != nil {
		if err := store.DeleteUsageRecordsForFile(ctx, AppGemini, telemetryLog); err != nil {
			return ScanStats{}, err
		}
	}

	file, err := os.Open(telemetryLog)
	if err != nil {
		return ScanStats{}, err
	}
	defer file.Close()
	if startOffset > 0 {
		if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
			return ScanStats{}, err
		}
	}

	reader := bufio.NewReader(file)
	seen := 0
	for {
		offset, _ := file.Seek(0, io.SeekCurrent)
		offset -= int64(reader.Buffered())
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if records := parseGeminiTelemetryLine(line, telemetryLog, offset, loc); len(records) > 0 {
				for _, record := range records {
					if err := store.UpsertUsageRecord(ctx, record); err != nil {
						return ScanStats{}, err
					}
					seen++
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return ScanStats{}, err
		}
	}
	lastOffset, _ := file.Seek(0, io.SeekCurrent)
	if err := store.UpsertFileScanState(ctx, newFileState(AppGemini, telemetryLog, lastOffset, info, nil)); err != nil {
		return ScanStats{}, err
	}
	return ScanStats{FilesScanned: 1, RecordsSeen: seen}, nil
}

func parseGeminiTelemetryLine(line, filePath string, offset int64, loc *time.Location) []UsageRecord {
	var payload any
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(line)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil
	}
	var out []UsageRecord
	collectGeminiTelemetryRecords(payload, "", nil, filePath, offset, loc, &out)
	return out
}

func collectGeminiTelemetryRecords(v any, inheritedName string, inheritedAttrs map[string]any, filePath string, offset int64, loc *time.Location, out *[]UsageRecord) {
	switch x := v.(type) {
	case map[string]any:
		name := telemetryName(x)
		if name == "" {
			name = inheritedName
		}
		attrs := cloneAttrs(inheritedAttrs)
		copyAttrs(attrs, telemetryAttributes(x))
		if isGeminiTokenMetric(name) && !hasNestedDataPoints(x) {
			if record, ok := geminiRecordFromTelemetryObject(x, name, attrs, filePath, offset, loc); ok {
				*out = append(*out, record)
			}
		}
		for _, child := range x {
			collectGeminiTelemetryRecords(child, name, attrs, filePath, offset, loc, out)
		}
	case []any:
		for _, child := range x {
			collectGeminiTelemetryRecords(child, inheritedName, inheritedAttrs, filePath, offset, loc, out)
		}
	}
}

func geminiRecordFromTelemetryObject(obj map[string]any, name string, attrs map[string]any, filePath string, offset int64, loc *time.Location) (UsageRecord, bool) {
	model := cleanString(attrs["model"])
	tokenType := cleanString(attrs["type"])
	if tokenType == "" {
		tokenType = cleanString(attrs["gen_ai.token.type"])
	}
	count := telemetryCount(obj)
	if count <= 0 || tokenType == "" {
		return UsageRecord{}, false
	}
	startedAt := telemetryTimestamp(obj)
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	record := UsageRecord{
		Source:            "gemini:telemetry",
		App:               AppGemini,
		ExternalID:        geminiExternalID(filePath, offset, model, tokenType, startedAt),
		StartedAt:         startedAt,
		LocalDate:         localDate(startedAt, loc),
		MeasurementMethod: MethodExact,
		Model:             model,
		TotalTokens:       count,
		Category:          "telemetry",
		Metadata: map[string]any{
			"session_id":   cleanString(attrs["sessionId"]),
			"session_file": resolvePath(filePath),
			"token_type":   tokenType,
			"metric_name":  name,
		},
	}
	switch strings.ToLower(tokenType) {
	case "input":
		record.InputTokens = count
	case "output":
		record.OutputTokens = count
	case "thought", "reasoning":
		record.ReasoningTokens = count
	case "cache", "cached":
		record.CachedInputTokens = count
	case "tool":
		record.ToolTokens = count
	}
	return record, true
}

func isGeminiTokenMetric(name string) bool {
	return name == "gemini_cli.token.usage" || name == "gen_ai.client.token.usage"
}

func hasNestedDataPoints(obj map[string]any) bool {
	for _, key := range []string{"sum", "gauge"} {
		if nested, ok := obj[key].(map[string]any); ok {
			if _, ok := nested["dataPoints"].([]any); ok {
				return true
			}
		}
	}
	_, ok := obj["dataPoints"].([]any)
	return ok
}

func cloneAttrs(attrs map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range attrs {
		out[key] = value
	}
	return out
}

func telemetryName(obj map[string]any) string {
	for _, key := range []string{"name", "metric", "eventName"} {
		if v := cleanString(obj[key]); v != "" {
			return v
		}
	}
	body, _ := obj["body"].(map[string]any)
	return cleanString(body["name"])
}

func telemetryAttributes(obj map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"attributes", "attrs"} {
		if attrs, ok := obj[key].(map[string]any); ok {
			copyAttrs(out, attrs)
		}
	}
	if body, ok := obj["body"].(map[string]any); ok {
		if attrs, ok := body["attributes"].(map[string]any); ok {
			copyAttrs(out, attrs)
		}
	}
	return out
}

func copyAttrs(dst, src map[string]any) {
	for key, value := range src {
		if attr, ok := value.(map[string]any); ok {
			if v, ok := attr["stringValue"]; ok {
				dst[key] = v
				continue
			}
			if v, ok := attr["intValue"]; ok {
				dst[key] = v
				continue
			}
		}
		dst[key] = value
	}
}

func telemetryCount(obj map[string]any) int {
	for _, key := range []string{"value", "count", "asInt", "intValue"} {
		if v := intValue(obj[key]); v > 0 {
			return v
		}
	}
	for _, key := range []string{"sum", "gauge"} {
		if nested, ok := obj[key].(map[string]any); ok {
			if v := telemetryCount(nested); v > 0 {
				return v
			}
		}
	}
	points, _ := obj["dataPoints"].([]any)
	total := 0
	for _, point := range points {
		if nested, ok := point.(map[string]any); ok {
			total += telemetryCount(nested)
		}
	}
	return total
}

func telemetryTimestamp(obj map[string]any) time.Time {
	for _, key := range []string{"time", "timestamp", "timeUnixNano", "startTimeUnixNano"} {
		switch v := obj[key].(type) {
		case string:
			if t, ok := parseTime(v); ok {
				return t
			}
			if n, err := parseInt64(v); err == nil && n > 0 {
				return time.Unix(0, n).UTC()
			}
		case json.Number:
			if n, err := v.Int64(); err == nil && n > 0 {
				return time.Unix(0, n).UTC()
			}
		case float64:
			if v > 0 {
				return time.Unix(0, int64(v)).UTC()
			}
		}
	}
	return time.Time{}
}

func geminiExternalID(filePath string, offset int64, model, tokenType string, t time.Time) string {
	return resolvePath(filePath) + ":" + int64String(offset) + ":" + model + ":" + tokenType + ":" + t.Format(time.RFC3339Nano)
}
