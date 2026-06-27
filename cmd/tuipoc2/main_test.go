package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPOC2PageKeysScrollViewport(t *testing.T) {
	var tm tea.Model = newModel()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 8})

	m := tm.(model)
	for range 60 {
		m.messages = append(m.messages, message{role: roleAssistant, text: "history line"})
	}
	m.follow = true
	m.rebuild()
	if m.yOffset == 0 {
		t.Fatal("setup should be scrollable")
	}

	before := m.yOffset
	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	afterUp := tm.(model)
	if afterUp.yOffset >= before {
		t.Fatalf("PageUp did not scroll up: %d -> %d", before, afterUp.yOffset)
	}
	if afterUp.follow {
		t.Fatal("PageUp away from bottom should disable follow")
	}

	tm, _ = afterUp.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	afterDown := tm.(model)
	if afterDown.yOffset <= afterUp.yOffset {
		t.Fatalf("PageDown did not scroll down: %d -> %d", afterUp.yOffset, afterDown.yOffset)
	}
}

func TestPOC2ResizeClampsSelection(t *testing.T) {
	m := newModel()
	m.width, m.height = 80, 12
	for range 20 {
		m.messages = append(m.messages, message{role: roleAssistant, text: "history line"})
	}
	m.rebuild()
	if len(m.lines) == 0 {
		t.Fatal("setup should produce transcript lines")
	}

	m.selStart = cell{line: 0, col: 0}
	m.selEnd = cell{line: len(m.lines) + 50, col: 999}
	m.messages = []message{{role: roleAssistant, text: "short"}}
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
	m := newModel()
	m.width, m.height = 24, 8
	m.messages = []message{
		{role: roleUser, text: strings.Repeat("a", 120)},
		{tool: true, summary: strings.Repeat("tool", 30), detail: strings.Repeat("detail", 30), expanded: true},
	}
	m.input = strings.Repeat("input", 30)
	m.cursor = m.inputLen()
	m.rebuild()

	for i, line := range strings.Split(m.render(), "\n") {
		if got := ansi.StringWidth(line); got > m.width {
			t.Fatalf("render line %d width = %d, want <= %d: %q", i, got, m.width, line)
		}
	}
}

func TestPOC2PasteStaysSingleLine(t *testing.T) {
	var tm tea.Model = newModel()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 30, Height: 8})
	tm, _ = tm.Update(tea.PasteMsg{Content: "alpha\nbeta\rgamma\r\ndelta"})

	m := tm.(model)
	if strings.ContainsAny(m.input, "\r\n") {
		t.Fatalf("paste left newline characters in input: %q", m.input)
	}
	if got, want := m.input, "alpha beta gamma delta"; got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
}
