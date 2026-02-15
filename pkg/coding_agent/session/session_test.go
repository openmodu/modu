package session

import (
	"testing"
)

func TestSessionManager(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}

	// Append entries
	entry1 := NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "hello"})
	if err := mgr.Append(entry1); err != nil {
		t.Fatal(err)
	}

	entry2 := NewEntry(EntryTypeMessage, "", MessageData{Role: "assistant", Content: "world"})
	if err := mgr.Append(entry2); err != nil {
		t.Fatal(err)
	}

	// Check entries
	entries := mgr.Load()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].ID != entry1.ID {
		t.Fatal("first entry ID mismatch")
	}
	if entries[1].ParentID != entry1.ID {
		t.Fatal("second entry should have first as parent")
	}

	// Test LastID
	if mgr.LastID() != entry2.ID {
		t.Fatal("last ID should be entry2")
	}
}

func TestSessionManagerPersistence(t *testing.T) {
	dir := t.TempDir()

	// Create and populate
	mgr1, _ := NewManager(dir, "/test/project")
	entry := NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "test"})
	mgr1.Append(entry)

	// Reload from same directory
	mgr2, _ := NewManager(dir, "/test/project")
	entries := mgr2.Load()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after reload, got %d", len(entries))
	}

	if entries[0].ID != entry.ID {
		t.Fatal("persisted entry ID mismatch")
	}
}

func TestSessionManagerFork(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(dir, "/test/project")

	entry1 := NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "a"})
	mgr.Append(entry1)
	entry2 := NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "b"})
	mgr.Append(entry2)

	// Fork back to entry1
	if err := mgr.Fork(entry1.ID); err != nil {
		t.Fatal(err)
	}

	if mgr.LastID() != entry1.ID {
		t.Fatal("last ID should be entry1 after fork")
	}

	// Fork to nonexistent should fail
	if err := mgr.Fork("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestTree(t *testing.T) {
	dir := t.TempDir()
	mgr, _ := NewManager(dir, "/test/project")

	entry1 := NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "start"})
	mgr.Append(entry1)

	tree := NewTree(mgr)

	path := tree.GetPath(entry1.ID)
	if len(path) != 1 {
		t.Fatalf("expected path length 1, got %d", len(path))
	}

	currentPath := tree.GetCurrentPath()
	if len(currentPath) != 1 {
		t.Fatalf("expected current path length 1, got %d", len(currentPath))
	}
}
