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
	if strings.TrimSpace(b.Call.Name) == "" && b.Call.Input == "" && b.Call.Output == "" && b.Call.Code == "" {
		block := b.CollapsibleBlock
		if permission != ToolPermissionUnknown {
			block.Summary += " · permission " + string(permission)
		}
		return block.Render(ctx)
	}

	summary := toolBlockSummary(b.Summary, b.Call)
	if permission != ToolPermissionUnknown {
		summary += " · permission " + string(permission)
	}

	out := BlockRender{}
	expanded := b.Expanded || b.Call.NoCollapse
	if !expanded {
		out.Add(toolExpandedLine(ctx.ContentWidth, "  "+summary), 0)
		return out
	}

	for _, line := range toolExpandedHeaderLines(ctx.ContentWidth, b.Call) {
		out.Add(line, 0)
	}
	for _, line := range toolOutputLines(ctx, b.Call) {
		out.Add(toolExpandedLine(ctx.ContentWidth, line), 0)
	}
	return out
}

func toolBlockSummary(summary string, call ToolCall) string {
	summary = strings.TrimSpace(summary)
	if summary != "" {
		return summary
	}
	if summary = strings.TrimSpace(call.Summary); summary != "" {
		return summary
	}
	if call.NoCollapse {
		return toolDisplayName(call.Name)
	}
	return "Ran " + toolDisplayName(call.Name)
}

func toolExpandedHeaderLines(width int, call ToolCall) []string {
	width = max(1, width)
	markerText := "⏺ "
	markerWidth := max(0, lipgloss.Width(markerText))
	continuation := toolHeaderContinuationPrefix()
	continuationWidth := lipgloss.Width(continuation)
	chunks := wrapToolHeader(toolInvocationLine(call), max(1, width-markerWidth), max(1, width-continuationWidth))
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	lines := make([]string, 0, len(chunks))
	marker := toolExpandedMarkerStyle.Render(markerText)
	lines = append(lines, marker+fitLine(dimStyle.Render(chunks[0]), max(1, width-markerWidth)))
	for _, chunk := range chunks[1:] {
		lines = append(lines, toolExpandedLine(width, continuation+chunk))
	}
	return lines
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

func toolOutputLines(ctx RenderContext, call ToolCall) []string {
	output := strings.TrimRight(call.Output, "\n")
	var lines []string
	if strings.TrimSpace(output) == "" {
		lines = append(lines, toolOutputBranchPrefix()+"no content data")
	} else {
		lines = append(lines, wrappedToolOutputLines(ctx.ContentWidth, output)...)
	}

	codeCtx := ctx
	codeCtx.ContentWidth = max(1, ctx.ContentWidth-toolOutputIndentWidth())
	for _, line := range toolCodeLines(codeCtx, call) {
		lines = append(lines, toolOutputIndent()+toolCodeLine(codeCtx.ContentWidth, line))
	}

	return lines
}

func toolOutputBranchPrefix() string { return "  └ " }

func toolOutputIndent() string { return "    " }

func toolOutputIndentWidth() int { return lipgloss.Width(toolOutputIndent()) }

func toolHeaderContinuationPrefix() string { return "  │ " }

func wrappedToolOutputLines(width int, output string) []string {
	width = max(1, width)
	branch := toolOutputBranchPrefix()
	indent := toolOutputIndent()
	branchWidth := lipgloss.Width(branch)
	indentWidth := lipgloss.Width(indent)
	branchContentWidth := max(1, width-branchWidth)
	indentContentWidth := max(1, width-indentWidth)

	var lines []string
	first := true
	for _, raw := range strings.Split(output, "\n") {
		contentWidth := indentContentWidth
		if first {
			contentWidth = branchContentWidth
		}
		chunks := wrapDisplayText(raw, contentWidth)
		if len(chunks) == 0 {
			chunks = []string{""}
		}
		for i, chunk := range chunks {
			prefix := indent
			if first && i == 0 {
				prefix = branch
			}
			lines = append(lines, prefix+chunk)
		}
		first = false
	}
	return lines
}

func wrapToolHeader(text string, firstWidth, continuationWidth int) []string {
	firstWidth = max(1, firstWidth)
	continuationWidth = max(1, continuationWidth)
	var out []string
	width := firstWidth
	for _, raw := range strings.Split(text, "\n") {
		if raw == "" {
			out = append(out, "")
			width = continuationWidth
			continue
		}
		remaining := raw
		for remaining != "" {
			var chunk string
			chunk, remaining = takeDisplayPrefix(remaining, width)
			out = append(out, chunk)
			width = continuationWidth
		}
	}
	return out
}

func wrapDisplayText(text string, width int) []string {
	width = max(1, width)
	var out []string
	remaining := text
	for remaining != "" {
		var chunk string
		chunk, remaining = takeDisplayPrefix(remaining, width)
		out = append(out, chunk)
	}
	return out
}

func takeDisplayPrefix(text string, width int) (string, string) {
	width = max(1, width)
	var b strings.Builder
	used := 0
	for i, r := range text {
		rw := ansi.StringWidth(string(r))
		if b.Len() > 0 && used+rw > width {
			return b.String(), text[i:]
		}
		b.WriteRune(r)
		used += rw
		if used >= width {
			next := i + utf8.RuneLen(r)
			if next < len(text) {
				return b.String(), text[next:]
			}
			return b.String(), ""
		}
	}
	return b.String(), ""
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
		out = append(out, renderToolDiffLine(width, line, lang)...)
	}
	return out
}

func renderToolDiffLine(width int, line, lang string) []string {
	switch {
	case strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "--- "):
		return wrapDiffSegments(highlightDiffCodeLine(line, lang), width, "52")
	case strings.HasPrefix(line, "+ ") && !strings.HasPrefix(line, "+++ "):
		return wrapDiffSegments(highlightDiffCodeLine(line, lang), width, "22")
	case strings.HasPrefix(line, "  "):
		return wrapDiffSegments(highlightDiffCodeLine(line, lang), width, "235")
	default:
		return wrapDiffSegments(dimStyle.Render(line), width, "")
	}
}

// wrapDiffSegments hard-wraps a single (possibly syntax-highlighted) diff line
// to width so long code wraps instead of being truncated, then pads each visual
// segment to the full width and paints the row background so wrapped
// continuations keep the same colored band. ansi.Hardwrap is ANSI- and
// wide-rune-aware, so it splits without corrupting the highlight escapes. An
// empty color skips the background (default/non-hunk lines).
func wrapDiffSegments(rendered string, width int, color string) []string {
	width = max(1, width)
	segments := strings.Split(ansi.Hardwrap(rendered, width, false), "\n")
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		fitted := fitLine(seg, width)
		if color != "" {
			fitted = ansiBackground(fitted, color)
		}
		out = append(out, fitted)
	}
	return out
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
