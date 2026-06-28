package modutui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
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

func TestPOC2InfoCardStaysAtTopAfterFirstMessage(t *testing.T) {
	var submitted string
	var tm tea.Model = NewModel(Options{
		Width:         48,
		Height:        10,
		InfoCardLines: []string{"modu_code", "model: Test", "commands: type /"},
		Hooks: Hooks{Submit: func(text string) {
			submitted = text
		}},
	})

	m := tm.(Model)
	rendered := ansi.Strip(m.render())
	for _, want := range []string{"┏", "modu_code", "model: Test", "commands: type /"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("initial info card missing %q:\n%s", want, rendered)
		}
	}

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "h", Code: 'h'}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "i", Code: 'i'}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = tm.(Model)

	if got, want := submitted, "hi"; got != want {
		t.Fatalf("submitted = %q, want %q", got, want)
	}
	afterSubmit := ansi.Strip(m.render())
	if !strings.Contains(afterSubmit, "commands: type /") {
		t.Fatalf("info card should stay at the top after the first submitted message:\n%s", afterSubmit)
	}
	if !strings.Contains(afterSubmit, "❯ hi") {
		t.Fatalf("submitted message should render below the info card:\n%s", afterSubmit)
	}
}

func TestPOC2PreformattedAssistantMessagePreservesSlashHelpLines(t *testing.T) {
	m := NewModel(Options{
		Width:  72,
		Height: 12,
		InitialMessages: []Message{{
			Role:         RoleAssistant,
			Text:         "Help\n/help, /h           — show this help\n/quit, /exit        — exit\n\nkeys\nctrl+j         — insert newline",
			Preformatted: true,
		}},
	})

	rendered := ansi.Strip(m.render())
	for _, want := range []string{
		"● Help",
		"  /help, /h           — show this help",
		"  /quit, /exit        — exit",
		"  keys",
		"  ctrl+j         — insert newline",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("preformatted help missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Help /help") {
		t.Fatalf("preformatted help should not collapse newlines into a paragraph:\n%s", rendered)
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

func TestPOC2LargePasteCollapsesInInputAndSubmitsExpandedText(t *testing.T) {
	pasted := strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
	}, "\n")
	var submitted string
	var tm tea.Model = NewModel(Options{
		Width:  72,
		Height: 10,
		Hooks: Hooks{Submit: func(text string) {
			submitted = text
		}},
	})
	tm, _ = tm.Update(tea.PasteMsg{Content: pasted})

	m := tm.(Model)
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "[Pasted text 6 lines]") {
		t.Fatalf("large paste should render as a collapsed label:\n%s", rendered)
	}
	if strings.Contains(rendered, "line 6") {
		t.Fatalf("large paste content should not be expanded in the input:\n%s", rendered)
	}

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = tm.(Model)
	if got := submitted; got != pasted {
		t.Fatalf("submitted paste = %q, want %q", got, pasted)
	}
	if len(m.messages) != 1 || m.messages[0].Text != pasted {
		t.Fatalf("transcript message should keep the expanded paste: %#v", m.messages)
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

func TestPOC2SubmitMessageReportsPromptFollowUpAndSteer(t *testing.T) {
	tests := []struct {
		name string
		busy bool
		key  tea.Key
		want SubmitKind
	}{
		{name: "prompt", key: tea.Key{Code: tea.KeyEnter}, want: SubmitKindPrompt},
		{name: "followup", busy: true, key: tea.Key{Code: tea.KeyEnter}, want: SubmitKindFollowUp},
		{name: "steer", busy: true, key: tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}, want: SubmitKindSteer},
		{name: "idle shift enter prompts", key: tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}, want: SubmitKindPrompt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got SubmitEvent
			var tm tea.Model = NewModel(Options{
				Hooks: Hooks{SubmitMessage: func(ev SubmitEvent) {
					got = ev
				}},
			})
			if tt.busy {
				tm, _ = tm.Update(SetBusyMsg{Busy: true})
			}
			tm, _ = tm.Update(tea.PasteMsg{Content: "next instruction"})
			tm, _ = tm.Update(tea.KeyPressMsg(tt.key))

			if got.Text != "next instruction" || got.Kind != tt.want {
				t.Fatalf("submit event = %#v, want text %q kind %q", got, "next instruction", tt.want)
			}
		})
	}
}

func TestPOC2SlashPickerCompletesCommandWithTab(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:         50,
		Height:        10,
		SlashCommands: []SlashCommand{{Name: "/help", Description: "Show help"}},
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	m := tm.(Model)
	if got := ansi.Strip(m.render()); !strings.Contains(got, "/help") || !strings.Contains(got, "┏") {
		t.Fatalf("slash picker not rendered:\n%s", got)
	}

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = tm.(Model)
	if got, want := m.input.Value, "/help "; got != want {
		t.Fatalf("completed slash input = %q, want %q", got, want)
	}
	if len(m.slashMatches) != 0 {
		t.Fatalf("slash matches should clear after completion: %#v", m.slashMatches)
	}
}

func TestPOC2SlashCommandHookReceivesSelectedCommand(t *testing.T) {
	var submitted string
	var slashLine string
	var tm tea.Model = NewModel(Options{
		Width:         50,
		Height:        10,
		SlashCommands: []SlashCommand{{Name: "/help", Description: "Show help"}},
		Hooks: Hooks{
			Submit: func(text string) {
				submitted = text
			},
			SlashCommand: func(line string) {
				slashLine = line
			},
		},
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if got, want := slashLine, "/help"; got != want {
		t.Fatalf("slash command line = %q, want %q", got, want)
	}
	if submitted != "" {
		t.Fatalf("normal submit should not run for slash command, got %q", submitted)
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
	if !strings.Contains(rendered, "Approval required for Bash") || !strings.Contains(rendered, "[y] allow") {
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
			Detail:   "go test ./...",
		},
		Respond: make(chan ToolApprovalDecision, 1),
	})

	m := tm.(Model)
	rendered := strings.Split(ansi.Strip(m.render()), "\n")
	if got, want := len(rendered), m.height; got != want {
		t.Fatalf("rendered lines = %d, want %d:\n%s", got, want, strings.Join(rendered, "\n"))
	}
	panelTop := m.vpHeight()
	if !strings.HasPrefix(rendered[panelTop], "┏") {
		t.Fatalf("approval panel should start immediately below viewport at line %d:\n%s", panelTop, strings.Join(rendered, "\n"))
	}
	if got, want := approvalBorderStyle.GetForeground(), lipgloss.Color("248"); got != want {
		t.Fatalf("approval border color = %#v, want %#v", got, want)
	}
	inputRule := m.vpHeight() + m.approvalPanelHeight()
	if got, want := rendered[inputRule], strings.Repeat("─", m.width); got != want {
		t.Fatalf("input top rule line = %q, want %q", got, want)
	}
	if !strings.Contains(strings.Join(rendered[panelTop:inputRule], "\n"), "[y] allow") {
		t.Fatalf("approval panel should include actions:\n%s", strings.Join(rendered[panelTop:inputRule], "\n"))
	}
	if !strings.Contains(strings.Join(rendered[panelTop:inputRule], "\n"), "Bash command:") ||
		!strings.Contains(strings.Join(rendered[panelTop:inputRule], "\n"), "go test ./...") {
		t.Fatalf("approval panel should include command preview:\n%s", strings.Join(rendered[panelTop:inputRule], "\n"))
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
