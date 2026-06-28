package modutui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTodoBlockRendersOutstandingTodos(t *testing.T) {
	lines := TodoBlock{Items: []TodoItem{
		{Content: "done", Status: "completed"},
		{Content: "active", Status: "in_progress"},
		{Content: "later", Status: "pending"},
	}}.RenderWidth(40)
	got := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"Todos", "☑ done", "☐ active", "☐ later"} {
		if !strings.Contains(got, want) {
			t.Fatalf("todo block missing %q:\n%s", want, got)
		}
	}
}

func TestTodoBlockHiddenWhenEmptyOrCompleted(t *testing.T) {
	if got := (TodoBlock{}).RenderWidth(40); len(got) != 0 {
		t.Fatalf("empty todos should not render: %#v", got)
	}
	if got := (TodoBlock{Items: []TodoItem{{Content: "done", Status: "completed"}}}).RenderWidth(40); len(got) != 0 {
		t.Fatalf("completed-only todos should not render: %#v", got)
	}
}

func TestTodoBlockTruncatesLongLists(t *testing.T) {
	items := make([]TodoItem, 4)
	for i := range items {
		items[i] = TodoItem{Content: "task", Status: "pending"}
	}
	lines := TodoBlock{Items: items, MaxRows: 2}.RenderWidth(40)
	got := ansi.Strip(strings.Join(lines, "\n"))
	if !strings.Contains(got, "... +2 more") {
		t.Fatalf("todo block should show overflow hint:\n%s", got)
	}
}
