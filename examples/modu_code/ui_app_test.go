package main

import (
	"context"
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
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.state = uiStateQuerying

	got := m.renderInputArea()
	if !strings.Contains(got, "draft while waiting") {
		t.Fatalf("expected querying hint, got %q", got)
	}

	m.state = uiStateInput
	got = m.renderInputArea()
	if !strings.Contains(got, "enter send") {
		t.Fatalf("expected input hint, got %q", got)
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
