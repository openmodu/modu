package tokenkit

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	codexVersionRE       = regexp.MustCompile(`OpenAI Codex \(v([^)]+)\)`)
	codexPercentLeftRE   = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%\s+left`)
	codexContextWindowRE = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)%\s+left\s+\(([^/]+)/\s*([^)]+)\)`)
	codexTokenCountRE    = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)([KM]?)`)
)

func ParseCodexStatus(text string, capturedAt time.Time) CodexStatusSnapshot {
	if capturedAt.IsZero() {
		capturedAt = time.Now()
	}
	snapshot := CodexStatusSnapshot{
		CapturedAt: capturedAt,
		RawText:    text,
	}
	currentLimitModel := ""
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := cleanStatusLine(scanner.Text())
		if line == "" {
			continue
		}
		if match := codexVersionRE.FindStringSubmatch(line); len(match) == 2 {
			snapshot.Version = match[1]
			continue
		}
		if strings.Contains(line, "chatgpt.com/codex/settings/usage") {
			for _, field := range strings.Fields(line) {
				if strings.HasPrefix(field, "https://") {
					snapshot.UsageURL = field
					break
				}
			}
			continue
		}
		key, value, ok := splitStatusKeyValue(line)
		if !ok {
			continue
		}
		switch key {
		case "Model":
			snapshot.Model, snapshot.Reasoning, snapshot.Summaries = parseCodexModelValue(value)
			currentLimitModel = snapshot.Model
		case "Directory":
			snapshot.Directory = value
		case "Permissions":
			snapshot.Permissions = value
		case "Agents.md":
			snapshot.AgentsFile = value
		case "Account":
			snapshot.AccountEmail, snapshot.AccountPlan = parseCodexAccount(value)
		case "Collaboration mode":
			snapshot.CollaborationMode = value
		case "Session":
			snapshot.SessionID = value
		case "Context window":
			snapshot.ContextWindow = parseCodexContextWindow(value)
		case "Warning":
			snapshot.Warning = value
		default:
			if strings.HasSuffix(key, "limit") && value == "" {
				currentLimitModel = strings.TrimSpace(strings.TrimSuffix(key, "limit"))
				continue
			}
			if key == "5h limit" || key == "Weekly limit" {
				snapshot.Limits = append(snapshot.Limits, parseCodexLimit(currentLimitModel, key, value))
			}
		}
	}
	return snapshot
}

func (s *Store) SaveCodexStatus(ctx context.Context, snapshot CodexStatusSnapshot) error {
	if snapshot.CapturedAt.IsZero() {
		snapshot.CapturedAt = time.Now()
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO tokenkit_codex_status_snapshots (
	captured_at,
	account_email,
	account_plan,
	model,
	session_id,
	directory,
	context_percent_left,
	context_used_tokens,
	context_max_tokens,
	status_json,
	raw_text
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`,
		snapshot.CapturedAt.Format(time.RFC3339Nano),
		snapshot.AccountEmail,
		snapshot.AccountPlan,
		snapshot.Model,
		snapshot.SessionID,
		snapshot.Directory,
		snapshot.ContextWindow.PercentLeft,
		snapshot.ContextWindow.UsedTokens,
		snapshot.ContextWindow.MaxTokens,
		string(payload),
		snapshot.RawText,
	)
	return err
}

func (s *Store) LatestCodexStatus(ctx context.Context) (*CodexStatusSnapshot, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, status_json
FROM tokenkit_codex_status_snapshots
ORDER BY captured_at DESC, id DESC
LIMIT 1
`)
	var id int64
	var payload string
	if err := row.Scan(&id, &payload); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	var snapshot CodexStatusSnapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return nil, err
	}
	snapshot.ID = id
	return &snapshot, nil
}

func cleanStatusLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "│")
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, ">_ ")
	return strings.TrimSpace(line)
}

func splitStatusKeyValue(line string) (string, string, bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

func parseCodexModelValue(value string) (model string, reasoning string, summaries string) {
	model = strings.TrimSpace(value)
	if i := strings.Index(value, "("); i >= 0 {
		model = strings.TrimSpace(value[:i])
		details := strings.TrimSuffix(strings.TrimSpace(value[i+1:]), ")")
		for _, part := range strings.Split(details, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "reasoning ") {
				reasoning = strings.TrimSpace(strings.TrimPrefix(part, "reasoning "))
			}
			if strings.HasPrefix(part, "summaries ") {
				summaries = strings.TrimSpace(strings.TrimPrefix(part, "summaries "))
			}
		}
	}
	return model, reasoning, summaries
}

func parseCodexAccount(value string) (email string, plan string) {
	email = strings.TrimSpace(value)
	if i := strings.Index(value, "("); i >= 0 {
		email = strings.TrimSpace(value[:i])
		plan = strings.TrimSpace(strings.TrimSuffix(value[i+1:], ")"))
	}
	return email, plan
}

func parseCodexContextWindow(value string) CodexContextWindow {
	window := CodexContextWindow{Raw: value}
	match := codexContextWindowRE.FindStringSubmatch(value)
	if len(match) != 4 {
		window.PercentLeft = parsePercentLeft(value)
		return window
	}
	window.PercentLeft = parseFloat(match[1])
	window.UsedTokens = parseTokenCount(match[2])
	window.MaxTokens = parseTokenCount(match[3])
	return window
}

func parseCodexLimit(model, window, value string) CodexLimit {
	limit := CodexLimit{
		Model:       model,
		Window:      window,
		PercentLeft: parsePercentLeft(value),
		Raw:         value,
	}
	if i := strings.Index(value, "resets "); i >= 0 {
		limit.ResetRaw = strings.TrimSpace(value[i+len("resets "):])
	}
	return limit
}

func parsePercentLeft(value string) float64 {
	match := codexPercentLeftRE.FindStringSubmatch(value)
	if len(match) != 2 {
		return 0
	}
	return parseFloat(match[1])
}

func parseFloat(value string) float64 {
	out, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return out
}

func parseTokenCount(value string) int {
	value = strings.TrimSpace(strings.ToUpper(value))
	match := codexTokenCountRE.FindStringSubmatch(value)
	if len(match) == 3 {
		value = match[1] + match[2]
	}
	multiplier := 1.0
	switch {
	case strings.HasSuffix(value, "K"):
		multiplier = 1000
		value = strings.TrimSuffix(value, "K")
	case strings.HasSuffix(value, "M"):
		multiplier = 1000000
		value = strings.TrimSuffix(value, "M")
	}
	return int(parseFloat(value) * multiplier)
}
