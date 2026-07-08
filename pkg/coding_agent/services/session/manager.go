package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openmodu/modu/pkg/coding_agent/foundation/runtimepaths"
)

// Manager manages append-only session trees stored as pi-compatible JSONL.
type Manager struct {
	dir      string
	header   Header
	entries  []SessionEntry
	byID     map[string]SessionEntry
	labels   map[string]string
	leafID   string
	filePath string
	flushed  bool
	mu       sync.RWMutex
}

// SessionInfo is the lightweight session selector/listing view.
type SessionInfo struct {
	Path          string
	ID            string
	Cwd           string
	Name          string
	ParentSession string
	Created       time.Time
	Modified      time.Time
	MessageCount  int
	FirstMessage  string
}

// NewManager creates or continues the most recent session for projectPath.
// agentDir is typically ~/.modu/.
func NewManager(agentDir, projectPath string) (*Manager, error) {
	dir := DefaultSessionDir(agentDir, projectPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}
	if recent := FindMostRecentSession(dir); recent != "" {
		return NewManagerFromFile(recent)
	}
	return newManager(projectPath, dir, "", "")
}

// NewFreshManager creates a new session for projectPath, ignoring any existing
// sessions in that per-cwd session directory.
func NewFreshManager(agentDir, projectPath string) (*Manager, error) {
	dir := DefaultSessionDir(agentDir, projectPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}
	return newManager(projectPath, dir, "", "")
}

// NewManagerFromFile creates a manager from an existing session file path.
func NewManagerFromFile(filePath string) (*Manager, error) {
	m := &Manager{
		dir:      filepath.Dir(filePath),
		filePath: filePath,
		byID:     make(map[string]SessionEntry),
		labels:   make(map[string]string),
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func newManager(cwd, dir, id, parentSession string) (*Manager, error) {
	if id == "" {
		id = uuid.NewString()
	}
	ts := time.Now().UTC()
	m := &Manager{
		dir: dir,
		header: Header{
			Type:          "session",
			Version:       CurrentSessionVersion,
			ID:            id,
			Timestamp:     ts.Format(time.RFC3339Nano),
			Cwd:           cwd,
			ParentSession: parentSession,
		},
		byID:   make(map[string]SessionEntry),
		labels: make(map[string]string),
	}
	m.filePath = filepath.Join(dir, id+".jsonl")
	return m, nil
}

// DefaultSessionDir returns the pi-style per-cwd session directory.
func DefaultSessionDir(agentDir, cwd string) string {
	safe := strings.TrimLeft(cwd, string(filepath.Separator))
	if safe == "" {
		safe = "root"
	}
	replacer := strings.NewReplacer(
		string(filepath.Separator), "-",
		"\\", "-",
		":", "-",
	)
	return filepath.Join(agentDir, "sessions", "--"+replacer.Replace(safe)+"--")
}

// Append adds a new entry to the session as a child of the current leaf.
func (m *Manager) Append(entry SessionEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.ID == "" {
		entry.ID = generateID(func(id string) bool {
			_, ok := m.byID[id]
			return ok
		})
	}
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().UnixMilli()
	}
	if entry.ParentID == "" && m.leafID != "" {
		entry.ParentID = m.leafID
	}

	m.entries = append(m.entries, entry)
	m.byID[entry.ID] = entry
	m.leafID = entry.ID
	m.applyDerivedState(entry)
	return m.appendToFileLocked(entry)
}

// AppendSidecar adds an entry to the session file without moving the
// conversational leaf. Runtime snapshots use this so operational state lives
// with the session without becoming part of the branch path.
func (m *Manager) AppendSidecar(entry SessionEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry.ID == "" {
		entry.ID = generateID(func(id string) bool {
			_, ok := m.byID[id]
			return ok
		})
	}
	if entry.Timestamp == 0 {
		entry.Timestamp = time.Now().UnixMilli()
	}
	if entry.ParentID == "" {
		entry.ParentID = m.leafID
	}

	m.entries = append(m.entries, entry)
	m.byID[entry.ID] = entry
	m.applyDerivedState(entry)
	return m.appendToFileLocked(entry)
}

// Load returns all non-header session entries.
func (m *Manager) Load() []SessionEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]SessionEntry, len(m.entries))
	copy(result, m.entries)
	return result
}

// LastID returns the current leaf id.
func (m *Manager) LastID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.leafID
}

// SessionID returns the stable persisted session id.
func (m *Manager) SessionID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.header.ID
}

// Header returns the session header.
func (m *Manager) Header() Header {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.header
}

// Dir returns the session directory.
func (m *Manager) Dir() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dir
}

// Fork moves the current leaf to the given entry id.
func (m *Manager) Fork(entryID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[entryID]; !ok {
		return fmt.Errorf("entry %s not found", entryID)
	}
	m.leafID = entryID
	return nil
}

// ResetLeaf moves the current leaf to before the first entry.
func (m *Manager) ResetLeaf() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leafID = ""
}

// GetEntry returns a specific entry by ID.
func (m *Manager) GetEntry(id string) (SessionEntry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.byID[id]
	return entry, ok
}

// GetBranch returns the root-to-leaf path for entryID, or the current leaf.
func (m *Manager) GetBranch(entryID string) []SessionEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	start := entryID
	if start == "" {
		start = m.leafID
	}
	return m.branchLocked(start)
}

func (m *Manager) branchLocked(start string) []SessionEntry {
	if start == "" {
		return nil
	}
	var path []SessionEntry
	current, ok := m.byID[start]
	for ok {
		path = append([]SessionEntry{current}, path...)
		if current.ParentID == "" {
			break
		}
		current, ok = m.byID[current.ParentID]
	}
	return path
}

// GetLabel returns the latest label for an entry.
func (m *Manager) GetLabel(id string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[id]
}

// AppendLabelChange sets or clears a label for an entry.
func (m *Manager) AppendLabelChange(targetID, label string) error {
	if _, ok := m.GetEntry(targetID); !ok {
		return fmt.Errorf("entry %s not found", targetID)
	}
	return m.Append(NewEntry(EntryTypeLabel, "", LabelData{TargetID: targetID, Text: label}))
}

// BranchWithSummary moves the leaf to branchFromID and appends a branch summary.
func (m *Manager) BranchWithSummary(branchFromID, summary string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if branchFromID != "" {
		if _, ok := m.byID[branchFromID]; !ok {
			return "", fmt.Errorf("entry %s not found", branchFromID)
		}
	}
	m.leafID = branchFromID
	entry := NewEntry(EntryTypeBranchSummary, branchFromID, nil)
	entry.Data = BranchSummaryData{
		Summary: summary,
		FromID:  branchFromID,
		ToID:    entry.ID,
	}
	m.entries = append(m.entries, entry)
	m.byID[entry.ID] = entry
	m.leafID = entry.ID
	if err := m.appendToFileLocked(entry); err != nil {
		return "", err
	}
	return entry.ID, nil
}

// CreateBranchedSession writes a new session containing only the path to leafID.
func (m *Manager) CreateBranchedSession(leafID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	path := m.branchLocked(leafID)
	if len(path) == 0 {
		return "", fmt.Errorf("entry %s not found", leafID)
	}
	fresh, err := newManager(m.header.Cwd, m.dir, "", m.filePath)
	if err != nil {
		return "", err
	}
	fresh.entries = append([]SessionEntry(nil), path...)
	fresh.rebuildIndexLocked()
	if err := fresh.rewriteFileLocked(); err != nil {
		return "", err
	}
	m.header = fresh.header
	m.entries = fresh.entries
	m.byID = fresh.byID
	m.labels = fresh.labels
	m.leafID = fresh.leafID
	m.filePath = fresh.filePath
	m.flushed = true
	return m.filePath, nil
}

// AppendSessionInfo appends a display-name metadata entry.
func (m *Manager) AppendSessionInfo(name string) error {
	return m.Append(NewEntry(EntryTypeSessionInfo, "", SessionInfoData{Name: strings.TrimSpace(name)}))
}

// SessionName returns the latest display name.
func (m *Manager) SessionName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Type != EntryTypeSessionInfo {
			continue
		}
		if data, ok := m.entries[i].Data.(SessionInfoData); ok {
			return strings.TrimSpace(data.Name)
		}
	}
	return ""
}

// FilePath returns the session file path.
func (m *Manager) FilePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.filePath
}

// Flush ensures the session header exists on disk even when no entries have
// been appended yet.
func (m *Manager) Flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.flushed {
		return nil
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}
	file, err := os.OpenFile(m.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open session file: %w", err)
	}
	defer file.Close()
	if err := m.writeHeaderLocked(file); err != nil {
		return err
	}
	m.flushed = true
	return nil
}

// Clear removes the current session file and starts a fresh in-memory session.
func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldPath := m.filePath
	fresh, err := newManager(m.header.Cwd, m.dir, "", "")
	if err != nil {
		return err
	}
	m.header = fresh.header
	m.entries = nil
	m.byID = make(map[string]SessionEntry)
	m.labels = make(map[string]string)
	m.leafID = ""
	m.filePath = fresh.filePath
	m.flushed = false
	if oldPath == "" {
		return nil
	}
	err = os.Remove(oldPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (m *Manager) load() error {
	file, err := os.Open(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			fresh, freshErr := newManager("", filepath.Dir(m.filePath), "", "")
			if freshErr != nil {
				return freshErr
			}
			m.header = fresh.header
			m.header.Cwd = ""
			m.byID = make(map[string]SessionEntry)
			m.labels = make(map[string]string)
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		lineNo++
		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &peek); err != nil {
			continue
		}
		if lineNo == 1 && peek.Type == "session" {
			if err := json.Unmarshal(line, &m.header); err != nil {
				return err
			}
			continue
		}
		var entry SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		m.entries = append(m.entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if m.header.Type == "" {
		m.header = Header{
			Type:      "session",
			Version:   CurrentSessionVersion,
			ID:        uuid.NewString(),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Cwd:       "",
		}
	}
	m.rebuildIndexLocked()
	m.flushed = true
	return nil
}

func (m *Manager) rebuildIndexLocked() {
	m.byID = make(map[string]SessionEntry, len(m.entries))
	m.labels = make(map[string]string)
	m.leafID = ""
	for _, entry := range m.entries {
		m.byID[entry.ID] = entry
		if entryAffectsLeaf(entry) {
			m.leafID = entry.ID
		}
		m.applyDerivedState(entry)
	}
}

func entryAffectsLeaf(entry SessionEntry) bool {
	return entry.Type != EntryTypeRuntimeState && entry.Type != EntryTypePlanSnapshot
}

func (m *Manager) applyDerivedState(entry SessionEntry) {
	if entry.Type != EntryTypeLabel {
		return
	}
	data, ok := entry.Data.(LabelData)
	if !ok {
		return
	}
	if strings.TrimSpace(data.Text) == "" {
		delete(m.labels, data.TargetID)
	} else {
		m.labels[data.TargetID] = data.Text
	}
}

func (m *Manager) appendToFileLocked(entry SessionEntry) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}
	file, err := os.OpenFile(m.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open session file: %w", err)
	}
	defer file.Close()
	if !m.flushed {
		if err := m.writeHeaderLocked(file); err != nil {
			return err
		}
		m.flushed = true
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}
	_, err = file.Write(append(data, '\n'))
	return err
}

func (m *Manager) writeHeaderLocked(file *os.File) error {
	header, err := json.Marshal(m.header)
	if err != nil {
		return err
	}
	_, err = file.Write(append(header, '\n'))
	return err
}

func (m *Manager) rewriteFileLocked() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}
	file, err := os.OpenFile(m.filePath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to rewrite session file: %w", err)
	}
	defer file.Close()
	header, err := json.Marshal(m.header)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(header, '\n')); err != nil {
		return err
	}
	for _, entry := range m.entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	m.flushed = true
	return nil
}

// FindMostRecentSession returns the newest valid JSONL session in dir.
func FindMostRecentSession(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if !isValidSessionFile(path) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{path: path, mod: info.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mod.After(candidates[j].mod)
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].path
}

func isValidSessionFile(path string) bool {
	header, err := readSessionHeader(path)
	return err == nil && header.Type == "session" && header.ID != ""
}

func readSessionHeader(path string) (Header, error) {
	file, err := os.Open(path)
	if err != nil {
		return Header{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return Header{}, fmt.Errorf("session file is empty")
	}
	var header Header
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return Header{}, err
	}
	return header, nil
}

// List returns all sessions for cwd sorted by modified time descending.
func List(agentDir, cwd string) ([]SessionInfo, error) {
	dir := DefaultSessionDir(agentDir, cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SessionInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := BuildSessionInfo(filepath.Join(dir, entry.Name()))
		if err == nil && info.Path != "" {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Modified.After(out[j].Modified)
	})
	return out, nil
}

// ListAll returns sessions across all cwd-specific session directories.
func ListAll(agentDir string) ([]SessionInfo, error) {
	root := filepath.Join(agentDir, "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SessionInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".jsonl") {
				continue
			}
			info, err := BuildSessionInfo(filepath.Join(root, entry.Name(), file.Name()))
			if err == nil && info.Path != "" {
				out = append(out, info)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Modified.After(out[j].Modified)
	})
	return out, nil
}

// ForkFrom creates a new session in targetCwd by copying entries from sourcePath.
func ForkFrom(agentDir, sourcePath, targetCwd string) (*Manager, error) {
	source, err := NewManagerFromFile(sourcePath)
	if err != nil {
		return nil, err
	}
	dir := DefaultSessionDir(agentDir, targetCwd)
	fresh, err := newManager(targetCwd, dir, "", sourcePath)
	if err != nil {
		return nil, err
	}
	fresh.entries = source.Load()
	fresh.rebuildIndexLocked()
	if err := fresh.rewriteFileLocked(); err != nil {
		return nil, err
	}
	return fresh, nil
}

// Delete removes a persisted session file after validating it lives under
// agentDir/sessions and has a session header.
func Delete(agentDir, sessionPath string) error {
	if strings.TrimSpace(sessionPath) == "" {
		return fmt.Errorf("session path is required")
	}
	root, err := filepath.Abs(filepath.Join(agentDir, "sessions"))
	if err != nil {
		return err
	}
	path, err := filepath.Abs(sessionPath)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return fmt.Errorf("refusing to delete session outside sessions dir: %s", sessionPath)
	}
	if !strings.HasSuffix(path, ".jsonl") {
		return fmt.Errorf("refusing to delete non-jsonl session: %s", sessionPath)
	}
	header, err := readSessionHeader(path)
	if err != nil || header.Type != "session" || header.ID == "" {
		return fmt.Errorf("refusing to delete invalid session file: %s", sessionPath)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if err := os.RemoveAll(sessionToolResultsDir(agentDir, header)); err != nil {
		return err
	}
	return nil
}

func sessionToolResultsDir(agentDir string, header Header) string {
	return runtimepaths.SessionToolResultsDir(agentDir, header.Cwd, header.ID)
}

// BuildSessionInfo reads a session file summary.
// On-disk line shapes (see SessionEntry.MarshalJSON): the header is a
// struct marshal, so its line STARTS with {"type":"session",...}; entries
// are map marshals with alphabetically sorted keys, so their authentic
// "type" is always the LAST key and the line ENDS with "type":"<T>"}.
// Matching prefix/suffix is exact and O(1) per line — the same byte
// sequence inside a huge message payload can never sit at the line edge.
var (
	gateHeaderPrefix      = []byte(`{"type":"session",`)
	gateMessageSuffix     = []byte(`"type":"message"}`)
	gateSessionInfoSuffix = []byte(`"type":"session_info"}`)
)

// BuildSessionInfo summarizes one session file for listings and id lookup.
//
// It deliberately does NOT load the session through NewManagerFromFile:
// listings touch every session in a directory, and cron-driven sessions grow
// to tens of megabytes each — fully parsing every entry (the old approach)
// made a simple `--resume <id>` burn ~40s of CPU against a 2GB session dir.
// Instead this streams the file once and only JSON-decodes the handful of
// lines the summary needs (header, session_info names, the first user
// message); message lines are counted via the line-edge gate without
// decoding their payload.
func BuildSessionInfo(path string) (SessionInfo, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return SessionInfo{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer file.Close()

	info := SessionInfo{
		Path:     path,
		Modified: stat.ModTime(),
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	headerSeen := false
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		switch {
		case bytes.HasSuffix(line, gateMessageSuffix):
			info.MessageCount++
			if info.FirstMessage == "" {
				var entry struct {
					Message struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"message"`
				}
				if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "user" {
					var text string
					if json.Unmarshal(entry.Message.Content, &text) == nil && text != "" {
						info.FirstMessage = text
					}
				}
			}
		case bytes.HasSuffix(line, gateSessionInfoSuffix):
			var entry struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(line, &entry) == nil {
				// Last session_info entry wins, mirroring SessionName().
				info.Name = entry.Name
			}
		case !headerSeen && bytes.HasPrefix(line, gateHeaderPrefix):
			var header Header
			if json.Unmarshal(line, &header) == nil && header.Type == "session" {
				headerSeen = true
				info.ID = header.ID
				info.Cwd = header.Cwd
				info.ParentSession = header.ParentSession
				if created, err := time.Parse(time.RFC3339Nano, header.Timestamp); err == nil {
					info.Created = created
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return SessionInfo{}, err
	}
	if info.Created.IsZero() {
		info.Created = stat.ModTime()
	}
	if info.FirstMessage == "" {
		info.FirstMessage = "(no messages)"
	}
	return info, nil
}

// FindByIDPrefix resolves a session id (or unique prefix) to its file using
// the filename convention — every session is written as <id>.jsonl — so the
// lookup is a directory listing, never a content parse. Returns ok=false
// when nothing matches; ambiguous prefixes are an error.
func FindByIDPrefix(agentDir, cwd, id string) (SessionInfo, bool, error) {
	dir := DefaultSessionDir(agentDir, cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return SessionInfo{}, false, nil
		}
		return SessionInfo{}, false, err
	}
	match := ""
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".jsonl")
		if stem == id {
			match = entry.Name()
			break
		}
		if strings.HasPrefix(stem, id) {
			if match != "" {
				return SessionInfo{}, false, fmt.Errorf("ambiguous session id prefix %q", id)
			}
			match = entry.Name()
		}
	}
	if match == "" {
		return SessionInfo{}, false, nil
	}
	path := filepath.Join(dir, match)
	info := SessionInfo{Path: path, ID: strings.TrimSuffix(match, ".jsonl")}
	if stat, err := os.Stat(path); err == nil {
		info.Modified = stat.ModTime()
	}
	return info, true, nil
}
