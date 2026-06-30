package memory

import (
	"context"
	"strings"
	"testing"

	memsvc "github.com/openmodu/modu/pkg/coding_agent/services/memory"
	"github.com/openmodu/modu/pkg/types"
)

func TestMemoryToolReadPathOperations(t *testing.T) {
	store := memsvc.New(t.TempDir(), t.TempDir())
	if err := store.WriteProjectLongTerm("alpha\nneedle\nomega\n"); err != nil {
		t.Fatal(err)
	}
	tool := NewMemoryTool(store)

	listRes, err := tool.Execute(context.Background(), "list", map[string]any{
		"operation": "list",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(listRes); !strings.Contains(text, "MEMORY.md") {
		t.Fatalf("list output missing MEMORY.md: %q", text)
	}
	listDetails := resultDetails(t, listRes)
	if listDetails["operation"] != "list" || listDetails["scope"] != "project" {
		t.Fatalf("unexpected list details: %#v", listDetails)
	}
	entries, ok := listDetails["entries"].([]memsvc.Entry)
	if !ok || len(entries) == 0 || entries[0].Path != "MEMORY.md" {
		t.Fatalf("expected structured list entries, got %#v", listDetails["entries"])
	}

	readRes, err := tool.Execute(context.Background(), "read", map[string]any{
		"operation":   "read",
		"path":        "MEMORY.md",
		"line_offset": 2,
		"max_lines":   1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(readRes); !strings.Contains(text, "needle") || strings.Contains(text, "alpha") {
		t.Fatalf("read output = %q", text)
	}
	readDetails := resultDetails(t, readRes)
	if readDetails["operation"] != "read" || readDetails["path"] != "MEMORY.md" || readDetails["truncated"] != true {
		t.Fatalf("unexpected read details: %#v", readDetails)
	}
	if !strings.Contains(readDetails["content"].(string), "needle") {
		t.Fatalf("expected read content detail to include needle, got %#v", readDetails["content"])
	}

	searchRes, err := tool.Execute(context.Background(), "search", map[string]any{
		"operation":     "search",
		"query":         "needle",
		"context_lines": 1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(searchRes); !strings.Contains(text, "MEMORY.md:2") || !strings.Contains(text, "alpha\nneedle\nomega") {
		t.Fatalf("search output = %q", text)
	}
	searchDetails := resultDetails(t, searchRes)
	if searchDetails["operation"] != "search" || searchDetails["query"] != "needle" {
		t.Fatalf("unexpected search details: %#v", searchDetails)
	}
	matches, ok := searchDetails["matches"].([]memsvc.SearchMatch)
	if !ok || len(matches) != 1 || matches[0].Path != "MEMORY.md" || matches[0].Line != 2 {
		t.Fatalf("expected structured search matches, got %#v", searchDetails["matches"])
	}
}

func TestMemoryToolReadRequiresPath(t *testing.T) {
	tool := NewMemoryTool(memsvc.New(t.TempDir(), t.TempDir()))
	res, err := tool.Execute(context.Background(), "read", map[string]any{
		"operation": "read",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(res); !strings.Contains(text, "path is required") {
		t.Fatalf("expected path error, got %q", text)
	}
}

func TestMemoryToolWriteSummary(t *testing.T) {
	store := memsvc.New(t.TempDir(), t.TempDir())
	if err := store.WriteProjectLongTerm("full project detail"); err != nil {
		t.Fatal(err)
	}
	tool := NewMemoryTool(store)

	res, err := tool.Execute(context.Background(), "summary", map[string]any{
		"operation": "write_summary",
		"content":   "project summary",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(res); !strings.Contains(text, "Successfully wrote project memory summary") {
		t.Fatalf("write_summary result = %q", text)
	}

	ctx := store.GetMemoryContext()
	if !strings.Contains(ctx, "project summary") {
		t.Fatalf("expected summary in memory context, got:\n%s", ctx)
	}
	if strings.Contains(ctx, "full project detail") {
		t.Fatalf("expected summary-first context to hide detail memory, got:\n%s", ctx)
	}
}

func TestMemoryToolWriteGlobalSummary(t *testing.T) {
	store := memsvc.New(t.TempDir(), t.TempDir())
	tool := NewMemoryTool(store)

	res, err := tool.Execute(context.Background(), "summary", map[string]any{
		"operation": "write_summary",
		"scope":     "global",
		"content":   "global summary",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := resultText(res); !strings.Contains(text, "Successfully wrote global memory summary") {
		t.Fatalf("write_summary result = %q", text)
	}
	if got := store.ReadGlobalSummary(); got != "global summary" {
		t.Fatalf("global summary = %q", got)
	}
}

func resultText(res types.ToolResult) string {
	var parts []string
	for _, block := range res.Content {
		if text, ok := block.(*types.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func resultDetails(t *testing.T, res types.ToolResult) map[string]any {
	t.Helper()
	details, ok := res.Details.(map[string]any)
	if !ok {
		t.Fatalf("expected map details, got %T", res.Details)
	}
	return details
}
