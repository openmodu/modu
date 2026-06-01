package coding_agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimePathsDoesNotCreateRuntimeDirs(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "repo")

	paths := (&engine{
		agentDir: agentDir,
		cwd:      cwd,
	}).RuntimePaths()

	for _, path := range []string{
		paths.ToolResultsDir,
		paths.RuntimeDir,
		paths.AsyncSubagentRunsDir,
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be created lazily, stat err=%v", path, err)
		}
	}
}
