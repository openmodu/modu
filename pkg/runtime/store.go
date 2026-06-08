package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ErrNotFound is returned when a session has no checkpoints.
var ErrNotFound = fmt.Errorf("runtime: session not found")

// Store is an append-only journal of checkpoints. Append-only storage keeps the
// full lineage of a session so rewinding to an earlier seq never loses history.
type Store interface {
	// Append durably records a checkpoint. Callers assign monotonically
	// increasing Seq values per session.
	Append(ctx context.Context, cp Checkpoint) error
	// Latest returns the most recently appended checkpoint for a session.
	Latest(ctx context.Context, sessionID string) (Checkpoint, error)
	// At returns the checkpoint with the given seq.
	At(ctx context.Context, sessionID string, seq int64) (Checkpoint, error)
	// History returns every checkpoint for a session in append order.
	History(ctx context.Context, sessionID string) ([]Checkpoint, error)
}

// MemoryStore is an in-process Store, useful for tests and ephemeral sessions.
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[string][]Checkpoint
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{sessions: map[string][]Checkpoint{}}
}

func (s *MemoryStore) Append(_ context.Context, cp Checkpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[cp.SessionID] = append(s.sessions[cp.SessionID], cp)
	return nil
}

func (s *MemoryStore) Latest(_ context.Context, sessionID string) (Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	history := s.sessions[sessionID]
	if len(history) == 0 {
		return Checkpoint{}, ErrNotFound
	}
	return history[len(history)-1], nil
}

func (s *MemoryStore) At(_ context.Context, sessionID string, seq int64) (Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.sessions[sessionID]) - 1; i >= 0; i-- {
		if s.sessions[sessionID][i].Seq == seq {
			return s.sessions[sessionID][i], nil
		}
	}
	return Checkpoint{}, ErrNotFound
}

func (s *MemoryStore) History(_ context.Context, sessionID string) ([]Checkpoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	history := s.sessions[sessionID]
	if len(history) == 0 {
		return nil, ErrNotFound
	}
	return append([]Checkpoint{}, history...), nil
}

// FileStore persists each session as an append-only JSONL file (one checkpoint
// per line) under Dir. Crash-safe by construction: a partially written trailing
// line is skipped on read, and earlier committed checkpoints remain intact.
type FileStore struct {
	mu  sync.Mutex
	Dir string
}

func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{Dir: dir}, nil
}

func (s *FileStore) path(sessionID string) string {
	return filepath.Join(s.Dir, sessionID+".jsonl")
}

func (s *FileStore) Append(_ context.Context, cp Checkpoint) error {
	line, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path(cp.SessionID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func (s *FileStore) History(_ context.Context, sessionID string) ([]Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readAll(sessionID)
}

func (s *FileStore) Latest(_ context.Context, sessionID string) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history, err := s.readAll(sessionID)
	if err != nil {
		return Checkpoint{}, err
	}
	return history[len(history)-1], nil
}

func (s *FileStore) At(_ context.Context, sessionID string, seq int64) (Checkpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history, err := s.readAll(sessionID)
	if err != nil {
		return Checkpoint{}, err
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Seq == seq {
			return history[i], nil
		}
	}
	return Checkpoint{}, ErrNotFound
}

// readAll parses every well-formed line. A trailing line that fails to parse
// (e.g. a torn write from a crash) is skipped rather than failing the read.
func (s *FileStore) readAll(sessionID string) ([]Checkpoint, error) {
	f, err := os.Open(s.path(sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer f.Close()

	var history []Checkpoint
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var cp Checkpoint
		if err := json.Unmarshal(line, &cp); err != nil {
			continue
		}
		history = append(history, cp)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return nil, ErrNotFound
	}
	sort.SliceStable(history, func(i, j int) bool { return history[i].Seq < history[j].Seq })
	return history, nil
}
