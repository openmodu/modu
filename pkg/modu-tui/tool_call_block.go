package modutui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

type ToolCallBlock struct {
	CollapsibleBlock
	Call       ToolCall
	Permission ToolPermissionState
}

func (b ToolCallBlock) Render(ctx RenderContext) BlockRender {
	permission := b.Permission
	if permission == ToolPermissionUnknown && ctx.Hooks.ToolPermission != nil {
		permission = ctx.Hooks.ToolPermission(b.Call)
	}
	if b.Call.Input == "" && b.Call.Output == "" {
		block := b.CollapsibleBlock
		if permission != ToolPermissionUnknown {
			block.Summary += " · permission " + string(permission)
		}
		return block.Render(ctx)
	}

	summary := strings.TrimSpace(b.Summary)
	if summary == "" {
		summary = b.Call.Summary
	}
	if summary == "" {
		summary = "Ran " + toolDisplayName(b.Call.Name)
	}
	if permission != ToolPermissionUnknown {
		summary += " · permission " + string(permission)
	}

	out := BlockRender{}
	if !b.Expanded {
		out.Add(dimStyle.Render("  "+summary), 0)
		return out
	}

	out.Add(toolExpandedHeaderLine(ctx.ContentWidth, b.Call), 0)
	for _, line := range toolDetailLines(b.Call) {
		out.Add(toolExpandedLine(ctx.ContentWidth, "  "+line), 0)
	}
	return out
}

func toolExpandedHeaderLine(width int, call ToolCall) string {
	width = max(1, width)
	markerText := "⏺ "
	markerWidth := max(0, lipgloss.Width(markerText))
	marker := toolExpandedMarkerStyle.
		Background(toolExpandedStyle.GetBackground()).
		Render(markerText)
	rest := toolExpandedStyle.Width(max(1, width-markerWidth)).Render(toolInvocationLine(call))
	return marker + rest
}

func toolExpandedLine(width int, text string) string {
	return toolExpandedStyle.Width(max(1, width)).Render(text)
}

func toolInvocationLine(call ToolCall) string {
	name := toolDisplayName(call.Name)
	input := strings.TrimSpace(call.Input)
	if input == "" {
		input = strings.TrimSpace(call.Detail)
	}
	if input == "" {
		return name
	}
	return name + "(" + input + ")"
}

func toolDetailLines(call ToolCall) []string {
	var lines []string
	output := strings.TrimRight(call.Output, "\n")
	if output != "" {
		lines = append(lines, strings.Split(output, "\n")...)
	}
	return lines
}

func toolDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Tool"
	}
	if strings.EqualFold(name, "bash") {
		return "Bash"
	}
	r, size := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError && size == 0 {
		return "Tool"
	}
	return string(unicode.ToUpper(r)) + name[size:]
}
