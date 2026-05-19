package session

import (
	"encoding/json"
	"os"
	"strings"
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

func TestSessionManagerWritesPiCompatibleHeaderAndEntries(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.Append(NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "hello"})); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(NewEntry(EntryTypeThinkingChange, "", ThinkingChangeData{Level: "high"})); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(mgr.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header plus two entries, got %d lines:\n%s", len(lines), string(data))
	}

	var header Header
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header.Type != "session" || header.Version != CurrentSessionVersion || header.ID == "" || header.Cwd != "/test/project" {
		t.Fatalf("unexpected header: %#v", header)
	}

	var msg map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &msg); err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "message" || msg["message"] == nil {
		t.Fatalf("expected pi-style message entry, got %#v", msg)
	}

	var thinking map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &thinking); err != nil {
		t.Fatal(err)
	}
	if thinking["type"] != "thinking_level_change" || thinking["thinkingLevel"] != "high" {
		t.Fatalf("expected pi-style thinking entry, got %#v", thinking)
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

func TestSessionManagerContinuesMostRecentSession(t *testing.T) {
	dir := t.TempDir()
	mgr1, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr1.Append(NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "first"})); err != nil {
		t.Fatal(err)
	}

	mgr2, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	if mgr2.FilePath() != mgr1.FilePath() {
		t.Fatalf("expected recent session %q, got %q", mgr1.FilePath(), mgr2.FilePath())
	}
	if got := mgr2.Load(); len(got) != 1 {
		t.Fatalf("expected one restored entry, got %#v", got)
	}
}

func TestSessionManagerListIncludesNameAndSortsByModified(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "hello list"})); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AppendSessionInfo("named session"); err != nil {
		t.Fatal(err)
	}

	infos, err := List(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected one session, got %#v", infos)
	}
	if infos[0].Name != "named session" || infos[0].FirstMessage != "hello list" || infos[0].ID == "" {
		t.Fatalf("unexpected session info: %#v", infos[0])
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
