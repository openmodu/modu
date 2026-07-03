package runlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// usageFileName is the per-day token ledger kept next to the task log dirs.
const usageFileName = "usage.json"

// usageRetainDays bounds the ledger so it never grows past a month of
// history; the daily-budget check only ever reads today's entry.
const usageRetainDays = 31

// usageMu serializes ledger read-modify-write cycles within this process.
// modu_cron runs as a single daemon, so a process-level mutex is enough —
// the same assumption crontools makes for tasks.yaml.
var usageMu sync.Mutex

// usageFile is the on-disk shape of the ledger: local-time day -> tokens.
type usageFile struct {
	Days map[string]int `json:"days"`
}

func usageDay(t time.Time) string {
	return t.Local().Format("2006-01-02")
}

func (s *Store) usagePath() string {
	return filepath.Join(s.root, usageFileName)
}

// DailyTokens returns the tokens recorded for day (local time). A missing
// ledger yields zero.
func (s *Store) DailyTokens(day time.Time) (int, error) {
	usageMu.Lock()
	defer usageMu.Unlock()
	file, err := s.readUsageLocked()
	if err != nil {
		return 0, err
	}
	return file.Days[usageDay(day)], nil
}

// AddDailyTokens adds tokens to day's total and prunes entries older than
// usageRetainDays. Zero or negative deltas are ignored.
func (s *Store) AddDailyTokens(day time.Time, tokens int) error {
	if tokens <= 0 {
		return nil
	}
	usageMu.Lock()
	defer usageMu.Unlock()
	file, err := s.readUsageLocked()
	if err != nil {
		return err
	}
	key := usageDay(day)
	file.Days[key] += tokens
	cutoff := usageDay(day.AddDate(0, 0, -usageRetainDays))
	for d := range file.Days {
		if d < cutoff {
			delete(file.Days, d)
		}
	}
	return s.writeUsageLocked(file)
}

func (s *Store) readUsageLocked() (usageFile, error) {
	file := usageFile{Days: map[string]int{}}
	data, err := os.ReadFile(s.usagePath())
	if err != nil {
		if os.IsNotExist(err) {
			return file, nil
		}
		return file, fmt.Errorf("read %s: %w", s.usagePath(), err)
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return file, fmt.Errorf("parse %s: %w", s.usagePath(), err)
	}
	if file.Days == nil {
		file.Days = map[string]int{}
	}
	return file, nil
}

func (s *Store) writeUsageLocked(file usageFile) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", s.root, err)
	}
	data, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("marshal usage: %w", err)
	}
	return os.WriteFile(s.usagePath(), data, 0o644)
}
