package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestToolGroupBlockCollapsedIsSingleLine(t *testing.T) {
	ctx := RenderContext{ContentWidth: 60}
	block := ToolGroupBlock{Calls: []ToolCall{
		{Name: "read", Summary: "Read a.txt", Done: true},
		{Name: "read", Summary: "Read b.txt", Done: true},
		{Name: "read", Summary: "Read c.txt", Done: true},
	}}
	lines := renderedTexts(block.Render(ctx))
	if len(lines) != 1 {
		t.Fatalf("collapsed group should be one line, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	got := ansi.Strip(lines[0])
	if !strings.Contains(got, "3 tools · parallel") {
		t.Fatalf("collapsed group missing header, got %q", got)
	}
	// Individual child summaries must not leak in the collapsed view.
	if strings.Contains(got, "a.txt") || strings.Contains(got, "└") {
		t.Fatalf("collapsed group leaked children: %q", got)
	}
}

func TestToolGroupBlockExpandedShowsChildren(t *testing.T) {
	ctx := RenderContext{ContentWidth: 60}
	block := ToolGroupBlock{Expanded: true, Calls: []ToolCall{
		{Name: "read", Input: "alpha.txt", Summary: "Read a.txt", Done: true, BatchSize: 3},
		{Name: "read", Input: "beta.txt", Summary: "Read b.txt", Done: true, BatchSize: 3},
		{Name: "grep", Input: "foo", Summary: "grep foo", Error: true, Done: true, BatchSize: 3},
	}}
	got := ansi.Strip(strings.Join(renderedTexts(block.Render(ctx)), "\n"))
	if !strings.Contains(got, "3 tools · parallel") || !strings.Contains(got, "1 error") {
		t.Fatalf("expanded header wrong:\n%s", got)
	}
	// Child rows show the invocation (which file each call hit), and the errored
	// child is flagged.
	for _, want := range []string{"Read(alpha.txt)", "Read(beta.txt)", "Grep(foo) · error"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expanded group missing child %q:\n%s", want, got)
		}
	}
	// The per-child "parallel N" tag must be suppressed inside the group.
	if strings.Contains(got, "parallel 3") {
		t.Fatalf("child rows should not repeat the parallel tag:\n%s", got)
	}
}

func TestBuildTranscriptFoldsBatchIntoOneLine(t *testing.T) {
	mv := NewModel(Options{Width: 80, Height: 24})
	m := &mv
	m.blockGap = 0
	batch := "batch-x"
	for _, call := range []ToolCall{
		{ID: "1", Name: "read", Summary: "Read a.txt", Done: true, BatchSize: 3, BatchID: batch},
		{ID: "2", Name: "read", Summary: "Read b.txt", Done: true, BatchSize: 3, BatchID: batch},
		{ID: "3", Name: "read", Summary: "Read c.txt", Done: true, BatchSize: 3, BatchID: batch},
	} {
		m.appendEntry(Entry{Role: RoleAssistant, Nodes: []Node{ToolNode{Call: call}}})
	}

	lines, _, _ := m.buildTranscript()
	joined := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "3 tools · parallel") {
		t.Fatalf("batch not folded:\n%s", joined)
	}
	if strings.Contains(joined, "Read b.txt") {
		t.Fatalf("collapsed batch leaked child rows:\n%s", joined)
	}

	// Toggling expansion reveals the children.
	if !m.toggleLatestToolExpansion() {
		t.Fatal("toggleLatestToolExpansion returned false")
	}
	lines, _, _ = m.buildTranscript()
	joined = ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"Read a.txt", "Read b.txt", "Read c.txt"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expanded batch missing %q:\n%s", want, joined)
		}
	}
}
