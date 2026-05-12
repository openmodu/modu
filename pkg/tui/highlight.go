package tui

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

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

// styleEditDiffLine prepares one line of edit-tool diff output for the TUI.
// It strips the leading `+` / `-` / ` ` marker, syntax-highlights the
// remaining code via chroma, and reattaches the marker in green / red / dim
// so the diff signal stays readable. When lexer is nil the line is rendered
// with the old whole-line tint (no syntax colors).
func styleEditDiffLine(line string, lexer chroma.Lexer) string {
	if line == "" {
		return line
	}
	switch line[0] {
	case '+':
		return uiSuccessText.Render("+") + highlightCodeLine(line[1:], lexer)
	case '-':
		return uiErrorText.Render("-") + highlightCodeLine(line[1:], lexer)
	default:
		if lexer == nil {
			return uiDimText.Render(line)
		}
		return highlightCodeLine(line, lexer)
	}
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
