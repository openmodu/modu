package ui

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
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

func TestUIQueryingEscInterrupts(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.state = uiStateQuerying
	m.statusMsg = "thinking"

	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if m.state != uiStateInput {
		t.Fatalf("expected input state after esc interrupt, got %v", m.state)
	}
	if m.statusMsg != "interrupted" {
		t.Fatalf("expected interrupted status, got %q", m.statusMsg)
	}
}

func TestUIEscIntoNormalKeepsMouseCaptured(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.state = uiStateInput
	m.mouseMode = true

	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if m.state != uiStateNormal {
		t.Fatalf("expected normal state after esc, got %v", m.state)
	}
	if !m.mouseMode {
		t.Fatal("expected mouse capture to stay enabled in normal mode")
	}
	if cmd != nil {
		t.Fatal("expected esc into normal mode to avoid disabling mouse")
	}
}

func TestUIRenderInputAreaUsesQueryingHint(t *testing.T) {
	session := newUITestSession(t)
	model := testUIModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	m.state = uiStateQuerying

	got := m.renderInputArea()
	if strings.Contains(got, "enter send") {
		t.Fatalf("did not expect old shortcut hint, got %q", got)
	}
	if !strings.Contains(got, "…") && !strings.Contains(got, model.Name) {
		t.Fatalf("expected meta footer or truncated footer, got %q", got)
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
	if !strings.Contains(got, "answer") {
		t.Fatalf("expected assistant content, got %q", got)
	}
	if !strings.Contains(got, "read") {
		t.Fatalf("expected tool line, got %q", got)
	}
}

func TestUIRenderConversationMarkdownDoesNotDuplicateHeadings(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.width = 72
	m.blocks = []uiBlock{
		{Kind: "assistant", Content: "### render.go (+39 -1)\n\n- first item\n- second item"},
	}

	got := m.renderConversation()
	got = ansiPattern.ReplaceAllString(got, "")
	if strings.Count(got, "render.go (+39 -1)") != 1 {
		t.Fatalf("expected markdown heading once, got %q", got)
	}
}

func TestNormalizeRenderedMarkdownStripsANSIPadding(t *testing.T) {
	raw := "\x1b[38;5;39m### title   \x1b[0m\n\x1b[38;5;252m     \x1b[0m\n"
	got := normalizeRenderedMarkdown(raw)
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ansi to be stripped, got %q", got)
	}
	if strings.Contains(got, "title   ") {
		t.Fatalf("expected trailing padding to be removed, got %q", got)
	}
	if strings.Count(got, "\n") > 1 {
		t.Fatalf("expected normalized markdown without extra filler lines, got %q", got)
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
	session := newUITestSession(t)
	model := testUIModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	got := m.renderInputMeta()
	if !strings.Contains(got, model.Name) || !strings.Contains(got, "("+model.ProviderID+")") {
		t.Fatalf("expected model in meta, got %q", got)
	}
	if !strings.Contains(got, filepath.Base(session.RuntimeState().Cwd)) {
		t.Fatalf("expected cwd in meta, got %q", got)
	}
}

func TestRenderInputMetaDoesNotDuplicateProvider(t *testing.T) {
	session := newUITestSession(t)
	model := &types.Model{
		ID:         "qwen/qwen3.5-35b-a3b",
		Name:       "qwen/qwen3.5-35b-a3b (lmstudio)",
		ProviderID: "lmstudio",
	}
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	got := m.renderInputMeta()
	if strings.Count(strings.ToLower(got), "lmstudio") != 1 {
		t.Fatalf("expected provider to appear once, got %q", got)
	}
}

func TestRenderInputAreaOmitsTrailingEmptyMetaLine(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	got := m.renderInputArea()
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("expected no trailing empty line, got %q", got)
	}
}

func TestWindowResizeUsesRenderedInputHeight(t *testing.T) {
	session := newUITestSession(t)
	model := testUIModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	m.ready = true
	m.state = uiStateInput

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	if m.viewport.Height <= 12 {
		t.Fatalf("expected viewport height to use rendered footer space, got %d", m.viewport.Height)
	}
}

func TestUIViewDoesNotExceedWindowHeightWhileQuerying(t *testing.T) {
	session := newUITestSession(t)
	model := testUIModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	m.state = uiStateQuerying
	m.statusMsg = "thinking"
	m.ready = true
	m.blocks = []uiBlock{
		{Kind: "assistant", Content: strings.Repeat("line\n", 40)},
	}

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	if got := lipgloss.Height(m.View()); got > 20 {
		t.Fatalf("expected rendered view to fit window height, got %d", got)
	}
}

func TestUIViewDoesNotExceedWindowHeightWhileStreamingMarkdown(t *testing.T) {
	session := newUITestSession(t)
	model := testUIModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	m.state = uiStateQuerying
	m.statusMsg = "thinking"
	m.ready = true
	m.blocks = []uiBlock{
		{
			Kind:      "assistant",
			Streaming: true,
			Content:   "## Title\n\n- item 1\n- item 2\n\n```go\nfmt.Println(\"hi\")\n```",
		},
	}

	m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})

	if got := lipgloss.Height(m.View()); got > 20 {
		t.Fatalf("expected rendered streaming markdown view to fit window height, got %d", got)
	}
}

func TestUIViewDoesNotExceedWindowHeightWithLongMarkdownLines(t *testing.T) {
	session := newUITestSession(t)
	model := testUIModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	m.state = uiStateInput
	m.ready = true
	m.blocks = []uiBlock{
		{
			Kind:    "assistant",
			Content: "```go\n" + strings.Repeat("veryLongIdentifierWithoutSpaces", 8) + "\n```",
		},
	}

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 18})

	if got := lipgloss.Height(m.View()); got > 18 {
		t.Fatalf("expected rendered long-markdown view to fit window height, got %d", got)
	}
}

func TestViewportConversationWrapsWithinViewportWidth(t *testing.T) {
	session := newUITestSession(t)
	model := testUIModel()
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	m.ready = true
	m.state = uiStateInput
	m.width = 60
	m.height = 18
	m.viewport.Width = 60
	m.viewport.Height = 10
	m.blocks = []uiBlock{
		{
			Kind:    "assistant",
			Content: "```txt\n" + strings.Repeat("LongToken", 12) + "\n```",
		},
	}

	got := m.renderConversation()
	maxLineWidth := 0
	for _, line := range strings.Split(got, "\n") {
		if w := lipgloss.Width(ansiPattern.ReplaceAllString(line, "")); w > maxLineWidth {
			maxLineWidth = w
		}
	}
	if maxLineWidth > m.viewportContentWidth() {
		t.Fatalf("expected wrapped conversation lines to fit viewport width, got line width %d > %d", maxLineWidth, m.viewportContentWidth())
	}
}

func TestRenderInputAreaTruncatesMetaWhenNarrow(t *testing.T) {
	session := newUITestSession(t)
	model := &types.Model{
		ID:         "qwen/qwen3.5-35b-a3b",
		Name:       "qwen/qwen3.5-35b-a3b",
		ProviderID: "lmstudio",
	}
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	m.width = 20
	got := m.renderInputArea()
	if !strings.Contains(got, "...") {
		t.Fatalf("expected truncated meta with ellipsis, got %q", got)
	}
}

func TestRenderStatusBarHidesThinkingDuringQuery(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.width = 80
	m.state = uiStateQuerying
	m.statusMsg = "thinking"
	if got := m.renderStatusBar(); strings.Contains(got, "thinking") {
		t.Fatalf("expected querying status bar to hide duplicate thinking text, got %q", got)
	}
}

func TestRenderStatusBarOmitsScrollPercentage(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.width = 80
	m.state = uiStateInput
	m.statusMsg = "ready"
	if got := m.renderStatusBar(); strings.Contains(got, "%") {
		t.Fatalf("expected status bar without scroll percentage, got %q", got)
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

// ─── Test helpers ────────────────────────────────

func testUIModel() *types.Model {
	return &types.Model{
		ID:         "test-model",
		Name:       "Test Model",
		ProviderID: "test",
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func newUITestSession(t *testing.T) *coding_agent.CodingSession {
	t.Helper()

	root := t.TempDir()
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:      root,
		AgentDir: filepath.Join(root, ".coding_agent"),
		Model:    testUIModel(),
		GetAPIKey: func(provider string) (string, error) {
			return "", nil
		},
		StreamFn: func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				last := llmCtx.Messages[len(llmCtx.Messages)-1]
				userText := ""
				if msg, ok := last.(types.UserMessage); ok {
					userText, _ = msg.Content.(string)
				}
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "assistant: " + userText}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
				stream.Close()
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}
