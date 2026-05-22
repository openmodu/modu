package coding_agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/types"
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

func TestSystemPromptBuilderIncludesConnectedModel(t *testing.T) {
	cwd := t.TempDir()
	prompt := NewSystemPromptBuilder(cwd).
		SetModel(&types.Model{
			ID:         "mimo-v2.5-pro",
			Name:       "MiMo V2.5 Pro",
			ProviderID: "xiaomi-mimo",
		}).
		Build()

	if !strings.Contains(prompt, "- Connected model: xiaomi-mimo/mimo-v2.5-pro") {
		t.Fatalf("expected connected model in prompt, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "- Connected model display name: MiMo V2.5 Pro") {
		t.Fatalf("expected connected model display name in prompt, got:\n%s", prompt)
	}
}

func TestDefaultSystemPromptAllowsNonCodingTasks(t *testing.T) {
	prompt := NewSystemPromptBuilder(t.TempDir()).Build()

	for _, want := range []string{
		"you can also answer general questions and perform safe non-coding tasks",
		"Do not refuse solely because the task is not about code",
		"If the user asks for current facts such as weather",
		"safe one-off commands, including read-only commands that answer non-coding requests",
		"For coding or repository tasks, follow this sequence",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected default prompt to contain %q, got:\n%s", want, prompt)
		}
	}
}
