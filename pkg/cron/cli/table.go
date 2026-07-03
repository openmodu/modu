package cli

import (
	"fmt"
	"io"
	"strings"

	runewidth "github.com/mattn/go-runewidth"
)

type tableColumn struct {
	Header string
	Max    int
	Right  bool
}

func writeTable(out io.Writer, cols []tableColumn, rows [][]string) {
	if len(cols) == 0 {
		return
	}
	widths := tableWidths(cols, rows)
	writeTableRule(out, widths)
	writeTableRow(out, cols, widths, tableHeaders(cols))
	writeTableRule(out, widths)
	for _, row := range rows {
		writeTableRow(out, cols, widths, row)
	}
	writeTableRule(out, widths)
}

func tableHeaders(cols []tableColumn) []string {
	headers := make([]string, len(cols))
	for i, col := range cols {
		headers[i] = col.Header
	}
	return headers
}

func tableWidths(cols []tableColumn, rows [][]string) []int {
	widths := make([]int, len(cols))
	for i, col := range cols {
		widths[i] = runewidth.StringWidth(col.Header)
	}
	for _, row := range rows {
		for i := range cols {
			cell := ""
			if i < len(row) {
				cell = cleanTableCell(row[i])
			}
			cell = truncateDisplay(cell, cols[i].Max)
			if w := runewidth.StringWidth(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}

func writeTableRule(out io.Writer, widths []int) {
	for _, width := range widths {
		fmt.Fprint(out, "+")
		fmt.Fprint(out, strings.Repeat("-", width+2))
	}
	fmt.Fprintln(out, "+")
}

func writeTableRow(out io.Writer, cols []tableColumn, widths []int, row []string) {
	for i, col := range cols {
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		cell = truncateDisplay(cleanTableCell(cell), col.Max)
		pad := widths[i] - runewidth.StringWidth(cell)
		if pad < 0 {
			pad = 0
		}
		if col.Right {
			fmt.Fprintf(out, "| %s%s ", strings.Repeat(" ", pad), cell)
		} else {
			fmt.Fprintf(out, "| %s%s ", cell, strings.Repeat(" ", pad))
		}
	}
	fmt.Fprintln(out, "|")
}

func cleanTableCell(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.Join(strings.Fields(s), " ")
}

func truncateDisplay(s string, max int) string {
	if max <= 0 || runewidth.StringWidth(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	var b strings.Builder
	limit := max - 1
	width := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if width+rw > limit {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String() + "…"
}
