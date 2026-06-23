package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
)

// todoPanelMaxRows caps how many todo lines the panel shows so a long list
// can't push the input box off-screen. Overflow collapses to a "+N more" hint.
const todoPanelMaxRows = 12

var (
	uiTodoDone    = lipgloss.NewStyle().Foreground(uiDim).Strikethrough(true)
	uiTodoActive  = lipgloss.NewStyle().Foreground(uiPrimary).Bold(true)
	uiTodoPending = uiMutedText
)

// renderTodoPanel renders the session todo list as a compact, ordered checklist
// shown just above the input box — mirroring Claude Code's layout. Completed
// items get a checked, struck-through line; the single in_progress item is
// highlighted; pending items are dimmed.
//
// The panel represents *outstanding* work, so it returns "" when there are no
// todos or when every item is already completed. That keeps a finished list
// from lingering above the input across later turns — once the work is done the
// panel disappears instead of showing a stale, fully-checked list forever.
func (b *bubbleTUI) renderTodoPanel() string {
	if b.session == nil {
		return ""
	}
	todos := b.session.GetTodos()
	if !hasOutstandingTodo(todos) {
		return ""
	}

	var lines []string
	lines = append(lines, "  "+uiPrimaryText.Render("Todos"))
	for i, todo := range todos {
		if i >= todoPanelMaxRows {
			lines = append(lines, "  "+uiDimText.Render(fmt.Sprintf("… +%d more", len(todos)-i)))
			break
		}
		var glyph, content string
		switch todo.Status {
		case "completed":
			glyph = uiSuccessText.Render("☑")
			content = uiTodoDone.Render(todo.Content)
		case "in_progress":
			glyph = uiTodoActive.Render("☐")
			content = uiTodoActive.Render(todo.Content)
		default:
			glyph = uiTodoPending.Render("☐")
			content = uiTodoPending.Render(todo.Content)
		}
		lines = append(lines, "  "+glyph+" "+content)
	}
	return strings.Join(lines, "\n")
}

// hasOutstandingTodo reports whether any todo is still pending or in_progress.
// An empty list or an all-completed list counts as no outstanding work.
func hasOutstandingTodo(todos []coding_agent.TodoItem) bool {
	for _, todo := range todos {
		if todo.Status != "completed" {
			return true
		}
	}
	return false
}
