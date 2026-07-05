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

func TestSessionManagerFileIsNamedBySessionID(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}

	want := mgr.SessionID() + ".jsonl"
	if got := filepath.Base(mgr.FilePath()); got != want {
		t.Fatalf("session file name = %q, want %q", got, want)
	}
}

func TestSessionManagerFlushPersistsEmptySessionHeader(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewFreshManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Flush(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(mgr.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("empty flushed session should contain only header, got %d lines:\n%s", len(lines), string(data))
	}
	var header Header
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header.ID != mgr.SessionID() || header.Cwd != "/test/project" {
		t.Fatalf("unexpected flushed header: %#v", header)
	}

	infos, err := List(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].ID != mgr.SessionID() {
		t.Fatalf("expected flushed empty session to be listable, got %#v", infos)
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

func TestSessionManagerPersistsCompactionUserAnchorCount(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	entry := NewEntry(EntryTypeCompaction, "", CompactionData{
		Summary:               "compact",
		TokensBefore:          9000,
		OriginalCount:         10,
		NewCount:              4,
		PreservedUserMessages: 2,
		ReadFiles:             []string{"old.go"},
		ModifiedFiles:         []string{"new.go"},
	})
	if err := mgr.Append(entry); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(mgr.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected header plus compaction entry, got %d lines:\n%s", len(lines), string(data))
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["preservedUserMessages"] != float64(2) {
		t.Fatalf("expected preservedUserMessages in JSON, got %#v", raw)
	}
	if files, ok := raw["readFiles"].([]any); !ok || len(files) != 1 || files[0] != "old.go" {
		t.Fatalf("expected readFiles in JSON, got %#v", raw)
	}
	if files, ok := raw["modifiedFiles"].([]any); !ok || len(files) != 1 || files[0] != "new.go" {
		t.Fatalf("expected modifiedFiles in JSON, got %#v", raw)
	}

	reloaded, err := NewManager(dir, "/test/project")
	if err != nil {
		t.Fatal(err)
	}
	entries := reloaded.Load()
	if len(entries) != 1 {
		t.Fatalf("expected one reloaded entry, got %d", len(entries))
	}
	compaction, ok := entries[0].Data.(CompactionData)
	if !ok {
		t.Fatalf("expected reloaded compaction data, got %T", entries[0].Data)
	}
	if compaction.PreservedUserMessages != 2 {
		t.Fatalf("expected reloaded preserved user count 2, got %d", compaction.PreservedUserMessages)
	}
	if len(compaction.ReadFiles) != 1 || compaction.ReadFiles[0] != "old.go" {
		t.Fatalf("expected reloaded read files, got %#v", compaction.ReadFiles)
	}
	if len(compaction.ModifiedFiles) != 1 || compaction.ModifiedFiles[0] != "new.go" {
		t.Fatalf("expected reloaded modified files, got %#v", compaction.ModifiedFiles)
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

func TestFindByIDPrefix(t *testing.T) {
	dir := t.TempDir()
	sessionDir := DefaultSessionDir(dir, "/test/project")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"abc123.jsonl", "abd456.jsonl", "abc.jsonl"} {
		if err := os.WriteFile(filepath.Join(sessionDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	info, ok, err := FindByIDPrefix(dir, "/test/project", "abc123")
	if err != nil || !ok || info.ID != "abc123" {
		t.Fatalf("exact match: info=%#v ok=%v err=%v", info, ok, err)
	}
	// "abc" is a prefix of "abc123" but also an exact filename — exact wins.
	info, ok, err = FindByIDPrefix(dir, "/test/project", "abc")
	if err != nil || !ok || info.ID != "abc" {
		t.Fatalf("exact over prefix: info=%#v ok=%v err=%v", info, ok, err)
	}
	info, ok, err = FindByIDPrefix(dir, "/test/project", "abd")
	if err != nil || !ok || info.ID != "abd456" {
		t.Fatalf("unique prefix: info=%#v ok=%v err=%v", info, ok, err)
	}
	if _, _, err = FindByIDPrefix(dir, "/test/project", "ab"); err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
	if _, ok, err = FindByIDPrefix(dir, "/test/project", "zzz"); err != nil || ok {
		t.Fatalf("missing id: ok=%v err=%v", ok, err)
	}
	if _, ok, err = FindByIDPrefix(dir, "/other/cwd", "abc"); err != nil || ok {
		t.Fatalf("missing dir: ok=%v err=%v", ok, err)
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
