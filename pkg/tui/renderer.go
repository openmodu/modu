package tui

import (
	"fmt"
	"io"
	"strings"

	"github.com/crosszan/modu/pkg/agent"
	"github.com/crosszan/modu/pkg/types"
)

// Renderer renders agent events to the terminal using ANSI escape codes.
//
// Two output modes:
//   - Plain (screen == nil): streams text directly; tool calls are shown as a
//     single line that is replaced in-place (via cursor-up) when the tool
//     completes.
//   - Screen: writes to the scroll-region viewport maintained by Screen.
type Renderer struct {
	out     io.Writer // used when screen is nil
	screen  *Screen   // optional viewport; takes precedence over out
	noColor bool

	// state tracked across events within one agent turn
	inThink bool
	hadText bool
	hadTool bool

	// plain-mode tool collapse: number of lines printed since the tool header.
	// In practice this should always be 0 because no other events fire between
	// ToolExecutionStart and ToolExecutionEnd, but we track it for safety.
	toolLines int
}

// NewRenderer creates a Renderer that writes to out.
// Colors are disabled automatically when out is not a terminal or NO_COLOR is set.
func NewRenderer(out io.Writer) *Renderer {
	return &Renderer{
		out:     out,
		noColor: shouldDisableColor(out),
	}
}

// NewRendererWithScreen creates a Renderer backed by a viewport Screen.
func NewRendererWithScreen(s *Screen) *Renderer {
	return &Renderer{
		screen:  s,
		noColor: s.noColor,
	}
}

// SetNoColor overrides the automatic color detection.
func (r *Renderer) SetNoColor(v bool) { r.noColor = v }

// write sends text to the appropriate output backend.
func (r *Renderer) write(text string) {
	if r.screen != nil {
		r.screen.Write(text)
	} else {
		fmt.Fprint(r.out, text)
	}
}

// writeln sends text + newline.
func (r *Renderer) writeln(text string) { r.write(text + "\n") }

// HandleEvent processes an agent event and renders it.
func (r *Renderer) HandleEvent(event agent.AgentEvent) {
	switch event.Type {

	case agent.EventTypeAgentStart:
		r.hadText = false
		r.hadTool = false
		r.inThink = false
		r.toolLines = 0

	case agent.EventTypeMessageUpdate:
		if event.StreamEvent == nil {
			return
		}
		evt := event.StreamEvent
		switch evt.Type {
		case types.EventThinkingStart:
			r.inThink = true
			r.write(styled(r.noColor, ansiDim+ansiCyan, "\n💭 thinking...\n"))
			r.toolLines++
		case types.EventThinkingDelta:
			r.write(styled(r.noColor, ansiDim+ansiCyan, evt.Delta))
		case types.EventThinkingEnd:
			r.inThink = false
			r.write(styled(r.noColor, ansiDim+ansiCyan, "\n"))
			r.toolLines++
		case types.EventTextDelta:
			if !r.hadText {
				r.write("\n")
				r.hadText = true
				r.toolLines++
			}
			r.write(evt.Delta)
		}

	case agent.EventTypeToolExecutionStart:
		r.hadTool = true
		r.toolLines = 0

		header := r.formatToolHeader(event.ToolName, event.Args, false)
		if r.screen != nil {
			r.write("\n")
			r.screen.WriteToolHeader(header)
		} else {
			// Plain mode: print one line; we will cursor-up to replace it on end.
			fmt.Fprint(r.out, "\n")
			fmt.Fprintln(r.out, header)
		}

	case agent.EventTypeToolExecutionEnd:
		summary := toolResultSummary(event)
		collapsed := r.formatToolHeader(event.ToolName, event.Args, !event.IsError) + summary

		if r.screen != nil {
			r.screen.CollapseToolHeader(collapsed)
			if event.IsError {
				r.screen.Writeln(styled(r.noColor, ansiRed, "  ✗ "+errorText(event)))
			}
		} else {
			// Plain mode: replace the header line in-place.
			upLines := r.toolLines + 1 // +1 for the header line itself
			fmt.Fprintf(r.out, "\033[%dA", upLines) // cursor up
			fmt.Fprint(r.out, ansiEraseLine)
			fmt.Fprintln(r.out, collapsed)
			if event.IsError {
				fmt.Fprintln(r.out, styled(r.noColor, ansiRed, "  ✗ "+errorText(event)))
			}
			r.toolLines = 0
		}

	case agent.EventTypeAgentEnd:
		r.write("\n")
	}
}

// formatToolHeader builds the single-line tool display.
//
//	⚙ ToolName  key=val, key2=val2   (running)
//	⚙ ToolName ✓  key=val, key2=val2 (done)
func (r *Renderer) formatToolHeader(name string, args any, done bool) string {
	status := styled(r.noColor, ansiDim+ansiYellow, "⟳")
	if done {
		status = styled(r.noColor, ansiGreen, "✓")
	}

	argStr := formatArgs(args, 80)

	line := fmt.Sprintf("  %s %s  %s",
		styled(r.noColor, ansiBold+ansiYellow, "⚙ "+name),
		status,
		styled(r.noColor, ansiDim, argStr),
	)
	return line
}

// formatArgs formats tool arguments as a compact key=val string, capped at maxLen.
func formatArgs(args any, maxLen int) string {
	m, ok := args.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	var parts []string
	for k, v := range m {
		val := fmt.Sprintf("%v", v)
		val = strings.ReplaceAll(val, "\n", "↵")
		if len(val) > 40 {
			val = val[:40] + "…"
		}
		parts = append(parts, k+"="+val)
	}
	result := strings.Join(parts, "  ")
	if len(result) > maxLen {
		result = result[:maxLen] + "…"
	}
	return result
}

// toolResultSummary returns a short " → …" string from the tool result.
func toolResultSummary(event agent.AgentEvent) string {
	result, ok := event.Result.(agent.AgentToolResult)
	if !ok {
		return ""
	}
	var text string
	for _, block := range result.Content {
		if tc, ok := block.(*types.TextContent); ok && tc != nil {
			text += tc.Text
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// First line only, truncated.
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		lines := strings.Count(text, "\n") + 1
		text = fmt.Sprintf("%s … (%d lines)", text[:idx], lines)
	}
	if len(text) > 60 {
		text = text[:60] + "…"
	}
	return "  → " + text
}

// errorText extracts error text from a tool result event.
func errorText(event agent.AgentEvent) string {
	result, ok := event.Result.(agent.AgentToolResult)
	if !ok {
		return "tool error"
	}
	for _, block := range result.Content {
		if tc, ok := block.(*types.TextContent); ok && tc != nil && tc.Text != "" {
			t := tc.Text
			if len(t) > 80 {
				t = t[:80] + "…"
			}
			return t
		}
	}
	return "tool error"
}

// PrintUser renders a user message (the prompt the user typed).
func (r *Renderer) PrintUser(msg string) {
	prefix := styled(r.noColor, ansiBold+ansiBlue, "You")
	r.writeln(fmt.Sprintf("\n%s: %s", prefix, msg))
}

// PrintInfo renders an informational message.
func (r *Renderer) PrintInfo(msg string) {
	r.writeln(styled(r.noColor, ansiDim, msg))
}

// PrintError renders an error message.
func (r *Renderer) PrintError(err error) {
	msg := fmt.Sprintf("error: %v", err)
	r.writeln(styled(r.noColor, ansiRed, msg))
}

// PrintBanner renders a welcome banner.
func (r *Renderer) PrintBanner(model, cwd string) {
	w := termWidth()
	r.writeln(styled(r.noColor, ansiBold+ansiMagenta, strings.Repeat("═", w)))
	r.writeln(styled(r.noColor, ansiBold+ansiMagenta, "  modu code"))
	r.writeln(styled(r.noColor, ansiDim, fmt.Sprintf("  model: %s", model)))
	r.writeln(styled(r.noColor, ansiDim, fmt.Sprintf("  cwd:   %s", cwd)))
	r.writeln(styled(r.noColor, ansiBold+ansiMagenta, strings.Repeat("═", w)))
	r.writeln("")
	r.writeln(styled(r.noColor, ansiDim, "Type your message and press Enter. /help for commands, Ctrl+C to abort."))
	r.writeln("")
}

// PrintSeparator renders a horizontal separator.
func (r *Renderer) PrintSeparator() {
	w := termWidth()
	r.writeln(separator(r.noColor, w))
}

// PrintUsage renders token usage after a response.
func (r *Renderer) PrintUsage(totalTokens int) {
	if totalTokens <= 0 {
		return
	}
	r.writeln(styled(r.noColor, ansiDim, fmt.Sprintf("  tokens: %d", totalTokens)))
}
