package modutui

import (
	"fmt"
	"strings"
)

const todoBlockMaxRows = 12

type TodoBlock struct {
	Items   []TodoItem
	MaxRows int
}

func (b TodoBlock) RenderWidth(width int) []string {
	if !hasOutstandingTodos(b.Items) {
		return nil
	}
	maxRows := b.MaxRows
	if maxRows <= 0 {
		maxRows = todoBlockMaxRows
	}
	lines := []string{botStyle.Render("Todos")}
	for i, item := range b.Items {
		if i >= maxRows {
			lines = append(lines, dimStyle.Render(fmt.Sprintf("... +%d more", len(b.Items)-i)))
			break
		}
		glyph := dimStyle.Render("☐")
		content := dimStyle.Render(item.Content)
		switch strings.TrimSpace(item.Status) {
		case "completed":
			glyph = toolExpandedMarkerStyle.Render("☑")
			content = dimStyle.Strikethrough(true).Render(item.Content)
		case "in_progress":
			glyph = botStyle.Render("☐")
			content = botStyle.Render(item.Content)
		}
		lines = append(lines, glyph+" "+content)
	}
	return CardBlock{Lines: lines}.RenderWidth(width)
}

func hasOutstandingTodos(items []TodoItem) bool {
	for _, item := range items {
		if strings.TrimSpace(item.Status) != "completed" {
			return true
		}
	}
	return false
}
