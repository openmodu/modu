package contextmgr

import (
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/coding_agent/resource"
)

func TestExtractToolPathsDedupesAcrossArgsAndResult(t *testing.T) {
	event := agent.Event{
		Result: agent.ToolResult{Details: map[string]any{
			"matched_paths": []string{"a.go", "b.go"},
		}},
		Args: map[string]any{"path": "a.go"},
	}
	paths := extractToolPaths(event)
	if len(paths) != 2 {
		t.Fatalf("expected 2 deduped paths, got %v", paths)
	}
	seen := strings.Join(paths, ",")
	if !strings.Contains(seen, "a.go") || !strings.Contains(seen, "b.go") {
		t.Fatalf("expected a.go and b.go, got %v", paths)
	}
}

func TestExtractToolPathsHandlesAnySliceAndBlanks(t *testing.T) {
	event := agent.Event{
		Result: agent.ToolResult{Details: map[string]any{
			"matched_paths": []any{"x.go", "", 42},
			"path":          "  ",
		}},
	}
	paths := extractToolPaths(event)
	if len(paths) != 1 || paths[0] != "x.go" {
		t.Fatalf("expected only x.go, got %v", paths)
	}
}

func TestFormatNestedContextEmptyWhenNoFiles(t *testing.T) {
	if got := formatNestedContext([]string{"a.go"}, nil); got != "" {
		t.Fatalf("expected empty string for no files, got %q", got)
	}
}

func TestFormatNestedContextIncludesTargetsAndContent(t *testing.T) {
	out := formatNestedContext(
		[]string{"pkg/main.go"},
		[]resource.ContextFile{{Name: "AGENTS.md", Path: "/x/AGENTS.md", Content: "rule one"}},
	)
	if !strings.Contains(out, "- pkg/main.go") {
		t.Fatalf("expected target path listed, got:\n%s", out)
	}
	if !strings.Contains(out, "# Path Context: AGENTS.md") || !strings.Contains(out, "rule one") {
		t.Fatalf("expected file content block, got:\n%s", out)
	}
}

func TestFormatNestedContextTruncatesOverBudget(t *testing.T) {
	big := strings.Repeat("z", nestedContextMaxFileBytes+1024)
	out := formatNestedContext(
		[]string{"main.go"},
		[]resource.ContextFile{{Name: "big.md", Path: "/x/big.md", Content: big}},
	)
	if !strings.Contains(out, "...[truncated for context budget]") {
		t.Fatalf("expected truncation marker, got len=%d", len(out))
	}
}

func TestTruncateWithNotice(t *testing.T) {
	if got := truncateWithNotice("short", 100, "lbl"); got != "short" {
		t.Fatalf("under-budget content should be unchanged, got %q", got)
	}
	got := truncateWithNotice(strings.Repeat("a", 200), 100, "lbl")
	if !strings.Contains(got, "...[truncated for context budget] (lbl)") {
		t.Fatalf("expected labelled truncation notice, got %q", got)
	}
	if len(got) > 100+len(" (lbl)") {
		t.Fatalf("truncated content exceeded budget: len=%d", len(got))
	}
}
