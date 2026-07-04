package runlog

import (
	"testing"
	"time"
)

func TestDailyTokensEmptyLedger(t *testing.T) {
	s := New(t.TempDir())
	got, err := s.DailyTokens(time.Now())
	if err != nil {
		t.Fatalf("DailyTokens: %v", err)
	}
	if got != 0 {
		t.Fatalf("empty ledger returned %d, want 0", got)
	}
}

func TestAddDailyTokensAccumulates(t *testing.T) {
	s := New(t.TempDir())
	day := time.Date(2026, 7, 3, 12, 0, 0, 0, time.Local)
	if err := s.AddDailyTokens(day, 100); err != nil {
		t.Fatalf("AddDailyTokens: %v", err)
	}
	if err := s.AddDailyTokens(day, 250); err != nil {
		t.Fatalf("AddDailyTokens: %v", err)
	}
	if err := s.AddDailyTokens(day, 0); err != nil {
		t.Fatalf("AddDailyTokens zero: %v", err)
	}
	got, err := s.DailyTokens(day)
	if err != nil {
		t.Fatalf("DailyTokens: %v", err)
	}
	if got != 350 {
		t.Fatalf("DailyTokens=%d, want 350", got)
	}
	// A different day reads independently.
	other, err := s.DailyTokens(day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("DailyTokens other day: %v", err)
	}
	if other != 0 {
		t.Fatalf("other day=%d, want 0", other)
	}
}

func TestAddDailyTokensPrunesOldDays(t *testing.T) {
	s := New(t.TempDir())
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if err := s.AddDailyTokens(old, 42); err != nil {
		t.Fatalf("AddDailyTokens old: %v", err)
	}
	recent := old.AddDate(0, 6, 0)
	if err := s.AddDailyTokens(recent, 7); err != nil {
		t.Fatalf("AddDailyTokens recent: %v", err)
	}
	gotOld, err := s.DailyTokens(old)
	if err != nil {
		t.Fatalf("DailyTokens old: %v", err)
	}
	if gotOld != 0 {
		t.Fatalf("old day should be pruned, got %d", gotOld)
	}
	gotRecent, err := s.DailyTokens(recent)
	if err != nil {
		t.Fatalf("DailyTokens recent: %v", err)
	}
	if gotRecent != 7 {
		t.Fatalf("recent day=%d, want 7", gotRecent)
	}
}
