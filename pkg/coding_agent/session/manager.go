package session

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Manager manages session persistence using JSONL files.
type Manager struct {
	dir      string
	entries  []SessionEntry
	lastID   string
	filePath string
	mu       sync.RWMutex
}

// NewManager creates a new session manager.
// agentDir is typically ~/.coding_agent/
// projectPath is the project's working directory (used to derive a unique hash).
func NewManager(agentDir, projectPath string) (*Manager, error) {
	hash := hashProject(projectPath)
	dir := filepath.Join(agentDir, "sessions", hash)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	m := &Manager{
		dir:      dir,
		filePath: filepath.Join(dir, "session.jsonl"),
	}

	// Load existing entries
	if err := m.load(); err != nil {
		// Non-fatal: start fresh if load fails
		m.entries = nil
	}

	return m, nil
}

// NewManagerFromFile creates a manager from an existing session file path.
func NewManagerFromFile(filePath string) (*Manager, error) {
	m := &Manager{
		dir:      filepath.Dir(filePath),
		filePath: filePath,
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// Append adds a new entry to the session.
func (m *Manager) Append(entry SessionEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.ParentID == "" && m.lastID != "" {
		entry.ParentID = m.lastID
	}

	m.entries = append(m.entries, entry)
	m.lastID = entry.ID

	return m.appendToFile(entry)
}

// Load returns all session entries.
func (m *Manager) Load() []SessionEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]SessionEntry, len(m.entries))
	copy(result, m.entries)
	return result
}

// LastID returns the ID of the last entry.
func (m *Manager) LastID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastID
}

// Fork creates a new branch from the given entry ID.
func (m *Manager) Fork(entryID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Verify the entry exists
	found := false
	for _, e := range m.entries {
		if e.ID == entryID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("entry %s not found", entryID)
	}

	m.lastID = entryID
	return nil
}

// GetEntry returns a specific entry by ID.
func (m *Manager) GetEntry(id string) (SessionEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, e := range m.entries {
		if e.ID == id {
			return e, true
		}
	}
	return SessionEntry{}, false
}

// FilePath returns the session file path.
func (m *Manager) FilePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.filePath
}

// Clear removes all entries.
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = nil
	m.lastID = ""

	return os.Remove(m.filePath)
}

func (m *Manager) load() error {
	file, err := os.Open(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var entry SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // Skip malformed entries
		}
		m.entries = append(m.entries, entry)
		m.lastID = entry.ID
	}

	return scanner.Err()
}

func (m *Manager) appendToFile(entry SessionEntry) error {
	file, err := os.OpenFile(m.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open session file: %w", err)
	}
	defer file.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	data = append(data, '\n')
	_, err = file.Write(data)
	return err
}

func hashProject(projectPath string) string {
	h := sha256.Sum256([]byte(projectPath))
	return hex.EncodeToString(h[:8])
}
