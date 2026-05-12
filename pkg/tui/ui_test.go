package tui

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/approval"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	"github.com/openmodu/modu/pkg/types"
)

func TestGoTUIApprovalUsesCoreDecisionNames(t *testing.T) {
	responseCh := make(chan string, 1)
	root := newGoTUIRoot(context.Background(), nil, nil, nil, "", nil, nil)
	root.handleApprovalRequest(approval.Request{
		ToolName:   "bash",
		ToolCallID: "call-1",
		Response:   responseCh,
	})

	root.approve("allow")

	select {
	case got := <-responseCh:
		if got != "allow" {
			t.Fatalf("expected core allow decision, got %q", got)
		}
	default:
		t.Fatal("expected approval response")
	}
	if root.model.pendingPerm != nil {
		t.Fatal("expected pending approval to be cleared")
	}
}

func TestGoTUIApprovalKeyMapCapturesApprovalKeys(t *testing.T) {
	responseCh := make(chan string, 1)
	root := newGoTUIRoot(context.Background(), nil, nil, nil, "", nil, nil)
	root.draft.Set("draft")
	root.handleApprovalRequest(approval.Request{
		ToolName:   "bash",
		ToolCallID: "call-1",
		Response:   responseCh,
	})

	if !dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: 'y'}) {
		t.Fatal("expected y key to be handled")
	}

	select {
	case got := <-responseCh:
		if got != "allow" {
			t.Fatalf("expected y to allow, got %q", got)
		}
	default:
		t.Fatal("expected approval response from y key")
	}
	if got := root.draft.Get(); got != "draft" {
		t.Fatalf("expected approval key not to edit draft, got %q", got)
	}
}

func TestGoTUIApprovalKeyMapEnterAllows(t *testing.T) {
	responseCh := make(chan string, 1)
	root := newGoTUIRoot(context.Background(), nil, nil, nil, "", nil, nil)
	root.handleApprovalRequest(approval.Request{
		ToolName:   "bash",
		ToolCallID: "call-1",
		Response:   responseCh,
	})

	if !dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyEnter}) {
		t.Fatal("expected enter key to be handled")
	}

	select {
	case got := <-responseCh:
		if got != "allow" {
			t.Fatalf("expected enter to allow, got %q", got)
		}
	default:
		t.Fatal("expected approval response from enter key")
	}
}

func TestGoTUIInputUsesCursorEditing(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, nil, "", nil, nil)

	for _, r := range []rune("hello") {
		dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: r})
	}
	dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyLeft})
	dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyLeft})
	dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: 'X'})

	if got := root.draft.Get(); got != "helXlo" {
		t.Fatalf("expected cursor insert to edit draft, got %q", got)
	}
}

func TestGoTUIInputBackspaceUsesCursor(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, nil, "", nil, nil)

	for _, r := range []rune("abcd") {
		dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: r})
	}
	dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyLeft})
	dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyLeft})
	dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyBackspace})

	if got := root.draft.Get(); got != "acd" {
		t.Fatalf("expected cursor backspace to edit draft, got %q", got)
	}
}

func TestGoTUIInputCtrlJInsertsNewline(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, nil, "", nil, nil)

	for _, r := range []rune("hello") {
		dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: r})
	}
	dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: 'j', Mod: gotui.ModCtrl})

	if got := root.draft.Get(); got != "hello\n" {
		t.Fatalf("expected ctrl+j to insert newline, got %q", got)
	}
}

func TestGoTUIInputRendersChineseCursorWithoutInsertedGlyph(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, nil, "", nil, nil)
	root.draft.Set("中文")
	root.cursor = 1

	got := collectGoTUIText(root.renderInput(80))
	if strings.Contains(got, "▌") {
		t.Fatalf("did not expect inserted cursor glyph in CJK input, got %q", got)
	}
	if !strings.Contains(got, "中文") {
		t.Fatalf("expected original Chinese text to remain contiguous, got %q", got)
	}
}

func TestGoTUIApprovalCancelClearsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelCh := make(chan struct{})
	root := newGoTUIRoot(ctx, nil, nil, nil, "", nil, nil)
	root.handleApprovalRequest(approval.Request{
		ToolName:   "bash",
		ToolCallID: "call-1",
		Response:   make(chan string, 1),
		Cancel:     cancelCh,
	})

	close(cancelCh)

	deadline := time.After(time.Second)
	for {
		if root.model.pendingPerm == nil {
			if root.model.statusMsg != "approval dismissed" {
				t.Fatalf("expected dismissed status, got %q", root.model.statusMsg)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("expected approval cancel to clear pending request")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestGoTUIAbortPendingApprovalDeniesResponse(t *testing.T) {
	responseCh := make(chan string, 1)
	root := newGoTUIRoot(context.Background(), nil, nil, nil, "", nil, nil)
	root.model.state = uiStatePermission
	root.handleApprovalRequest(approval.Request{
		ToolName:   "bash",
		ToolCallID: "call-1",
		Response:   responseCh,
	})

	root.abortQuery()

	select {
	case got := <-responseCh:
		if got != "deny" {
			t.Fatalf("expected abort to deny pending approval, got %q", got)
		}
	default:
		t.Fatal("expected abort to resolve pending approval")
	}
	if root.model.pendingPerm != nil {
		t.Fatal("expected pending approval to be cleared")
	}
}

func TestParseGoTUIANSITextPreservesStyledSegments(t *testing.T) {
	segments := parseGoTUIANSIText("plain \x1b[31;1mred\x1b[0m tail")
	if len(segments) != 3 {
		t.Fatalf("expected 3 segments, got %#v", segments)
	}
	if segments[0].Text != "plain " || segments[1].Text != "red" || segments[2].Text != " tail" {
		t.Fatalf("unexpected segment text: %#v", segments)
	}
	if !segments[1].Style.Fg.Equal(gotui.Red) {
		t.Fatalf("expected red foreground, got %#v", segments[1].Style.Fg)
	}
	if !segments[1].Style.HasAttr(gotui.AttrBold) {
		t.Fatal("expected bold segment")
	}
	if !segments[2].Style.Equal(gotui.NewStyle()) {
		t.Fatalf("expected reset style for tail, got %#v", segments[2].Style)
	}
}

func TestParseGoTUIANSITextPreservesTrueColor(t *testing.T) {
	segments := parseGoTUIANSIText("\x1b[38;2;12;34;56mcode")
	if len(segments) != 1 {
		t.Fatalf("expected one segment, got %#v", segments)
	}
	if !segments[0].Style.Fg.Equal(gotui.RGBColor(12, 34, 56)) {
		t.Fatalf("expected truecolor foreground, got %#v", segments[0].Style.Fg)
	}
}

func dispatchFirstGoTUIKey(km gotui.KeyMap, ev gotui.KeyEvent) bool {
	for _, binding := range km {
		if !goTUIKeyPatternMatches(binding.Pattern, ev) {
			continue
		}
		binding.Handler(ev)
		return true
	}
	return false
}

func goTUIKeyPatternMatches(pattern gotui.KeyPattern, ev gotui.KeyEvent) bool {
	if pattern.AnyKey {
		return true
	}
	if pattern.ExcludeMods != 0 && ev.Mod&pattern.ExcludeMods != 0 {
		return false
	}
	if pattern.Mod != 0 && ev.Mod != pattern.Mod {
		return false
	}
	if pattern.AnyRune && ev.Key == gotui.KeyRune {
		return true
	}
	if pattern.Rune != 0 && ev.Key == gotui.KeyRune && ev.Rune == pattern.Rune {
		return true
	}
	return pattern.Key != 0 && ev.Key == pattern.Key
}

func collectGoTUIText(el *gotui.Element) string {
	return uiANSIPattern.ReplaceAllString(gotui.Sprint(el, gotui.WithPrintWidth(80)), "")
}

// renderAllBlocks joins per-block scrollback renders the way pkg/tui pushes
// them at runtime, so tests can exercise the same path as production.
func renderAllBlocks(m *uiModel) string {
	var parts []string
	for _, b := range m.blocks {
		if s := strings.TrimRight(m.renderSingleBlock(b), "\n"); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func TestUIRenderBlocksIncludesUserAndAssistantContent(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.width = 100
	m.ready = true
	m.blocks = []uiBlock{
		{Kind: "user", Content: "hello"},
		{Kind: "assistant", Content: "world"},
	}

	got := renderAllBlocks(m)
	if !strings.Contains(got, "hello") {
		t.Fatalf("expected user content, got %q", got)
	}
	if !strings.Contains(got, "world") {
		t.Fatalf("expected assistant content, got %q", got)
	}
}

func TestUIRenderBlocksUsesBulletPrefixes(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.width = 100
	m.blocks = []uiBlock{
		{Kind: "assistant", Thinking: "step one", Content: "answer"},
		{Kind: "tool", Tools: []*uiToolState{{Name: "read", Status: "running"}}},
	}

	got := renderAllBlocks(m)
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

func TestUIRenderBlocksMarkdownDoesNotDuplicateHeadings(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.width = 72
	m.blocks = []uiBlock{
		{Kind: "assistant", Content: "### render.go (+39 -1)\n\n- first item\n- second item"},
	}

	got := ansiPattern.ReplaceAllString(renderAllBlocks(m), "")
	if strings.Count(got, "render.go (+39 -1)") != 1 {
		t.Fatalf("expected markdown heading once, got %q", got)
	}
}

func TestNormalizeRenderedMarkdownPreservesStylingCollapsesBlanks(t *testing.T) {
	raw := "\x1b[38;5;39mtitle\x1b[0m\n\x1b[38;5;252m     \x1b[0m\n"
	got := normalizeRenderedMarkdown(raw)
	// glamour's heading colors must survive — losing them was the original
	// bug that turned styled markdown into plain text.
	if !strings.Contains(got, "\x1b[38;5;39m") {
		t.Fatalf("expected ANSI styling preserved, got %q", got)
	}
	if !strings.Contains(got, "title") {
		t.Fatalf("expected heading text preserved, got %q", got)
	}
	// The ANSI-padded whitespace-only line collapses to a single blank.
	if strings.Count(got, "\n") > 1 {
		t.Fatalf("expected one separator newline, got %q", got)
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

func TestHandleToolExecutionEndUpdatesByToolCallID(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, nil, "", nil, nil, "")
	m.handleAgentEvent(agent.AgentEvent{Type: agent.EventTypeToolExecutionStart, ToolCallID: "call-1", ToolName: "bash"})
	m.handleAgentEvent(agent.AgentEvent{Type: agent.EventTypeToolExecutionStart, ToolCallID: "call-2", ToolName: "bash"})

	m.handleAgentEvent(agent.AgentEvent{
		Type:       agent.EventTypeToolExecutionEnd,
		ToolCallID: "call-2",
		ToolName:   "bash",
		Result:     "done",
	})

	tools := m.blocks[0].Tools
	if tools[0].Status != "running" {
		t.Fatalf("expected first same-name tool to remain running, got %q", tools[0].Status)
	}
	if tools[1].Status != "done" || tools[1].Output != "done" {
		t.Fatalf("expected second tool to be completed, got status=%q output=%q", tools[1].Status, tools[1].Output)
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
		ID:         "qwen/qwen3.6-35b-a3b",
		Name:       "qwen/qwen3.6-35b-a3b (lmstudio)",
		ProviderID: "lmstudio",
	}
	m := newUIModel(context.Background(), session, model, nil, "", nil, nil, "")
	got := m.renderInputMeta()
	if strings.Count(strings.ToLower(got), "lmstudio") != 1 {
		t.Fatalf("expected provider to appear once, got %q", got)
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
	m.blocks = []uiBlock{
		{
			Kind:    "assistant",
			Content: "```txt\n" + strings.Repeat("LongToken", 12) + "\n```",
		},
	}

	got := renderAllBlocks(m)
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

func TestRenderUIAssistantBlockContinuationAlignsWithFirstLine(t *testing.T) {
	got := renderUIAssistantBlock("first line\nsecond line", 80)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %q", got)
	}
	if !strings.HasPrefix(lines[0], blockIndent+"● ") {
		t.Fatalf("expected indented bullet prefix on first line, got %q", lines[0])
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
