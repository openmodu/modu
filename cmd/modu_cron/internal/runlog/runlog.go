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
	"sort"
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

// Entry describes one finished run's log file.
type Entry struct {
	Name    string    // filename only (e.g. "2026-05-23T10-03-41Z.log")
	Path    string    // absolute path
	Size    int64     // bytes
	ModTime time.Time // last write time
}

// TaskDir returns the directory where logs for taskID live. The directory may
// not yet exist if the task has never run.
func (s *Store) TaskDir(taskID string) string {
	return filepath.Join(s.root, sanitize(taskID))
}

// List returns all log files for taskID, newest first. Missing directory is
// not an error — yields an empty slice so callers can treat "task never ran"
// uniformly with "task has no logs yet".
func (s *Store) List(taskID string) ([]Entry, error) {
	dir := s.TaskDir(taskID)
	infos, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	entries := make([]Entry, 0, len(infos))
	for _, di := range infos {
		if di.IsDir() || !strings.HasSuffix(di.Name(), ".log") {
			continue
		}
		fi, err := di.Info()
		if err != nil {
			continue
		}
		entries = append(entries, Entry{
			Name:    di.Name(),
			Path:    filepath.Join(dir, di.Name()),
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ModTime.After(entries[j].ModTime)
	})
	return entries, nil
}

// Resolve returns the absolute path for taskID's log named name, verifying it
// exists. Returns os.ErrNotExist if the file is missing.
func (s *Store) Resolve(taskID, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty filename")
	}
	// Block escapes like ".." or absolute paths — keep callers within the
	// task's own log directory.
	if name != filepath.Base(name) {
		return "", fmt.Errorf("invalid filename %q", name)
	}
	path := filepath.Join(s.TaskDir(taskID), name)
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
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
	// Filename includes nanoseconds so back-to-back Open calls (e.g. kill +
	// restart within the same second under OverlapKill) get distinct names.
	// Colons are replaced because they break Windows / some shells.
	stamp := strings.ReplaceAll(time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z"), ":", "-")
	name := stamp + ".log"
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
