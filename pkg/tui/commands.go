package tui

import (
	"strings"
)

func hotkeyHelpText() string {
	lines := []string{
		"Navigation",
		"  Up/Down: move cursor, history, or selector",
		"  Home/End: line start/end or selector start/end",
		"  PageUp/PageDown: scroll transcript or selector page",
		"  Esc: close selector",
		"",
		"Editing",
		"  Enter: submit; while running, queue follow-up",
		"  Shift+Enter: while running, steer current task",
		"  Ctrl+J: newline",
		"  Tab: autocomplete or selector scope",
		"  @file: fuzzy file reference; Tab/Enter completes",
		"  !cmd: run shell and send output; !!cmd display only",
		"",
		"App",
		"  Ctrl+C: interrupt or exit",
		"  Ctrl+D: exit when input is empty",
		"  Ctrl+L: clear screen",
		"  Ctrl+O: expand/collapse tool output",
		"  Ctrl+P/Ctrl+N: cycle models",
		"  Shift+Tab: toggle plan mode",
		"  Tree: Ctrl+F branch-session, Ctrl+S summary",
		"",
		"Commands",
		"  /settings, /config, /model, /scoped-models, /sessions",
		"  /tree, /fork, /clone, /skills, /prompts",
		"  /steer <message> (/s), /followup <message> (/f)",
		"  /queue, /queue clear [steer|followup], /queue drop",
		"  /export, /copy, /changelog",
	}
	return strings.Join(lines, "\n")
}
