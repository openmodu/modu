package tui

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// buildDiffPreview colours unified-diff lines the way Claude Code does:
//   - "--- " / "+++ " headers → dim
//   - lines starting with "-"  → red
//   - lines starting with "+"  → green
//   - everything else          → dim
func buildDiffPreview(lines []string, noColor bool) []string {
	if noColor {
		return nil
	}
	result := make([]string, len(lines))
	for i, l := range lines {
		var line string
		switch {
		case strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ "):
			line = ansiDim + "     " + l + ansiReset
		case strings.HasPrefix(l, "-"):
			line = ansiRed + "     " + l + ansiReset
		case strings.HasPrefix(l, "+"):
			line = ansiBrightGreen + "     " + l + ansiReset
		default:
			line = ansiDim + "     " + l + ansiReset
		}
		result[i] = line
	}
	return result
}

// highlightCode tokenises source code and returns one ANSI-coloured string per
// line.  filePath is used for language detection via file extension.
// Returns nil if the language is unknown or highlighting fails.
func highlightCode(code, filePath string) []string {
	lexer := lexers.Match(filePath)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		return nil
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Fallback
	}
	// Use true-color formatter; most modern terminals support it.
	formatter := formatters.Get("terminal16m")
	if formatter == nil {
		return nil
	}

	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return nil
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, it); err != nil {
		return nil
	}

	out := strings.TrimRight(buf.String(), "\n")
	return strings.Split(out, "\n")
}

// buildHighlightedPreview takes the numbered lines produced by the read tool
// (format: "N  <code>") and returns ready-to-print strings with:
//   - dim ANSI styling on the "     N  " prefix
//   - syntax-highlighted code content
//
// Returns nil when noColor is true or the language cannot be detected.
func buildHighlightedPreview(numberedLines []string, filePath string, noColor bool) []string {
	if noColor || filePath == "" {
		return nil
	}

	prefixes := make([]string, len(numberedLines))
	codes := make([]string, len(numberedLines))
	for i, l := range numberedLines {
		idx := strings.Index(l, "  ")
		if idx > 0 && idx < 10 {
			prefixes[i] = l[:idx+2] // "N  "
			codes[i] = l[idx+2:]    // actual code
		} else {
			prefixes[i] = ""
			codes[i] = l
		}
	}

	hlines := highlightCode(strings.Join(codes, "\n"), filePath)
	if hlines == nil {
		return nil
	}

	result := make([]string, len(numberedLines))
	for i := range numberedLines {
		content := ""
		if i < len(hlines) {
			content = hlines[i] + ansiReset
		}
		// Dim prefix, then syntax-coloured content.
		result[i] = ansiDim + "     " + prefixes[i] + ansiReset + content
	}
	return result
}
