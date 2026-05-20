package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	gotui "github.com/grindlemire/go-tui"

	"github.com/openmodu/modu/pkg/agent"
	"github.com/openmodu/modu/pkg/approval"
	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	sessionpkg "github.com/openmodu/modu/pkg/coding_agent/session"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

func TestGoTUIApprovalUsesCoreDecisionNames(t *testing.T) {
	responseCh := make(chan string, 1)
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
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
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
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
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
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

func TestApprovalWidgetShowsLayeredToolDetails(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.model.pendingPerm = &approval.Request{
		ToolName: "bash",
		Args:     map[string]any{"command": "go test ./pkg/tui"},
	}

	text := collectGoTUIText(root.renderApprovalWidget())
	for _, want := range []string{"Permission required", "tool: bash", "go test ./pkg/tui", "actions:", "[Y]es", "[D]eny always"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected approval widget to contain %q, got %q", want, text)
		}
	}
}

func TestPlanApprovalWidgetShowsStepCountAndRisk(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.model.pendingPerm = &approval.Request{
		ToolName: "exit_plan_mode",
		Args: map[string]any{
			"steps": []any{"inspect", "", "implement"},
		},
	}

	text := collectGoTUIText(root.renderApprovalWidget())
	for _, want := range []string{"Plan approval", "steps=2", "auto-accept allows write/edit/bash", "[Y]es, start coding"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected plan approval widget to contain %q, got %q", want, text)
		}
	}
}

func TestGoTUIInputUsesCursorEditing(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)

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
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)

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
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)

	for _, r := range []rune("hello") {
		dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: r})
	}
	dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: 'j', Mod: gotui.ModCtrl})

	if got := root.draft.Get(); got != "hello\n" {
		t.Fatalf("expected ctrl+j to insert newline, got %q", got)
	}
}

func TestGoTUIInputRendersChineseCursorWithoutInsertedGlyph(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
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
	root := newGoTUIRoot(ctx, nil, nil, "", nil, nil)
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
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
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
	m := newUIModel(context.Background(), nil, nil, "", nil, nil, "")
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

func TestUIUserBlockUsesPromptBackground(t *testing.T) {
	raw := renderUIUserBlock("hello prompt", 80)
	plain := ansiPattern.ReplaceAllString(raw, "")
	if !strings.Contains(plain, "❯ hello prompt") {
		t.Fatalf("expected user prompt text with ❯ glyph, got %q", plain)
	}
	if bg := uiUserPrompt.GetBackground(); bg == nil {
		t.Fatalf("expected user prompt background style, got nil")
	}
	external := ansiPattern.ReplaceAllString(renderUIUserBlockWithSource("remote prompt", "external", 80), "")
	if !strings.Contains(external, "◆ remote prompt") {
		t.Fatalf("expected external prompt marker, got %q", external)
	}
	if bg := uiExternalUserPrompt.GetBackground(); bg == nil {
		t.Fatalf("expected external user prompt background style, got nil")
	}
}

func TestUIRenderBlocksUsesBulletPrefixes(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, "", nil, nil, "")
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
	m := newUIModel(context.Background(), nil, nil, "", nil, nil, "")
	m.width = 72
	m.blocks = []uiBlock{
		{Kind: "assistant", Content: "### render.go (+39 -1)\n\n- first item\n- second item"},
	}

	got := ansiPattern.ReplaceAllString(renderAllBlocks(m), "")
	if strings.Count(got, "render.go (+39 -1)") != 1 {
		t.Fatalf("expected markdown heading once, got %q", got)
	}
}

func TestUISectionRendersStructuredKeyValues(t *testing.T) {
	raw := renderUISection("Plan", "active: true\ntodos: total=2 pending=1\n\nNavigation\n  Enter: submit", 80)
	plain := ansiPattern.ReplaceAllString(raw, "")
	for _, want := range []string{
		"Plan",
		"active: true",
		"todos: total=2 pending=1",
		"Navigation",
		"  Enter: submit",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected section to contain %q, got %q", want, plain)
		}
	}
}

func TestSplitSectionKeyValueSkipsIndentedLines(t *testing.T) {
	if key, value, ok := splitSectionKeyValue("active: true"); !ok || key != "active" || value != "true" {
		t.Fatalf("expected key/value split, got key=%q value=%q ok=%v", key, value, ok)
	}
	if _, _, ok := splitSectionKeyValue("  Enter: submit"); ok {
		t.Fatal("expected indented hotkey line to stay unstructured")
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
	got := renderUIToolOutput("bash", "this is a long line that should wrap instead of disappearing", "", true, 24)
	if !strings.Contains(got, "this is a") {
		t.Fatalf("expected first wrapped segment, got %q", got)
	}
	if !strings.Contains(got, "should wrap") {
		t.Fatalf("expected later wrapped segment, got %q", got)
	}
}

func TestRenderToolOutputCollapsedShowsExpandHint(t *testing.T) {
	got := renderUIToolOutput("read", "l1\nl2\nl3\nl4\nl5", "", false, 80)
	if !strings.Contains(got, "ctrl+o to expand") {
		t.Fatalf("expected expand hint, got %q", got)
	}
	if strings.Contains(got, "l5") {
		t.Fatalf("expected collapsed output to hide later lines, got %q", got)
	}
}

func TestRenderToolOutputCollapsedShowsExpandHintForWrappedSingleLine(t *testing.T) {
	got := renderUIToolOutput("bash", "this is one extremely long output line that should wrap into many terminal rows and still show the expand hint", "", false, 24)
	if !strings.Contains(got, "ctrl+o to expand") {
		t.Fatalf("expected expand hint for wrapped single line, got %q", got)
	}
}

func TestMatchSlashCommandsMergesBuiltinsAndSkills(t *testing.T) {
	// Built-ins win the top spots; skill entries fill in below, only when
	// they match the prefix.
	extras := []slashCommandDef{
		{Name: "/git-commit", Description: "create a commit"},
		{Name: "/git-branch", Description: "create a branch"},
		{Name: "/security-review", Description: "review for vulns"},
	}

	all := matchSlashCommands("/", extras)
	gotNames := make(map[string]bool, len(all))
	for _, c := range all {
		gotNames[c.Name] = true
	}
	for _, want := range []string{"/help", "/skills", "/git-commit", "/git-branch", "/security-review"} {
		if !gotNames[want] {
			t.Fatalf("expected %q in merged matches, got %v", want, gotNames)
		}
	}

	gitOnly := matchSlashCommands("/git", extras)
	if len(gitOnly) != 2 {
		t.Fatalf("expected 2 /git matches (skills only), got %d: %v", len(gitOnly), gitOnly)
	}

	// Prefix that matches neither built-ins nor skills returns empty.
	if got := matchSlashCommands("/zzz", extras); len(got) != 0 {
		t.Fatalf("expected no matches for /zzz, got %v", got)
	}
}

func TestRenderEditToolOutputSyntaxHighlightsWhenFilePathKnown(t *testing.T) {
	// A Go-flavored diff. With a .go file path the keyword `func` should
	// receive chroma's monokai keyword color (an SGR sequence). Without a
	// file path the line should fall back to a single dim style with no
	// per-token coloring.
	diff := "- func old() {}\n+ func new() {}"

	withPath := renderUIToolOutput("edit", diff, "main.go", true, 120)
	if !strings.Contains(withPath, "\x1b[38;5;") {
		t.Fatalf("expected chroma SGR colors when file path is known, got %q", withPath)
	}
	// The `+` and `-` diff markers must still be visible.
	if !strings.Contains(ansiPattern.ReplaceAllString(withPath, ""), "+ func new()") {
		t.Fatalf("expected `+` marker preserved on added line, got %q", withPath)
	}
	if !strings.Contains(ansiPattern.ReplaceAllString(withPath, ""), "- func old()") {
		t.Fatalf("expected `-` marker preserved on removed line, got %q", withPath)
	}

	withoutPath := renderUIToolOutput("edit", diff, "", true, 120)
	// Strip ANSI to confirm the visible content still has the diff markers.
	if !strings.Contains(ansiPattern.ReplaceAllString(withoutPath, ""), "+ func new()") {
		t.Fatalf("expected `+` marker preserved without file path, got %q", withoutPath)
	}
}

func TestHandleToolExecutionEndUpdatesByToolCallID(t *testing.T) {
	m := newUIModel(context.Background(), nil, nil, "", nil, nil, "")
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
	m := newUIModel(context.Background(), session, model, "", nil, nil, "")
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
	m := newUIModel(context.Background(), session, model, "", nil, nil, "")
	got := m.renderInputMeta()
	if strings.Count(strings.ToLower(got), "lmstudio") != 1 {
		t.Fatalf("expected provider to appear once, got %q", got)
	}
}

func TestFormatActivityDurationUsesMinutes(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "seconds", in: 42 * time.Second, want: "42s"},
		{name: "whole minute", in: time.Minute, want: "1min"},
		{name: "minute and seconds", in: 75 * time.Second, want: "1min 15s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatActivityDuration(tt.in); got != tt.want {
				t.Fatalf("formatActivityDuration(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestActivityLinePersistsCompletedTurn(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.model.queryStartTime = time.Now().Add(-75 * time.Second)
	root.model.finishActivity(nil)

	got, ok := root.activityLine()
	if !ok {
		t.Fatal("expected completed activity line")
	}
	got = uiANSIPattern.ReplaceAllString(got, "")
	if !strings.Contains(got, "Completed") || !strings.Contains(got, "1min 15s") {
		t.Fatalf("expected persistent completed duration, got %q", got)
	}
	if bottom, _ := root.bottomLine(); strings.Contains(bottom, "Completed") {
		t.Fatalf("expected completed activity above input, not in bottom line: %q", bottom)
	}
}

func TestTransientStatusExpiresToIdleLine(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.model.setTransientStatus("saved")
	root.model.statusExpiresAt = time.Now().Add(-time.Second)

	line, _ := root.bottomLine()
	if strings.Contains(line, "saved") {
		t.Fatalf("expected expired status to clear, got %q", line)
	}
	if root.model.statusMsg != "" {
		t.Fatalf("expected model status to be cleared, got %q", root.model.statusMsg)
	}
}

func TestPersistentStatusIgnoresStaleTransientExpiry(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.model.setTransientStatus("saved")
	root.model.statusExpiresAt = time.Now().Add(-time.Second)
	root.model.statusMsg = "permission required"

	line, _ := root.bottomLine()
	if !strings.Contains(line, "permission required") {
		t.Fatalf("expected persistent status to remain visible, got %q", line)
	}
}

func TestCompletedActivityExpires(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.model.queryStartTime = time.Now().Add(-2 * time.Second)
	root.model.finishActivity(nil)
	root.model.activityExpiresAt = time.Now().Add(-time.Second)

	if got, ok := root.activityLine(); ok {
		t.Fatalf("expected expired activity to clear, got %q", got)
	}
	if root.model.lastActivity != "" {
		t.Fatalf("expected model activity to be cleared, got %q", root.model.lastActivity)
	}
}

func TestModelSelectEnterSwitchesModel(t *testing.T) {
	providers.Models["ui-model-select"] = map[string]*types.Model{
		"model-a": {ID: "model-a", Name: "Model A", ProviderID: "ui-model-select"},
		"model-b": {ID: "model-b", Name: "Model B", ProviderID: "ui-model-select"},
	}
	session := newUITestSession(t)
	session.SetModel(providers.Models["ui-model-select"]["model-a"])
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)

	root.openModelSelect()
	if root.model.state != uiStateModelSelect {
		t.Fatalf("expected model select state, got %v", root.model.state)
	}
	root.modelSearch = "model-b"
	root.filterModelChoices()
	header := collectGoTUIText(root.renderModelSelectWidget())
	for _, want := range []string{"Select model", "search: model-b", "mode: scope="} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected model selector header to contain %q, got %q", want, header)
		}
	}
	root.confirmModelSelect()

	if got := session.GetModel(); got.ID != "model-b" {
		t.Fatalf("expected model-b, got %#v", got)
	}
	if root.model.state != uiStateInput {
		t.Fatalf("expected input state after confirm, got %v", root.model.state)
	}
	if !strings.Contains(root.model.statusMsg, "context cleared") {
		t.Fatalf("expected model switch status to mention cleared context, got %q", root.model.statusMsg)
	}
}

func TestSessionSelectResumeForkDelete(t *testing.T) {
	session := newUITestSession(t)
	session.SetSessionName("active")
	agentDir := filepath.Dir(filepath.Dir(filepath.Dir(session.GetSessionFile())))

	sourceFile := writeUITestSessionFile(t, agentDir, filepath.Join(t.TempDir(), "source"), "source session", "resume from picker")
	deleteFile := writeUITestSessionFile(t, agentDir, filepath.Join(t.TempDir(), "delete"), "delete session", "remove me")
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)

	root.openSessionSelect(true)
	if root.model.state != uiStateSessionSelect {
		t.Fatalf("expected session select state, got %v", root.model.state)
	}
	for _, r := range "resume" {
		root.appendSessionText(r)
	}
	if got := len(root.sessionChoices); got == 0 {
		t.Fatal("expected search to keep matching session")
	}
	header := collectGoTUIText(root.renderSessionSelectWidget())
	for _, want := range []string{"Select session", "search: resume", "mode: scope=all sort=threaded filter=all"} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected session selector header to contain %q, got %q", want, header)
		}
	}
	root.sessionSearch = ""
	root.filterSessionChoices()
	root.sessionSelectIdx = indexSessionChoice(t, root.sessionChoices, sourceFile)
	root.confirmSessionSelect()
	if got := session.GetSessionFile(); got != sourceFile {
		t.Fatalf("expected resumed source session, got %q want %q", got, sourceFile)
	}
	if got := session.GetMessages(); len(got) != 1 {
		t.Fatalf("expected resumed messages, got %#v", got)
	}

	root.openSessionSelect(true)
	root.sessionSelectIdx = indexSessionChoice(t, root.sessionChoices, sourceFile)
	root.forkSessionSelect()
	forkedFile := session.GetSessionFile()
	if forkedFile == "" || forkedFile == sourceFile {
		t.Fatalf("expected forked session file, got %q source %q", forkedFile, sourceFile)
	}
	if _, err := os.Stat(forkedFile); err != nil {
		t.Fatalf("expected forked file to exist: %v", err)
	}
	if got := session.GetMessages(); len(got) != 1 {
		t.Fatalf("expected forked messages, got %#v", got)
	}

	root.openSessionSelect(true)
	root.sessionSelectIdx = indexSessionChoice(t, root.sessionChoices, deleteFile)
	root.startSessionRename()
	root.sessionRenameText = "renamed delete session"
	root.confirmSessionRename()
	root.sessionSelectIdx = indexSessionChoice(t, root.sessionChoices, deleteFile)
	if got := root.sessionChoices[root.sessionSelectIdx].Name; got != "renamed delete session" {
		t.Fatalf("expected renamed session, got %q", got)
	}
	root.startDeleteSessionSelect()
	if root.sessionConfirmDelete == "" {
		t.Fatal("expected delete confirmation")
	}
	root.deleteSessionSelect()
	if _, err := os.Stat(deleteFile); !os.IsNotExist(err) {
		t.Fatalf("expected deleted session file, stat err=%v", err)
	}
	if root.model.statusMsg != "deleted session" {
		t.Fatalf("expected deleted status, got %q", root.model.statusMsg)
	}
}

func TestScopedModelsSelectorTogglesSessionScope(t *testing.T) {
	providers.Models["ui-scoped-models"] = map[string]*types.Model{
		"scoped-a": {ID: "scoped-a", Name: "Scoped A", ProviderID: "ui-scoped-models"},
		"scoped-b": {ID: "scoped-b", Name: "Scoped B", ProviderID: "ui-scoped-models"},
	}
	session := newUITestSession(t)
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)

	root.openScopedModelsSelect()
	root.modelSearch = "scoped-b"
	root.filterModelChoices()
	if len(root.modelChoices) != 1 || root.modelChoices[0].ID != "scoped-b" {
		t.Fatalf("expected scoped-b search result, got %#v", root.modelChoices)
	}
	root.toggleScopedModelSelection()

	for _, id := range session.GetScopedModelIDs() {
		if id == "scoped-b" {
			t.Fatalf("expected scoped-b removed from scope, got %v", session.GetScopedModelIDs())
		}
	}
	if len(session.GetScopedModelIDs()) == 0 {
		t.Fatal("expected remaining scoped model ids")
	}
}

func TestTreeSelectNavigatesWithBranchSummary(t *testing.T) {
	session := newUITestSession(t)
	if err := session.Prompt(context.Background(), "first tree prompt"); err != nil {
		t.Fatal(err)
	}
	session.WaitForIdle()
	if err := session.Prompt(context.Background(), "second tree prompt"); err != nil {
		t.Fatal(err)
	}
	session.WaitForIdle()

	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)
	root.openTreeSelect()
	if root.model.state != uiStateTreeSelect {
		t.Fatalf("expected tree select state, got %v", root.model.state)
	}
	for _, r := range "first tree" {
		root.appendTreeSearch(r)
	}
	if len(root.treeNodes) == 0 {
		t.Fatal("expected tree search result")
	}
	root.confirmTreeSelect()
	if root.model.state != uiStateInput {
		t.Fatalf("expected input state after tree navigation, got %v", root.model.state)
	}
	if !strings.Contains(root.model.statusMsg, "jumped") {
		t.Fatalf("expected jumped status, got %q", root.model.statusMsg)
	}
	messages := session.GetMessages()
	if len(messages) == 0 {
		t.Fatal("expected restored messages")
	}
	lastText := uiTestMessageText(messages[len(messages)-1])
	if !strings.Contains(lastText, "[Branch Navigation Summary]") || !strings.Contains(lastText, "first tree prompt") {
		t.Fatalf("expected branch summary message for selected point, got %q", lastText)
	}
}

func TestTreeNodeLineShowsIDRoleLabelAndBranchCount(t *testing.T) {
	line := treeNodeLine(coding_agent.SessionTreeNode{
		ID:            "1234567890abcdef",
		Type:          "message",
		Role:          "assistant",
		Label:         "main branch",
		Preview:       "hello\nworld",
		ChildCount:    3,
		InCurrentPath: true,
	}, true, false)

	for _, want := range []string{"#12345678", "assistant", "[main branch]", "hello world", "branches=3"} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected tree line to contain %q, got %q", want, line)
		}
	}
}

func TestTreeNodeLineShowsBranchSummaryFallbackLabel(t *testing.T) {
	line := treeNodeLine(coding_agent.SessionTreeNode{
		ID:      "branch-entry",
		Type:    "branch_summary",
		Label:   "from #12345678",
		Preview: "restored context",
	}, false, false)
	for _, want := range []string{"branch", "[from #12345678]", "restored context"} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected tree line to contain %q, got %q", want, line)
		}
	}
}

func TestSelectorHeaderLineShowsCountsQueryAndMode(t *testing.T) {
	line := selectorHeaderLine(selectorHeaderOptions{
		Title:    "Session tree",
		Selected: 2,
		Visible:  4,
		Total:    9,
		Query:    "branch",
		Mode:     "summary",
	})
	for _, want := range []string{"Session tree", "3/4", "filtered 4/9", "search: branch", "mode: summary"} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected selector header to contain %q, got %q", want, line)
		}
	}
}

func TestFileReferenceSuggestionsCompleteAndExpandPrompt(t *testing.T) {
	session := newUITestSession(t)
	cwd := session.GetContextInfo().Cwd
	if err := os.MkdirAll(filepath.Join(cwd, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "pkg", "target.go"), []byte("package pkg\n\nconst Answer = 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)
	root.draft.Set("inspect @target")
	root.cursor = len([]rune(root.draft.Get()))

	root.updateInputSuggestions()
	if len(root.fileMatches) == 0 || root.fileMatches[0].Path != "pkg/target.go" {
		t.Fatalf("expected pkg/target.go suggestion, got %#v", root.fileMatches)
	}
	if !root.completeFileMatch() {
		t.Fatal("expected file completion")
	}
	if got := root.draft.Get(); got != "inspect @pkg/target.go" {
		t.Fatalf("unexpected completed draft %q", got)
	}

	expanded := root.expandFileReferencesForPrompt(root.draft.Get())
	if !strings.Contains(expanded, "Referenced files") || !strings.Contains(expanded, "const Answer = 42") {
		t.Fatalf("expected referenced file content, got %q", expanded)
	}
}

func TestPathTokenCompletion(t *testing.T) {
	session := newUITestSession(t)
	cwd := session.GetContextInfo().Cwd
	if err := os.MkdirAll(filepath.Join(cwd, "cmd", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)
	root.draft.Set("open ./cm")
	root.cursor = len([]rune(root.draft.Get()))

	if !root.completePathToken() {
		t.Fatal("expected path token completion")
	}
	if got := root.draft.Get(); got != "open ./cmd/" {
		t.Fatalf("unexpected completed path %q", got)
	}
}

func TestShellShortcutSendsSingleBangToModel(t *testing.T) {
	session := newUITestSession(t)
	var promptMu sync.Mutex
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, &promptMu)

	root.submit("!printf model-output")
	waitForUITestIdle(t, root, session)

	messages := session.GetMessages()
	if len(messages) == 0 {
		t.Fatal("expected shell output prompt to reach model")
	}
	if got := uiTestMessageText(messages[0]); !strings.Contains(got, "$ printf model-output") || !strings.Contains(got, "model-output") {
		t.Fatalf("expected shell command and output in prompt, got %q", got)
	}
}

func TestShellShortcutDoubleBangDoesNotSendToModel(t *testing.T) {
	session := newUITestSession(t)
	var promptMu sync.Mutex
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, &promptMu)

	root.submit("!!printf display-only")
	waitForUITestCondition(t, time.Second, func() bool {
		for _, block := range root.model.blocks {
			if block.Kind == "system" && strings.Contains(block.Content, "display-only") {
				return true
			}
		}
		return false
	})

	if got := len(session.GetMessages()); got != 0 {
		t.Fatalf("expected no model messages for !! shell command, got %d", got)
	}
}

func TestConfigHookCommandRendersOutput(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.commandHooks.Config = func(args string) (string, error) {
		if args != "validate" {
			t.Fatalf("unexpected config args %q", args)
		}
		return "status: ok\n", nil
	}

	root.submit("/config validate")

	waitForUITestCondition(t, time.Second, func() bool {
		for _, block := range root.model.blocks {
			if block.Title == "Config" && strings.Contains(block.Content, "status: ok") {
				return true
			}
		}
		return false
	})
}

func TestResourceSelectFiltersAndInsertsCommand(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.model.state = uiStateResourceSelect
	root.resourceTitle = "Skills"
	root.resourceAllChoices = []resourceChoice{
		{Name: "git-commit", Description: "create a commit", Source: "user"},
		{Name: "security-review", Description: "review code", Source: "project"},
	}

	for _, ch := range "security" {
		root.appendResourceSearch(ch)
	}
	if len(root.resourceChoices) != 1 || root.resourceChoices[0].Name != "security-review" {
		t.Fatalf("expected security-review match, got %#v", root.resourceChoices)
	}
	root.confirmResourceSelect()
	if root.model.state != uiStateInput {
		t.Fatalf("expected input state, got %v", root.model.state)
	}
	if got := root.draft.Get(); got != "/security-review " {
		t.Fatalf("expected inserted command, got %q", got)
	}
}

func TestResourceSelectPagingAndPathDisplay(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	for i := range resourceSelectVisibleRows + 3 {
		root.resourceChoices = append(root.resourceChoices, resourceChoice{Name: fmt.Sprintf("skill-%02d", i)})
	}

	root.moveResourceSelect(resourceSelectVisibleRows)
	if root.resourceSelectIdx != resourceSelectVisibleRows {
		t.Fatalf("expected page down to move to %d, got %d", resourceSelectVisibleRows, root.resourceSelectIdx)
	}
	if root.resourceSelectScroll != 1 {
		t.Fatalf("expected scroll to keep selected row visible, got %d", root.resourceSelectScroll)
	}

	line := resourceChoiceLine(resourceChoice{
		Name:        "review",
		Description: "check changes",
		Source:      "project",
		Path:        "/Users/test/.codex/skills/review/SKILL.md",
	}, true)
	if !strings.Contains(line, "[project] /Users/test/.codex/skills/review/SKILL.md") {
		t.Fatalf("expected source and path in resource line, got %q", line)
	}
}

func TestPersistedTUISettingsRoundTrip(t *testing.T) {
	session := newUITestSession(t)
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)
	root.model.transcriptMode = true
	if err := root.savePersistedTUISettings(); err != nil {
		t.Fatal(err)
	}

	next := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)
	next.loadPersistedTUISettings()
	if !next.model.transcriptMode {
		t.Fatal("expected persisted transcript mode")
	}
}

func TestCtrlOTogglesTranscriptModeAndStatus(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)

	if !dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: 'o', Mod: gotui.ModCtrl}) {
		t.Fatal("expected ctrl+o to be handled")
	}
	if !root.model.transcriptMode {
		t.Fatal("expected ctrl+o to expand tool output")
	}
	if root.model.statusMsg != "tool output expanded" {
		t.Fatalf("expected expanded status, got %q", root.model.statusMsg)
	}

	if !dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: 'o', Mod: gotui.ModCtrl}) {
		t.Fatal("expected second ctrl+o to be handled")
	}
	if root.model.transcriptMode {
		t.Fatal("expected second ctrl+o to collapse tool output")
	}
	if root.model.statusMsg != "tool output collapsed" {
		t.Fatalf("expected collapsed status, got %q", root.model.statusMsg)
	}
}

func TestCtrlOTogglesTranscriptModeDuringApproval(t *testing.T) {
	responseCh := make(chan string, 1)
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.handleApprovalRequest(approval.Request{
		ToolName:   "bash",
		ToolCallID: "call-1",
		Response:   responseCh,
	})

	if !dispatchFirstGoTUIKey(root.KeyMap(), gotui.KeyEvent{Key: gotui.KeyRune, Rune: 'o', Mod: gotui.ModCtrl}) {
		t.Fatal("expected approval ctrl+o to be handled")
	}
	if !root.model.transcriptMode {
		t.Fatal("expected ctrl+o to expand tool output during approval")
	}
	if root.model.pendingPerm == nil {
		t.Fatal("expected approval to remain pending")
	}
	select {
	case got := <-responseCh:
		t.Fatalf("expected ctrl+o not to resolve approval, got %q", got)
	default:
	}
}

func TestSettingsSelectPagingClamps(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.settingsChoices = []settingsChoice{
		{Label: "one"},
		{Label: "two"},
		{Label: "three"},
	}

	root.pageSettingsSelect(settingsSelectVisibleRows)
	if root.settingsSelectIdx != 2 {
		t.Fatalf("expected page down to clamp to last setting, got %d", root.settingsSelectIdx)
	}
	root.pageSettingsSelect(-settingsSelectVisibleRows)
	if root.settingsSelectIdx != 0 {
		t.Fatalf("expected page up to clamp to first setting, got %d", root.settingsSelectIdx)
	}
}

func TestPromptErrorCollapsesRepeatsAndOffersActions(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	err := errors.New("max retries (3) exceeded: dial tcp 127.0.0.1:1234: connect: operation timed out")

	root.model.setPromptError(err)
	root.model.setPromptError(err)

	line, _ := root.bottomLine()
	for _, want := range []string{
		"repeated 2x",
		"/retry",
		"/model",
		"/doctor",
		"ctrl+c",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected bottom line to contain %q, got %q", want, line)
		}
	}
}

func TestBottomLineShowsSessionStatusWhenIdle(t *testing.T) {
	session := newUITestSession(t)
	session.EnterPlanMode()
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, nil)

	line, _ := root.bottomLine()
	for _, want := range []string{
		"model test/test-model",
		"plan",
		"shift+tab plan",
		"/help",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected bottom line to contain %q, got %q", want, line)
		}
	}
}

func TestHotkeyHelpIncludesSelectorAndResourceCommands(t *testing.T) {
	text := hotkeyHelpText()
	for _, want := range []string{
		"PageUp/PageDown: scroll transcript or selector page",
		"Tree: Ctrl+F branch-session, Ctrl+S summary",
		"/skills",
		"/prompts",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected hotkey help to contain %q, got %q", want, text)
		}
	}
}

func TestPlanAndWorktreePanelsRenderLifecycleState(t *testing.T) {
	session := newUITestSession(t)
	session.EnterPlanMode()
	session.ExitPlanMode("panel plan", []string{"ship panel"})

	plan := planPanelContent(session)
	for _, want := range []string{"active: no", "latest: yes", "revisions: 1", "[pending] ship panel"} {
		if !strings.Contains(plan, want) {
			t.Fatalf("expected plan panel to contain %q, got:\n%s", want, plan)
		}
	}

	worktree := worktreePanelContent(session)
	for _, want := range []string{"active: no", "managed worktrees: 0"} {
		if !strings.Contains(worktree, want) {
			t.Fatalf("expected worktree panel to contain %q, got:\n%s", want, worktree)
		}
	}
}

func TestPromptErrorCompactsLongMultilineText(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)
	root.model.setPromptError(errors.New(strings.Repeat("long line\n", 80)))

	if strings.Contains(root.model.errMsg, "\n") {
		t.Fatalf("expected single-line error, got %q", root.model.errMsg)
	}
	maxWithHint := maxInlineErrorChars + len(" | try: /retry, /model, /doctor, ctrl+c")
	if len(root.model.errMsg) > maxWithHint {
		t.Fatalf("expected compact error, got %d chars: %q", len(root.model.errMsg), root.model.errMsg)
	}
}

func TestRetryWithoutFailedPromptSetsStatus(t *testing.T) {
	root := newGoTUIRoot(context.Background(), nil, nil, "", nil, nil)

	root.submit("/retry")

	if root.model.statusMsg != "no failed prompt to retry" {
		t.Fatalf("unexpected retry status: %q", root.model.statusMsg)
	}
}

func TestRetrySubmitsLastFailedPrompt(t *testing.T) {
	session := newUITestSession(t)
	var promptMu sync.Mutex
	root := newGoTUIRoot(context.Background(), session, session.GetModel(), "", nil, &promptMu)
	root.lastFailedPrompt = "hello again"

	root.submit("/retry")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !root.model.queryActive && len(session.GetMessages()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if root.model.queryActive {
		t.Fatal("expected retry prompt to finish")
	}
	if root.lastFailedPrompt != "" {
		t.Fatalf("expected successful retry to clear lastFailedPrompt, got %q", root.lastFailedPrompt)
	}
	if got := len(session.GetMessages()); got == 0 {
		t.Fatal("expected retry to submit a prompt")
	}
}

func TestViewportConversationWrapsWithinViewportWidth(t *testing.T) {
	session := newUITestSession(t)
	model := testUIModel()
	m := newUIModel(context.Background(), session, model, "", nil, nil, "")
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

func writeUITestSessionFile(t *testing.T, agentDir, cwd, name, prompt string) string {
	t.Helper()
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr, err := sessionpkg.NewManager(agentDir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(sessionpkg.NewEntry(sessionpkg.EntryTypeMessage, "", sessionpkg.MessageData{
		Role:    agent.RoleUser,
		Content: prompt,
	})); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AppendSessionInfo(name); err != nil {
		t.Fatal(err)
	}
	return mgr.FilePath()
}

func uiTestMessageText(msg agent.AgentMessage) string {
	switch m := msg.(type) {
	case types.UserMessage:
		return uiTestContentText(m.Content)
	case *types.UserMessage:
		return uiTestContentText(m.Content)
	case types.AssistantMessage:
		return uiTestContentText(m.Content)
	case *types.AssistantMessage:
		return uiTestContentText(m.Content)
	default:
		return ""
	}
}

func uiTestContentText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []types.ContentBlock:
		var parts []string
		for _, block := range value {
			if text, ok := block.(*types.TextContent); ok && text != nil {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func waitForUITestIdle(t *testing.T, root *goTUIRoot, session *coding_agent.CodingSession) {
	t.Helper()
	waitForUITestCondition(t, time.Second, func() bool {
		return !root.model.queryActive && len(session.GetMessages()) > 0
	})
}

func waitForUITestCondition(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func indexSessionChoice(t *testing.T, choices []coding_agent.SessionInfo, path string) int {
	t.Helper()
	for i, choice := range choices {
		if sameSessionPath(choice.Path, path) {
			return i
		}
	}
	t.Fatalf("session %q not found in choices %#v", path, choices)
	return 0
}
