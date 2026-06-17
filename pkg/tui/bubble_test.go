package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"github.com/openmodu/modu/pkg/approval"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

func TestBubbleTUIInputUsesCursorEditing(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{})

	for _, r := range []rune("hello") {
		root.updateKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	root.updateKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	root.updateKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	root.updateKey(tea.KeyPressMsg{Code: 'X', Text: "X"})

	if got := root.draft; got != "helXlo" {
		t.Fatalf("expected cursor insert to edit draft, got %q", got)
	}
}

func TestBubbleTUIApprovalAllowsWithY(t *testing.T) {
	responseCh := make(chan string, 1)
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{})
	root.handleApprovalRequest(approval.Request{
		ToolName:   "bash",
		ToolCallID: "call-1",
		Response:   responseCh,
	})

	root.updatePermissionKey(tea.KeyPressMsg{Code: 'y', Text: "y"})

	select {
	case got := <-responseCh:
		if got != "allow" {
			t.Fatalf("expected allow decision, got %q", got)
		}
	default:
		t.Fatal("expected approval response")
	}
	if root.model.pendingPerm != nil {
		t.Fatal("expected pending approval to be cleared")
	}
}

func TestBubbleTUIApprovalUsesAgenvoyStyleToolCard(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{})
	root.model.pendingPerm = &approval.Request{
		ToolName:   "bash",
		ToolCallID: "call-1",
		Args:       map[string]any{"command": "go test ./pkg/tui"},
	}

	rendered := stripANSIForGoTUI(root.renderApproval())
	for _, want := range []string{
		"⏺ Permission required",
		"  tool: bash",
		"  args: go test ./pkg/tui",
		"  actions: [Y]es  [N]o  [A]llow this command  [D]eny this command",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected Agenvoy-style approval card to contain %q, got %q", want, rendered)
		}
	}
}

func TestBubbleTUIApprovalUsesAlwaysLabelsForNonBashTools(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{})
	root.model.pendingPerm = &approval.Request{
		ToolName:   "edit",
		ToolCallID: "call-1",
		Args:       map[string]any{"path": "/tmp/example.go"},
	}

	rendered := stripANSIForGoTUI(root.renderApproval())
	if !strings.Contains(rendered, "[A]lways allow") || !strings.Contains(rendered, "[D]eny always") {
		t.Fatalf("expected non-bash approval card to keep always labels, got %q", rendered)
	}
}

func TestBubbleTUIPlanApprovalUsesAgenvoyStyleActions(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{})
	root.model.pendingPerm = &approval.Request{
		ToolName:   "exit_plan_mode",
		ToolCallID: "plan",
		Args:       map[string]any{"steps": []any{"one", "two"}},
	}

	rendered := stripANSIForGoTUI(root.renderApproval())
	for _, want := range []string{
		"⏺ Plan approval",
		"  plan shown above  steps=2",
		"  actions: [Y]es, start coding  [A] auto-accept edits  [N]o, keep planning",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected Agenvoy-style plan approval to contain %q, got %q", want, rendered)
		}
	}
}

func TestBubbleTUIConfigHookRendersSection(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{
		Config: func(args string) (string, error) {
			if args != "validate" {
				t.Fatalf("expected validate args, got %q", args)
			}
			return "status: ok", nil
		},
	})

	cmd := root.runConfigHook("validate")
	if cmd == nil {
		t.Fatal("expected config command")
	}
	msg, ok := cmd().(bubbleConfigDoneMsg)
	if !ok {
		t.Fatalf("expected bubbleConfigDoneMsg, got %T", msg)
	}
	next, _ := root.Update(msg)
	root = next.(*bubbleTUI)

	if len(root.model.blocks) != 1 {
		t.Fatalf("expected one rendered block, got %d", len(root.model.blocks))
	}
	if got := root.model.blocks[0].Content; !strings.Contains(got, "status: ok") {
		t.Fatalf("expected config output in block, got %q", got)
	}
}

func TestBubbleTUIConfigOpensMenu(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{})

	cmd := root.submit("/config", submitModeNormal)
	if cmd != nil {
		t.Fatal("expected /config to open menu without command")
	}
	if root.model.state != uiStateConfigMenu {
		t.Fatalf("expected config menu state, got %v", root.model.state)
	}
	rendered := root.renderConfigMenu()
	for _, want := range []string{"Config", "Active Model", "Provider"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected menu to contain %q, got %q", want, rendered)
		}
	}
	for _, notWant := range []string{"Custom model", "Scoped models", "Remove model"} {
		if strings.Contains(rendered, notWant) {
			t.Fatalf("expected menu not to contain %q, got %q", notWant, rendered)
		}
	}
}

func TestBubbleTUIConfigProviderInteractive(t *testing.T) {
	var got ConfigProviderInput
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{
		ConfigSetProvider: func(input ConfigProviderInput) (string, error) {
			got = input
			return "saved provider: " + input.Provider, nil
		},
	})

	root.openConfigProvider()
	if root.model.state != uiStateConfigInput {
		t.Fatalf("expected config input state, got %v", root.model.state)
	}
	rendered := root.renderConfigInput()
	for _, want := range []string{"ProviderName", "API Key", "BaseURL (optional)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected provider form to show %q, got %q", want, rendered)
		}
	}
	for _, value := range []string{"openai", "sk-test", "https://api.openai.com/v1"} {
		for _, r := range value {
			root.updateConfigInputKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		cmd := root.updateConfigInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd != nil {
			msg, ok := cmd().(bubbleConfigDoneMsg)
			if !ok {
				t.Fatalf("expected bubbleConfigDoneMsg, got %T", msg)
			}
			if msg.err != nil || !strings.Contains(msg.out, "saved provider: openai") {
				t.Fatalf("unexpected provider result: out=%q err=%v", msg.out, msg.err)
			}
		}
	}
	if got.Provider != "openai" || got.Type != "openai-compatible" || got.BaseURL != "https://api.openai.com/v1" || got.APIKey != "sk-test" {
		t.Fatalf("unexpected provider input: %#v", got)
	}
}

func TestBubbleTUIConfigProviderMenuCanReturn(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{
		ConfigProviders: func() ([]ConfigProviderEntry, error) {
			return []ConfigProviderEntry{{Name: "openai", Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"}}, nil
		},
	})

	cmd := root.openConfigProviderMenu()
	if cmd == nil {
		t.Fatal("expected provider list command")
	}
	msg, ok := cmd().(bubbleConfigProvidersMsg)
	if !ok {
		t.Fatalf("expected bubbleConfigProvidersMsg, got %T", msg)
	}
	next, _ := root.Update(msg)
	root = next.(*bubbleTUI)
	if root.model.state != uiStateConfigSelect || root.configAction != "provider-select" {
		t.Fatalf("expected provider selector, state=%v action=%q", root.model.state, root.configAction)
	}
	if rendered := root.renderConfigSelect(); !strings.Contains(rendered, "Custom OpenAI-compatible") || !strings.Contains(rendered, "openai") {
		t.Fatalf("unexpected provider selector: %q", rendered)
	}

	root.updateConfigSelectKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if root.model.state != uiStateConfigMenu {
		t.Fatalf("expected esc to return to config menu, got %v", root.model.state)
	}
}

func TestBubbleTUIConfigProviderSelectorOpensExistingProvider(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{
		ConfigProviders: func() ([]ConfigProviderEntry, error) {
			return []ConfigProviderEntry{{Name: "openai", Type: "openai-compatible", BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY"}}, nil
		},
		ConfigSetProvider: func(input ConfigProviderInput) (string, error) {
			return "saved provider: " + input.Provider, nil
		},
	})

	cmd := root.openConfigProviderMenu()
	next, _ := root.Update(cmd())
	root = next.(*bubbleTUI)
	root.configSearch = "openai"
	root.filterConfigChoices()
	if len(root.configProviderChoices) != 2 {
		t.Fatalf("expected custom plus openai choices, got %#v", root.configProviderChoices)
	}
	root.configSelectIdx = 1
	cmd = root.confirmConfigSelect()
	if cmd != nil {
		t.Fatal("expected existing provider select to open input synchronously")
	}
	if root.model.state != uiStateConfigInput {
		t.Fatalf("expected provider input state, got %v", root.model.state)
	}
	if root.configFields[root.configFieldIdx].key != "provider" || root.draft != "openai" {
		t.Fatalf("expected provider field prefilled, field=%#v draft=%q", root.configFields[root.configFieldIdx], root.draft)
	}
	if len(root.configFields) != 3 || root.configFields[0].label != "ProviderName" || root.configFields[1].label != "API Key" || root.configFields[2].label != "BaseURL (optional)" {
		t.Fatalf("unexpected provider fields: %#v", root.configFields)
	}
}

func TestBubbleTUIConfigAddInteractive(t *testing.T) {
	var got ConfigModelInput
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{
		ConfigAdd: func(input ConfigModelInput) (string, error) {
			got = input
			return "added model: " + input.Name, nil
		},
	})

	cmd := root.submit("/config add", submitModeNormal)
	if cmd != nil {
		t.Fatal("expected interactive config add to stay in TUI")
	}
	if root.model.state != uiStateConfigInput {
		t.Fatalf("expected config input state, got %v", root.model.state)
	}

	type fieldInput struct {
		value string
		want  string
	}
	fields := []fieldInput{
		{"local-qwen", "provider"},
		{"lmstudio", "model"},
		{"qwen", "baseUrl"},
		{"http://127.0.0.1:1234/v1", "apiKey"},
		{"local-key", "description"},
		{"local coding model", ""},
	}
	var last tea.Cmd
	for _, field := range fields {
		for _, r := range field.value {
			root.updateConfigInputKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		last = root.updateConfigInputKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		if field.want != "" {
			if root.configFields[root.configFieldIdx].key != field.want {
				t.Fatalf("expected next field %q, got %q", field.want, root.configFields[root.configFieldIdx].key)
			}
		}
	}
	if last == nil {
		t.Fatal("expected final config add command")
	}
	msg, ok := last().(bubbleConfigDoneMsg)
	if !ok {
		t.Fatalf("expected bubbleConfigDoneMsg, got %T", msg)
	}
	if msg.err != nil || !strings.Contains(msg.out, "added model: local-qwen") {
		t.Fatalf("unexpected config add result: out=%q err=%v", msg.out, msg.err)
	}
	if got.Name != "local-qwen" || got.Provider != "lmstudio" || got.Model != "qwen" || got.APIKey != "local-key" || got.Description != "local coding model" {
		t.Fatalf("unexpected config input: %#v", got)
	}
	if root.model.state != uiStateConfigMenu {
		t.Fatalf("expected config menu after add, got %v", root.model.state)
	}
}

func TestBubbleTUIConfigUseInteractive(t *testing.T) {
	used := ""
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{
		ConfigModels: func() ([]ConfigModelEntry, error) {
			return []ConfigModelEntry{
				{Name: "alpha", Provider: "openai", Model: "gpt-4o", Active: true},
				{Name: "beta", Provider: "deepseek", Model: "deepseek-chat", Description: "fallback"},
			}, nil
		},
		ConfigUse: func(target string) (string, error) {
			used = target
			return "active: " + target, nil
		},
	})

	cmd := root.submit("/config use", submitModeNormal)
	if cmd == nil {
		t.Fatal("expected config model list command")
	}
	msg, ok := cmd().(bubbleConfigModelsMsg)
	if !ok {
		t.Fatalf("expected bubbleConfigModelsMsg, got %T", msg)
	}
	next, _ := root.Update(msg)
	root = next.(*bubbleTUI)
	if root.model.state != uiStateConfigSelect {
		t.Fatalf("expected config select state, got %v", root.model.state)
	}
	if rendered := root.renderConfigSelect(); !strings.Contains(rendered, "Config use") || !strings.Contains(rendered, "alpha") {
		t.Fatalf("unexpected rendered selector: %q", rendered)
	}

	root.moveConfigSelect(1)
	cmd = root.confirmConfigSelect()
	if cmd == nil {
		t.Fatal("expected config use command")
	}
	done, ok := cmd().(bubbleConfigDoneMsg)
	if !ok {
		t.Fatalf("expected bubbleConfigDoneMsg, got %T", done)
	}
	if done.err != nil || used != "beta" {
		t.Fatalf("unexpected config use result: used=%q out=%q err=%v", used, done.out, done.err)
	}
}

func TestBubbleTUISlashSelectorCompletesCommand(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{})

	for _, r := range []rune("/hot") {
		root.updateKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(root.slashMatches) == 0 {
		t.Fatal("expected slash matches")
	}
	root.updateKey(tea.KeyPressMsg{Code: tea.KeyTab})

	if got := root.draft; got != "/hotkeys " {
		t.Fatalf("expected completed slash command, got %q", got)
	}
}

func TestBubbleTUIViewUsesAgenvoyStyleChrome(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, &types.Model{ID: "test", Name: "Test", ProviderID: "test"}, "", nil, CommandHooks{})
	root.width = 80
	root.height = 24

	view := root.viewString()
	for _, want := range []string{"modu_code", "Bubble Tea", "/model", "Test", "❯ █"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected Bubble view chrome to contain %q, got %q", want, view)
		}
	}
	if got := lipgloss.Height(root.renderHeader()); got != 1 {
		t.Fatalf("expected persistent header to stay one line, got %d", got)
	}
}

func TestBubbleInlineViewKeepsTranscriptOutOfRenderer(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, &types.Model{ID: "test", Name: "Test", ProviderID: "test"}, "", nil, CommandHooks{})
	root.inline = true
	root.width = 80
	root.height = 24
	root.appendBlock(uiBlock{Kind: "user", Content: "selectable scrollback text", Source: "local"})

	view := root.viewString()
	if strings.Contains(view, "selectable scrollback text") {
		t.Fatalf("inline Bubble view should not re-render completed transcript, got %q", view)
	}
	if strings.Contains(view, "modu_code ·") {
		t.Fatalf("inline Bubble view should not keep a persistent full header, got %q", view)
	}
	if !strings.Contains(view, "❯ █") {
		t.Fatalf("expected inline Bubble view to keep the input widget, got %q", view)
	}
}

func TestBubbleStatusLineShowsContextWindowUsage(t *testing.T) {
	session := newUITestSessionWithStream(t, func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			msg := testAssistantMessageForLastUser(model, llmCtx)
			msg.Usage = types.AgentUsage{Input: 1200, Output: 300, TotalTokens: 1500}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	})
	session.GetModel().ContextWindow = 12000
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})

	line := stripANSIForGoTUI(root.renderStatusLine())
	if !strings.Contains(line, "ctx 1.5K/12K 12%") {
		t.Fatalf("expected status line to show context usage, got %q", line)
	}
}

func TestBubbleStatusLineShowsContextWindowDuringTransientStatus(t *testing.T) {
	session := newUITestSession(t)
	session.GetModel().ContextWindow = 8000
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})
	root.model.setTransientStatus("model changed; context cleared")

	line := stripANSIForGoTUI(root.renderStatusLine())
	for _, want := range []string{"model changed; context cleared", "ctx 0/8K 0%"} {
		if !strings.Contains(line, want) {
			t.Fatalf("expected status line to contain %q, got %q", want, line)
		}
	}
}

func TestBubbleStatusLineResetsContextUsageAfterClear(t *testing.T) {
	session := newUITestSessionWithStream(t, func(ctx context.Context, model *types.Model, llmCtx *types.LLMContext, opts *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			msg := testAssistantMessageForLastUser(model, llmCtx)
			msg.Usage = types.AgentUsage{Input: 1200, Output: 300, TotalTokens: 1500}
			stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
			stream.Resolve(msg, nil)
			stream.Close()
		}()
		return stream, nil
	})
	session.GetModel().ContextWindow = 12000
	if err := session.Prompt(context.Background(), "hello"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := session.ClearConversation(); err != nil {
		t.Fatalf("ClearConversation: %v", err)
	}
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})

	line := stripANSIForGoTUI(root.renderStatusLine())
	if !strings.Contains(line, "ctx 0/12K 0%") {
		t.Fatalf("expected status line to reset context usage, got %q", line)
	}
}

func TestFormatCompactTokens(t *testing.T) {
	tests := map[int]string{
		0:       "0",
		999:     "999",
		1500:    "1.5K",
		12000:   "12K",
		1250000: "1.2M",
	}
	for in, want := range tests {
		if got := formatCompactTokens(in); got != want {
			t.Fatalf("formatCompactTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestBubbleInlineHeaderIsPrintableButNotPersistent(t *testing.T) {
	session := newUITestSession(t)
	session.GetModel().ContextWindow = 1000000
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})
	root.inline = true
	root.width = 80
	root.model.tgUsername = "ityike_quick_snap_bot"

	header := stripANSIForGoTUI(root.renderInlineHeader())
	for _, want := range []string{"modu_code", "session " + shortSessionID(session.GetSessionID()), "Test Model", "test/test-model", "context 0/1M 0%"} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected printable inline header to contain %q, got %q", want, header)
		}
	}
	for _, want := range []string{"session", "model", "context", "mode"} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected printable inline header to expose %q as a separate row, got %q", want, header)
		}
	}
	if !strings.Contains(header, "channel @ityike_quick_snap_bot") {
		t.Fatalf("expected Telegram username to be rendered as channel, got %q", header)
	}
	if strings.Contains(header, "mode   @ityike_quick_snap_bot") {
		t.Fatalf("expected Telegram username not to be rendered as mode, got %q", header)
	}
	if got := strings.Count(header, "\n"); got < 4 {
		t.Fatalf("expected printable inline header to be multi-line, got %d newlines in %q", got, header)
	}
	for _, want := range []string{"╭", "╰"} {
		if !strings.Contains(header, want) {
			t.Fatalf("expected printable inline header to use a bordered box containing %q, got %q", want, header)
		}
	}
	view := stripANSIForGoTUI(root.viewString())
	for _, unwanted := range []string{"modu_code", "test/test"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("inline header should not be persistent in view; found %q in %q", unwanted, view)
		}
	}
}

func TestBubbleHeaderLineShowsSessionID(t *testing.T) {
	session := newUITestSession(t)
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})
	root.width = 120

	header := stripANSIForGoTUI(root.renderHeaderLine(root.width))
	if want := "session " + shortSessionID(session.GetSessionID()); !strings.Contains(header, want) {
		t.Fatalf("expected header line to contain %q, got %q", want, header)
	}
}

func TestBubbleExitSessionMetaShowsSessionID(t *testing.T) {
	session := newUITestSession(t)
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})

	meta := stripANSIForGoTUI(root.model.renderExitSessionMeta())
	if want := "session: " + shortSessionID(session.GetSessionID()); !strings.Contains(meta, want) {
		t.Fatalf("expected exit session meta to contain %q, got %q", want, meta)
	}
}

func TestBubbleTUIModelSelectEnterSwitchesModel(t *testing.T) {
	providers.Models["bubble-model-select"] = map[string]*types.Model{
		"bubble-alpha": {ID: "bubble-alpha", Name: "Bubble Alpha", ProviderID: "bubble-model-select"},
		"bubble-beta":  {ID: "bubble-beta", Name: "Bubble Beta", ProviderID: "bubble-model-select"},
	}
	session := newUITestSession(t)
	session.SetModel(providers.Models["bubble-model-select"]["bubble-alpha"])
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})

	root.openModelSelect("bubble-beta")
	if root.model.state != uiStateModelSelect {
		t.Fatalf("expected model select state, got %v", root.model.state)
	}
	if len(root.modelChoices) != 1 || root.modelChoices[0].ID != "bubble-beta" {
		t.Fatalf("expected bubble-beta search result, got %#v", root.modelChoices)
	}
	if rendered := root.renderModelSelect(); !strings.Contains(rendered, "Select model") || !strings.Contains(rendered, "search: bubble-beta") {
		t.Fatalf("expected rendered selector header, got %q", rendered)
	}

	cmd := root.confirmModelSelect()
	if root.model.state != uiStateInput {
		t.Fatalf("expected input state immediately after confirm, got %v", root.model.state)
	}
	runBubbleTestCmd(t, root, cmd)

	if got := session.GetModel(); got.ID != "bubble-beta" {
		t.Fatalf("expected bubble-beta, got %#v", got)
	}
	if !strings.Contains(root.model.statusMsg, "context cleared") {
		t.Fatalf("expected model switch status to mention cleared context, got %q", root.model.statusMsg)
	}
}

func TestBubbleTUIModelSelectUpdateClosesBeforeSwitch(t *testing.T) {
	providers.Models["bubble-model-update"] = map[string]*types.Model{
		"bubble-update-alpha": {ID: "bubble-update-alpha", Name: "Bubble Update Alpha", ProviderID: "bubble-model-update"},
		"bubble-update-beta":  {ID: "bubble-update-beta", Name: "Bubble Update Beta", ProviderID: "bubble-model-update"},
	}
	session := newUITestSession(t)
	session.SetModel(providers.Models["bubble-model-update"]["bubble-update-alpha"])
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})

	root.openModelSelect("bubble-update-beta")
	next, cmd := root.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected asynchronous model switch command")
	}
	root = next.(*bubbleTUI)

	if root.model.state != uiStateInput {
		t.Fatalf("expected selector to close after enter, got state %v", root.model.state)
	}
	runBubbleTestCmd(t, root, cmd)
	if got := session.GetModel().ID; got != "bubble-update-beta" {
		t.Fatalf("expected bubble-update-beta, got %q", got)
	}
	if strings.Contains(root.viewString(), "Select model") {
		t.Fatalf("expected view to leave selector, got %q", root.viewString())
	}
}

func TestBubbleTUIModelSelectKeysMoveAndConfirm(t *testing.T) {
	providers.Models["bubble-model-keys"] = map[string]*types.Model{
		"bubble-key-alpha": {ID: "bubble-key-alpha", Name: "Bubble Key Alpha", ProviderID: "bubble-model-keys"},
		"bubble-key-beta":  {ID: "bubble-key-beta", Name: "Bubble Key Beta", ProviderID: "bubble-model-keys"},
	}
	session := newUITestSession(t)
	session.SetModel(providers.Models["bubble-model-keys"]["bubble-key-alpha"])
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})

	root.openModelSelect("bubble-key")
	if root.modelSelectIdx != 0 {
		t.Fatalf("expected initial selection at current model, got %d", root.modelSelectIdx)
	}
	root.updateModelSelectKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if got := root.modelChoices[root.modelSelectIdx].ID; got != "bubble-key-beta" {
		t.Fatalf("expected j to select beta, got %q", got)
	}
	_, cmd := root.updateModelSelectKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	runBubbleTestCmd(t, root, cmd)
	if got := session.GetModel().ID; got != "bubble-key-beta" {
		t.Fatalf("expected enter to confirm beta, got %q", got)
	}
	if root.model.state != uiStateInput {
		t.Fatalf("expected input state after confirm, got %v", root.model.state)
	}
}

func TestBubbleTUIModelSelectFallbackKeysConfirmAndClose(t *testing.T) {
	providers.Models["bubble-model-fallback"] = map[string]*types.Model{
		"bubble-fallback-alpha": {ID: "bubble-fallback-alpha", Name: "Bubble Fallback Alpha", ProviderID: "bubble-model-fallback"},
		"bubble-fallback-beta":  {ID: "bubble-fallback-beta", Name: "Bubble Fallback Beta", ProviderID: "bubble-model-fallback"},
	}
	session := newUITestSession(t)
	session.SetModel(providers.Models["bubble-model-fallback"]["bubble-fallback-alpha"])
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})

	root.openModelSelect("bubble-fallback")
	root.updateModelSelectKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, cmd := root.updateModelSelectKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	runBubbleTestCmd(t, root, cmd)
	if got := session.GetModel().ID; got != "bubble-fallback-beta" {
		t.Fatalf("expected y to confirm beta, got %q", got)
	}

	root.openModelSelect("bubble-fallback")
	root.updateModelSelectKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if root.model.state != uiStateInput {
		t.Fatalf("expected q to close selector, got state %v", root.model.state)
	}

	root.openModelSelect("bubble-fallback")
	_, cmd = root.updateModelSelectKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if root.model.state != uiStateInput {
		t.Fatalf("expected rune carriage return to confirm and close selector, got state %v", root.model.state)
	}
	runBubbleTestCmd(t, root, cmd)
}

func TestBubbleTUIScopedModelsSelectorTogglesScope(t *testing.T) {
	providers.Models["bubble-scoped-models"] = map[string]*types.Model{
		"bubble-scope-alpha": {ID: "bubble-scope-alpha", Name: "Bubble Scope Alpha", ProviderID: "bubble-scoped-models"},
		"bubble-scope-beta":  {ID: "bubble-scope-beta", Name: "Bubble Scope Beta", ProviderID: "bubble-scoped-models"},
	}
	session := newUITestSession(t)
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{})

	root.openScopedModelsSelect()
	root.modelSearch = "bubble-scope-beta"
	root.filterModelChoices()
	if len(root.modelChoices) != 1 || root.modelChoices[0].ID != "bubble-scope-beta" {
		t.Fatalf("expected bubble-scope-beta search result, got %#v", root.modelChoices)
	}
	root.toggleScopedModelSelection()

	for _, id := range session.GetScopedModelIDs() {
		if id == "bubble-scope-beta" {
			t.Fatalf("expected bubble-scope-beta removed from scope, got %v", session.GetScopedModelIDs())
		}
	}
	if len(session.GetScopedModelIDs()) == 0 {
		t.Fatal("expected remaining scoped model ids")
	}
}

func TestBubbleTUIScopedModelsSlashCommands(t *testing.T) {
	providers.Models["bubble-scoped-slash"] = map[string]*types.Model{
		"scope-alpha": {ID: "scope-alpha", Name: "Scope Alpha", ProviderID: "bubble-scoped-slash"},
		"scope-beta":  {ID: "scope-beta", Name: "Scope Beta", ProviderID: "bubble-scoped-slash"},
	}
	session := newUITestSession(t)
	session.SetModel(providers.Models["bubble-scoped-slash"]["scope-alpha"])
	var saved []string
	root := newBubbleTUI(context.Background(), session, session.GetModel(), "", nil, CommandHooks{
		SaveScopedModels: func(ids []string) error {
			saved = append([]string(nil), ids...)
			return nil
		},
	})

	cmd := root.submit("/scoped-models set scope-beta", submitModeNormal)
	if cmd != nil {
		runBubbleTestCmd(t, root, cmd)
	}
	if got := session.GetScopedModelIDs(); len(got) != 1 || got[0] != "scope-beta" {
		t.Fatalf("expected scope-beta scope, got %v", got)
	}
	if len(saved) != 1 || saved[0] != "scope-beta" {
		t.Fatalf("expected saved scope-beta, got %v", saved)
	}
	if got := root.model.blocks[len(root.model.blocks)-1].Content; !strings.Contains(got, "scope: 1 model") || !strings.Contains(got, "Scope Beta") {
		t.Fatalf("unexpected scoped-models output:\n%s", got)
	}

	cmd = root.submit("/scoped-models clear", submitModeNormal)
	if cmd != nil {
		runBubbleTestCmd(t, root, cmd)
	}
	if got := session.GetScopedModelIDs(); len(got) != 0 {
		t.Fatalf("expected cleared scope, got %v", got)
	}
}

func runBubbleTestCmd(t *testing.T, root *bubbleTUI, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	next, _ := root.Update(cmd())
	if updated, ok := next.(*bubbleTUI); ok && updated != root {
		*root = *updated
	}
}

// collectPrintlnBodies executes a tea.Cmd and recursively collects the bodies
// of any tea.Println messages it emits, walking batch/sequence command slices.
func collectPrintlnBodies(cmd tea.Cmd) []string {
	if cmd == nil {
		return nil
	}
	return collectPrintlnFromMsg(cmd())
}

func collectPrintlnFromMsg(msg tea.Msg) []string {
	if msg == nil {
		return nil
	}
	// tea.Sequence / tea.Batch produce an unexported []tea.Cmd message.
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice {
		var out []string
		for i := 0; i < v.Len(); i++ {
			if c, ok := v.Index(i).Interface().(tea.Cmd); ok {
				out = append(out, collectPrintlnBodies(c)...)
			}
		}
		return out
	}
	if strings.Contains(fmt.Sprintf("%T", msg), "printLineMessage") {
		// printLineMessage formats as "{body}".
		return []string{strings.Trim(fmt.Sprintf("%v", msg), "{}")}
	}
	return nil
}

func TestBubbleInlineCommitsCompletionSummaryToScrollback(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, &types.Model{ID: "test", Name: "Test", ProviderID: "test"}, "", nil, CommandHooks{})
	root.inline = true
	root.width = 80
	root.model.state = uiStateQuerying
	root.model.queryActive = true
	root.model.queryStartTime = time.Now().Add(-3 * time.Second)

	cmd := root.finishPromptOperation(nil, "")

	bodies := collectPrintlnBodies(cmd)
	found := false
	for _, b := range bodies {
		if strings.Contains(stripANSIForGoTUI(b), "Completed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected completion summary printed to scrollback, got %#v", bodies)
	}
	// The transient live region must not also retain the summary (it would
	// otherwise vanish after the TTL, the bug being fixed).
	if got := root.model.effectiveLastActivity(time.Now()); strings.TrimSpace(stripANSIForGoTUI(got)) != "" {
		t.Fatalf("expected transient activity cleared in inline mode, got %q", got)
	}
}

// TestBubbleInlineResizeReflowsActiveRegionAndKeepsScrollback exercises the two
// halves of the bubbletea v2 (cellbuf renderer) migration: completed turns are
// committed to terminal scrollback via tea.Println (so the cellbuf renderer
// never has to repaint them), and the small active region that the renderer
// *does* own reflows to the new terminal width on a WindowSizeMsg. Under v2 the
// renderer Erase()s and re-diffs the cell buffer on a size change, so the active
// region must track the new width rather than keeping the stale wide layout that
// the v1 relative-cursor renderer left behind.
func TestBubbleInlineResizeReflowsActiveRegionAndKeepsScrollback(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, &types.Model{ID: "test", Name: "Test", ProviderID: "test"}, "", nil, CommandHooks{})
	root.inline = true

	root.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if root.width != 100 {
		t.Fatalf("expected width 100 after resize, got %d", root.width)
	}

	// A completed block must land in scrollback via tea.Println, not in the
	// active region the renderer repaints — that is what survives resize cleanly.
	block := uiBlock{Kind: "assistant", Content: "hello world", Timestamp: time.Now()}
	if bodies := collectPrintlnBodies(root.printBlockCmd(block)); len(bodies) == 0 {
		t.Fatal("expected completed block to be committed to scrollback via tea.Println")
	}

	// Shrink the terminal. The active region must re-render at the new width
	// without panicking and without leaving lines wider than the terminal.
	root.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	if root.width != 40 {
		t.Fatalf("expected width 40 after resize, got %d", root.width)
	}
	// View().Content is what the cellbuf renderer actually paints; it must fit
	// the resized width or v2 would clip (not wrap) the over-wide lines.
	view := root.View().Content
	if strings.TrimSpace(view) == "" {
		t.Fatal("expected non-empty active-region view after resize")
	}
	for _, line := range strings.Split(stripANSIForGoTUI(view), "\n") {
		if w := lipgloss.Width(line); w > 40 {
			t.Fatalf("active-region line exceeds resized width 40: %q (w=%d)", line, w)
		}
	}
}

func TestBubbleInlineTurnSeparatorStaysBelowMinimumTerminalWidth(t *testing.T) {
	if turnSeparatorWidth >= 20 {
		t.Fatalf("turn separator width must stay below minimum terminal width 20, got %d", turnSeparatorWidth)
	}
	root := newBubbleTUI(context.Background(), nil, nil, "", nil, CommandHooks{})
	root.inline = true
	root.width = 120

	bodies := collectPrintlnBodies(root.printTurnSeparatorCmd())
	if len(bodies) != 1 {
		t.Fatalf("expected one separator line, got %#v", bodies)
	}
	line := strings.TrimSpace(stripANSIForGoTUI(bodies[0]))
	if got := lipgloss.Width(line); got != turnSeparatorWidth {
		t.Fatalf("expected fixed separator width %d, got %d (%q)", turnSeparatorWidth, got, line)
	}
}

func TestBubbleNonInlineKeepsTransientCompletionSummary(t *testing.T) {
	root := newBubbleTUI(context.Background(), nil, &types.Model{ID: "test", Name: "Test", ProviderID: "test"}, "", nil, CommandHooks{})
	root.inline = false
	root.width = 80
	root.model.state = uiStateQuerying
	root.model.queryActive = true
	root.model.queryStartTime = time.Now().Add(-2 * time.Second)

	_ = root.finishPromptOperation(nil, "")

	if got := stripANSIForGoTUI(root.model.effectiveLastActivity(time.Now())); !strings.Contains(got, "Completed") {
		t.Fatalf("expected non-inline mode to keep transient completion summary, got %q", got)
	}
}
