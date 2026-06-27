package modutui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type SlashCommandBlock struct {
	Commands []SlashCommand
	Selected int
	MaxRows  int
}

func (b SlashCommandBlock) Render(ctx RenderContext) BlockRender {
	var out BlockRender
	lines := b.cardLines(ctx.ContentWidth)
	for _, line := range lines {
		out.Add(line, 0)
	}
	return out
}

func (b SlashCommandBlock) RenderWidth(width int) []string {
	return b.cardLines(width)
}

func (b SlashCommandBlock) cardLines(width int) []string {
	if len(b.Commands) == 0 {
		return nil
	}
	maxRows := b.MaxRows
	if maxRows <= 0 {
		maxRows = 8
	}
	total := len(b.Commands)
	selected := clamp(b.Selected, 0, total-1)
	start, end := windowRange(selected, total, maxRows)
	maxName := 0
	for _, item := range b.Commands[start:end] {
		if w := lipgloss.Width(item.Name); w > maxName {
			maxName = w
		}
	}
	body := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		item := b.Commands[i]
		marker := "  "
		style := dimStyle
		if i == selected {
			marker = botStyle.Render("› ")
			style = botStyle
		}
		pad := strings.Repeat(" ", maxName-lipgloss.Width(item.Name))
		line := marker + style.Render(item.Name) + pad
		if item.Description != "" {
			line += "  " + dimStyle.Render(item.Description)
		}
		body = append(body, line)
	}
	if total > maxRows {
		body = append(body, dimStyle.Render(fmt.Sprintf("  %d/%d", selected+1, total)))
	}
	return CardBlock{Lines: body, BorderStyle: cardBorderStyle}.RenderWidth(width)
}

func matchSlashCommands(input string, commands []SlashCommand) []SlashCommand {
	query := strings.ToLower(strings.TrimSpace(input))
	if !strings.HasPrefix(query, "/") || strings.ContainsAny(query, " \t\n\r") {
		return nil
	}
	matches := make([]SlashCommand, 0, len(commands))
	for _, cmd := range commands {
		name := strings.TrimSpace(cmd.Name)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		if strings.HasPrefix(strings.ToLower(name), query) {
			matches = append(matches, SlashCommand{Name: name, Description: cmd.Description})
		}
	}
	return matches
}

func windowRange(cursor, total, size int) (start, end int) {
	if total <= size {
		return 0, total
	}
	start = cursor - size/2
	if start < 0 {
		start = 0
	}
	end = start + size
	if end > total {
		end = total
		start = total - size
	}
	return start, end
}
