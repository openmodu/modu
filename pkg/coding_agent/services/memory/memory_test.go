package memory

import (
	"os"
	"path/filepath"
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

func TestGetMemoryContextUsesSummaryWhenPresent(t *testing.T) {
	m := New(t.TempDir(), t.TempDir())
	_ = m.WriteGlobalLongTerm("full global")
	_ = m.WriteProjectLongTerm("full project")
	_ = m.WriteProjectSummary("project summary")

	ctx := m.GetMemoryContext()
	for _, want := range []string{"## Memory Summary", "project summary", "memo"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("context missing %q:\n%s", want, ctx)
		}
	}
	for _, unwanted := range []string{"full global", "full project", "Recent Daily Notes"} {
		if strings.Contains(ctx, unwanted) {
			t.Fatalf("summary context should not include %q:\n%s", unwanted, ctx)
		}
	}
}

func TestScopedMemoryContextsUseSummaryWhenPresent(t *testing.T) {
	m := New(t.TempDir(), t.TempDir())
	_ = m.WriteGlobalLongTerm("full global")
	_ = m.WriteProjectLongTerm("full project")
	_ = m.WriteGlobalSummary("global summary")
	_ = m.WriteProjectSummary("project summary")

	global := m.GetGlobalMemoryContext()
	if !strings.Contains(global, "## Global Memory Summary") || !strings.Contains(global, "global summary") || !strings.Contains(global, "memo") {
		t.Fatalf("global context missing summary content:\n%s", global)
	}
	if strings.Contains(global, "full global") {
		t.Fatalf("global summary context should not include raw memory:\n%s", global)
	}

	project := m.GetProjectMemoryContext()
	if !strings.Contains(project, "## Project Memory Summary") || !strings.Contains(project, "project summary") || !strings.Contains(project, "memo") {
		t.Fatalf("project context missing summary content:\n%s", project)
	}
	if strings.Contains(project, "full project") {
		t.Fatalf("project summary context should not include raw memory:\n%s", project)
	}
}

func TestScopedReadPathListReadSearch(t *testing.T) {
	m := New(t.TempDir(), t.TempDir())
	if err := os.MkdirAll(filepath.Join(m.projectDir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.projectDir, "notes", "one.md"), []byte("alpha\nneedle\nomega\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, truncated, err := m.List("project", "notes", 10)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(entries) != 1 || entries[0].Path != "notes/one.md" {
		t.Fatalf("unexpected entries truncated=%v entries=%+v", truncated, entries)
	}

	content, truncated, err := m.Read("project", "notes/one.md", 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if content != "needle" || !truncated {
		t.Fatalf("read content=%q truncated=%v", content, truncated)
	}

	matches, truncated, err := m.Search("project", "needle", "", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(matches) != 1 {
		t.Fatalf("unexpected search truncated=%v matches=%+v", truncated, matches)
	}
	if matches[0].Path != "notes/one.md" || matches[0].Line != 2 || !strings.Contains(matches[0].Content, "alpha\nneedle\nomega") {
		t.Fatalf("unexpected match: %+v", matches[0])
	}
}

func TestScopedReadPathTruncationRequiresMoreResults(t *testing.T) {
	m := New(t.TempDir(), t.TempDir())
	if err := os.WriteFile(filepath.Join(m.projectDir, "a.md"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.projectDir, "b.md"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, truncated, err := m.List("project", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(entries) != 2 {
		t.Fatalf("exact list limit should not be truncated: truncated=%v entries=%+v", truncated, entries)
	}

	entries, truncated, err = m.List("project", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(entries) != 1 {
		t.Fatalf("list over limit should be truncated: truncated=%v entries=%+v", truncated, entries)
	}

	matches, truncated, err := m.Search("project", "needle", "", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(matches) != 2 {
		t.Fatalf("exact search limit should not be truncated: truncated=%v matches=%+v", truncated, matches)
	}

	matches, truncated, err = m.Search("project", "needle", "", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(matches) != 1 {
		t.Fatalf("search over limit should be truncated: truncated=%v matches=%+v", truncated, matches)
	}
}

func TestScopedReadPathRejectsTraversal(t *testing.T) {
	m := New(t.TempDir(), t.TempDir())
	if _, _, err := m.List("project", "../outside", 10); err == nil {
		t.Fatal("expected parent traversal to fail")
	}
	if _, _, err := m.Read("project", ".hidden", 1, 1); err == nil {
		t.Fatal("expected hidden path to fail")
	}
}
