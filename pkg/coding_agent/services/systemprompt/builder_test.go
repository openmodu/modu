package systemprompt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/types"
)

type stubTool struct {
	name string
	desc string
}

func (t stubTool) Name() string        { return t.name }
func (t stubTool) Label() string       { return t.name }
func (t stubTool) Description() string { return t.desc }
func (t stubTool) Parameters() any     { return map[string]any{"type": "object"} }
func (t stubTool) Execute(context.Context, string, map[string]any, types.ToolUpdateCallback) (types.ToolResult, error) {
	return types.ToolResult{}, nil
}

type stubMemory string

func (m stubMemory) GetMemoryContext() string { return string(m) }

func TestBuilderDedupesAndTruncatesContext(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "AGENTS.md")
	content := strings.Repeat("a", maxContextFileBytes+512)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	prompt := NewBuilder(cwd).
		AddContextFile(path).
		Build()

	if strings.Count(prompt, "# Context: AGENTS.md") != 1 {
		t.Fatalf("expected deduped AGENTS.md context, got prompt:\n%s", prompt)
	}
	if !strings.Contains(prompt, "...[truncated for context budget]") {
		t.Fatalf("expected truncation marker in prompt, got:\n%s", prompt)
	}
}

func TestBuilderIncludesConnectedModel(t *testing.T) {
	cwd := t.TempDir()
	prompt := NewBuilder(cwd).
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
	prompt := NewBuilder(t.TempDir()).Build()

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

func TestBuilderCustomPromptReplacesDefault(t *testing.T) {
	prompt := NewBuilder(t.TempDir()).
		SetCustomPrompt("CUSTOM BASE PROMPT").
		Build()

	if !strings.Contains(prompt, "CUSTOM BASE PROMPT") {
		t.Fatalf("expected custom base prompt, got:\n%s", prompt)
	}
	if strings.Contains(prompt, "expert software engineer operating as a terminal assistant") {
		t.Fatalf("custom prompt should replace the default, got:\n%s", prompt)
	}
}

func TestBuilderIncludesToolDescriptions(t *testing.T) {
	prompt := NewBuilder(t.TempDir()).
		SetTools([]types.Tool{stubTool{name: "read", desc: "reads a file"}}).
		Build()

	if !strings.Contains(prompt, "# Available Tools") {
		t.Fatalf("expected tools header, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "## read") || !strings.Contains(prompt, "reads a file") {
		t.Fatalf("expected tool description, got:\n%s", prompt)
	}
}

func TestBuilderIncludesDynamicWorkflowGuidanceWhenWorkflowToolAvailable(t *testing.T) {
	prompt := NewBuilder(t.TempDir()).
		SetTools([]types.Tool{
			stubTool{name: "read", desc: "reads a file"},
			stubTool{name: "workflow", desc: "runs Lua workflows"},
		}).
		Build()

	for _, want := range []string{
		"# Dynamic Workflows",
		"When the `workflow` tool is available",
		"`ultracode`",
		"Write Lua, not JavaScript",
		"`meta`",
		"`parallel(..., { concurrency = N })`",
		"`/workflows`",
		"not a status or management API",
		"`action`",
		"`/workflows show <run-id>`",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected workflow prompt to contain %q, got:\n%s", want, prompt)
		}
	}

	withoutWorkflow := NewBuilder(t.TempDir()).
		SetTools([]types.Tool{stubTool{name: "read", desc: "reads a file"}}).
		Build()
	if strings.Contains(withoutWorkflow, "# Dynamic Workflows") {
		t.Fatalf("workflow guidance should only appear when workflow tool is available, got:\n%s", withoutWorkflow)
	}
}

func TestBuilderAppendsAndIncludesMemory(t *testing.T) {
	prompt := NewBuilder(t.TempDir()).
		AppendPrompt("EXTRA SETTINGS PROMPT").
		SetMemoryProvider(stubMemory("# Memory\nremembered fact")).
		Build()

	if !strings.Contains(prompt, "EXTRA SETTINGS PROMPT") {
		t.Fatalf("expected appended prompt, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "remembered fact") {
		t.Fatalf("expected memory context, got:\n%s", prompt)
	}
}

func TestBuilderTruncatesOversizedMemory(t *testing.T) {
	prompt := NewBuilder(t.TempDir()).
		SetMemoryProvider(stubMemory(strings.Repeat("m", maxMemoryContextBytes+1024))).
		Build()

	if !strings.Contains(prompt, "...[truncated for context budget] (memory context)") {
		t.Fatalf("expected memory truncation notice, got:\n%s", prompt)
	}
}

func TestBuilderAppendsModeBlocks(t *testing.T) {
	prompt := NewBuilder(t.TempDir()).
		SetModeBlocks([]string{UltracodeBlock, PlanModeBlock, WorktreeBlock("/tmp/wt")}).
		Build()

	if !strings.Contains(prompt, "## Active Mode: Ultracode") {
		t.Fatalf("expected ultracode mode block, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "## Active Mode: Plan") {
		t.Fatalf("expected plan mode block, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "isolated git worktree at: /tmp/wt") {
		t.Fatalf("expected worktree block, got:\n%s", prompt)
	}
}
