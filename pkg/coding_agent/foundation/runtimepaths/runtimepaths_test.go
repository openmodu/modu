package runtimepaths

import (
	"path/filepath"
	"testing"
)

func TestSessionToolResultsDirUsesProjectToolResultsDir(t *testing.T) {
	agentDir := t.TempDir()
	cwd := "/test/project"
	sessionID := "session-1"

	got := SessionToolResultsDir(agentDir, cwd, sessionID)
	want := filepath.Join(ProjectToolResultsDir(agentDir, cwd), "sessions", sessionID)
	if got != want {
		t.Fatalf("SessionToolResultsDir = %q, want %q", got, want)
	}
}
