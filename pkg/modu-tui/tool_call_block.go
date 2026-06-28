package modutui

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2/quick"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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
	expanded := b.Expanded || b.Call.NoCollapse
	if !expanded {
		out.Add(dimStyle.Render("  "+summary), 0)
		return out
	}

	out.Add(toolExpandedHeaderLine(ctx.ContentWidth, b.Call), 0)
	for _, line := range toolDetailLines(b.Call) {
		out.Add(toolExpandedLine(ctx.ContentWidth, toolDetailLinePrefix(b.Call)+line), 0)
	}
	for _, line := range toolCodeLines(ctx, b.Call) {
		out.Add(toolCodeLine(ctx.ContentWidth, line), 0)
	}
	return out
}

func toolDetailLinePrefix(call ToolCall) string {
	if call.NoCollapse {
		return "  └ "
	}
	return "  "
}

func toolExpandedHeaderLine(width int, call ToolCall) string {
	width = max(1, width)
	markerText := "⏺ "
	markerWidth := max(0, lipgloss.Width(markerText))
	marker := toolExpandedMarkerStyle.Render(markerText)
	rest := fitLine(dimStyle.Render(toolInvocationLine(call)), max(1, width-markerWidth))
	return marker + rest
}

func toolExpandedLine(width int, text string) string {
	return fitLine(dimStyle.Render(text), max(1, width))
}

func toolCodeLine(width int, text string) string {
	if ansi.StringWidth(text) == max(1, width) {
		return text
	}
	return fitLine(text, max(1, width))
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

func toolCodeLines(ctx RenderContext, call ToolCall) []string {
	code := strings.TrimRight(call.Code, "\n")
	if code == "" {
		return nil
	}
	lang := strings.TrimSpace(call.Language)
	if strings.EqualFold(lang, "diff") {
		return toolDiffCodeLines(ctx.ContentWidth, code, call.Input)
	}
	fence := "```" + lang + "\n" + code + "\n```"
	body := fence
	if ctx.Markdown != nil {
		if out, err := ctx.Markdown.Render(fence); err == nil {
			body = strings.Trim(out, "\n")
		}
	}
	return strings.Split(body, "\n")
}

func toolDiffCodeLines(width int, code, input string) []string {
	width = max(1, width)
	lang := toolDiffSourceLanguage(input)
	rawLines := strings.Split(strings.TrimRight(code, "\n"), "\n")
	out := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		out = append(out, indentToolDiffLine(width, renderToolDiffLine(max(1, width-toolDiffIndentWidth()), line, lang)))
	}
	return out
}

func indentToolDiffLine(width int, line string) string {
	indent := toolDiffIndent()
	if width <= lipgloss.Width(indent) {
		return fitLine(indent, width)
	}
	return indent + line
}

func toolDiffIndent() string { return "    " }

func toolDiffIndentWidth() int { return lipgloss.Width(toolDiffIndent()) }

func renderToolDiffLine(width int, line, lang string) string {
	switch {
	case strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "--- "):
		return ansiBackground(fitLine(highlightDiffCodeLine(line, lang), width), "52")
	case strings.HasPrefix(line, "+ ") && !strings.HasPrefix(line, "+++ "):
		return ansiBackground(fitLine(highlightDiffCodeLine(line, lang), width), "22")
	case strings.HasPrefix(line, "  "):
		return ansiBackground(fitLine(highlightDiffCodeLine(line, lang), width), "235")
	default:
		return fitLine(dimStyle.Render(line), width)
	}
}

func ansiBackground(line, color string) string {
	bg := "\x1b[48;5;" + color + "m"
	return bg + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+bg) + "\x1b[0m"
}

var toolDiffNumberedLineRE = regexp.MustCompile(`^([ +-]\s+\d+\s\s)(.*)$`)

func highlightDiffCodeLine(line, lang string) string {
	if strings.TrimSpace(lang) == "" {
		return line
	}
	matches := toolDiffNumberedLineRE.FindStringSubmatch(line)
	if len(matches) != 3 {
		return line
	}
	code := highlightCodeFragment(matches[2], lang)
	return matches[1] + code
}

func highlightCodeFragment(code, lang string) string {
	if strings.TrimSpace(code) == "" {
		return code
	}
	var buf bytes.Buffer
	if err := quick.Highlight(&buf, code, lang, "terminal256", "monokai"); err != nil {
		return code
	}
	return strings.TrimRight(buf.String(), "\n")
}

func toolDiffSourceLanguage(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if idx := strings.Index(input, " · "); idx >= 0 {
		input = input[:idx]
	}
	if idx := strings.Index(input, "\n"); idx >= 0 {
		input = input[:idx]
	}
	input = strings.Trim(input, `"'`)
	switch strings.ToLower(filepath.Ext(input)) {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".jsx":
		return "jsx"
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "markdown"
	case ".py":
		return "python"
	case ".sh", ".bash", ".zsh":
		return "bash"
	case ".yaml", ".yml":
		return "yaml"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".sql":
		return "sql"
	default:
		return ""
	}
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
