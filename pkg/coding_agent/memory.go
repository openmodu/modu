package coding_agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MemoryStore manages persistent memory for the coding agent at two scopes:
//
//   - Global (~/.coding_agent/memory/): shared across all projects.
//   - Project (<cwd>/memory/):          specific to the current project.
//
// Both scopes store a MEMORY.md for long-term facts and
// daily notes under YYYYMM/YYYYMMDD.md.
type MemoryStore struct {
	globalDir  string // e.g. ~/.coding_agent/memory
	projectDir string // e.g. <cwd>/memory
}

// NewMemoryStore creates a MemoryStore backed by two directories.
// agentDir is the global config dir (e.g. ~/.coding_agent/);
// cwd is the current project directory.
func NewMemoryStore(agentDir, cwd string) *MemoryStore {
	globalDir := filepath.Join(agentDir, "memory")
	projectDir := filepath.Join(cwd, ".modu_code", "memory")
	os.MkdirAll(globalDir, 0o755)
	os.MkdirAll(projectDir, 0o755)
	return &MemoryStore{
		globalDir:  globalDir,
		projectDir: projectDir,
	}
}

// ── read ──────────────────────────────────────────────────────────────────────

// ReadLongTerm reads the project-scoped MEMORY.md (default scope for tools).
func (ms *MemoryStore) ReadLongTerm() string {
	return ms.ReadProjectLongTerm()
}

// ReadProjectLongTerm reads <cwd>/memory/MEMORY.md.
func (ms *MemoryStore) ReadProjectLongTerm() string {
	data, err := os.ReadFile(filepath.Join(ms.projectDir, "MEMORY.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

// ReadGlobalLongTerm reads ~/.coding_agent/memory/MEMORY.md.
func (ms *MemoryStore) ReadGlobalLongTerm() string {
	data, err := os.ReadFile(filepath.Join(ms.globalDir, "MEMORY.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

// ── write ─────────────────────────────────────────────────────────────────────

// WriteLongTerm writes to the project-scoped MEMORY.md (default scope for tools).
func (ms *MemoryStore) WriteLongTerm(content string) error {
	return ms.WriteProjectLongTerm(content)
}

// WriteProjectLongTerm overwrites <cwd>/memory/MEMORY.md.
func (ms *MemoryStore) WriteProjectLongTerm(content string) error {
	return os.WriteFile(filepath.Join(ms.projectDir, "MEMORY.md"), []byte(content), 0o600)
}

// WriteGlobalLongTerm overwrites ~/.coding_agent/memory/MEMORY.md.
func (ms *MemoryStore) WriteGlobalLongTerm(content string) error {
	return os.WriteFile(filepath.Join(ms.globalDir, "MEMORY.md"), []byte(content), 0o600)
}

// ── daily notes ───────────────────────────────────────────────────────────────

// AppendToday appends content to today's project-scoped daily note.
func (ms *MemoryStore) AppendToday(content string) error {
	return ms.appendTodayToDir(ms.projectDir, content)
}

func (ms *MemoryStore) appendTodayToDir(dir, content string) error {
	today := time.Now().Format("20060102")
	monthDir := today[:6]
	filePath := filepath.Join(dir, monthDir, today+".md")

	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}

	var existing string
	if data, err := os.ReadFile(filePath); err == nil {
		existing = string(data)
	}

	var newContent string
	if existing == "" {
		newContent = fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02")) + content
	} else {
		newContent = existing + "\n" + content
	}
	return os.WriteFile(filePath, []byte(newContent), 0o600)
}

// GetRecentDailyNotes returns daily notes from the last N days (project scope).
func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	return ms.recentDailyNotesFromDir(ms.projectDir, days)
}

func (ms *MemoryStore) recentDailyNotesFromDir(dir string, days int) string {
	var sb strings.Builder
	first := true
	for i := 0; i < days; i++ {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102")
		filePath := filepath.Join(dir, dateStr[:6], dateStr+".md")
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		if !first {
			sb.WriteString("\n\n---\n\n")
		}
		sb.Write(data)
		first = false
	}
	return sb.String()
}

// ── context for system prompt ─────────────────────────────────────────────────

// GetMemoryContext returns merged memory from both scopes for the system prompt.
// Global memory appears first; project memory is labelled separately.
func (ms *MemoryStore) GetMemoryContext() string {
	global := ms.ReadGlobalLongTerm()
	project := ms.ReadProjectLongTerm()
	recent := ms.GetRecentDailyNotes(3)

	if global == "" && project == "" && recent == "" {
		return ""
	}

	var sb strings.Builder

	if global != "" {
		sb.WriteString("## Global Memory\n\n")
		sb.WriteString(global)
	}

	if project != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString("## Project Memory\n\n")
		sb.WriteString(project)
	}

	if recent != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n---\n\n")
		}
		sb.WriteString("## Recent Daily Notes\n\n")
		sb.WriteString(recent)
	}

	return sb.String()
}
