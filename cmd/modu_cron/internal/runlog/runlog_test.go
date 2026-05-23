package runlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCreatesFileAndWrites(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	run, err := store.Open("task1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := run.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !strings.HasPrefix(run.Path(), filepath.Join(root, "task1")) {
		t.Errorf("path %q should be under %s/task1", run.Path(), root)
	}
	data, err := os.ReadFile(run.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello\n" {
		t.Errorf("content = %q, want %q", data, "hello\n")
	}
}

func TestOpenRejectsEmptyID(t *testing.T) {
	_, err := New(t.TempDir()).Open("")
	if err == nil {
		t.Fatal("expected error for empty taskID")
	}
}

func TestSanitizeKeepsPathFSSafe(t *testing.T) {
	root := t.TempDir()
	store := New(root)
	run, err := store.Open("weird/id with spaces!")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer run.Close()
	if strings.ContainsAny(filepath.Base(filepath.Dir(run.Path())), `/\ !`) {
		t.Errorf("sanitized dir still has unsafe chars: %s", run.Path())
	}
}

func TestListReturnsNewestFirst(t *testing.T) {
	store := New(t.TempDir())

	// Open three runs in succession; the runlog filename embeds the time so
	// later opens get distinct names. Bump mtime explicitly to be robust
	// against fast clocks that produce identical RFC3339 stamps within the
	// same second.
	mkrun := func(when time.Time, body string) string {
		run, err := store.Open("t")
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if _, err := run.Write([]byte(body)); err != nil {
			t.Fatalf("Write: %v", err)
		}
		_ = run.Close()
		_ = os.Chtimes(run.Path(), when, when)
		return run.Path()
	}
	base := time.Now()
	oldest := mkrun(base.Add(-2*time.Hour), "a")
	middle := mkrun(base.Add(-1*time.Hour), "bb")
	newest := mkrun(base, "ccc")

	entries, err := store.List("t")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Path != newest || entries[2].Path != oldest {
		t.Errorf("order wrong: got %v %v %v", entries[0].Path, entries[1].Path, entries[2].Path)
	}
	if entries[1].Path != middle {
		t.Errorf("middle entry = %v, want %v", entries[1].Path, middle)
	}
	if entries[2].Size != 1 || entries[0].Size != 3 {
		t.Errorf("size mismatch: %+v", entries)
	}
}

func TestListMissingDirIsEmpty(t *testing.T) {
	entries, err := New(t.TempDir()).List("never-ran")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty, got %d", len(entries))
	}
}

func TestResolveRejectsTraversal(t *testing.T) {
	store := New(t.TempDir())
	if _, err := store.Resolve("t", "../escape.log"); err == nil {
		t.Error("expected error for path traversal")
	}
	if _, err := store.Resolve("t", "sub/file.log"); err == nil {
		t.Error("expected error for nested path")
	}
	if _, err := store.Resolve("t", ""); err == nil {
		t.Error("expected error for empty name")
	}
}

func TestResolveReturnsMissing(t *testing.T) {
	store := New(t.TempDir())
	_, err := store.Resolve("t", "nonexistent.log")
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got: %v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	run, err := New(t.TempDir()).Open("t")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("second Close should be nil, got: %v", err)
	}
}
