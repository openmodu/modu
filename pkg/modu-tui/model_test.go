package modutui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPOC2PageKeysScrollViewport(t *testing.T) {
	var tm tea.Model = NewModel()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 8})

	m := tm.(Model)
	for range 60 {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history line"})
	}
	m.follow = true
	m.rebuild()
	if m.yOffset == 0 {
		t.Fatal("setup should be scrollable")
	}

	before := m.yOffset
	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	afterUp := tm.(Model)
	if afterUp.yOffset >= before {
		t.Fatalf("PageUp did not scroll up: %d -> %d", before, afterUp.yOffset)
	}
	if afterUp.follow {
		t.Fatal("PageUp away from bottom should disable follow")
	}

	tm, _ = afterUp.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	afterDown := tm.(Model)
	if afterDown.yOffset <= afterUp.yOffset {
		t.Fatalf("PageDown did not scroll down: %d -> %d", afterUp.yOffset, afterDown.yOffset)
	}
}

func TestPOC2ResizeClampsSelection(t *testing.T) {
	m := NewModel()
	m.width, m.height = 80, 12
	for range 20 {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history line"})
	}
	m.rebuild()
	if len(m.lines) == 0 {
		t.Fatal("setup should produce transcript lines")
	}

	m.selStart = cell{line: 0, col: 0}
	m.selEnd = cell{line: len(m.lines) + 50, col: 999}
	m.messages = []Message{{Role: RoleAssistant, Text: "short"}}
	m.width, m.height = 20, 8
	m.rebuild()

	if !m.hasSelection() {
		t.Fatal("selection should be retained and clamped")
	}
	if m.selStart.line < 0 || m.selStart.line >= len(m.lines) {
		t.Fatalf("selStart line out of range after resize: %+v, lines=%d", m.selStart, len(m.lines))
	}
	if m.selEnd.line < 0 || m.selEnd.line >= len(m.lines) {
		t.Fatalf("selEnd line out of range after resize: %+v, lines=%d", m.selEnd, len(m.lines))
	}
	_ = m.selectedText()
	for i := range m.lines {
		_ = m.highlightLine(i)
	}
}

func TestPOC2RenderConstrainsLineWidths(t *testing.T) {
	m := NewModel()
	m.width, m.height = 24, 8
	m.messages = []Message{
		{Role: RoleUser, Text: strings.Repeat("a", 120)},
		{Tool: true, Summary: strings.Repeat("tool", 30), Detail: strings.Repeat("detail", 30), Expanded: true},
	}
	m.input.Value = strings.Repeat("input", 30)
	m.input.Cursor = m.input.Len()
	m.rebuild()

	for i, line := range strings.Split(m.render(), "\n") {
		if got := ansi.StringWidth(line); got > m.width {
			t.Fatalf("render line %d width = %d, want <= %d: %q", i, got, m.width, line)
		}
	}
}

func TestPOC2InputHasTopAndBottomRules(t *testing.T) {
	m := NewModel(Options{Width: 16, Height: 8})
	lines := strings.Split(m.render(), "\n")
	if got, want := len(lines), m.height; got != want {
		t.Fatalf("rendered line count = %d, want %d", got, want)
	}
	topRule := ansi.Strip(lines[m.vpHeight()])
	bottomRule := ansi.Strip(lines[m.vpHeight()+2])
	wantRule := strings.Repeat("─", m.width)
	if topRule != wantRule {
		t.Fatalf("top input rule = %q, want %q", topRule, wantRule)
	}
	if bottomRule != wantRule {
		t.Fatalf("bottom input rule = %q, want %q", bottomRule, wantRule)
	}
}

func TestPOC2AddsGapBetweenBlocks(t *testing.T) {
	m := NewModel(Options{
		Width:  40,
		Height: 12,
		InitialMessages: []Message{
			{Role: RoleUser, Text: "alpha"},
			{Role: RoleUser, Text: "beta"},
		},
	})
	lines := m.Lines()
	blankBetween := false
	for i := 1; i < len(lines)-1; i++ {
		if strings.TrimSpace(ansi.Strip(lines[i])) == "" &&
			strings.Contains(ansi.Strip(lines[i-1]), "alpha") &&
			strings.Contains(ansi.Strip(lines[i+1]), "beta") {
			blankBetween = true
			break
		}
	}
	if !blankBetween {
		t.Fatalf("expected a blank line between blocks:\n%s", strings.Join(lines, "\n"))
	}
}

func TestPOC2PasteStaysSingleLine(t *testing.T) {
	var tm tea.Model = NewModel()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	tm, _ = tm.Update(tea.PasteMsg{Content: "alpha\nbeta\rgamma\r\ndelta"})

	m := tm.(Model)
	if strings.ContainsAny(m.input.Value, "\r\n") {
		t.Fatalf("paste left newline characters in input: %q", m.input.Value)
	}
	if got, want := m.input.Value, "alpha beta gamma delta"; got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}
