package modutui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestPOC2MultilineInputAltEnterAndAutoHeight(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 40, Height: 20})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	// Alt+Enter inserts a hard newline rather than submitting.
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModAlt}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Text: "b"}))
	m := tm.(Model)

	if got := m.input.ExpandedValue(); got != "a\nb" {
		t.Fatalf("input value = %q, want %q", got, "a\nb")
	}
	if got := m.inputRows(); got != 2 {
		t.Fatalf("inputRows = %d, want 2", got)
	}
	if got, want := m.bottomFixedRows(), bottomFixedRowsBase+2; got != want {
		t.Fatalf("bottomFixedRows = %d, want %d", got, want)
	}
	lines, cursorRow, _ := m.input.Render(m.inputRenderWidth(), maxInputRows)
	if len(lines) != 2 {
		t.Fatalf("rendered input lines = %d, want 2", len(lines))
	}
	if cursorRow != 1 {
		t.Fatalf("cursorRow = %d, want 1 (caret on second line)", cursorRow)
	}
	if !strings.Contains(ansi.Strip(lines[0]), "❯") {
		t.Fatalf("first line should carry the ❯ prefix: %q", ansi.Strip(lines[0]))
	}

	// Input height is capped at maxInputRows even with more logical lines.
	for range maxInputRows + 3 {
		m.input.InsertNewline()
	}
	if got := m.inputRows(); got != maxInputRows {
		t.Fatalf("inputRows = %d, want capped at %d", got, maxInputRows)
	}
	capped, _, _ := m.input.Render(m.inputRenderWidth(), maxInputRows)
	if len(capped) != maxInputRows {
		t.Fatalf("rendered input lines = %d, want capped at %d", len(capped), maxInputRows)
	}
}

func TestPOC2LongInputSoftWrapsAndIncreasesHeight(t *testing.T) {
	m := NewModel(Options{Width: 18, Height: 12})
	m.input.Insert(strings.Repeat("a", 50))
	if got := m.inputRows(); got <= 1 {
		t.Fatalf("inputRows = %d, want soft-wrapped long input to use more than one row", got)
	}
	lines, cursorRow, _ := m.input.Render(m.inputRenderWidth(), maxInputRows)
	if len(lines) != m.inputRows() {
		t.Fatalf("rendered input lines = %d, want %d", len(lines), m.inputRows())
	}
	if cursorRow != len(lines)-1 {
		t.Fatalf("cursorRow = %d, want last rendered line %d", cursorRow, len(lines)-1)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > m.inputRenderWidth() {
			t.Fatalf("wrapped input line exceeds width %d: %q", m.inputRenderWidth(), line)
		}
	}
}

func TestPOC2PageKeysScrollViewport(t *testing.T) {
	var tm tea.Model = NewModel()
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 80, Height: 12})

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

func TestPOC2CopySelectionUsesOSC52OverSSH(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/pts/1")
	// Isolate multiplexer env so the sequence is plain OSC52, not screen/tmux
	// DCS-wrapped, regardless of the ambient TERM/TMUX (e.g. running over SSH
	// inside screen).
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "xterm-256color")
	oldWrite := writeLocalClipboard
	writeLocalClipboard = func(string) error { return nil }
	t.Cleanup(func() { writeLocalClipboard = oldWrite })

	m := NewModel(Options{
		Width:           40,
		Height:          8,
		InitialMessages: []Message{{Role: RoleAssistant, Text: "copy me"}},
	})
	m.selStart = cell{line: 0, col: 2}
	m.selEnd = cell{line: 0, col: 9}

	cmd := m.copySelection()
	if cmd == nil {
		t.Fatal("copySelection should return an OSC52 command over SSH")
	}
	raw, hasSetClipboard := copyCommandMessages(cmd)
	if !strings.Contains(raw, "\x1b]52;c;") || !strings.HasSuffix(raw, "\x07") {
		t.Fatalf("raw clipboard sequence should be OSC52, got %q", raw)
	}
	if !hasSetClipboard {
		t.Fatal("copySelection should also send Bubble Tea SetClipboard for SSH compatibility")
	}
	if !strings.Contains(m.status, "local+OSC52") {
		t.Fatalf("copy status should report OSC52 path, got %q", m.status)
	}
	if !strings.Contains(m.status, "Shift+drag") {
		t.Fatalf("OSC52 copy status should hint at terminal-native Shift+drag fallback, got %q", m.status)
	}
}

func TestPOC2CopySelectionUsesTmuxPassthrough(t *testing.T) {
	t.Setenv("SSH_TTY", "/dev/pts/1")
	t.Setenv("TMUX", "/tmp/tmux")

	seq := clipboardSequence("hi")
	if !strings.Contains(seq, "\x1bPtmux;") || !strings.Contains(seq, "52;c;") {
		t.Fatalf("tmux clipboard sequence missing passthrough wrapper: %q", seq)
	}
}

func TestPOC2ClipboardSequenceScreenTermEmitsBothWrappings(t *testing.T) {
	// TERM=screen-256color is also tmux's default TERM, and over SSH only
	// TERM (not TMUX) is forwarded from the local side — so the actual
	// multiplexer is unknowable. Both wrappings must be emitted so whichever
	// one is really there unwraps its own format.
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "screen-256color")

	seq := clipboardSequence("hi")
	if !strings.Contains(seq, "\x1bPtmux;") {
		t.Fatalf("screen TERM sequence should include tmux passthrough wrapping: %q", seq)
	}
	if !strings.HasPrefix(seq, "\x1bP") || strings.HasPrefix(seq, "\x1bPtmux;") {
		t.Fatalf("screen TERM sequence should start with a screen DCS chunk: %q", seq)
	}
}

func TestPOC2CopySelectionUsesOSC52InsideTmuxWithoutSSHEnv(t *testing.T) {
	// Reattaching to a tmux session over SSH after it was created locally
	// leaves SSH_TTY/SSH_CONNECTION/SSH_CLIENT unset inside the pane, even
	// though the attached client may now be remote. isRemoteSession must
	// treat "inside tmux" itself as reason enough to try OSC52, or a
	// successful local clipboard write on the tmux host would be mistaken
	// for a successful copy to the actual (possibly remote) client.
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("TMUX", "/tmp/tmux")
	oldWrite := writeLocalClipboard
	writeLocalClipboard = func(string) error { return nil }
	t.Cleanup(func() { writeLocalClipboard = oldWrite })

	m := NewModel(Options{
		Width:           40,
		Height:          8,
		InitialMessages: []Message{{Role: RoleAssistant, Text: "copy me"}},
	})
	m.selStart = cell{line: 0, col: 2}
	m.selEnd = cell{line: 0, col: 9}

	cmd := m.copySelection()
	if cmd == nil {
		t.Fatal("copySelection should still try OSC52 inside tmux even without SSH env vars")
	}
	raw, _ := copyCommandMessages(cmd)
	if !strings.Contains(raw, "\x1bPtmux;") || !strings.Contains(raw, "52;c;") {
		t.Fatalf("expected tmux-wrapped OSC52 sequence, got %q", raw)
	}
}

func TestPOC2CopySelectionUsesLocalClipboardWithoutOSC52WhenLocalSucceeds(t *testing.T) {
	// This case asserts the non-remote path, so clear any inherited SSH/tmux/
	// screen env (e.g. when the test itself runs over SSH or inside tmux).
	t.Setenv("SSH_TTY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("TMUX", "")
	t.Setenv("STY", "")
	t.Setenv("TERM", "xterm-256color")
	oldWrite := writeLocalClipboard
	writeLocalClipboard = func(string) error { return nil }
	t.Cleanup(func() { writeLocalClipboard = oldWrite })

	m := NewModel(Options{
		Width:           40,
		Height:          8,
		InitialMessages: []Message{{Role: RoleAssistant, Text: "copy me"}},
	})
	m.selStart = cell{line: 0, col: 2}
	m.selEnd = cell{line: 0, col: 9}

	if cmd := m.copySelection(); cmd != nil {
		t.Fatalf("local successful clipboard copy should not emit OSC52 command, got %#v", cmd())
	}
	if !strings.Contains(m.status, "(clipboard)") {
		t.Fatalf("copy status should report local clipboard path, got %q", m.status)
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
	inputRow := m.vpHeight() + 3
	for i, line := range lines {
		stripped := ansi.Strip(strings.TrimSuffix(line, "\x1b[K"))
		want := m.width
		if i == inputRow {
			want = m.inputRenderWidth()
		}
		if got := ansi.StringWidth(stripped); got != want {
			t.Fatalf("render line %d width = %d, want %d: %q", i, got, want, line)
		}
	}
}

func TestPOC2RenderPlacesAgentStatusAboveInputAndFooterAtBottom(t *testing.T) {
	m := NewModel(Options{
		Width:  56,
		Height: 8,
		Footer: "ctx 1K/10K · test · …/repo",
	})
	m.busy = true
	rendered := ansi.Strip(m.render())
	lines := strings.Split(rendered, "\n")
	if len(lines) != m.height {
		t.Fatalf("rendered lines = %d, want %d:\n%s", len(lines), m.height, rendered)
	}
	gapRow := lines[len(lines)-6]
	statusRow := lines[len(lines)-5]
	inputRow := lines[len(lines)-3]
	footerRow := lines[len(lines)-1]
	if strings.TrimSpace(gapRow) != "" {
		t.Fatalf("agent status should have a blank row above it, got %q in:\n%s", gapRow, rendered)
	}
	if !strings.Contains(statusRow, "● running") {
		t.Fatalf("agent status should render above input, got %q in:\n%s", statusRow, rendered)
	}
	if !strings.Contains(inputRow, "❯") {
		t.Fatalf("input row should remain between rules, got %q in:\n%s", inputRow, rendered)
	}
	if !strings.Contains(footerRow, "ctx 1K/10K") || !strings.Contains(footerRow, "test") {
		t.Fatalf("footer should render at bottom, got %q in:\n%s", footerRow, rendered)
	}
}

func TestPOC2EscInterruptsRunningAgent(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.Key
	}{
		{name: "key code", key: tea.Key{Code: tea.KeyEsc}},
		{name: "legacy ctrl bracket", key: tea.Key{Code: '[', Mod: tea.ModCtrl}},
		{name: "raw text", key: tea.Key{Text: "\x1b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			interrupted := false
			var tm tea.Model = NewModel(Options{
				Width:  40,
				Height: 8,
				Hooks: Hooks{Interrupt: func() {
					interrupted = true
				}},
			})
			m := tm.(Model)
			m.busy = true

			tm, _ = m.Update(tea.KeyPressMsg(tc.key))
			m = tm.(Model)
			if !interrupted {
				t.Fatal("esc should call interrupt hook while busy")
			}
			if got, want := m.status, "interrupting"; got != want {
				t.Fatalf("status = %q, want %q", got, want)
			}
		})
	}
}

func TestPOC2CtrlCQuitsWithSSHKeyShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.Key
	}{
		{name: "ctrl modifier", key: tea.Key{Code: 'c', Mod: tea.ModCtrl}},
		{name: "raw text", key: tea.Key{Text: "\x03"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tm tea.Model = NewModel(Options{Width: 40, Height: 8})
			_, cmd := tm.Update(tea.KeyPressMsg(tc.key))
			requireQuitCmd(t, cmd)
		})
	}
}

func TestPOC2CtrlCClearsNonEmptyInputBeforeQuit(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:         40,
		Height:        8,
		SlashCommands: []SlashCommand{{Name: "/goal", Description: "Set a goal"}},
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "/go"}))
	m := tm.(Model)
	if m.input.Value == "" || len(m.slashMatches) == 0 {
		t.Fatalf("setup should have input and slash matches: input=%q matches=%#v", m.input.Value, m.slashMatches)
	}

	tm, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd != nil {
		t.Fatalf("ctrl+c with input should clear input, not quit: %#v", cmd)
	}
	m = tm.(Model)
	if m.input.Value != "" || len(m.slashMatches) != 0 {
		t.Fatalf("ctrl+c should clear input and slash matches, input=%q matches=%#v", m.input.Value, m.slashMatches)
	}

	_, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	requireQuitCmd(t, cmd)
}

func TestPOC2ApprovalEscDeniesWithSSHKeyShape(t *testing.T) {
	decisions := make(chan ToolApprovalDecision, 1)
	var tm tea.Model = NewModel(Options{Width: 40, Height: 8})
	tm, _ = tm.Update(RequestToolApprovalMsg{
		Request: ToolApprovalRequest{ID: "call-1", ToolName: "bash"},
		Respond: decisions,
	})

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: '[', Mod: tea.ModCtrl}))
	if tm.(Model).approval != nil {
		t.Fatal("approval should clear after esc")
	}
	select {
	case got := <-decisions:
		if got != ToolApprovalDeny {
			t.Fatalf("decision = %q, want %q", got, ToolApprovalDeny)
		}
	case <-time.After(time.Second):
		t.Fatal("expected approval decision")
	}
}

func TestPOC2CompletionStatusDoesNotShowIdlePrefix(t *testing.T) {
	m := NewModel(Options{Width: 40, Height: 8})
	m.status = "✓ Completed 2s"
	rendered := ansi.Strip(m.render())
	lines := strings.Split(rendered, "\n")
	statusRow := lines[len(lines)-5]
	if !strings.Contains(statusRow, "✓ Completed 2s") || strings.Contains(statusRow, "idle") {
		t.Fatalf("completion status should be compact, got %q in:\n%s", statusRow, rendered)
	}
}

func requireQuitCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected quit command, got nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected quit command, got %T", msg)
	}
}

func copyCommandMessages(cmd tea.Cmd) (raw string, hasSetClipboard bool) {
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		if rawMsg, ok := msg.(tea.RawMsg); ok {
			return fmt.Sprint(rawMsg.Msg), false
		}
		return "", fmt.Sprintf("%T", msg) == "tea.setClipboardMsg"
	}
	for _, child := range batch {
		childMsg := child()
		switch msg := childMsg.(type) {
		case tea.RawMsg:
			raw += fmt.Sprint(msg.Msg)
		default:
			if fmt.Sprintf("%T", childMsg) == "tea.setClipboardMsg" {
				hasSetClipboard = true
			}
		}
	}
	return raw, hasSetClipboard
}

func TestPOC2InfoCardStaysAtTopAfterFirstMessage(t *testing.T) {
	var submitted string
	var tm tea.Model = NewModel(Options{
		Width:         48,
		Height:        12,
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

func TestPOC2InputCoalescesMobileSSHChineseIMEPreedit(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 48, Height: 10})
	for _, text := range []string{"z", "zh", "zhe", "zheg", "zhege", "这个", "这"} {
		tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: text}))
	}

	m := tm.(Model)
	if got, want := m.input.Value, "这个"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
}

func TestPOC2InputKeepsNormalASCIIKeyPresses(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 48, Height: 10})
	for _, text := range []string{"z", "h", "e", "g", "e"} {
		tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: text, Code: []rune(text)[0]}))
	}

	m := tm.(Model)
	if got, want := m.input.Value, "zhege"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
}

func TestPOC2CtrlWDeletesWordBeforeCursor(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 48, Height: 10})
	for _, text := range []string{"hello", " ", "world"} {
		tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: text}))
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: 'w', Mod: tea.ModCtrl}))
	m := tm.(Model)
	if got, want := m.input.Value, "hello "; got != want {
		t.Fatalf("input value after ctrl+w = %q, want %q", got, want)
	}
}

func TestPOC2InputDoesNotReplaceSingleASCIIBeforeChinese(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 48, Height: 10})
	for _, text := range []string{"a", "你"} {
		tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: text}))
	}

	m := tm.(Model)
	if got, want := m.input.Value, "a你"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
	}
}

func TestPOC2InputCoalescesConsecutiveChineseIMEWords(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 48, Height: 10})
	for _, text := range []string{"z", "zh", "zhe", "zhege", "这个", "n", "ni", "你"} {
		tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: text}))
	}

	m := tm.(Model)
	if got, want := m.input.Value, "这个你"; got != want {
		t.Fatalf("input value = %q, want %q", got, want)
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

func TestPOC2ThinkingBlockIsCollapsedAndClickable(t *testing.T) {
	m := NewModel(Options{
		Width:  72,
		Height: 10,
		InitialMessages: []Message{{
			Role:     RoleAssistant,
			Text:     "reasoning detail",
			Thinking: true,
		}},
	})
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "● Thinking") {
		t.Fatalf("thinking block summary missing:\n%s", rendered)
	}
	if strings.Contains(rendered, "reasoning detail") {
		t.Fatalf("thinking block should default collapsed:\n%s", rendered)
	}
	if _, ok := m.headers[0]; !ok {
		t.Fatalf("thinking block header should be clickable, headers=%#v", m.headers)
	}

	_ = m.onPress(1, 0)
	rendered = ansi.Strip(m.render())
	if !strings.Contains(rendered, "reasoning detail") {
		t.Fatalf("clicking thinking block should expand detail:\n%s", rendered)
	}
}

func TestPOC2StreamingAssistantMarkerBlinks(t *testing.T) {
	m := NewModel(Options{Width: 72, Height: 10, StreamReply: "streaming reply"})
	m.startStream()
	m.streamIdx = len(m.streamRunes)
	m.rebuild()
	if got, want := streamingAssistantMarkerStyle.GetForeground(), lipgloss.Color("231"); got != want {
		t.Fatalf("streaming assistant marker foreground = %#v, want %#v", got, want)
	}
	if !streamingAssistantMarkerStyle.GetBlink() {
		t.Fatal("streaming assistant marker should blink")
	}
}

func TestPOC2JumpHintSharesAgentStatusRow(t *testing.T) {
	m := NewModel(Options{Width: 72, Height: 8})
	m.busy = true
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
	statusRow := m.vpHeight() + m.approvalPanelHeight() + m.humanPromptPanelHeight() + m.slashPanelHeight() + m.todoPanelHeight() + 1
	if !strings.Contains(lines[statusRow], "● running") || !strings.Contains(lines[statusRow], jumpHintText()) {
		t.Fatalf("jump hint should share the agent status row, got %q in:\n%s", lines[statusRow], rendered)
	}
	idx := strings.Index(lines[statusRow], jumpHintText())
	if idx < 0 {
		t.Fatalf("status row missing jump text: %q", lines[statusRow])
	}
	gotCol := ansi.StringWidth(lines[statusRow][:idx])
	wantTextCol := (m.width-ansi.StringWidth(" "+jumpHintText()+" "))/2 + 1
	if gotCol != wantTextCol {
		t.Fatalf("jump hint text column = %d, want centered block text at %d in row %q", gotCol, wantTextCol, lines[statusRow])
	}
	if raw := m.render(); !strings.Contains(raw, "48;5;63") {
		t.Fatalf("jump hint should keep its background style, raw render missing background escape:\n%q", raw)
	}
}

func TestPOC2JumpHintShowsNewMessageCountWithCtrlEnd(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 72, Height: 8})
	m := tm.(Model)
	for i := 0; i < 20; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history"})
	}
	m.rebuild()
	m.scroll(-2)

	tm, _ = m.Update(AppendMessageMsg{Message: Message{Role: RoleAssistant, Text: "one"}})
	m = tm.(Model)
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "Have 1 new message (ctrl+End) ↓") {
		t.Fatalf("new message hint should include count and ctrl+End:\n%s", rendered)
	}

	tm, _ = m.Update(AppendMessageMsg{Message: Message{Role: RoleAssistant, Text: "two"}})
	m = tm.(Model)
	rendered = ansi.Strip(m.render())
	if !strings.Contains(rendered, "Have 2 new messages (ctrl+End) ↓") {
		t.Fatalf("new message hint should increment for newly appended messages:\n%s", rendered)
	}
}

func TestPOC2MergedToolUpdateDoesNotIncrementNewMessageCount(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 72, Height: 8})
	m := tm.(Model)
	for i := 0; i < 20; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history"})
	}
	m.messages = append(m.messages, Message{
		Tool:      true,
		ToolID:    "call-1",
		ToolName:  "bash",
		Summary:   "Running shell command",
		ToolInput: "go test ./pkg/modu-tui",
	})
	m.rebuild()
	m.scroll(-2)

	tm, _ = m.Update(AppendMessageMsg{Message: Message{
		Tool:       true,
		ToolID:     "call-1",
		ToolName:   "bash",
		Summary:    "Ran 1 shell command",
		ToolOutput: "ok",
		ToolDone:   true,
	}})
	m = tm.(Model)
	rendered := ansi.Strip(m.render())
	if strings.Contains(rendered, "Have 1 new message") {
		t.Fatalf("merged tool update should not count as a newly appended message:\n%s", rendered)
	}
	if !strings.Contains(rendered, jumpHintText()) {
		t.Fatalf("away-from-bottom hint should fall back to jump text after a merge-only update:\n%s", rendered)
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

	statusRow := m.vpHeight() + m.approvalPanelHeight() + m.humanPromptPanelHeight() + m.slashPanelHeight() + m.todoPanelHeight() + 1
	_ = m.onPress(1, statusRow)
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
	topRule := ansi.Strip(lines[m.vpHeight()+2])
	bottomRule := ansi.Strip(lines[m.vpHeight()+4])
	wantRule := strings.Repeat("─", m.width)
	if topRule != wantRule {
		t.Fatalf("top input rule = %q, want %q", topRule, wantRule)
	}
	if bottomRule != wantRule {
		t.Fatalf("bottom input rule = %q, want %q", bottomRule, wantRule)
	}
}

func TestPOC2HistoryHintRendersOnTopInputRule(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:        32,
		Height:       8,
		InputHistory: []string{"first", "second"},
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m := tm.(Model)
	lines := strings.Split(ansi.Strip(m.render()), "\n")
	topRule := lines[m.vpHeight()+2]
	inputLine := lines[m.vpHeight()+3]
	if !strings.Contains(topRule, "History 2/2") {
		t.Fatalf("history hint should render on top rule, got %q in:\n%s", topRule, strings.Join(lines, "\n"))
	}
	if strings.Contains(inputLine, "History") {
		t.Fatalf("history hint should not render inside input line, got %q", inputLine)
	}
	if !strings.Contains(inputLine, "❯ second") {
		t.Fatalf("history input line should keep selected text only, got %q", inputLine)
	}
}

func TestPOC2InputLineLeavesLastColumnForMobileTerminals(t *testing.T) {
	m := NewModel(Options{Width: 24, Height: 8})
	m.input.Insert(strings.Repeat("j", 120))
	rendered := m.render()
	lines := strings.Split(rendered, "\n")
	inputLine := lines[len(lines)-3]
	if strings.Contains(inputLine, "\x1b[?7l") || strings.Contains(inputLine, "\x1b[?7h") {
		t.Fatalf("input line should not toggle terminal autowrap, got %q", inputLine)
	}
	if !strings.HasSuffix(inputLine, "\x1b[K") {
		t.Fatalf("input line should clear to end of line, got %q", inputLine)
	}
	stripped := ansi.Strip(strings.TrimSuffix(inputLine, "\x1b[K"))
	if strings.Contains(stripped, "\r") {
		t.Fatalf("input line should not return carriage, got %q", inputLine)
	}
	if got, want := ansi.StringWidth(stripped), m.inputRenderWidth(); got != want {
		t.Fatalf("stripped input line width = %d, want %d: %q", got, want, stripped)
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

func TestPOC2CtrlVPastesClipboardImageAndSubmitsAttachment(t *testing.T) {
	var submitted SubmitEvent
	var tm tea.Model = NewModel(Options{
		Width:  50,
		Height: 10,
		Hooks: Hooks{
			ReadClipboardImages: func() ([]ImageAttachment, error) {
				return []ImageAttachment{{
					Name:     "clipboard.png",
					MimeType: "image/png",
					Data:     []byte("png"),
				}}, nil
			},
			SubmitMessage: func(event SubmitEvent) {
				submitted = event
			},
		},
	})

	var cmd tea.Cmd
	tm, cmd = tm.Update(tea.KeyPressMsg(tea.Key{Code: 'v', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatal("ctrl+v should return an asynchronous clipboard command")
	}
	tm, _ = tm.Update(cmd())
	m := tm.(Model)
	if got := ansi.Strip(m.render()); !strings.Contains(got, "[Image #1]") {
		t.Fatalf("input should render the pasted image attachment:\n%s", got)
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = tm.(Model)
	if submitted.Text != "" || len(submitted.Images) != 1 || submitted.Images[0].MimeType != "image/png" {
		t.Fatalf("submitted event = %#v", submitted)
	}
	if len(m.messages) != 1 || m.messages[0].Text != "[Image #1]" {
		t.Fatalf("transcript should show the image label, got %#v", m.messages)
	}
	if len(m.input.ImageAttachments()) != 0 {
		t.Fatalf("input attachments should reset after submit: %#v", m.input.ImageAttachments())
	}
}

func TestPOC2PastedImagePathBecomesAttachment(t *testing.T) {
	var resolved string
	var tm tea.Model = NewModel(Options{
		Hooks: Hooks{
			ResolvePastedImages: func(value string) ([]ImageAttachment, bool, error) {
				resolved = value
				return []ImageAttachment{{
					Name:     "screen shot.png",
					MimeType: "image/png",
					Data:     []byte("png"),
				}}, true, nil
			},
		},
	})

	tm, _ = tm.Update(tea.PasteMsg{Content: `/tmp/screen\ shot.png `})
	m := tm.(Model)
	if resolved != `/tmp/screen\ shot.png ` {
		t.Fatalf("resolver input = %q", resolved)
	}
	if got := m.input.ImageAttachments(); len(got) != 1 || got[0].Name != "screen shot.png" {
		t.Fatalf("resolved attachments = %#v", got)
	}
	if strings.Contains(m.input.ExpandedValue(), "/tmp/") {
		t.Fatalf("resolved image path should not remain as prompt text: %q", m.input.ExpandedValue())
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

func TestPOC2InputHistoryNavigatesWithUpAndDown(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:        72,
		Height:       10,
		InputHistory: []string{"first", "second", "third"},
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "d", Code: 'd'}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "r", Code: 'r'}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "a", Code: 'a'}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "f", Code: 'f'}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "t", Code: 't'}))

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m := tm.(Model)
	if got, want := m.input.Value, "third"; got != want {
		t.Fatalf("first history up = %q, want %q", got, want)
	}
	if got := ansi.Strip(m.render()); !strings.Contains(got, "History 3/3") {
		t.Fatalf("history hint missing after up:\n%s", got)
	}

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = tm.(Model)
	if got, want := m.input.Value, "second"; got != want {
		t.Fatalf("second history up = %q, want %q", got, want)
	}
	if got := ansi.Strip(m.render()); !strings.Contains(got, "History 2/3") {
		t.Fatalf("history hint should update index:\n%s", got)
	}

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = tm.(Model)
	if got, want := m.input.Value, "draft"; got != want {
		t.Fatalf("down should restore held draft = %q, want %q", got, want)
	}
	if got := ansi.Strip(m.render()); strings.Contains(got, "History ") {
		t.Fatalf("history hint should hide after returning to draft:\n%s", got)
	}
}

func TestPOC2ArrowKeysScrollWhenConfiguredAndInputEmpty(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:           40,
		Height:          8,
		ArrowKeysScroll: true,
	})
	m := tm.(Model)
	for i := 0; i < 30; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history line"})
	}
	m.rebuild()
	before := m.yOffset
	if before == 0 {
		t.Fatal("setup should start at scrollable bottom")
	}

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = tm.(Model)
	if got := m.yOffset; got >= before {
		t.Fatalf("up arrow should scroll transcript when input is empty: %d -> %d", before, got)
	}
	if got := m.input.Value; got != "" {
		t.Fatalf("up arrow should not enter input history when input is empty in arrow-scroll mode, got %q", got)
	}
}

func TestPOC2ArrowKeysPreferHistoryWhenConfiguredAndHistoryExists(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:           40,
		Height:          8,
		InputHistory:    []string{"previous prompt"},
		ArrowKeysScroll: true,
	})
	m := tm.(Model)
	for i := 0; i < 30; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history line"})
	}
	m.rebuild()
	before := m.yOffset

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = tm.(Model)
	if got, want := m.input.Value, "previous prompt"; got != want {
		t.Fatalf("up arrow should navigate input history before scrolling, got %q want %q", got, want)
	}
	if got := m.yOffset; got != before {
		t.Fatalf("up arrow should not scroll when history exists: %d -> %d", before, got)
	}
}

func TestPOC2ArrowKeysStillNavigateHistoryWhenInputHasText(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:           40,
		Height:          8,
		InputHistory:    []string{"previous prompt"},
		ArrowKeysScroll: true,
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "d", Code: 'd'}))
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))

	m := tm.(Model)
	if got, want := m.input.Value, "previous prompt"; got != want {
		t.Fatalf("up arrow should still navigate history when input has text, got %q want %q", got, want)
	}
}

func TestPOC2InputHistoryKeepsMostRecent100AndSavesOnSubmit(t *testing.T) {
	history := make([]string, 105)
	for i := range history {
		history[i] = fmt.Sprintf("old-%03d", i)
	}
	var saved []string
	var submitted string
	var tm tea.Model = NewModel(Options{
		InputHistory: history,
		Hooks: Hooks{
			InputHistoryChanged: func(history []string) {
				saved = append([]string(nil), history...)
			},
			Submit: func(text string) {
				submitted = text
			},
		},
	})
	m := tm.(Model)
	if got, want := len(m.inputHistory), 100; got != want {
		t.Fatalf("initial input history len = %d, want %d", got, want)
	}
	if got, want := m.inputHistory[0], "old-005"; got != want {
		t.Fatalf("oldest retained history = %q, want %q", got, want)
	}
	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = tm.(Model)
	if got := ansi.Strip(m.render()); !strings.Contains(got, "History 100/100") {
		t.Fatalf("full history hint should render History 100/100:\n%s", got)
	}
	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = tm.(Model)

	tm, _ = m.Update(tea.PasteMsg{Content: "new prompt"})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = tm.(Model)
	if got, want := submitted, "new prompt"; got != want {
		t.Fatalf("submitted = %q, want %q", got, want)
	}
	if got, want := len(m.inputHistory), 100; got != want {
		t.Fatalf("history len after submit = %d, want %d", got, want)
	}
	if got, want := m.inputHistory[len(m.inputHistory)-1], "new prompt"; got != want {
		t.Fatalf("newest history = %q, want %q", got, want)
	}
	if len(saved) != 100 || saved[len(saved)-1] != "new prompt" {
		t.Fatalf("saved history should receive trimmed latest 100 entries: len=%d last=%q", len(saved), saved[len(saved)-1])
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

func TestPOC2SlashPickerRefreshesCommandsFromProvider(t *testing.T) {
	commands := []SlashCommand{{Name: "/old", Description: "Old command"}}
	var tm tea.Model = NewModel(Options{
		Width:  50,
		Height: 10,
		SlashCommandsProvider: func() []SlashCommand {
			return commands
		},
	})
	commands = []SlashCommand{{Name: "/fresh", Description: "Fresh command"}}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	m := tm.(Model)
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "/fresh") {
		t.Fatalf("slash picker should include refreshed command:\n%s", rendered)
	}
	if strings.Contains(rendered, "/old") {
		t.Fatalf("slash picker should not keep stale command:\n%s", rendered)
	}
}

func TestPOC2SlashPickerDoesNotShowJumpHintAtBottom(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:         72,
		Height:        14,
		SlashCommands: []SlashCommand{{Name: "/goal", Description: "Set a goal"}},
	})
	m := tm.(Model)
	for i := 0; i < 20; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history"})
	}
	m.rebuild()
	if !m.atBottom() {
		t.Fatal("setup should be at bottom")
	}

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	m = tm.(Model)
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "/goal") {
		t.Fatalf("slash picker should be visible:\n%s", rendered)
	}
	if strings.Contains(rendered, jumpHintText()) {
		t.Fatalf("slash picker should not trigger jump hint at bottom:\n%s", rendered)
	}
}

func TestPOC2SlashPickerKeepsJumpHintWhenAwayFromBottom(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:         72,
		Height:        14,
		SlashCommands: []SlashCommand{{Name: "/goal", Description: "Set a goal"}},
	})
	m := tm.(Model)
	for i := 0; i < 20; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history"})
	}
	m.rebuild()
	m.scroll(-2)
	if m.atBottom() {
		t.Fatal("setup should be away from bottom")
	}

	tm, _ = m.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	m = tm.(Model)
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "/goal") {
		t.Fatalf("slash picker should be visible:\n%s", rendered)
	}
	if !strings.Contains(rendered, jumpHintText()) {
		t.Fatalf("slash picker should keep jump hint when away from bottom:\n%s", rendered)
	}
}

func TestPOC2ResizeKeepsInputAndCursorAlignedWithSlashPanel(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:  50,
		Height: 14,
		SlashCommands: []SlashCommand{
			{Name: "/help", Description: "Show help"},
			{Name: "/model", Description: "Switch model"},
			{Name: "/tokens", Description: "Show tokens"},
			{Name: "/tools", Description: "Show tools"},
		},
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "/", Code: '/'}))
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 28, Height: 8})

	m := tm.(Model)
	view := m.View()
	lines := strings.Split(ansi.Strip(view.Content), "\n")
	if got, want := len(lines), m.height; got != want {
		t.Fatalf("rendered lines after resize = %d, want %d:\n%s", got, want, strings.Join(lines, "\n"))
	}
	if view.Cursor == nil || view.Cursor.Y < 0 || view.Cursor.Y >= len(lines) {
		t.Fatalf("cursor should stay inside resized view, cursor=%#v lines=%d", view.Cursor, len(lines))
	}
	if got := lines[view.Cursor.Y]; !strings.Contains(got, "❯ /") {
		t.Fatalf("cursor row should be the input line after resize, got row %d: %q\n%s", view.Cursor.Y, got, strings.Join(lines, "\n"))
	}
}

func TestPOC2ViewCanDisableMouseReporting(t *testing.T) {
	enabled := NewModel(Options{Width: 24, Height: 8}).View()
	if got, want := enabled.MouseMode, tea.MouseModeCellMotion; got != want {
		t.Fatalf("default mouse mode = %v, want %v", got, want)
	}

	disabled := NewModel(Options{Width: 24, Height: 8, DisableMouse: true}).View()
	if got, want := disabled.MouseMode, tea.MouseModeNone; got != want {
		t.Fatalf("disabled mouse mode = %v, want %v", got, want)
	}
}

func TestPOC2AutoScrollStopsWhenMouseReleaseIsMissing(t *testing.T) {
	m := NewModel(Options{Width: 40, Height: 8})
	for i := 0; i < 30; i++ {
		m.messages = append(m.messages, Message{Role: RoleAssistant, Text: "history line"})
	}
	m.rebuild()
	if cmd := m.onPress(1, 0); cmd != nil {
		t.Fatalf("press should not start a command, got %#v", cmd)
	}
	if cmd := m.onDrag(1, m.vpHeight()); cmd == nil {
		t.Fatal("dragging past viewport edge should start auto-scroll")
	}
	if !m.selecting || !m.autoScrolling || m.autoScroll == 0 {
		t.Fatalf("setup should be auto-scrolling, selecting=%v autoScrolling=%v autoScroll=%d", m.selecting, m.autoScrolling, m.autoScroll)
	}

	var tm tea.Model = m
	for i := 0; i <= maxAutoScrollTicksWithoutDrag; i++ {
		tm, _ = tm.Update(autoScrollTickMsg{})
	}
	m = tm.(Model)
	if m.selecting || m.autoScrolling || m.autoScroll != 0 {
		t.Fatalf("missing mouse release should stop auto-scroll, selecting=%v autoScrolling=%v autoScroll=%d ticks=%d",
			m.selecting, m.autoScrolling, m.autoScroll, m.autoScrollTicks)
	}
	if m.hasSelection() {
		t.Fatal("missing mouse release should clear the partial selection")
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

func TestPOC2ResizeKeepsApprovalInputAndCursorVisible(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 42, Height: 12})
	tm, _ = tm.Update(RequestToolApprovalMsg{
		Request: ToolApprovalRequest{
			ID:       "call-1",
			ToolName: "bash",
			Summary:  "approval required: bash",
			Detail:   "go test ./pkg/modu-tui && git diff --check",
		},
		Respond: make(chan ToolApprovalDecision, 1),
	})
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 30, Height: 8})

	m := tm.(Model)
	view := m.View()
	lines := strings.Split(ansi.Strip(view.Content), "\n")
	if got, want := len(lines), m.height; got != want {
		t.Fatalf("rendered lines after approval resize = %d, want %d:\n%s", got, want, strings.Join(lines, "\n"))
	}
	if view.Cursor == nil || view.Cursor.Y < 0 || view.Cursor.Y >= len(lines) {
		t.Fatalf("approval cursor should stay inside resized view, cursor=%#v lines=%d", view.Cursor, len(lines))
	}
	if got := lines[view.Cursor.Y]; !strings.Contains(got, "approval pending") {
		t.Fatalf("approval cursor row should be the fixed input line, got row %d: %q\n%s", view.Cursor.Y, got, strings.Join(lines, "\n"))
	}
	if m.approvalPanelHeight() > m.approvalPanelBudget() {
		t.Fatalf("approval panel height = %d exceeds budget %d", m.approvalPanelHeight(), m.approvalPanelBudget())
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
	if got := ansi.Strip(m.render()); !strings.Contains(got, "running") {
		t.Fatalf("running state missing:\n%s", got)
	}
}

func TestPOC2MergesToolMessagesByToolID(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 80, Height: 18})
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

func TestPOC2CtrlOTogglesLatestToolAndReadsArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "call-1.output")
	if err := os.WriteFile(path, []byte("full artifact output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var tm tea.Model = NewModel(Options{
		Width:  100,
		Height: 20,
		InitialMessages: []Message{{
			Tool:             true,
			ToolID:           "call-1",
			ToolName:         "bash",
			Summary:          "Ran 1 shell command",
			ToolOutput:       "preview only",
			ToolArtifactID:   "call-1",
			ToolArtifactPath: path,
			ToolTruncated:    true,
			ToolDone:         true,
		}},
	})
	m := tm.(Model)
	if m.messages[0].Expanded {
		t.Fatal("setup should start collapsed")
	}
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = tm.(Model)
	if !m.messages[0].Expanded {
		t.Fatal("ctrl+o should expand latest tool")
	}
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "full artifact output") || strings.Contains(rendered, "preview only") {
		t.Fatalf("expanded latest tool should render artifact, got:\n%s", rendered)
	}
}

func TestPOC2ExpandedArtifactIsCachedAcrossRebuilds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "call-1.output")
	if err := os.WriteFile(path, []byte("first artifact output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var tm tea.Model = NewModel(Options{
		Width:  100,
		Height: 20,
		InitialMessages: []Message{{
			Tool:             true,
			ToolID:           "call-1",
			ToolName:         "bash",
			Summary:          "Ran 1 shell command",
			ToolOutput:       "preview only",
			ToolArtifactID:   "call-1",
			ToolArtifactPath: path,
			ToolTruncated:    true,
			ToolDone:         true,
		}},
	})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m := tm.(Model)
	if !strings.Contains(ansi.Strip(m.render()), "first artifact output") {
		t.Fatalf("expanded tool should render first artifact read, got:\n%s", ansi.Strip(m.render()))
	}
	if err := os.WriteFile(path, []byte("second artifact output\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.rebuild()
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "first artifact output") || strings.Contains(rendered, "second artifact output") {
		t.Fatalf("expanded tool should reuse cached artifact across rebuilds, got:\n%s", rendered)
	}
}

func TestPOC2ToolApprovalResolvesFromKeyboard(t *testing.T) {
	results := make(chan ToolApprovalResult, 1)
	decisions := make(chan ToolApprovalDecision, 1)
	var tm tea.Model = NewModel(Options{
		Width:  80,
		Height: 12,
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

func TestPOC2HumanPromptResolvesFromKeyboard(t *testing.T) {
	responses := make(chan string, 1)
	var tm tea.Model = NewModel(Options{Width: 80, Height: 12})
	tm, _ = tm.Update(RequestHumanPromptMsg{
		Request: HumanPromptRequest{
			Title: "Choose commit shape",
			Body:  "Split into 2 commits, or merge into 1?",
			Options: []HumanPromptOption{
				{Label: "2 commits", Value: "two"},
				{Label: "1 commit", Value: "one"},
			},
			DefaultIndex: 0,
		},
		Respond: responses,
	})

	pending := tm.(Model)
	if pending.humanPrompt == nil {
		t.Fatal("expected pending human prompt")
	}
	rendered := ansi.Strip(pending.render())
	for _, want := range []string{"Human input required", "Choose commit shape", "1. 2 commits", "[up/down] select", "human input pending"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("human prompt missing %q:\n%s", want, rendered)
		}
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	moved := tm.(Model)
	if moved.humanPrompt == nil || moved.humanPrompt.selected != 1 {
		t.Fatalf("expected down key to select second option, got %#v", moved.humanPrompt)
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	resolved := tm.(Model)
	if resolved.humanPrompt != nil {
		t.Fatal("human prompt should clear after response")
	}
	select {
	case got := <-responses:
		if got != "one" {
			t.Fatalf("response = %q, want one", got)
		}
	case <-time.After(time.Second):
		t.Fatal("expected human prompt response")
	}
}

func TestPOC2PanelRendersScrollableMainViewAndCloses(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 60, Height: 12, InitialMessages: []Message{{
		Role: RoleAssistant,
		Text: "transcript stays behind panel",
	}}})
	tm, _ = tm.Update(SetPanelMsg{Panel: Panel{
		ID:       "workflow",
		Title:    "Workflow Cockpit",
		Subtitle: "completed 1  running 0",
		Lines: []string{
			"overview",
			"run one",
			"run two",
			"run three",
			"run four",
			"run five",
			"run six",
			"run seven",
			"run eight",
			"run nine",
		},
		Footer: "[esc/q] close",
	}})

	open := tm.(Model)
	rendered := ansi.Strip(open.render())
	for _, want := range []string{"Workflow Cockpit", "completed 1", "overview", "panel open", "● panel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("panel render missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "transcript stays behind panel") {
		t.Fatalf("panel should replace viewport, not append transcript:\n%s", rendered)
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	scrolled := tm.(Model)
	if scrolled.panelOffset == 0 {
		t.Fatal("expected PgDown to scroll panel")
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "q"}))
	closed := tm.(Model)
	if closed.panel != nil {
		t.Fatal("expected q to close panel")
	}
	if got := ansi.Strip(closed.render()); !strings.Contains(got, "transcript stays behind panel") {
		t.Fatalf("transcript should return after panel closes:\n%s", got)
	}
}

func TestPOC2PanelRowsSelectAndEmitAction(t *testing.T) {
	actions := make(chan PanelAction, 1)
	var tm tea.Model = NewModel(Options{
		Width:  72,
		Height: 12,
		Hooks: Hooks{
			PanelAction: func(action PanelAction) {
				actions <- action
			},
		},
	})
	tm, _ = tm.Update(SetPanelMsg{Panel: Panel{
		ID:    "workflow",
		Title: "Workflow Cockpit",
		Rows: []PanelRow{
			{Label: "run one [completed]", Detail: "5/5 · 1min", Value: "run-one", Command: "/workflows show run-one"},
			{Label: "run two [running]", Detail: "2/5 · Research", Value: "run-two", Command: "/workflows show run-two"},
		},
	}})
	open := tm.(Model)
	if open.panelSelected != 0 {
		t.Fatalf("panelSelected = %d, want 0", open.panelSelected)
	}
	rendered := ansi.Strip(open.render())
	for _, want := range []string{"run one [completed]", "5/5", "[up/down] select"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("selectable panel missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "↑") || strings.Contains(rendered, "↓") {
		t.Fatalf("panel footer should avoid arrow glyphs:\n%s", rendered)
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	selected := tm.(Model)
	if selected.panelSelected != 1 {
		t.Fatalf("panelSelected = %d, want 1", selected.panelSelected)
	}
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	afterEnter := tm.(Model)
	if afterEnter.panel == nil {
		t.Fatal("panel should stay open until the hook replaces or clears it, to avoid a no-panel flicker frame")
	}
	select {
	case action := <-actions:
		if action.PanelID != "workflow" || action.Index != 1 || action.Row.Value != "run-two" || action.Command != "/workflows show run-two" {
			t.Fatalf("unexpected panel action: %#v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("expected panel action")
	}
}

func TestPOC2PanelStylesTitleAndRendersMarkdownBlocks(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 72, Height: 16})
	tm, _ = tm.Update(SetPanelMsg{Panel: Panel{
		ID:       "workflow-result",
		Title:    "Workflow Result",
		Markdown: true,
		Lines: []string{
			"context",
			"workflow: market_watch",
			"",
			"# Report",
			"- market breadth improved",
			"- watch policy headlines",
		},
		Rows: []PanelRow{{Label: "Back", Command: "back"}},
	}})

	model := tm.(Model)
	rendered := model.render()
	stripped := ansi.Strip(rendered)
	if !strings.Contains(rendered, panelTitleStyle.Render("Workflow Result")) {
		t.Fatalf("panel title should use panel title style:\n%q", rendered)
	}
	if !strings.Contains(rendered, panelSectionStyle.Render("context")) {
		t.Fatalf("plain section heading should use panel section style:\n%q", rendered)
	}
	if strings.Contains(stripped, "# Report") {
		t.Fatalf("markdown heading should be rendered instead of shown raw:\n%s", stripped)
	}
	for _, want := range []string{"Report", "market breadth improved", "watch policy headlines"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered panel markdown missing %q:\n%s", want, stripped)
		}
	}
}

func TestPOC2PanelRendersMarkdownParagraphsAndFencedCode(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 80, Height: 18})
	tm, _ = tm.Update(SetPanelMsg{Panel: Panel{
		ID:       "workflow-result",
		Title:    "Workflow Result",
		Markdown: true,
		Lines: []string{
			"## Markdown report",
			"This is **important** and `inline`.",
			"",
			"```go",
			"package main",
			"",
			"func main() {}",
			"```",
		},
	}})

	model := tm.(Model)
	stripped := ansi.Strip(model.render())
	for _, raw := range []string{"## Markdown report", "**important**", "`inline`", "```go"} {
		if strings.Contains(stripped, raw) {
			t.Fatalf("panel markdown should render %q instead of showing it raw:\n%s", raw, stripped)
		}
	}
	for _, want := range []string{"Markdown report", "important", "inline", "package main", "func main() {}"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered panel markdown missing %q:\n%s", want, stripped)
		}
	}
}

func TestPOC2PanelDoesNotRenderMarkdownByDefault(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 80, Height: 18})
	tm, _ = tm.Update(SetPanelMsg{Panel: Panel{
		ID:    "workflow-script",
		Title: "Workflow Script",
		Lines: []string{
			"script",
			"return \"# Smoke Report\"",
			"```txt",
			"result code block",
			"```",
		},
	}})

	model := tm.(Model)
	stripped := ansi.Strip(model.render())
	for _, want := range []string{"return \"# Smoke Report\"", "```txt", "result code block", "```"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("plain panel should keep script markdown-looking text %q:\n%s", want, stripped)
		}
	}
}

func TestPOC2PanelShortcutEmitsAction(t *testing.T) {
	actions := make(chan PanelAction, 1)
	var tm tea.Model = NewModel(Options{
		Width:  72,
		Height: 12,
		Hooks: Hooks{
			PanelAction: func(action PanelAction) {
				actions <- action
			},
		},
	})
	tm, _ = tm.Update(SetPanelMsg{Panel: Panel{
		ID:    "workflow-run",
		Title: "Workflow Run",
		Rows: []PanelRow{
			{Label: "Open agents", Command: "workflow-panel:agents:run-1"},
		},
		Shortcuts: []PanelShortcut{{
			Key:     "x",
			Label:   "Stop",
			Command: "workflow-panel:control:stop:run-1",
		}},
	}})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "x", Code: 'x'}))

	select {
	case action := <-actions:
		if action.PanelID != "workflow-run" || action.Index != -1 || action.Command != "workflow-panel:control:stop:run-1" || action.Row.Label != "Stop" {
			t.Fatalf("unexpected shortcut action: %#v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("expected shortcut action")
	}
	if tm.(Model).panel == nil {
		t.Fatal("panel should stay open until the hook replaces or clears it, to avoid a no-panel flicker frame")
	}
}

func TestPOC2PanelRefreshPreservesSelectionAndCloseHook(t *testing.T) {
	closed := make(chan string, 1)
	var tm tea.Model = NewModel(Options{
		Width:  72,
		Height: 12,
		Hooks: Hooks{
			PanelClosed: func(panelID string) {
				closed <- panelID
			},
		},
	})
	tm, _ = tm.Update(SetPanelMsg{Panel: Panel{
		ID:    "workflow",
		Title: "Workflow Cockpit",
		Rows: []PanelRow{
			{Label: "run one", Value: "run-one"},
			{Label: "run two", Value: "run-two"},
		},
	}})
	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	selected := tm.(Model)
	if selected.panelSelected != 1 {
		t.Fatalf("panelSelected before refresh = %d, want 1", selected.panelSelected)
	}

	tm, _ = tm.Update(RefreshPanelMsg{Panel: Panel{
		ID:    "workflow",
		Title: "Workflow Cockpit",
		Rows: []PanelRow{
			{Label: "run one [done]", Value: "run-one"},
			{Label: "run two [running]", Value: "run-two"},
			{Label: "run three [queued]", Value: "run-three"},
		},
	}})
	refreshed := tm.(Model)
	if refreshed.panelSelected != 1 {
		t.Fatalf("panelSelected after refresh = %d, want 1", refreshed.panelSelected)
	}
	rendered := ansi.Strip(refreshed.render())
	if !strings.Contains(rendered, "run two [running]") {
		t.Fatalf("refreshed panel content missing updated row:\n%s", rendered)
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: "q"}))
	if tm.(Model).panel != nil {
		t.Fatal("panel should close after q")
	}
	select {
	case panelID := <-closed:
		if panelID != "workflow" {
			t.Fatalf("closed panel id = %q, want workflow", panelID)
		}
	case <-time.After(time.Second):
		t.Fatal("expected panel close hook")
	}
}

func TestPOC2PanelSelectionStaysVisible(t *testing.T) {
	rows := make([]PanelRow, 0, 16)
	for i := 0; i < 16; i++ {
		rows = append(rows, PanelRow{Label: fmt.Sprintf("run-%02d", i+1), Command: fmt.Sprintf("/workflows show run-%02d", i+1)})
	}
	var tm tea.Model = NewModel(Options{Width: 60, Height: 10})
	tm, _ = tm.Update(SetPanelMsg{Panel: Panel{
		ID:    "workflow",
		Title: "Workflow Cockpit",
		Rows:  rows,
	}})
	for i := 0; i < 12; i++ {
		tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	}
	m := tm.(Model)
	if m.panelSelected != 12 {
		t.Fatalf("panelSelected = %d, want 12", m.panelSelected)
	}
	if m.panelOffset == 0 {
		t.Fatal("expected panel offset to follow selected row")
	}
	rendered := ansi.Strip(m.render())
	if !strings.Contains(rendered, "run-13") {
		t.Fatalf("selected row should be visible:\n%s", rendered)
	}
}

func TestPOC2HumanTextSecretInputMasksAndResolves(t *testing.T) {
	responses := make(chan string, 1)
	var tm tea.Model = NewModel(Options{Width: 80, Height: 18})
	tm, _ = tm.Update(RequestHumanTextMsg{
		Request: HumanTextRequest{
			Title:       "API key",
			Body:        "Paste API key",
			Placeholder: "sk-...",
			Secret:      true,
			Required:    true,
		},
		Respond: responses,
	})
	for _, r := range "sk-secret" {
		tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Text: string(r), Code: r}))
	}
	pending := tm.(Model)
	rendered := ansi.Strip(pending.render())
	if strings.Contains(rendered, "sk-secret") {
		t.Fatalf("secret input should be masked:\n%s", rendered)
	}
	if !strings.Contains(rendered, "*********") || !strings.Contains(rendered, "[enter] save") {
		t.Fatalf("secret prompt missing masked value/actions:\n%s", rendered)
	}

	tm, _ = tm.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	resolved := tm.(Model)
	if resolved.humanText != nil {
		t.Fatal("human text prompt should clear after response")
	}
	select {
	case got := <-responses:
		if got != "sk-secret" {
			t.Fatalf("response = %q, want sk-secret", got)
		}
	default:
		t.Fatal("expected human text response")
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
	inputRule := m.vpHeight() + m.approvalPanelHeight() + 2
	if got, want := rendered[inputRule], strings.Repeat("─", m.width); got != want {
		t.Fatalf("input top rule line = %q, want %q", got, want)
	}
	panelEnd := panelTop + m.approvalPanelHeight()
	if !strings.Contains(strings.Join(rendered[panelTop:panelEnd], "\n"), "[y] allow") {
		t.Fatalf("approval panel should include actions:\n%s", strings.Join(rendered[panelTop:panelEnd], "\n"))
	}
	if !strings.Contains(strings.Join(rendered[panelTop:panelEnd], "\n"), "go test ./...") {
		t.Fatalf("approval panel should include command preview:\n%s", strings.Join(rendered[panelTop:panelEnd], "\n"))
	}
}

func TestPOC2TodoPanelRendersAboveInput(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:  50,
		Height: 12,
		InitialMessages: []Message{
			{Role: RoleAssistant, Text: strings.Repeat("history\n", 12)},
		},
		Todos: []TodoItem{
			{Content: "first step", Status: "in_progress"},
			{Content: "second step", Status: "pending"},
		},
	})
	tm, _ = tm.Update(SetBusyMsg{Busy: true})
	tm, _ = tm.Update(SetTodosMsg{Todos: []TodoItem{
		{Content: "first step", Status: "in_progress"},
		{Content: "second step", Status: "pending"},
	}})
	m := tm.(Model)

	rendered := strings.Split(ansi.Strip(m.render()), "\n")
	if got, want := len(rendered), m.height; got != want {
		t.Fatalf("rendered lines = %d, want %d:\n%s", got, want, strings.Join(rendered, "\n"))
	}
	panelTop := m.vpHeight()
	inputRule := m.vpHeight() + m.todoPanelHeight() + 2
	if !strings.HasPrefix(rendered[panelTop], "┏") {
		t.Fatalf("todo panel should start immediately below viewport at line %d:\n%s", panelTop, strings.Join(rendered, "\n"))
	}
	panel := strings.Join(rendered[panelTop:inputRule], "\n")
	for _, want := range []string{"Todos", "first step", "second step"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("todo panel missing %q:\n%s", want, panel)
		}
	}
	if got, want := rendered[inputRule], strings.Repeat("─", m.width); got != want {
		t.Fatalf("input top rule line = %q, want %q", got, want)
	}
}

func TestPOC2TodoPanelHiddenWhileIdle(t *testing.T) {
	m := NewModel(Options{
		Width:  50,
		Height: 10,
		Todos:  []TodoItem{{Content: "stale task", Status: "pending"}},
	})

	if got := ansi.Strip(m.render()); strings.Contains(got, "Todos") || strings.Contains(got, "stale task") {
		t.Fatalf("idle model should hide todo panel:\n%s", got)
	}
}

func TestPOC2TodoPanelIgnoresStaleTodosOnNewRun(t *testing.T) {
	var tm tea.Model = NewModel(Options{
		Width:  50,
		Height: 10,
		Todos:  []TodoItem{{Content: "old task", Status: "pending"}},
	})
	tm, _ = tm.Update(SetBusyMsg{Busy: true})

	m := tm.(Model)
	if got := ansi.Strip(m.render()); strings.Contains(got, "Todos") || strings.Contains(got, "old task") {
		t.Fatalf("new run should not show stale todos before current SetTodosMsg:\n%s", got)
	}

	tm, _ = tm.Update(SetTodosMsg{Todos: []TodoItem{{Content: "current task", Status: "pending"}}})
	m = tm.(Model)
	if got := ansi.Strip(m.render()); !strings.Contains(got, "current task") {
		t.Fatalf("current-run todo update should show panel:\n%s", got)
	}
}

func TestPOC2SetTodosMsgUpdatesTodoPanel(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 50, Height: 10})
	tm, _ = tm.Update(SetBusyMsg{Busy: true})
	tm, _ = tm.Update(SetTodosMsg{Todos: []TodoItem{{Content: "new task", Status: "pending"}}})
	m := tm.(Model)
	if got := ansi.Strip(m.render()); !strings.Contains(got, "new task") {
		t.Fatalf("expected todo panel after SetTodosMsg:\n%s", got)
	}

	tm, _ = tm.Update(SetTodosMsg{Todos: []TodoItem{{Content: "new task", Status: "completed"}}})
	m = tm.(Model)
	if got := ansi.Strip(m.render()); strings.Contains(got, "new task") || strings.Contains(got, "Todos") {
		t.Fatalf("completed-only todos should hide panel:\n%s", got)
	}
}

func TestPOC2TransientStatusExpiresWithoutClearingNewStatus(t *testing.T) {
	var tm tea.Model = NewModel()
	var cmd tea.Cmd
	tm, cmd = tm.Update(SetStatusMsg{Status: "✓ Completed 1s", TransientFor: time.Second})
	if cmd == nil {
		t.Fatal("transient status should schedule an expiry command")
	}
	m := tm.(Model)
	if got := m.status; got != "✓ Completed 1s" {
		t.Fatalf("status = %q", got)
	}

	tm, _ = tm.Update(SetStatusMsg{Status: "running"})
	tm, _ = tm.Update(statusExpireMsg{status: "✓ Completed 1s"})
	m = tm.(Model)
	if got := m.status; got != "running" {
		t.Fatalf("old transient expiry should not clear new status, got %q", got)
	}

	tm, _ = tm.Update(SetStatusMsg{Status: "done", TransientFor: time.Second})
	m = tm.(Model)
	m.statusExpiresAt = time.Now().Add(-time.Millisecond)
	tm = m
	tm, _ = tm.Update(statusExpireMsg{status: "done"})
	m = tm.(Model)
	if got := m.status; got != "" {
		t.Fatalf("expired transient status should clear, got %q", got)
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

func TestSetMessagesMsgReplacesTranscript(t *testing.T) {
	var tm tea.Model = NewModel(Options{Width: 72, Height: 12, InitialMessages: []Message{
		{Role: RoleUser, Text: "old conversation"},
		{Role: RoleAssistant, Text: "old reply"},
	}})
	m := tm.(Model)

	tm, _ = m.Update(SetMessagesMsg{Messages: []Message{
		{Role: RoleUser, Text: "resumed question"},
		{Role: RoleAssistant, Text: "resumed answer"},
	}})
	m = tm.(Model)

	rendered := ansi.Strip(m.render())
	if strings.Contains(rendered, "old conversation") || strings.Contains(rendered, "old reply") {
		t.Fatalf("old transcript should be gone after SetMessagesMsg:\n%s", rendered)
	}
	for _, want := range []string{"resumed question", "resumed answer"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("replayed transcript missing %q:\n%s", want, rendered)
		}
	}
}
