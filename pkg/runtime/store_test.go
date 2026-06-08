package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

func sampleCheckpoint(session string, seq int64) Checkpoint {
	return Checkpoint{
		Version:   checkpointVersion,
		SessionID: session,
		Seq:       seq,
		ParentSeq: seq - 1,
		Status:    types.SessionStatusRunning,
	}
}

func testStores(t *testing.T) map[string]Store {
	t.Helper()
	fs, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("file store: %v", err)
	}
	return map[string]Store{
		"memory": NewMemoryStore(),
		"file":   fs,
	}
}

func TestStoreAppendAndQuery(t *testing.T) {
	ctx := context.Background()
	for name, store := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Latest(ctx, "missing"); err != ErrNotFound {
				t.Fatalf("expected ErrNotFound, got %v", err)
			}
			for seq := range int64(3) {
				if err := store.Append(ctx, sampleCheckpoint("s1", seq)); err != nil {
					t.Fatalf("append: %v", err)
				}
			}

			latest, err := store.Latest(ctx, "s1")
			if err != nil || latest.Seq != 2 {
				t.Fatalf("latest seq = %d, err = %v", latest.Seq, err)
			}
			at, err := store.At(ctx, "s1", 1)
			if err != nil || at.Seq != 1 {
				t.Fatalf("at seq = %d, err = %v", at.Seq, err)
			}
			history, err := store.History(ctx, "s1")
			if err != nil || len(history) != 3 {
				t.Fatalf("history len = %d, err = %v", len(history), err)
			}
		})
	}
}

func TestFileStoreSkipsTornLine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("file store: %v", err)
	}
	if err := store.Append(ctx, sampleCheckpoint("s1", 0)); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Simulate a crash mid-write: a trailing, unterminated, invalid line.
	f, err := os.OpenFile(filepath.Join(dir, "s1.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.WriteString(`{"seq":1,"messa`)
	f.Close()

	latest, err := store.Latest(ctx, "s1")
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if latest.Seq != 0 {
		t.Fatalf("expected torn line skipped, latest seq = %d", latest.Seq)
	}
}
