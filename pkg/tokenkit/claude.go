package tokenkit

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

var claudeEntrypointRE = regexp.MustCompile(`(?i)cc_entrypoint=([a-z0-9-]+)`)

type claudeCandidate struct {
	Record UsageRecord
	Rank   claudeRank
}

type claudeRank struct {
	Total     int
	Output    int
	StartedAt string
}

func ScanClaudeCode(ctx context.Context, store *Store, claudeHome string, loc *time.Location) (ScanStats, error) {
	var stats ScanStats
	root := filepath.Join(claudeHome, "projects")
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if isNotExist(err) {
				return nil
			}
			return err
		}
		if d != nil && !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil && !isNotExist(err) {
		return stats, err
	}

	var mu sync.Mutex
	g, ctx := errgroup.WithContext(ctx)
	for _, file := range files {
		filePath := file
		g.Go(func() error {
			scanned, seen, err := scanClaudeFile(ctx, store, filePath, claudeHome, loc)
			if err != nil {
				return err
			}
			if scanned {
				mu.Lock()
				stats.FilesScanned++
				stats.RecordsSeen += seen
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return stats, err
	}
	return stats, nil
}

func scanClaudeFile(ctx context.Context, store *Store, filePath, claudeHome string, loc *time.Location) (bool, int, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		if isNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}
	sessionID := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	entrypoint := claudeEntrypoint(claudeHome, sessionID)
	stateKey := stateKeyForFile(AppClaudeCode, filePath)
	previous, err := store.GetFileScanState(ctx, stateKey)
	if err != nil {
		return false, 0, err
	}
	if sameFileState(previous, info) && cleanString(previous.Metadata["entrypoint"]) == entrypoint {
		return false, 0, nil
	}

	var startOffset int64
	fullReset := true
	if previous != nil && cleanString(previous.Metadata["entrypoint"]) == entrypoint && canContinueFromOffset(previous, info) {
		fullReset = false
		startOffset = previous.Offset
	}
	if fullReset {
		// Handled in transaction below
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
	best := map[string]claudeCandidate{}
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if candidate, ok := parseClaudeLine(line, filePath, sessionID, entrypoint, loc); ok {
				current, exists := best[candidate.Record.ExternalID]
				if !exists || candidate.Rank.greaterOrEqual(current.Rank) {
					best[candidate.Record.ExternalID] = candidate
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, 0, err
		}
	}
	lastOffset, _ := file.Seek(0, io.SeekCurrent)

	err = store.WithTx(ctx, func(tx *sql.Tx) error {
		if fullReset {
			if _, err := tx.ExecContext(ctx, "DELETE FROM tokenkit_usage_records WHERE app = ? AND external_id LIKE ?", AppClaudeCode, sessionID+":%"); err != nil {
				return err
			}
		}

		seen := 0
		for _, candidate := range best {
			if err := upsertUsageRecord(ctx, tx, candidate.Record); err != nil {
				return err
			}
			seen++
		}
		return upsertFileScanState(ctx, tx, newFileState(AppClaudeCode, filePath, lastOffset, info, map[string]any{
			"session_id": sessionID,
			"entrypoint": entrypoint,
		}))
	})

	seen := len(best)
	return true, seen, err
}

func parseClaudeLine(line, filePath, sessionID, fallbackEntrypoint string, loc *time.Location) (claudeCandidate, bool) {
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &event); err != nil {
		return claudeCandidate{}, false
	}
	if cleanString(event["type"]) != "assistant" {
		return claudeCandidate{}, false
	}
	message, _ := event["message"].(map[string]any)
	usage, _ := message["usage"].(map[string]any)
	if len(usage) == 0 {
		return claudeCandidate{}, false
	}
	startedRaw := cleanString(event["timestamp"])
	startedAt, ok := parseTime(startedRaw)
	if !ok {
		return claudeCandidate{}, false
	}
	messageID := cleanString(message["id"])
	if messageID == "" {
		messageID = cleanString(event["uuid"])
	}
	if messageID == "" {
		return claudeCandidate{}, false
	}

	entrypoint := cleanString(event["entrypoint"])
	if entrypoint == "" {
		entrypoint = fallbackEntrypoint
	}
	source := claudeSourceForEntrypoint(entrypoint)
	input, cached, output, total := claudeUsageTotals(usage)
	if total <= 0 {
		return claudeCandidate{}, false
	}
	record := UsageRecord{
		Source:            source,
		App:               AppClaudeCode,
		ExternalID:        sessionID + ":" + messageID,
		StartedAt:         startedAt,
		LocalDate:         localDate(startedAt, loc),
		MeasurementMethod: MethodExact,
		Model:             cleanString(message["model"]),
		InputTokens:       input,
		OutputTokens:      output,
		CachedInputTokens: cached,
		TotalTokens:       total,
		Category:          sourceCategory(source),
		Workspace:         cleanString(event["cwd"]),
		Metadata: map[string]any{
			"session_file":   resolvePath(filePath),
			"session_id":     sessionID,
			"entrypoint":     entrypoint,
			"message_uuid":   event["uuid"],
			"message_type":   message["type"],
			"claude_version": event["version"],
			"git_branch":     event["gitBranch"],
		},
	}
	return claudeCandidate{Record: record, Rank: claudeRank{Total: total, Output: output, StartedAt: startedRaw}}, true
}

func claudeUsageTotals(usage map[string]any) (input int, cached int, output int, total int) {
	directInput := intValue(usage["input_tokens"])
	cacheCreation := intValue(usage["cache_creation_input_tokens"])
	cached = intValue(usage["cache_read_input_tokens"])
	output = intValue(usage["output_tokens"])
	input = directInput + cacheCreation
	total = input + cached + output
	return input, cached, output, total
}

func claudeEntrypoint(claudeHome, sessionID string) string {
	path := filepath.Join(claudeHome, "debug", sessionID+".txt")
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		match := claudeEntrypointRE.FindStringSubmatch(scanner.Text())
		if len(match) == 2 {
			return strings.ToLower(strings.TrimSpace(match[1]))
		}
	}
	return ""
}

func claudeSourceForEntrypoint(entrypoint string) string {
	switch strings.ToLower(strings.TrimSpace(entrypoint)) {
	case "":
		return "claude-code:unknown"
	case "claude-vscode":
		return "claude-code:vscode"
	case "cli", "sdk-cli":
		return "claude-code:cli"
	default:
		return "claude-code:" + strings.ToLower(strings.TrimSpace(entrypoint))
	}
}

func sourceCategory(source string) string {
	if i := strings.Index(source, ":"); i >= 0 && i+1 < len(source) {
		return source[i+1:]
	}
	return source
}

func (r claudeRank) greaterOrEqual(other claudeRank) bool {
	if r.Total != other.Total {
		return r.Total > other.Total
	}
	if r.Output != other.Output {
		return r.Output > other.Output
	}
	return r.StartedAt >= other.StartedAt
}
