package tui

import (
	"fmt"
	"strings"
)

// mdWriter buffers streaming text and renders it as Markdown to ANSI.
//
// It is line-oriented: text is buffered until a newline arrives, then the
// complete line is styled and forwarded to the output callback.  This avoids
// any column-tracking issues and produces flicker-free output.
//
// Supported syntax:
//
//	# / ## / ###    ATX headings
//	**bold**        bold text
//	*italic*        italic text
//	`code`          inline code
//	```[lang]       fenced code block
//	- / * / +       unordered list
//	1.              ordered list
//	> text          blockquote
//	---             horizontal rule
type mdWriter struct {
	noColor  bool
	out      func(string) // write already-rendered ANSI text

	lineBuf  string // current in-progress (incomplete) line
	inCode   bool   // inside a fenced code block
	codeLang string
}

func newMDWriter(noColor bool, out func(string)) *mdWriter {
	return &mdWriter{noColor: noColor, out: out}
}

// Feed accepts a text chunk (may contain newlines) from the streaming response.
func (m *mdWriter) Feed(text string) {
	m.lineBuf += text
	for {
		idx := strings.IndexByte(m.lineBuf, '\n')
		if idx < 0 {
			break
		}
		m.renderLine(m.lineBuf[:idx])
		m.lineBuf = m.lineBuf[idx+1:]
	}
}

// Flush renders any remaining buffered text (call at end of turn).
// The last line of an LLM response typically has no trailing newline, so
// we run it through the full renderLine pipeline (list/heading/blockquote).
func (m *mdWriter) Flush() {
	if m.lineBuf == "" {
		return
	}
	m.renderLine(m.lineBuf)
	m.lineBuf = ""
}

// Reset clears all state; call at the start of each new turn.
func (m *mdWriter) Reset() {
	m.lineBuf = ""
	m.inCode = false
	m.codeLang = ""
}

// ── line-level rendering ──────────────────────────────────────────────────────

func (m *mdWriter) renderLine(line string) {
	// ── fenced code block ───────────────────────────────────────────────────
	if strings.HasPrefix(line, "```") {
		if m.inCode {
			// Closing fence: draw a bottom rule.
			m.inCode = false
			m.codeLang = ""
			w := termWidth() - 2
			if w < 4 {
				w = 4
			}
			m.out(styled(m.noColor, ansiBrightBlack, "  └"+strings.Repeat("─", w)) + "\n")
		} else {
			// Opening fence: draw a top rule with optional language label.
			m.inCode = true
			m.codeLang = strings.TrimSpace(strings.TrimPrefix(line, "```"))
			w := termWidth() - 2
			if w < 4 {
				w = 4
			}
			if m.codeLang != "" {
				label := " " + m.codeLang + " "
				dashes := w - len([]rune(label))
				if dashes < 0 {
					dashes = 0
				}
				m.out(styled(m.noColor, ansiBrightBlack, "  ┌"+label+strings.Repeat("─", dashes)) + "\n")
			} else {
				m.out(styled(m.noColor, ansiBrightBlack, "  ┌"+strings.Repeat("─", w)) + "\n")
			}
		}
		return
	}

	if m.inCode {
		m.out(m.codeLine(line) + "\n")
		return
	}

	// ── horizontal rule ─────────────────────────────────────────────────────
	if isHorizontalRule(line) {
		w := termWidth()
		m.out(styled(m.noColor, ansiBrightBlack, strings.Repeat("─", w)) + "\n")
		return
	}

	// ── ATX headings ────────────────────────────────────────────────────────
	if strings.HasPrefix(line, "#") {
		level := 0
		for _, r := range line {
			if r == '#' {
				level++
			} else {
				break
			}
		}
		if level <= 6 && len(line) > level && line[level] == ' ' {
			content := m.renderInline(strings.TrimSpace(line[level+1:]))
			switch level {
			case 1:
				m.out(styled(m.noColor, ansiBold+ansiBrightGreen, content) + "\n")
			case 2:
				m.out(styled(m.noColor, ansiBold+ansiBrightWhite, content) + "\n")
			case 3:
				m.out(styled(m.noColor, ansiBold, content) + "\n")
			default:
				m.out(styled(m.noColor, ansiDim+ansiBold, content) + "\n")
			}
			return
		}
	}

	// ── blockquote ──────────────────────────────────────────────────────────
	if strings.HasPrefix(line, "> ") {
		inner := m.renderInline(line[2:])
		m.out(styled(m.noColor, ansiDim, "│ "+inner) + "\n")
		return
	}

	// ── unordered list ──────────────────────────────────────────────────────
	if len(line) >= 2 && (line[0] == '-' || line[0] == '*' || line[0] == '+') && line[1] == ' ' {
		bullet := styled(m.noColor, ansiBrightGreen, "•")
		m.out(bullet + " " + m.renderInline(line[2:]) + "\n")
		return
	}
	if len(line) >= 4 && line[:2] == "  " && (line[2] == '-' || line[2] == '*' || line[2] == '+') && line[3] == ' ' {
		bullet := styled(m.noColor, ansiGreen, "  •")
		m.out(bullet + " " + m.renderInline(line[4:]) + "\n")
		return
	}

	// ── ordered list ────────────────────────────────────────────────────────
	if num, rest, ok := parseOrderedList(line); ok {
		numStr := styled(m.noColor, ansiBrightGreen, fmt.Sprintf("%d.", num))
		m.out(numStr + " " + m.renderInline(rest) + "\n")
		return
	}

	// ── normal paragraph line ────────────────────────────────────────────────
	m.out(m.renderInline(line) + "\n")
}

// codeLine returns a code line styled for inside a fenced code block.
func (m *mdWriter) codeLine(line string) string {
	bar := styled(m.noColor, ansiBrightBlack, "  │")
	return bar + " " + styled(m.noColor, ansiBrightCyan, line)
}

// ── inline rendering ─────────────────────────────────────────────────────────

// renderInline styles **bold**, *italic*, and `code` spans within a single line.
// It works at byte level for ASCII delimiters, which is safe for UTF-8 content.
func (m *mdWriter) renderInline(s string) string {
	if s == "" {
		return s
	}
	var out strings.Builder
	out.Grow(len(s) + 32)
	i := 0
	n := len(s)

	for i < n {
		// ── inline code: `...` ──────────────────────────────────────────────
		if s[i] == '`' {
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				code := s[i+1 : i+1+j]
				out.WriteString(styled(m.noColor, ansiCyan, "`"+code+"`"))
				i = i + 1 + j + 1
				continue
			}
		}

		// ── bold: **...** ───────────────────────────────────────────────────
		if i+1 < n && s[i] == '*' && s[i+1] == '*' {
			if j := strings.Index(s[i+2:], "**"); j >= 0 {
				text := s[i+2 : i+2+j]
				out.WriteString(styled(m.noColor, ansiBold, text))
				i = i + 2 + j + 2
				continue
			}
		}
		if i+1 < n && s[i] == '_' && s[i+1] == '_' {
			if j := strings.Index(s[i+2:], "__"); j >= 0 {
				text := s[i+2 : i+2+j]
				out.WriteString(styled(m.noColor, ansiBold, text))
				i = i + 2 + j + 2
				continue
			}
		}

		// ── italic: *...* ───────────────────────────────────────────────────
		// Only match when not adjacent to another * (to avoid matching **)
		if s[i] == '*' && (i+1 >= n || s[i+1] != '*') {
			if j := strings.IndexByte(s[i+1:], '*'); j >= 0 && s[i+1+j-0] != '*' {
				text := s[i+1 : i+1+j]
				// Avoid matching empty spans or spans starting with space.
				if text != "" && text[0] != ' ' {
					out.WriteString(styled(m.noColor, ansiItalic, text))
					i = i + 1 + j + 1
					continue
				}
			}
		}

		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// ── helpers ──────────────────────────────────────────────────────────────────

func isHorizontalRule(line string) bool {
	s := strings.ReplaceAll(line, " ", "")
	if len(s) < 3 {
		return false
	}
	for _, ch := range []byte{'-', '*', '_'} {
		if strings.IndexByte(s, ch) == 0 && strings.Trim(s, string(ch)) == "" {
			return true
		}
	}
	return false
}

func parseOrderedList(line string) (num int, rest string, ok bool) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line)-1 || line[i] != '.' || line[i+1] != ' ' {
		return 0, "", false
	}
	n := 0
	fmt.Sscanf(line[:i], "%d", &n)
	return n, line[i+2:], true
}
