package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUIQueryingKeepsDraftOnEnter(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.state = uiStateQuerying
	m.input.Focus()

	for _, r := range []rune("draft") {
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := m.input.RawValue(); got != "draft" {
		t.Fatalf("expected draft input, got %q", got)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.input.RawValue(); got != "draft" {
		t.Fatalf("expected enter during querying to keep draft, got %q", got)
	}
	if m.state != uiStateQuerying {
		t.Fatalf("expected to remain in querying state, got %v", m.state)
	}
	if m.statusMsg != "busy: press ctrl+c to interrupt" {
		t.Fatalf("expected busy hint, got %q", m.statusMsg)
	}
}

func TestUISubmitLineKeepsInputFocusedDuringQuery(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.input.Blur()

	_ = m.submitLineCmd("hello")

	if m.state != uiStateQuerying {
		t.Fatalf("expected querying state, got %v", m.state)
	}
	if !m.input.focused {
		t.Fatal("expected input to stay focused while querying")
	}
	if m.statusMsg != "thinking" {
		t.Fatalf("expected thinking status, got %q", m.statusMsg)
	}
}

func TestUIRenderInputAreaUsesQueryingHint(t *testing.T) {
	session := newExampleTestSession(t)
	model := testExampleModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	m.state = uiStateQuerying

	got := m.renderInputArea()
	if !strings.Contains(got, "model "+model.Name) {
		t.Fatalf("expected model info, got %q", got)
	}
	if !strings.Contains(got, "cwd ") {
		t.Fatalf("expected cwd info, got %q", got)
	}

	m.state = uiStateInput
	got = m.renderInputArea()
	if strings.Contains(got, "enter send") {
		t.Fatalf("did not expect old shortcut hint, got %q", got)
	}
}

func TestUIRenderExitTranscriptIncludesConversation(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.width = 100
	m.ready = true
	m.blocks = []uiBlock{
		{Kind: "user", Content: "hello"},
		{Kind: "assistant", Content: "world"},
	}
	m.statusMsg = "thinking"
	m.errMsg = "boom"

	got := m.renderExitTranscript()
	if !strings.Contains(got, "hello") {
		t.Fatalf("expected user content in transcript, got %q", got)
	}
	if !strings.Contains(got, "world") {
		t.Fatalf("expected assistant content in transcript, got %q", got)
	}
	if !strings.Contains(got, "! boom") {
		t.Fatalf("expected error line in transcript, got %q", got)
	}
	if strings.Contains(got, "thinking") {
		t.Fatalf("did not expect live status in exit transcript, got %q", got)
	}
}

func TestUIRenderConversationUsesBulletPrefixes(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.width = 100
	m.blocks = []uiBlock{
		{Kind: "assistant", Thinking: "step one", Content: "answer"},
		{Kind: "tool", Tools: []*uiToolState{{Name: "read", Status: "running"}}},
	}

	got := m.renderConversation()
	if !strings.Contains(got, "●") {
		t.Fatalf("expected bullet markers, got %q", got)
	}
	if !strings.Contains(got, "thinking") {
		t.Fatalf("expected thinking header, got %q", got)
	}
	if !strings.Contains(got, "answer") {
		t.Fatalf("expected assistant content, got %q", got)
	}
	if !strings.Contains(got, "read") {
		t.Fatalf("expected tool line, got %q", got)
	}
}

func TestUIInputCtrlJInsertsNewline(t *testing.T) {
	input := newUIInputModel()
	input.Focus()
	input.ta.InsertString("hello")

	submitted, _ := input.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	if submitted {
		t.Fatal("expected ctrl+j to insert newline, not submit")
	}
	if got := input.RawValue(); got != "hello\n" {
		t.Fatalf("expected newline inserted, got %q", got)
	}
}

func TestExtractThinkText(t *testing.T) {
	thinking, visible := extractThinkText("before<think>secret plan</think>after")
	if thinking != "secret plan" {
		t.Fatalf("expected thinking content, got %q", thinking)
	}
	if visible != "beforeafter" {
		t.Fatalf("expected visible text without think block, got %q", visible)
	}
}

func TestExtractThinkTextHidesIncompleteThinkBlock(t *testing.T) {
	thinking, visible := extractThinkText("hello<think>partial")
	if thinking != "" {
		t.Fatalf("expected no completed thinking block, got %q", thinking)
	}
	if visible != "hello" {
		t.Fatalf("expected visible text before incomplete think block, got %q", visible)
	}
}

func TestRenderToolOutputWrapsWhenNarrow(t *testing.T) {
	got := renderUIToolOutput("bash", "this is a long line that should wrap instead of disappearing", true, 24)
	if !strings.Contains(got, "this is a") {
		t.Fatalf("expected first wrapped segment, got %q", got)
	}
	if !strings.Contains(got, "should wrap") {
		t.Fatalf("expected later wrapped segment, got %q", got)
	}
}

func TestRenderToolOutputCollapsedShowsExpandHint(t *testing.T) {
	got := renderUIToolOutput("read", "l1\nl2\nl3\nl4\nl5", false, 80)
	if !strings.Contains(got, "ctrl+o to expand") {
		t.Fatalf("expected expand hint, got %q", got)
	}
	if strings.Contains(got, "l5") {
		t.Fatalf("expected collapsed output to hide later lines, got %q", got)
	}
}

func TestRenderToolOutputCollapsedShowsExpandHintForWrappedSingleLine(t *testing.T) {
	got := renderUIToolOutput("bash", "this is one extremely long output line that should wrap into many terminal rows and still show the expand hint", false, 24)
	if !strings.Contains(got, "ctrl+o to expand") {
		t.Fatalf("expected expand hint for wrapped single line, got %q", got)
	}
}

func TestRenderInputMetaUsesShortenedCwd(t *testing.T) {
	session := newExampleTestSession(t)
	model := testExampleModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	got := m.renderInputMeta()
	if !strings.Contains(got, "model "+model.Name) {
		t.Fatalf("expected model in meta, got %q", got)
	}
	if !strings.Contains(got, filepath.Base(session.RuntimeState().Cwd)) {
		t.Fatalf("expected cwd in meta, got %q", got)
	}
}

func TestRenderInputAreaOmitsTrailingEmptyMetaLine(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	got := m.renderInputArea()
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("expected no trailing empty line, got %q", got)
	}
}

func TestRenderUIAssistantBlockContinuationAlignsWithFirstLine(t *testing.T) {
	got := renderUIAssistantBlock("first line\nsecond line", 80)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %q", got)
	}
	if !strings.HasPrefix(lines[0], "● ") {
		t.Fatalf("expected bullet prefix on first line, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], assistantPad) {
		t.Fatalf("expected assistant continuation indent, got %q", lines[1])
	}
	if strings.HasPrefix(lines[1], dotPad) && assistantPad != dotPad {
		t.Fatalf("expected assistant continuation not to use tool indent, got %q", lines[1])
	}
}
