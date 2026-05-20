package tui

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// editSummary builds the one-line summary shown on the hook (⎿) row of an
// edit tool block, matching Claude Code's natural-language phrasing:
//
//	Added 4 lines, removed 2 lines  (both non-zero)
//	Added 4 lines                   (only adds)
//	Removed 2 lines                 (only removes)
//	Updated                         (idempotent edit, both counts zero)
//
// The file path is not repeated here because the tool-header row already
// shows it (e.g. `⏺ edit(pkg/tui/render.go)`).
func editSummary(added, removed int) string {
	plural := func(n int) string {
		if n == 1 {
			return "line"
		}
		return "lines"
	}
	switch {
	case added > 0 && removed > 0:
		return fmt.Sprintf("Added %d %s, removed %d %s", added, plural(added), removed, plural(removed))
	case added > 0:
		return fmt.Sprintf("Added %d %s", added, plural(added))
	case removed > 0:
		return fmt.Sprintf("Removed %d %s", removed, plural(removed))
	default:
		return "Updated"
	}
}

// chromaStyle is the syntax-highlight palette. Monokai reads well on dark
// terminals and ships with chroma so we don't pull in extra theme deps.
var chromaStyle = styles.Get("monokai")

// chromaFormatter renders chroma tokens as ANSI escape sequences. terminal256
// covers most modern terminals; truecolor isn't worth the binary churn for a
// diff highlight.
var chromaFormatter chroma.Formatter = formatters.Get("terminal256")

var (
	lexerCacheMu sync.RWMutex
	lexerCache   = map[string]chroma.Lexer{}
)

// lexerForPath returns a chroma lexer for the file at path, cached by
// extension. Falls back to nil when no lexer matches — callers must treat nil
// as "no highlighting, render raw".
func lexerForPath(path string) chroma.Lexer {
	if path == "" {
		return nil
	}
	key := strings.ToLower(filepath.Ext(path))
	if key == "" {
		key = filepath.Base(path)
	}
	lexerCacheMu.RLock()
	if l, ok := lexerCache[key]; ok {
		lexerCacheMu.RUnlock()
		return l
	}
	lexerCacheMu.RUnlock()

	l := lexers.Match(filepath.Base(path))
	if l != nil {
		l = chroma.Coalesce(l)
	}
	lexerCacheMu.Lock()
	lexerCache[key] = l
	lexerCacheMu.Unlock()
	return l
}

// parseEditDiffLine inspects a single line from the edit tool's diff output.
// edit.go emits each row as `<MARKER><SPACE><LINENO><SPACE><SPACE><CONTENT>`
// where MARKER is one of ' ', '+', '-'. The function returns the marker, the
// line number string (empty if the row doesn't carry one), and the visible
// content. When the input does not fit this shape (e.g. the unit test format
// `"- func old()"` with no line number) it falls back to marker + content,
// with lineno = "".
func parseEditDiffLine(line string) (marker byte, lineno, content string) {
	if line == "" {
		return 0, "", ""
	}
	marker = line[0]
	if len(line) < 2 || line[1] != ' ' {
		// No separator after marker — treat everything after marker as content.
		return marker, "", line[1:]
	}
	rest := line[2:]
	// Try `<digits>  <content>`.
	idx := strings.Index(rest, "  ")
	if idx > 0 {
		head := rest[:idx]
		allDigits := true
		for i := 0; i < len(head); i++ {
			if head[i] < '0' || head[i] > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return marker, head, rest[idx+2:]
		}
	}
	return marker, "", rest
}

// styleEditDiffRow renders one diff row in Claude Code layout, split into
// (firstPrefix, restPrefix, content) so the caller (writeSingleWrappedRowBudget)
// can prepend a per-row leader that preserves the ± marker on every wrapped
// continuation. Visible layout:
//
//	firstPrefix = "<padded lineno> <colored marker>  "
//	restPrefix  = "<lineno-width spaces> <colored marker>  "
//	content     = chroma-highlighted body
//
// `linenoWidth` is the column width reserved for the line-number gutter so
// context lines and ± lines align even when numbers vary in digit count.
// When the input row lacks a line number (e.g. the simplified diff format
// used by unit tests) the legacy `<marker> <content>` shape is returned with
// an empty restPrefix-equivalent indent.
func styleEditDiffRow(line string, lexer chroma.Lexer, linenoWidth int) (firstPrefix, restPrefix, content string) {
	if line == "" {
		return "", "", ""
	}
	marker, lineno, body := parseEditDiffLine(line)

	var coloredMarker string
	switch marker {
	case '+':
		coloredMarker = uiSuccessText.Render("+")
	case '-':
		coloredMarker = uiErrorText.Render("-")
	default:
		coloredMarker = " "
	}

	if marker == ' ' && lexer == nil {
		content = uiDimText.Render(body)
	} else {
		content = highlightCodeLine(body, lexer)
	}

	if lineno == "" {
		// Legacy / test format: keep `<marker> <body>` visible shape.
		switch marker {
		case '+', '-':
			firstPrefix = coloredMarker + " "
			restPrefix = "  "
		default:
			// Plain content row (no marker, no lineno). Caller already
			// receives the styled content; emit empty prefixes.
		}
		return
	}

	pad := linenoWidth - len(lineno)
	if pad < 0 {
		pad = 0
	}
	firstGutter := uiDimText.Render(strings.Repeat(" ", pad) + lineno)
	restGutter := strings.Repeat(" ", linenoWidth)
	firstPrefix = firstGutter + " " + coloredMarker + "  "
	restPrefix = restGutter + " " + coloredMarker + "  "
	return
}

// Raw SGR sequences for diff row backgrounds — emitted directly rather than
// going through lipgloss so the color survives lipgloss's TTY/profile
// detection (which silently strips colors in non-TTY contexts like tests).
// Truecolor (24-bit) is supported by every modern terminal we care about.
const (
	sgrDiffAddedBg   = "\x1b[48;2;26;46;26m" // dark muted green
	sgrDiffRemovedBg = "\x1b[48;2;58;26;26m" // dark muted red
	sgrReset         = "\x1b[0m"
)

// tintDiffRow wraps an already-styled diff row with a background color and
// keeps that color alive past every inner `\033[0m` reset emitted by chroma
// (without this fixup the bg vanishes at every token boundary). The result is
// also right-padded with bg-colored spaces to `targetWidth` cells so the tint
// extends to the column edge — matching how diff viewers conventionally
// highlight changed rows.
//
// `marker` selects which bg color to use ('+', '-'). For any other marker
// the input is returned unchanged.
func tintDiffRow(row string, marker byte, targetWidth int) string {
	var openSeq string
	switch marker {
	case '+':
		openSeq = sgrDiffAddedBg
	case '-':
		openSeq = sgrDiffRemovedBg
	default:
		return row
	}

	// Restore the bg every time chroma resets the style.
	patched := strings.ReplaceAll(row, sgrReset, sgrReset+openSeq)

	// Pad to targetWidth with bg-colored spaces.
	visible := uiANSIPattern.ReplaceAllString(row, "")
	visibleW := lipgloss.Width(visible)
	pad := targetWidth - visibleW
	if pad < 0 {
		pad = 0
	}

	return openSeq + patched + strings.Repeat(" ", pad) + sgrReset
}

// highlightCodeLine syntax-highlights one line of code using the given lexer
// and returns an ANSI-styled string. Returns the input unchanged when the
// lexer is nil or chroma fails — callers don't have to error-check.
func highlightCodeLine(content string, lexer chroma.Lexer) string {
	if lexer == nil || content == "" {
		return content
	}
	iter, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content
	}
	var buf bytes.Buffer
	if err := chromaFormatter.Format(&buf, chromaStyle, iter); err != nil {
		return content
	}
	out := buf.String()
	// chroma appends a trailing newline; the caller assembles its own line
	// breaks so strip it to avoid double-blank rows.
	return strings.TrimRight(out, "\n")
}
