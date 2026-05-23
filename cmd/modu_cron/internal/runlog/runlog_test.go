package runlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
