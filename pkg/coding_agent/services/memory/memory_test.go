package memory

import (
	"strings"
	"testing"
)

func TestLongTermRoundTrip(t *testing.T) {
	m := New(t.TempDir(), t.TempDir())

	if got := m.ReadLongTerm(); got != "" {
		t.Fatalf("expected empty project memory, got %q", got)
	}
	if err := m.WriteLongTerm("project fact"); err != nil {
		t.Fatal(err)
	}
	if got := m.ReadLongTerm(); got != "project fact" {
		t.Fatalf("project round-trip = %q", got)
	}

	if err := m.WriteGlobalLongTerm("global fact"); err != nil {
		t.Fatal(err)
	}
	if got := m.ReadGlobalLongTerm(); got != "global fact" {
		t.Fatalf("global round-trip = %q", got)
	}
}

func TestGetMemoryContextMergesScopes(t *testing.T) {
	m := New(t.TempDir(), t.TempDir())
	if m.GetMemoryContext() != "" {
		t.Fatal("empty store should yield empty context")
	}

	_ = m.WriteGlobalLongTerm("g")
	_ = m.WriteProjectLongTerm("p")
	_ = m.AppendToday("note today")

	ctx := m.GetMemoryContext()
	for _, want := range []string{"## Global Memory", "g", "## Project Memory", "p", "## Recent Daily Notes", "note today"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q:\n%s", want, ctx)
		}
	}
	// Global must appear before project.
	if strings.Index(ctx, "## Global Memory") > strings.Index(ctx, "## Project Memory") {
		t.Fatal("global memory should appear before project memory")
	}
}

func TestAppendTodayAccumulates(t *testing.T) {
	m := New(t.TempDir(), t.TempDir())
	_ = m.AppendToday("first")
	_ = m.AppendToday("second")
	notes := m.GetRecentDailyNotes(1)
	if !strings.Contains(notes, "first") || !strings.Contains(notes, "second") {
		t.Fatalf("expected both notes, got %q", notes)
	}
}
