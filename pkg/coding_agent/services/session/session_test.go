package session

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestAppendSidecarDoesNotMoveLeaf(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}

	entry1 := NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "hello"})
	if err := mgr.Append(entry1); err != nil {
		t.Fatal(err)
	}
	sidecar := NewEntry(EntryTypeRuntimeState, "", map[string]any{"mode": "test"})
	if err := mgr.AppendSidecar(sidecar); err != nil {
		t.Fatal(err)
	}
	planSnapshot := NewEntry(EntryTypePlanSnapshot, "", map[string]any{"content": "plan"})
	if err := mgr.AppendSidecar(planSnapshot); err != nil {
		t.Fatal(err)
	}
	if got := mgr.LastID(); got != entry1.ID {
		t.Fatalf("sidecar moved leaf to %s, want %s", got, entry1.ID)
	}

	reloaded, err := NewManagerFromFile(mgr.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.LastID(); got != entry1.ID {
		t.Fatalf("reloaded sidecar moved leaf to %s, want %s", got, entry1.ID)
	}
	entry2 := NewEntry(EntryTypeMessage, "", MessageData{Role: "assistant", Content: "world"})
	if err := reloaded.Append(entry2); err != nil {
		t.Fatal(err)
	}
	entries := reloaded.Load()
	if got := entries[len(entries)-1].ParentID; got != entry1.ID {
		t.Fatalf("message parent = %s, want previous conversational leaf %s", got, entry1.ID)
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

func TestSessionManagerCreateBranchedSessionCopiesPathOnly(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	entry1 := NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "one"})
	if err := mgr.Append(entry1); err != nil {
		t.Fatal(err)
	}
	entry2 := NewEntry(EntryTypeMessage, "", MessageData{Role: "assistant", Content: "two"})
	if err := mgr.Append(entry2); err != nil {
		t.Fatal(err)
	}
	entry3 := NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "three"})
	if err := mgr.Append(entry3); err != nil {
		t.Fatal(err)
	}
	original := mgr.FilePath()

	branchedPath, err := mgr.CreateBranchedSession(entry2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if branchedPath == original {
		t.Fatalf("expected a new session file, got original %s", original)
	}
	entries := mgr.Load()
	if len(entries) != 2 {
		t.Fatalf("expected branched path to keep two entries, got %#v", entries)
	}
	if entries[0].ID != entry1.ID || entries[1].ID != entry2.ID {
		t.Fatalf("unexpected branched entries: %#v", entries)
	}
	header := mgr.Header()
	if header.ParentSession != original {
		t.Fatalf("expected parent session %q, got %#v", original, header)
	}
}

func TestSessionManagerListAllAndForkFrom(t *testing.T) {
	dir := t.TempDir()
	source, err := NewManager(dir, "/source/project")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Append(NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "source message"})); err != nil {
		t.Fatal(err)
	}
	if err := source.AppendSessionInfo("source name"); err != nil {
		t.Fatal(err)
	}

	forked, err := ForkFrom(dir, source.FilePath(), "/target/project")
	if err != nil {
		t.Fatal(err)
	}
	if forked.Header().ParentSession != source.FilePath() {
		t.Fatalf("expected parent source path, got %#v", forked.Header())
	}
	if forked.Header().Cwd != "/target/project" {
		t.Fatalf("expected target cwd, got %#v", forked.Header())
	}
	if got := forked.Load(); len(got) != len(source.Load()) {
		t.Fatalf("expected forked entries to be copied, got %#v", got)
	}

	infos, err := ListAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected source and forked sessions, got %#v", infos)
	}
}

func TestSessionDeleteValidatesPathAndHeader(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(NewEntry(EntryTypeMessage, "", MessageData{Role: "user", Content: "delete me"})); err != nil {
		t.Fatal(err)
	}
	path := mgr.FilePath()
	if err := Delete(dir, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected session file deleted, stat err=%v", err)
	}

	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"type":"session","id":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir, outside); err == nil {
		t.Fatal("expected outside session delete to be rejected")
	}

	invalid := filepath.Join(DefaultSessionDir(dir, "/test/project"), "invalid.jsonl")
	if err := os.MkdirAll(filepath.Dir(invalid), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte(`{"type":"not-session","id":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir, invalid); err == nil {
		t.Fatal("expected invalid session delete to be rejected")
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
