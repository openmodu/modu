package resource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadContextFilesIncludesNestedProjectContexts(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "repo")
	cwd := filepath.Join(project, "pkg", "feature")
	agentDir := filepath.Join(root, "agent")

	for _, dir := range []string{cwd, agentDir, filepath.Join(project, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(project, "AGENTS.md"), "root instructions")
	write(filepath.Join(project, "pkg", "AGENTS.md"), "pkg instructions")
	write(filepath.Join(cwd, "CLAUDE.md"), "feature instructions")
	write(filepath.Join(agentDir, "context.md"), "global context")

	loader := NewLoader(agentDir, cwd)
	files := loader.LoadContextFiles()
	if len(files) != 4 {
		t.Fatalf("expected 4 context files, got %d", len(files))
	}

	got := make(map[string]string, len(files))
	for _, file := range files {
		got[file.Name] = file.Content
	}

	if got[filepath.Join("..", "..", "AGENTS.md")] != "root instructions" {
		t.Fatalf("missing repo root context: %#v", got)
	}
	if got[filepath.Join("..", "AGENTS.md")] != "pkg instructions" {
		t.Fatalf("missing nested package context: %#v", got)
	}
	if got["CLAUDE.md"] != "feature instructions" {
		t.Fatalf("missing cwd context: %#v", got)
	}
	if got["global/context.md"] != "global context" {
		t.Fatalf("missing global context: %#v", got)
	}
}
