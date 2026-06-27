package modutui

import (
	"strings"
	"testing"
	"time"

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

func TestPOC2RenderPadsEveryLineToTerminalWidth(t *testing.T) {
	m := NewModel(Options{
		Width:  32,
		Height: 10,
		InitialMessages: []Message{
			{Role: RoleAssistant, Text: "short"},
		},
	})

	lines := strings.Split(m.render(), "\n")
	if got, want := len(lines), m.height; got != want {
		t.Fatalf("rendered line count = %d, want %d", got, want)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != m.width {
			t.Fatalf("render line %d width = %d, want %d: %q", i, got, m.width, line)
		}
	}
}

func TestPOC2JumpHintStaysInFixedRowAboveInput(t *testing.T) {
	m := NewModel(Options{Width: 72, Height: 8})
	for i := 0; i < 20; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history"})
	}
	m.rebuild()
	m.scroll(-2)

	rendered := ansi.Strip(m.render())
	if got := strings.Count(rendered, jumpHintText()); got != 1 {
		t.Fatalf("jump hint count = %d, want 1:\n%s", got, rendered)
	}
	lines := strings.Split(rendered, "\n")
	jumpRow := m.vpHeight() + m.approvalPanelHeight()
	if !strings.Contains(lines[jumpRow], jumpHintText()) {
		t.Fatalf("jump hint should render in the fixed row above input:\n%s", rendered)
	}
	if strings.Contains(lines[len(lines)-1], jumpHintText()) {
		t.Fatalf("jump hint should not render in status line:\n%s", rendered)
	}
}

func TestPOC2JumpRowClickScrollsToBottom(t *testing.T) {
	m := NewModel(Options{Width: 72, Height: 8})
	for i := 0; i < 20; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history"})
	}
	m.rebuild()
	m.scroll(-2)
	if m.atBottom() {
		t.Fatal("setup should be scrolled away from bottom")
	}

	_ = m.onPress(1, m.vpHeight()+m.approvalPanelHeight())
	if !m.atBottom() {
		t.Fatalf("jump row click should scroll to bottom, offset=%d max=%d", m.yOffset, m.maxOffset())
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

func TestPOC2SubmitHookReceivesEnteredText(t *testing.T) {
	var submitted string
	var tm tea.Model = NewModel(Options{
		Hooks: Hooks{Submit: func(text string) {
			submitted = text
		}},
	})
	tm, _ = tm.Update(tea.PasteMsg{Content: "hello"})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	m := tm.(Model)
	if got, want := submitted, "hello"; got != want {
		t.Fatalf("submitted = %q, want %q", got, want)
	}
	if got := m.input.Value; got != "" {
		t.Fatalf("input should reset after submit, got %q", got)
	}
	if len(m.messages) != 1 || m.messages[0].Role != RoleUser || m.messages[0].Text != "hello" {
		t.Fatalf("submitted message not appended: %#v", m.messages)
	}
}

func TestPOC2AcceptsExternalMessagesAndBusyState(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 40, Height: 8})
	tm, _ = tm.Update(AppendMessageMsg{Message: Message{Role: RoleAssistant, Text: "external reply"}})
	tm, _ = tm.Update(SetBusyMsg{Busy: true})

	m := tm.(Model)
	if got := strings.Join(m.Lines(), "\n"); !strings.Contains(ansi.Strip(got), "external reply") {
		t.Fatalf("external message missing:\n%s", got)
	}
	if got := ansi.Strip(m.render()); !strings.Contains(got, "busy") {
		t.Fatalf("busy state missing:\n%s", got)
	}
}

func TestPOC2MergesToolMessagesByToolID(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 80, Height: 12})
	tm, _ = tm.Update(AppendMessageMsg{Message: Message{
		Tool:      true,
		ToolID:    "call-1",
		ToolName:  "bash",
		Summary:   "Running shell command",
		ToolInput: "go test ./...",
	}})
	tm, _ = tm.Update(AppendMessageMsg{Message: Message{
		Tool:       true,
		ToolID:     "call-1",
		ToolName:   "bash",
		Summary:    "Ran 1 shell command",
		ToolOutput: "ok ./pkg/modu-tui",
		ToolDone:   true,
	}})

	m := tm.(Model)
	if len(m.messages) != 1 {
		t.Fatalf("tool messages should merge into one block, got %d: %#v", len(m.messages), m.messages)
	}
	if got := m.messages[0].Summary; got != "Ran 1 shell command" {
		t.Fatalf("merged summary = %q, want Ran 1 shell command", got)
	}
}

func TestPOC2InitialToolMessagesAreMerged(t *testing.T) {
	m := NewModel(Options{
		Width:  80,
		Height: 12,
		InitialMessages: []Message{
			{Tool: true, ToolID: "call-1", ToolName: "bash", Summary: "Running shell command", ToolInput: "git diff --stat"},
			{Tool: true, ToolID: "call-1", ToolName: "bash", Summary: "Ran 1 shell command", ToolOutput: "1 file changed", ToolDone: true},
		},
	})
	if len(m.messages) != 1 {
		t.Fatalf("initial tool messages should merge into one block, got %d: %#v", len(m.messages), m.messages)
	}
}

func TestPOC2ExpandedToolBlockCanCollapseFromAnyRenderedLine(t *testing.T) {
	m := NewModel(Options{
		Width:  80,
		Height: 12,
		InitialMessages: []Message{{
			Tool:       true,
			ToolID:     "call-1",
			ToolName:   "bash",
			Summary:    "Ran 1 shell command",
			ToolInput:  "go test ./pkg/modu-tui",
			ToolOutput: "ok ./pkg/modu-tui",
			ToolDone:   true,
			Expanded:   true,
		}},
	})
	if !m.messages[0].Expanded {
		t.Fatal("setup should start expanded")
	}
	if _, ok := m.headers[1]; !ok {
		t.Fatalf("expanded tool output line should be clickable, headers=%#v", m.headers)
	}

	_ = m.onPress(1, 1)
	if m.messages[0].Expanded {
		t.Fatal("clicking an expanded tool output line should collapse the block")
	}
}

func TestPOC2ToolApprovalResolvesFromKeyboard(t *testing.T) {
	results := make(chan ToolApprovalResult, 1)
	decisions := make(chan ToolApprovalDecision, 1)
	var tm tea.Model = NewModel(Options{
		Width:  80,
		Height: 10,
		Hooks: Hooks{ToolApprovalDecision: func(result ToolApprovalResult) {
			results <- result
		}},
	})
	tm, _ = tm.Update(RequestToolApprovalMsg{
		Request: ToolApprovalRequest{
			ID:       "call-1",
			ToolName: "bash",
			Detail:   `{"command":"go test ./..."}`,
		},
		Respond: decisions,
	})

	pending := tm.(Model)
	if pending.approval == nil {
		t.Fatal("expected pending approval")
	}
	rendered := ansi.Strip(pending.render())
	if !strings.Contains(rendered, "Tool approval: bash") || !strings.Contains(rendered, "[y] allow") {
		t.Fatalf("pending approval not rendered:\n%s", rendered)
	}
	if got := strings.Join(pending.Lines(), "\n"); strings.Contains(ansi.Strip(got), "approval required") {
		t.Fatalf("approval should not be part of transcript lines:\n%s", got)
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "a", Code: 'a'}))
	resolved := tm.(Model)
	if resolved.approval != nil {
		t.Fatal("approval should clear after decision")
	}
	select {
	case got := <-decisions:
		if got != ToolApprovalAllowAlways {
			t.Fatalf("decision = %q, want %q", got, ToolApprovalAllowAlways)
		}
	case <-time.After(time.Second):
		t.Fatal("expected approval decision")
	}
	select {
	case got := <-results:
		if got.Request.ID != "call-1" || got.Decision != ToolApprovalAllowAlways {
			t.Fatalf("hook result = %#v", got)
		}
	default:
		t.Fatal("expected approval hook result")
	}
}

func TestPOC2ToolApprovalPanelIsFixedAboveInput(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:  50,
		Height: 12,
		InitialMessages: []Message{
			{Role: RoleAssistant, Text: strings.Repeat("history\n", 12)},
		},
	})
	tm, _ = tm.Update(RequestToolApprovalMsg{
		Request: ToolApprovalRequest{
			ID:       "call-1",
			ToolName: "bash",
			Summary:  "approval required: bash",
			Detail:   `{"command":"go test ./..."}`,
		},
		Respond: make(chan ToolApprovalDecision, 1),
	})

	m := tm.(Model)
	rendered := strings.Split(ansi.Strip(m.render()), "\n")
	if got, want := len(rendered), m.height; got != want {
		t.Fatalf("rendered lines = %d, want %d:\n%s", got, want, strings.Join(rendered, "\n"))
	}
	panelTop := m.vpHeight()
	if !strings.HasPrefix(rendered[panelTop], "╭") {
		t.Fatalf("approval panel should start immediately below viewport at line %d:\n%s", panelTop, strings.Join(rendered, "\n"))
	}
	inputRule := m.vpHeight() + m.approvalPanelHeight()
	if got, want := rendered[inputRule], strings.Repeat("─", m.width); got != want {
		t.Fatalf("input top rule line = %q, want %q", got, want)
	}
	if !strings.Contains(strings.Join(rendered[panelTop:inputRule], "\n"), "[y] allow") {
		t.Fatalf("approval panel should include actions:\n%s", strings.Join(rendered[panelTop:inputRule], "\n"))
	}
}

func TestPOC2ToolApprovalBlocksInputEditing(t *testing.T) {
	var tm tea.Model = NewModel()
	tm, _ = tm.Update(RequestToolApprovalMsg{
		Request: ToolApprovalRequest{ID: "call-1", ToolName: "read"},
		Respond: make(chan ToolApprovalDecision, 1),
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "x", Code: 'x'}))

	m := tm.(Model)
	if got := m.input.Value; got != "" {
		t.Fatalf("approval mode should not edit input, got %q", got)
	}
	if m.approval == nil {
		t.Fatal("approval should remain pending after unrelated key")
	}
}
