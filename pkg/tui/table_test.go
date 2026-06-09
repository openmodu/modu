package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestSplitMarkdownTablesBasic(t *testing.T) {
	md := "intro\n\n| 按键 | 说明 |\n|------|------|\n| Enter | 提交消息 |\n| ctrl+c | 中断当前请求 |\n\noutro"
	segs := splitMarkdownTables(md)
	if len(segs) != 3 {
		t.Fatalf("want 3 segments, got %d: %#v", len(segs), segs)
	}
	if segs[0].isTable || segs[2].isTable {
		t.Fatalf("prose segments misclassified as tables")
	}
	tbl := segs[1]
	if !tbl.isTable {
		t.Fatalf("middle segment should be a table")
	}
	if len(tbl.rows) != 3 {
		t.Fatalf("want 3 rows (header+2), got %d: %#v", len(tbl.rows), tbl.rows)
	}
	if tbl.rows[0][0] != "按键" || tbl.rows[1][1] != "提交消息" {
		t.Fatalf("unexpected cells: %#v", tbl.rows)
	}
}

func TestSplitMarkdownTablesIgnoresCodeFence(t *testing.T) {
	md := "```\n| not | a | table |\n|---|---|---|\n```"
	segs := splitMarkdownTables(md)
	for _, s := range segs {
		if s.isTable {
			t.Fatalf("table inside code fence should be ignored: %#v", segs)
		}
	}
}

func TestSplitMarkdownTablesIgnoresIndentedCodeBlock(t *testing.T) {
	md := "    | not | a | table |\n    |---|---|---|"
	segs := splitMarkdownTables(md)
	for _, s := range segs {
		if s.isTable {
			t.Fatalf("table inside indented code block should be ignored: %#v", segs)
		}
	}
}

func TestSplitMarkdownTablesFenceUsesOpeningLength(t *testing.T) {
	md := "````\n| not | a | table |\n|---|---|---|\n```\nstill code\n````\n\n| ok | table |\n|---|---|\n| a | b |"
	segs := splitMarkdownTables(md)
	var tables []mdSegment
	for _, s := range segs {
		if s.isTable {
			tables = append(tables, s)
		}
	}
	if len(tables) != 1 {
		t.Fatalf("want only the table after the four-backtick fence, got %d: %#v", len(tables), segs)
	}
	if got := tables[0].rows[0][0]; got != "ok" {
		t.Fatalf("unexpected table detected: first header cell=%q rows=%#v", got, tables[0].rows)
	}
}

func TestParseTableAligns(t *testing.T) {
	a := parseTableAligns("|:---|:--:|---:|")
	want := []lipgloss.Position{lipgloss.Left, lipgloss.Center, lipgloss.Right}
	if len(a) != 3 || a[0] != want[0] || a[1] != want[1] || a[2] != want[2] {
		t.Fatalf("alignment mismatch: %#v", a)
	}
}

func TestAssistantMarkdownTableWidthDoesNotExceedContentWidth(t *testing.T) {
	for _, viewWidth := range []int{20, 24, 40, 120} {
		tableWidth := assistantMarkdownTableWidth(viewWidth)
		contentWidth := widthForPrefix(viewWidth)
		if tableWidth > contentWidth {
			t.Fatalf("view width %d: table width %d exceeds content width %d", viewWidth, tableWidth, contentWidth)
		}
	}
}

func TestRenderMarkdownTableCJKAligned(t *testing.T) {
	seg := mdSegment{
		isTable: true,
		rows:    [][]string{{"按键", "说明"}, {"Enter", "提交消息"}, {"ctrl+c", "中断当前请求"}},
		aligns:  []lipgloss.Position{lipgloss.Left, lipgloss.Left},
	}
	out := renderMarkdownTableSegment(seg, 80)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Box must be present (top + header rule + bottom borders).
	if !strings.HasPrefix(lines[0], "┏") || !strings.HasPrefix(lines[len(lines)-1], "┗") {
		t.Fatalf("missing box borders:\n%s", out)
	}
	// All rendered lines must share the same display width (CJK-aware).
	w0 := lipgloss.Width(lines[0])
	for i, ln := range lines {
		if lipgloss.Width(ln) != w0 {
			t.Fatalf("line %d width %d != %d (misaligned):\n%s", i, lipgloss.Width(ln), w0, out)
		}
	}
}

func TestRenderMarkdownTableWidthCap(t *testing.T) {
	seg := mdSegment{
		isTable: true,
		rows:    [][]string{{"a", "b"}, {"x", strings.Repeat("长", 100)}},
		aligns:  []lipgloss.Position{lipgloss.Left, lipgloss.Left},
	}
	out := renderMarkdownTableSegment(seg, 40)
	for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if lipgloss.Width(ln) > 40 {
			t.Fatalf("line exceeds max width 40: width=%d\n%s", lipgloss.Width(ln), out)
		}
	}
}
