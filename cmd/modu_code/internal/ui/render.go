package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/reflow/wrap"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/types"
)

var uiANSIPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// dotPadW is the visual cell-width of "● " (● may be 2 cells in CJK terminals).
var dotPadW = lipgloss.Width("● ")

// hookStr is the raw connector string: 2 spaces + ⎿ + 1 space.
const hookStr = "  ⎿ "

// hookPadW is the visual width of hookStr.
var hookPadW = lipgloss.Width(hookStr)

// dotPad aligns continuation lines to the widest of the two prefixes.
var dotPad = strings.Repeat(" ", max(dotPadW, hookPadW))

// hookPad renders the ⎿ connector at fixed width.
var hookPad = uiDimText.Render(hookStr)

// assistantPad keeps assistant continuation lines aligned with the first
// content character after the leading "● ".
var assistantPad = strings.Repeat(" ", dotPadW)

// ─── View ────────────────────────────────────────

func (m *uiModel) View() string {
	if !m.ready {
		return "  " + m.spinner.View() + " loading modu_code..."
	}

	footer := m.renderFooter()
	if footer == "" {
		return m.viewport.View()
	}
	return m.viewport.View() + "\n" + footer
}

func (m *uiModel) renderHeader() string {
	return ""
}

func (m *uiModel) renderSessionMeta() string {
	return ""
}

func (m *uiModel) renderFooter() string {
	var parts []string
	switch m.state {
	case uiStatePermission:
		parts = append(parts, m.renderInputArea())
	case uiStateQuerying:
		parts = append(parts, m.renderActivityLine())
		if m.showSlash && len(m.slashMatches) > 0 {
			parts = append(parts, m.renderSlashSuggestions())
		}
		parts = append(parts, m.renderInputArea())
	case uiStateNormal:
		parts = append(parts, m.renderInputArea())
	case uiStateInit:
		parts = append(parts, "  "+m.spinner.View()+" Initializing...")
	default:
		if m.showSlash && len(m.slashMatches) > 0 {
			parts = append(parts, m.renderSlashSuggestions())
		}
		parts = append(parts, m.renderInputArea())
	}
	if sb := m.renderStatusBar(); sb != "" {
		parts = append(parts, sb)
	}
	return strings.Join(parts, "\n")
}

func (m *uiModel) renderStatusBar() string {
	var parts []string

	// Vim mode indicator
	switch m.state {
	case uiStateNormal:
		pending := ""
		if m.pendingKey != "" {
			pending = m.pendingKey
		}
		parts = append(parts, uiPrimaryText.Bold(true).Render("NORMAL"+pending))
	case uiStateInput:
		parts = append(parts, uiDimText.Render("INSERT"))
	}

	if m.statusMsg != "" && m.statusMsg != "thinking" {
		parts = append(parts, uiPrimaryText.Render(m.statusMsg))
	}
	if m.errMsg != "" {
		parts = append(parts, uiErrorText.Render(m.errMsg))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, uiDimText.Render(" · "))
}

func (m *uiModel) renderActivityLine() string {
	hint := "esc to interrupt"
	if !m.queryStartTime.IsZero() {
		secs := int(time.Since(m.queryStartTime).Seconds())
		if secs > 0 {
			hint = fmt.Sprintf("%ds • esc to interrupt", secs)
		}
	}
	return "  " + uiDimText.Render("Working ("+hint+")")
}

func (m *uiModel) renderInputArea() string {
	box := m.input.View()
	meta := strings.TrimSpace(m.renderInputMeta())
	rule := uiDimText.Render(strings.Repeat("─", max(10, m.width)))
	if m.errMsg != "" {
		if meta == "" {
			return rule + "\n" + uiErrorText.Render("  ! "+m.errMsg) + "\n" + box + "\n" + rule
		}
		hintText := uiDimText.Render(truncateUI(meta, max(8, m.width)))
		return rule + "\n" + uiErrorText.Render("  ! "+m.errMsg) + "\n" + box + "\n" + rule + "\n" + hintText
	}
	if meta == "" {
		return rule + "\n" + box + "\n" + rule
	}
	hintText := uiDimText.Render(truncateUI(meta, max(8, m.width)))
	return rule + "\n" + box + "\n" + rule + "\n" + hintText
}

func (m *uiModel) renderInputMeta() string {
	var parts []string
	if m.model != nil {
		modelText := m.model.Name
		if modelText == "" {
			modelText = m.model.ID
		}
		if m.model.ProviderID != "" && !strings.Contains(modelText, "(") {
			modelText += " (" + m.model.ProviderID + ")"
		}
		if strings.TrimSpace(modelText) != "" {
			parts = append(parts, modelText)
		}
	}
	if m.session != nil {
		cwd := m.session.RuntimeState().Cwd
		if cwd != "" {
			cwd = shortenUIPath(cwd)
			parts = append(parts, cwd)
		}
	}
	if m.tgUsername != "" {
		parts = append(parts, "@"+m.tgUsername)
	}
	return strings.Join(parts, "  ·  ")
}

func (m *uiModel) renderPermissionInline(width int) string {
	if m.pendingPerm == nil {
		return ""
	}

	boxWidth := max(40, min(width-4, 80))

	input := formatToolInput(m.pendingPerm.ToolName, m.pendingPerm.Args)
	if len(input) > 300 {
		input = input[:300] + "..."
	}
	input = wrap.String(input, max(20, boxWidth-4))

	titleStyle := uiWarningText.Bold(true)
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(uiWarning).
		Padding(0, 1).
		Width(boxWidth)

	var body strings.Builder
	body.WriteString(titleStyle.Render(m.pendingPerm.ToolName))
	if input != "" {
		body.WriteString("\n")
		body.WriteString(uiDimText.Render(input))
	}

	box := boxStyle.Render(body.String())

	actions := "  " +
		uiSuccessText.Bold(true).Render("[Y]es") + "  " +
		uiErrorText.Bold(true).Render("[N]o") + "  " +
		uiWarningText.Bold(true).Render("[A]lways allow") + "  " +
		uiMutedText.Render("[D]eny always")

	return box + "\n" + actions
}

func (m *uiModel) renderSlashSuggestions() string {
	maxShow := 8
	if len(m.slashMatches) < maxShow {
		maxShow = len(m.slashMatches)
	}
	var inner strings.Builder
	for i := 0; i < maxShow; i++ {
		cmd := m.slashMatches[i]
		name := lipgloss.NewStyle().Bold(true).Foreground(uiSecondary).Render(cmd.Name)
		desc := uiDimText.Render("  " + cmd.Description)
		prefix := "  " + uiDimText.Render("·") + " "
		inner.WriteString(prefix + name + desc)
		if i < maxShow-1 {
			inner.WriteString("\n")
		}
	}
	if len(m.slashMatches) > maxShow {
		inner.WriteString(fmt.Sprintf("\n  %s", uiDimText.Render(fmt.Sprintf("+%d more", len(m.slashMatches)-maxShow))))
	}
	return inner.String()
}

// ─── Conversation rendering ───────────────────────

func (m *uiModel) renderConversation() string {
	if len(m.blocks) == 0 {
		return m.renderWelcome()
	}
	viewWidth := m.viewportContentWidth()
	// Build a glamour style with zero document margin so we control all
	// indentation ourselves. We pass a large word wrap and re-wrap ourselves
	// using reflow/wrap which is display-width-aware (handles CJK correctly).
	contentWidth := max(20, viewWidth-max(dotPadW, hookPadW))
	noMargin := uint(0)
	style := glamourstyles.DarkStyleConfig
	style.Document = glamouransi.StyleBlock{
		StylePrimitive: style.Document.StylePrimitive,
		Margin:         &noMargin,
	}
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(contentWidth),
	)
	var rendered []string
	for _, block := range m.blocks {
		var s string
		switch block.Kind {
		case "user":
			s = renderUIUserBlock(block.Content, viewWidth)
		case "assistant":
			var ab strings.Builder
			if block.Thinking != "" {
				ab.WriteString(renderUIThinking(block.Thinking, viewWidth))
			}
			if strings.TrimSpace(block.Content) != "" {
				if !block.Streaming && renderer != nil {
					if md, err := renderer.Render(block.Content); err == nil {
						ab.WriteString(renderUIAssistantMarkdownBlock(normalizeRenderedMarkdown(md), viewWidth))
					} else {
						content := wrap.String(block.Content, contentWidth)
						ab.WriteString(renderUIAssistantBlock(content, viewWidth))
					}
				} else {
					content := wrap.String(block.Content, contentWidth)
					ab.WriteString(renderUIAssistantBlock(content, viewWidth))
				}
			}
			s = ab.String()
		case "tool":
			var tb strings.Builder
			for _, tool := range block.Tools {
				tb.WriteString(renderUITool(tool, m.transcriptMode, viewWidth))
			}
			s = tb.String()
		case "section":
			s = renderUISection(block.Title, block.Content, viewWidth)
		default:
			var db strings.Builder
			for _, line := range strings.Split(block.Content, "\n") {
				var lb strings.Builder
				writeWrappedStyledLines(&lb, line, max(12, viewWidth-lipgloss.Width("  ")), "  ", "  ", uiDimText)
				db.WriteString(lb.String())
			}
			s = db.String()
		}
		s = strings.TrimRight(s, "\n")
		if s != "" {
			rendered = append(rendered, s)
		}
	}
	result := strings.TrimRight(hardWrapRenderedText(strings.Join(rendered, "\n\n"), viewWidth), "\n")
	if m.pendingPerm != nil {
		result += "\n\n" + m.renderPermissionInline(viewWidth)
	}
	return result
}

func renderUIUserBlock(content string, width int) string {
	var b strings.Builder
	writeWrappedStyledLines(&b, content, max(20, width-lipgloss.Width("> ")), uiSecondaryText.Render(">")+" ", strings.Repeat(" ", lipgloss.Width("> ")), lipgloss.NewStyle())
	return b.String()
}

func renderUIAssistantBlock(content string, width int) string {
	var b strings.Builder
	writeWrappedStyledLines(&b, content, max(12, width-dotPadW), uiWhiteText.Render("●")+" ", assistantPad, lipgloss.NewStyle())
	return b.String()
}

func renderUIAssistantMarkdownBlock(content string, width int) string {
	var b strings.Builder
	writeWrappedStyledLines(&b, content, widthForPrefix(width), uiWhiteText.Render("●")+" ", assistantPad, lipgloss.NewStyle())
	return strings.TrimRight(b.String(), "\n")
}

func normalizeRenderedMarkdown(content string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		stripped := uiANSIPattern.ReplaceAllString(line, "")
		trimmed := strings.TrimRight(stripped, " \t")
		if strings.TrimSpace(trimmed) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, trimmed)
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func renderUIThinking(content string, width int) string {
	var b strings.Builder
	b.WriteString(uiSecondaryText.Render("●") + " " + uiMutedText.Render("thinking") + "\n")
	writeWrappedStyledLines(&b, content, widthForPrefix(width), hookPad, dotPad, uiMutedText)
	return b.String()
}

func renderUITool(tool *uiToolState, expanded bool, width int) string {
	var b strings.Builder
	w := width

	dot := uiWhiteText.Render("●")
	nameStyle := uiPrimaryText.Bold(true)
	if tool.Status == "done" {
		dot = uiSuccessText.Render("●")
		nameStyle = uiSuccessText.Bold(true)
	} else if tool.IsError || tool.Status == "error" {
		dot = uiErrorText.Render("●")
		nameStyle = uiErrorText
	}

	args := ""
	if tool.Input != "" {
		args = uiMutedText.Render("(" + truncateUI(tool.Input, 80) + ")")
	}
	b.WriteString(fmt.Sprintf("%s %s%s\n", dot, nameStyle.Render(tool.Name), args))

	if tool.Status == "running" {
		b.WriteString(hookPad + uiDimText.Render("running") + "\n")
		return b.String()
	}

	if tool.IsError && tool.Output != "" {
		errLines := strings.Split(tool.Output, "\n")
		for i, line := range errLines {
			pad := dotPad
			if i == 0 {
				pad = hookPad
			}
			if i >= 5 {
				b.WriteString(dotPad + uiErrorText.Render(fmt.Sprintf("... +%d more lines", len(errLines)-5)) + "\n")
				break
			}
			writeWrappedStyledLines(&b, line, widthForPrefix(w), pad, dotPad, uiErrorText)
		}
		return b.String()
	}

	if tool.Output == "" {
		return b.String()
	}

	b.WriteString(renderUIToolOutput(tool.Name, tool.Output, expanded, w))
	return b.String()
}

func renderUIToolOutput(toolName, output string, expanded bool, w int) string {
	var b strings.Builder
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	collapsedMax := 3
	expandedMax := 30
	rowWidth := widthForPrefix(w)

	// padAt returns hookPad for the first line (idx==0), dotPad otherwise.
	padAt := func(idx int) string {
		if idx == 0 {
			return hookPad
		}
		return dotPad
	}
	maxRows := collapsedMax
	if expanded {
		maxRows = expandedMax
	}

	appendMoreHint := func(remaining int) {
		if remaining > 0 {
			b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines (ctrl+o to expand)", remaining)) + "\n")
		}
	}

	switch toolName {
	case "read":
		used, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if expanded {
			if remaining > 0 {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
			}
		} else if used > 0 {
			appendMoreHint(remaining)
		}

	case "bash":
		_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if !expanded {
			appendMoreHint(remaining)
		} else if remaining > 0 {
			b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
		}

	case "edit":
		if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
			b.WriteString(hookPad + uiSuccessText.Render("updated") + "\n")
		} else {
			usedRows := 0
			remaining := 0
			for lineIdx := 0; lineIdx < len(lines) && usedRows < maxRows; lineIdx++ {
				pad := padAt(lineIdx)
				line := lines[lineIdx]
				style := uiDimText
				if strings.HasPrefix(line, "+") {
					style = uiSuccessText
				} else if strings.HasPrefix(line, "-") {
					style = uiErrorText
				}
				used, complete, hidden := writeSingleWrappedRowBudget(&b, line, rowWidth, pad, dotPad, style, maxRows-usedRows)
				usedRows += used
				if !complete {
					remaining += hidden
					remaining += wrappedLineCount(lines[lineIdx+1:], rowWidth)
					break
				}
				if lineIdx == len(lines)-1 {
					break
				}
				remaining = wrappedLineCount(lines[lineIdx+1:], rowWidth)
			}
			if expanded {
				if remaining > 0 {
					b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
				}
			} else {
				appendMoreHint(remaining)
			}
		}

	case "write":
		if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
			b.WriteString(hookPad + uiSuccessText.Render("written") + "\n")
		} else {
			_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
			if expanded {
				if remaining > 0 {
					b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
				}
			} else {
				appendMoreHint(remaining)
			}
		}

	case "glob":
		_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if expanded {
			if remaining > 0 {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
			}
		} else {
			appendMoreHint(remaining)
		}

	case "grep":
		_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if expanded {
			if remaining > 0 {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
			}
		} else {
			appendMoreHint(remaining)
		}

	default:
		_, remaining := writeWrappedStyledRows(&b, lines, rowWidth, hookPad, dotPad, uiDimText, maxRows)
		if expanded {
			if remaining > 0 {
				b.WriteString(dotPad + uiDimText.Render(fmt.Sprintf("... +%d more lines", remaining)) + "\n")
			}
		} else {
			appendMoreHint(remaining)
		}
	}
	return b.String()
}

func renderUISection(title, content string, width int) string {
	var b strings.Builder
	b.WriteString("  " + uiPrimaryText.Render(title) + "\n")
	writeWrappedStyledLines(&b, content, max(12, width-lipgloss.Width("  ")), "  ", "  ", lipgloss.NewStyle())
	return strings.TrimRight(b.String(), "\n")
}

func (m *uiModel) renderWelcome() string {
	return ""
}

// ─── Exit rendering ───────────────────────────────

func (m *uiModel) renderExitSessionMeta() string {
	var parts []string
	if m.session != nil {
		stats := m.session.GetSessionStats()
		if stats.TotalTokens > 0 {
			parts = append(parts, fmt.Sprintf("~%d tokens", stats.TotalTokens))
		}
		if m.session.IsPlanMode() {
			parts = append(parts, "plan")
		}
		if m.session.ActiveWorktree() != "" {
			parts = append(parts, "worktree")
		}
	}
	if m.transcriptMode {
		parts = append(parts, "expanded")
	}
	if len(parts) == 0 {
		return ""
	}
	return "session: " + strings.Join(parts, " | ")
}

func (m *uiModel) renderExitTranscript() string {
	var parts []string
	if meta := m.renderExitSessionMeta(); meta != "" {
		parts = append(parts, meta)
	}
	if conv := m.renderConversation(); conv != "" {
		parts = append(parts, conv)
	}
	if m.errMsg != "" {
		parts = append(parts, uiErrorText.Render("  ! "+m.errMsg))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// ─── Block helpers ───────────────────────────────

func assistantMessageFromEvent(msg agent.AgentMessage) (types.AssistantMessage, bool) {
	switch v := msg.(type) {
	case types.AssistantMessage:
		return v, true
	case *types.AssistantMessage:
		if v != nil {
			return *v, true
		}
	}
	return types.AssistantMessage{}, false
}

func (m *uiModel) latestRunningTool() *uiToolState {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		for j := len(m.blocks[i].Tools) - 1; j >= 0; j-- {
			if m.blocks[i].Tools[j].Status == "running" {
				return m.blocks[i].Tools[j]
			}
		}
	}
	return nil
}

// ─── Tool input formatting ───────────────────────

func formatToolInput(toolName string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch toolName {
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			if idx := strings.Index(cmd, "\n"); idx > 0 {
				return cmd[:idx] + "..."
			}
			return cmd
		}
	case "read":
		if fp, ok := args["file_path"].(string); ok {
			s := shortenUIPath(fp)
			if offset, ok := args["offset"].(float64); ok && offset > 0 {
				s += fmt.Sprintf(":%d", int(offset))
			}
			return s
		}
	case "write":
		if fp, ok := args["file_path"].(string); ok {
			return shortenUIPath(fp)
		}
	case "edit":
		if fp, ok := args["file_path"].(string); ok {
			old, _ := args["old_string"].(string)
			if len(old) > 40 {
				old = old[:40] + "..."
			}
			return fmt.Sprintf("%s: %q → ...", shortenUIPath(fp), old)
		}
	case "glob":
		if p, ok := args["pattern"].(string); ok {
			path, _ := args["path"].(string)
			if path != "" {
				return p + " in " + shortenUIPath(path)
			}
			return p
		}
	case "grep":
		if p, ok := args["pattern"].(string); ok {
			path, _ := args["path"].(string)
			if path != "" {
				return fmt.Sprintf("%q in %s", p, shortenUIPath(path))
			}
			return fmt.Sprintf("%q", p)
		}
	case "web_fetch":
		if u, ok := args["url"].(string); ok {
			return u
		}
	case "web_search":
		if q, ok := args["query"].(string); ok {
			return fmt.Sprintf("%q", q)
		}
	case "agent":
		if desc, ok := args["description"].(string); ok && desc != "" {
			return desc
		}
		if prompt, ok := args["prompt"].(string); ok {
			if len(prompt) > 60 {
				prompt = prompt[:60] + "..."
			}
			return prompt
		}
	}
	// Fallback: sort keys and render key=value
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, args[key]))
	}
	s := strings.Join(parts, " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func shortenUIPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// ─── Result text extraction ──────────────────────

func fullResultText(ev agent.AgentEvent) string {
	if ev.Result == nil {
		return ""
	}
	if texts, ok := ev.Result.([]types.ContentBlock); ok {
		var parts []string
		for _, block := range texts {
			if t, ok := block.(*types.TextContent); ok && t != nil {
				parts = append(parts, t.Text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	switch v := ev.Result.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		raw, _ := json.Marshal(v)
		return strings.TrimSpace(string(raw))
	}
}

// ─── Utility ─────────────────────────────────────

func widthForPrefix(totalWidth int) int {
	return max(12, totalWidth-max(dotPadW, hookPadW))
}

func (m *uiModel) viewportContentWidth() int {
	return max(20, m.width-m.viewport.Style.GetHorizontalFrameSize())
}

func hardWrapRenderedText(text string, width int) string {
	width = max(8, width)
	var out []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		out = append(out, strings.Split(xansi.Hardwrap(line, width, true), "\n")...)
	}
	return strings.Join(out, "\n")
}

func writeWrappedStyledLines(b *strings.Builder, text string, width int, firstPrefix, restPrefix string, style lipgloss.Style) {
	width = max(8, width)
	first := true
	for _, rawLine := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(rawLine) == "" {
			b.WriteString("\n")
			continue
		}
		wrapped := wrap.String(rawLine, width)
		for _, part := range strings.Split(strings.TrimRight(wrapped, "\n"), "\n") {
			prefix := restPrefix
			if first {
				prefix = firstPrefix
				first = false
			}
			b.WriteString(prefix + style.Render(part) + "\n")
		}
	}
}

func wrapSegments(rawLine string, width int) []string {
	width = max(8, width)
	if strings.TrimSpace(rawLine) == "" {
		return []string{""}
	}
	wrapped := wrap.String(rawLine, width)
	parts := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}

func wrappedLineCount(lines []string, width int) int {
	total := 0
	for _, line := range lines {
		total += len(wrapSegments(line, width))
	}
	return total
}

func writeWrappedStyledRows(b *strings.Builder, lines []string, width int, firstPrefix, restPrefix string, style lipgloss.Style, maxRows int) (used int, remaining int) {
	maxRows = max(1, maxRows)
	first := true
	for i, line := range lines {
		segments := wrapSegments(line, width)
		for j, seg := range segments {
			if used >= maxRows {
				remaining += len(segments) - j
				remaining += wrappedLineCount(lines[i+1:], width)
				return used, remaining
			}
			prefix := restPrefix
			if first {
				prefix = firstPrefix
				first = false
			}
			b.WriteString(prefix + style.Render(seg) + "\n")
			used++
		}
	}
	return used, remaining
}

func writeSingleWrappedRowBudget(b *strings.Builder, line string, width int, firstPrefix, restPrefix string, style lipgloss.Style, budget int) (used int, complete bool, hidden int) {
	segments := wrapSegments(line, width)
	budget = max(0, budget)
	for i, seg := range segments {
		if used >= budget {
			return used, false, len(segments) - i
		}
		prefix := restPrefix
		if i == 0 {
			prefix = firstPrefix
		}
		b.WriteString(prefix + style.Render(seg) + "\n")
		used++
	}
	return used, true, 0
}

func truncateUI(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= maxLen {
		return s
	}
	if maxLen == 1 {
		return string(rs[:1])
	}
	return string(rs[:maxLen-1]) + "…"
}

func extractThinkText(raw string) (thinking string, visible string) {
	const openTag = "<think>"
	const closeTag = "</think>"

	var thinkParts []string
	var visibleParts strings.Builder
	rest := raw

	for {
		start := strings.Index(rest, openTag)
		if start < 0 {
			visibleParts.WriteString(rest)
			break
		}

		visibleParts.WriteString(rest[:start])
		rest = rest[start+len(openTag):]

		end := strings.Index(rest, closeTag)
		if end < 0 {
			break
		}

		chunk := strings.TrimSpace(rest[:end])
		if chunk != "" {
			thinkParts = append(thinkParts, chunk)
		}
		rest = rest[end+len(closeTag):]
	}

	return strings.Join(thinkParts, "\n"), visibleParts.String()
}
