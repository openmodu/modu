// Package runlog manages per-task run log files.
//
// Layout: <root>/<task_id>/<RFC3339-timestamp>.log
// Default root: ~/.modu_cron/logs.
package runlog

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Store creates and opens log files under a root directory.
type Store struct {
	root string
}

// New returns a Store rooted at the given directory. Empty root falls back to
// ~/.modu_cron/logs.
func New(root string) *Store {
	if root == "" {
		root = DefaultRoot()
	}
	return &Store{root: root}
}

// DefaultRoot returns ~/.modu_cron/logs (or "./logs" if HOME is unset).
func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", "logs")
	}
	return filepath.Join(home, ".modu_cron", "logs")
}

// Open creates a new log file for taskID at the current time. The caller owns
// the returned Run and must Close it when done.
func (s *Store) Open(taskID string) (*Run, error) {
	if taskID == "" {
		return nil, fmt.Errorf("empty taskID")
	}
	dir := filepath.Join(s.root, sanitize(taskID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Filename: 2026-05-23T10-03-41Z.log (colons replaced for FS compatibility).
	name := strings.ReplaceAll(time.Now().UTC().Format(time.RFC3339), ":", "-") + ".log"
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Run{path: path, f: f}, nil
}

// Run is one task execution's log file. Safe for concurrent Write calls.
type Run struct {
	path string
	mu   sync.Mutex
	f    *os.File
}

// Path returns the on-disk path of the log file.
func (r *Run) Path() string { return r.path }

// Write appends bytes to the log file.
func (r *Run) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, io.ErrClosedPipe
	}
	return r.f.Write(p)
}

// Close flushes and closes the underlying file. Idempotent.
func (r *Run) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// sanitize keeps task IDs filesystem-safe.
func sanitize(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
