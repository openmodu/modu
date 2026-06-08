package tui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
)

// tableDelimiterRe matches a GFM table delimiter row such as "|---|:--:|".
// The whole line must consist only of pipes, dashes, colons and spaces and
// contain at least one dash; the caller additionally requires a pipe so that
// a bare "---" horizontal rule is not mistaken for a single-column table.
var tableDelimiterRe = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$`)

// inlineCodeRe matches a GFM inline-code span (single backtick delimited).
var inlineCodeRe = regexp.MustCompile("`([^`]+)`")

// renderInlineCellMarkdown styles inline-code spans inside a table cell so the
// backtick delimiters are dropped and the code is colored, instead of leaking
// literal backticks into the rendered table. lipgloss.Width strips the added
// ANSI before measuring, so column sizing stays correct.
func renderInlineCellMarkdown(s string) string {
	return inlineCodeRe.ReplaceAllStringFunc(s, func(m string) string {
		return uiCodeText.Render(m[1 : len(m)-1])
	})
}

// mdSegment is a contiguous run of assistant markdown that is either a GFM
// table (rendered with lipgloss) or plain markdown (rendered with glamour).
type mdSegment struct {
	isTable bool
	text    string             // plain markdown, when isTable is false
	rows    [][]string         // rows[0] is the header, when isTable is true
	aligns  []lipgloss.Position // per-column alignment from the delimiter row
}

// splitMarkdownTables segments content into ordered table / non-table runs.
// Lines inside fenced code blocks are never treated as tables.
func splitMarkdownTables(content string) []mdSegment {
	lines := strings.Split(content, "\n")
	var segs []mdSegment
	var buf []string
	flush := func() {
		if len(buf) > 0 {
			segs = append(segs, mdSegment{text: strings.Join(buf, "\n")})
			buf = nil
		}
	}

	inFence := false
	fenceMarker := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if inFence {
			buf = append(buf, line)
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = true
			fenceMarker = trimmed[:3]
			buf = append(buf, line)
			continue
		}

		if strings.Contains(line, "|") && i+1 < len(lines) &&
			strings.Contains(lines[i+1], "|") && tableDelimiterRe.MatchString(lines[i+1]) {
			seg := mdSegment{isTable: true}
			seg.aligns = parseTableAligns(lines[i+1])
			seg.rows = append(seg.rows, parseTableRow(line))

			j := i + 2
			for j < len(lines) {
				bt := strings.TrimSpace(lines[j])
				if bt == "" || !strings.Contains(lines[j], "|") ||
					strings.HasPrefix(bt, "```") || strings.HasPrefix(bt, "~~~") {
					break
				}
				seg.rows = append(seg.rows, parseTableRow(lines[j]))
				j++
			}

			flush()
			segs = append(segs, seg)
			i = j - 1
			continue
		}

		buf = append(buf, line)
	}
	flush()
	return segs
}

// parseTableRow splits a table row on unescaped pipes and drops the empty
// cells produced by leading/trailing border pipes.
func parseTableRow(line string) []string {
	var cells []string
	var cur strings.Builder
	runes := []rune(line)
	for k := 0; k < len(runes); k++ {
		if runes[k] == '\\' && k+1 < len(runes) && runes[k+1] == '|' {
			cur.WriteRune('|')
			k++
			continue
		}
		if runes[k] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteRune(runes[k])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))

	if len(cells) > 0 && cells[0] == "" {
		cells = cells[1:]
	}
	if len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	return cells
}

// parseTableAligns reads ":---", "---:" and ":--:" markers from the delimiter
// row into lipgloss alignment positions.
func parseTableAligns(delim string) []lipgloss.Position {
	cells := parseTableRow(delim)
	aligns := make([]lipgloss.Position, len(cells))
	for i, c := range cells {
		left := strings.HasPrefix(c, ":")
		right := strings.HasSuffix(c, ":")
		switch {
		case left && right:
			aligns[i] = lipgloss.Center
		case right:
			aligns[i] = lipgloss.Right
		default:
			aligns[i] = lipgloss.Left
		}
	}
	return aligns
}

// renderMarkdownTableSegment renders a parsed table with square borders and a
// header rule (no inter-row separators). It sizes to content but caps the
// total width at maxWidth, wrapping cell text inside the box when needed.
func renderMarkdownTableSegment(seg mdSegment, maxWidth int) string {
	if len(seg.rows) == 0 {
		return ""
	}

	ncol := 0
	for _, r := range seg.rows {
		if len(r) > ncol {
			ncol = len(r)
		}
	}
	if ncol == 0 {
		return ""
	}
	norm := func(r []string) []string {
		out := make([]string, ncol)
		for i := 0; i < ncol; i++ {
			if i < len(r) {
				out[i] = renderInlineCellMarkdown(r[i])
			}
		}
		return out
	}

	build := func(width int) string {
		t := table.New().
			Border(lipgloss.ThickBorder()).
			BorderStyle(uiMutedText).
			BorderRow(true).
			Headers(norm(seg.rows[0])...).
			StyleFunc(func(row, col int) lipgloss.Style {
				st := lipgloss.NewStyle().Padding(0, 1)
				if col >= 0 && col < len(seg.aligns) {
					st = st.Align(seg.aligns[col])
				}
				if row == table.HeaderRow {
					st = st.Bold(true)
				}
				return st
			})
		for _, r := range seg.rows[1:] {
			t = t.Row(norm(r)...)
		}
		if width > 0 {
			t = t.Width(width)
		}
		return t.Render()
	}

	out := build(0)
	if maxWidth > 0 {
		widest := 0
		for _, line := range strings.Split(out, "\n") {
			if w := lipgloss.Width(line); w > widest {
				widest = w
			}
		}
		if widest > maxWidth {
			out = build(maxWidth)
		}
	}
	return out
}

// renderAssistantMarkdown renders assistant content, routing GFM tables
// through the lipgloss table renderer (square borders, CJK-correct widths)
// and everything else through glamour. tableWidth caps table width to the
// available content width so rendered tables never re-wrap downstream.
func renderAssistantMarkdown(renderer *glamour.TermRenderer, content string, tableWidth int) (string, error) {
	segs := splitMarkdownTables(content)
	hasTable := false
	for _, s := range segs {
		if s.isTable {
			hasTable = true
			break
		}
	}
	if !hasTable {
		md, err := renderer.Render(content)
		if err != nil {
			return "", err
		}
		return normalizeRenderedMarkdown(md), nil
	}

	var parts []string
	for _, s := range segs {
		if s.isTable {
			parts = append(parts, renderMarkdownTableSegment(s, tableWidth))
			continue
		}
		if strings.TrimSpace(s.text) == "" {
			continue
		}
		md, err := renderer.Render(s.text)
		if err != nil {
			return "", err
		}
		parts = append(parts, normalizeRenderedMarkdown(md))
	}
	return strings.Join(parts, "\n\n"), nil
}
