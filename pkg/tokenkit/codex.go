package tokenkit

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type codexCursor struct {
	SessionID     string
	SessionSource string
	CWD           string
	Originator    string
	ModelProvider string
	CurrentModel  string
	CurrentTurnID string
}

func ScanCodex(ctx context.Context, store *Store, codexHome string, loc *time.Location) (ScanStats, error) {
	var stats ScanStats
	files, err := codexSessionFiles(codexHome)
	if err != nil {
		return stats, err
	}
	for _, file := range files {
		scanned, seen, err := scanCodexFile(ctx, store, file, loc)
		if err != nil {
			return stats, err
		}
		if scanned {
			stats.FilesScanned++
			stats.RecordsSeen += seen
		}
	}
	return stats, nil
}

func codexSessionFiles(codexHome string) ([]string, error) {
	var files []string
	for _, root := range []string{
		filepath.Join(codexHome, "archived_sessions"),
		filepath.Join(codexHome, "sessions"),
	} {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				if isNotExist(err) {
					return nil
				}
				return err
			}
			if d == nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil && !isNotExist(err) {
			return nil, err
		}
	}
	return files, nil
}

func scanCodexFile(ctx context.Context, store *Store, filePath string, loc *time.Location) (bool, int, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		if isNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}

	stateKey := stateKeyForFile(AppCodex, filePath)
	previous, err := store.GetFileScanState(ctx, stateKey)
	if err != nil {
		return false, 0, err
	}
	if sameFileState(previous, info) {
		return false, 0, nil
	}

	var startOffset int64
	fullReset := previous == nil
	if previous != nil {
		if canContinueFromOffset(previous, info) {
			startOffset = previous.Offset
		} else {
			fullReset = true
		}
	}
	if fullReset {
		if err := store.DeleteUsageRecordsForFile(ctx, AppCodex, filePath); err != nil {
			return false, 0, err
		}
	}

	cursor := codexCursorFromState(previous)
	if fullReset {
		cursor = codexCursor{}
	}
	file, err := os.Open(filePath)
	if err != nil {
		return false, 0, err
	}
	defer file.Close()
	if startOffset > 0 {
		if _, err := file.Seek(startOffset, io.SeekStart); err != nil {
			return false, 0, err
		}
	}

	reader := bufio.NewReader(file)
	lastOffset := startOffset
	seen := 0
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			pos, seekErr := file.Seek(0, io.SeekCurrent)
			if seekErr == nil {
				lastOffset = pos - int64(reader.Buffered())
			}
			record, ok := parseCodexLine(line, filePath, &cursor, loc)
			if ok {
				if err := store.UpsertUsageRecord(ctx, record); err != nil {
					return false, seen, err
				}
				seen++
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, seen, err
		}
	}
	if pos, err := file.Seek(0, io.SeekCurrent); err == nil {
		lastOffset = pos
	}

	err = store.UpsertFileScanState(ctx, newFileState(AppCodex, filePath, lastOffset, info, map[string]any{
		"session_id":      cursor.SessionID,
		"session_source":  cursor.SessionSource,
		"cwd":             cursor.CWD,
		"originator":      cursor.Originator,
		"model_provider":  cursor.ModelProvider,
		"current_model":   cursor.CurrentModel,
		"current_turn_id": cursor.CurrentTurnID,
	}))
	return true, seen, err
}

func parseCodexLine(line, filePath string, cursor *codexCursor, loc *time.Location) (UsageRecord, bool) {
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &event); err != nil {
		return UsageRecord{}, false
	}
	payload, _ := event["payload"].(map[string]any)
	switch cleanString(event["type"]) {
	case "session_meta":
		if v := cleanString(payload["id"]); v != "" {
			cursor.SessionID = v
		}
		if v := cleanString(payload["source"]); v != "" {
			cursor.SessionSource = v
		}
		if v := cleanString(payload["cwd"]); v != "" {
			cursor.CWD = v
		}
		if v := cleanString(payload["originator"]); v != "" {
			cursor.Originator = v
		}
		if v := cleanString(payload["model_provider"]); v != "" {
			cursor.ModelProvider = v
		}
	case "turn_context":
		if v := cleanString(payload["turn_id"]); v != "" {
			cursor.CurrentTurnID = v
		}
		if v := extractCodexTurnModel(payload); v != "" {
			cursor.CurrentModel = v
		}
		if v := cleanString(payload["cwd"]); v != "" {
			cursor.CWD = v
		}
	case "event_msg":
		if cleanString(payload["type"]) != "token_count" {
			return UsageRecord{}, false
		}
		info, _ := payload["info"].(map[string]any)
		usage, _ := info["last_token_usage"].(map[string]any)
		if len(usage) == 0 {
			return UsageRecord{}, false
		}
		timestamp := cleanString(event["timestamp"])
		startedAt, ok := parseTime(timestamp)
		if !ok {
			return UsageRecord{}, false
		}
		source := cursor.SessionSource
		if source == "" {
			source = "unknown"
		}
		sessionID := cursor.SessionID
		if sessionID == "" {
			sessionID = filepath.Base(filePath)
		}
		input := intValue(usage["input_tokens"])
		output := intValue(usage["output_tokens"])
		cached := intValue(usage["cached_input_tokens"])
		reasoning := intValue(usage["reasoning_output_tokens"])
		total := intValue(usage["total_tokens"])
		if total == 0 {
			total = input + output + cached + reasoning
		}
		unsplit := 0
		if total > 0 && input == 0 && output == 0 && cached == 0 && reasoning == 0 {
			unsplit = total
		}
		return UsageRecord{
			Source:            "codex:" + source,
			App:               AppCodex,
			ExternalID:        sessionID + ":" + timestamp,
			StartedAt:         startedAt,
			LocalDate:         localDate(startedAt, loc),
			MeasurementMethod: MethodExact,
			Model:             cursor.CurrentModel,
			InputTokens:       input,
			OutputTokens:      output,
			CachedInputTokens: cached,
			ReasoningTokens:   reasoning,
			UnsplitTokens:     unsplit,
			TotalTokens:       total,
			Category:          source,
			Workspace:         cursor.CWD,
			Metadata: map[string]any{
				"session_id":           cursor.SessionID,
				"session_file":         resolvePath(filePath),
				"originator":           cursor.Originator,
				"turn_id":              cursor.CurrentTurnID,
				"turn_model":           cursor.CurrentModel,
				"model_provider":       cursor.ModelProvider,
				"model_context_window": info["model_context_window"],
			},
		}, true
	}
	return UsageRecord{}, false
}

func extractCodexTurnModel(payload map[string]any) string {
	if v := cleanString(payload["model"]); v != "" {
		return v
	}
	mode, _ := payload["collaboration_mode"].(map[string]any)
	if v := cleanString(mode["model"]); v != "" {
		return v
	}
	settings, _ := mode["settings"].(map[string]any)
	return cleanString(settings["model"])
}

func codexCursorFromState(state *FileScanState) codexCursor {
	if state == nil || state.Metadata == nil {
		return codexCursor{}
	}
	return codexCursor{
		SessionID:     cleanString(state.Metadata["session_id"]),
		SessionSource: cleanString(state.Metadata["session_source"]),
		CWD:           cleanString(state.Metadata["cwd"]),
		Originator:    cleanString(state.Metadata["originator"]),
		ModelProvider: cleanString(state.Metadata["model_provider"]),
		CurrentModel:  cleanString(state.Metadata["current_model"]),
		CurrentTurnID: cleanString(state.Metadata["current_turn_id"]),
	}
}
