package coding_agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemPromptBuilderDedupesAndTruncatesContext(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "AGENTS.md")
	content := strings.Repeat("a", maxContextFileBytes+512)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	prompt := NewSystemPromptBuilder(cwd).
		AddContextFile(path).
		Build()

	if strings.Count(prompt, "# Context: AGENTS.md") != 1 {
		t.Fatalf("expected deduped AGENTS.md context, got prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "...[truncated for context budget]") {
		t.Fatalf("expected truncation marker in prompt, got:\n%s", prompt)
	}
}
