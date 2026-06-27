package main

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/types"
)

func TestMessagesFromAssistantMessageIncludesTextAndToolCall(t *testing.T) {
	messages := messagesFromAgentMessage(types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "hello"},
			&types.ToolCallContent{Type: "toolCall", ID: "call-1", Name: "read", Arguments: map[string]any{"path": "main.go"}},
		},
	})

	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2: %#v", len(messages), messages)
	}
	if got := messages[0].Text; got != "hello" {
		t.Fatalf("text message = %q, want hello", got)
	}
	if !messages[1].Tool || messages[1].ToolName != "read" || !strings.Contains(messages[1].Detail, "main.go") {
		t.Fatalf("tool message not converted: %#v", messages[1])
	}
}

func TestMessagesFromAgentEventSkipsUserMessageEnd(t *testing.T) {
	user := types.Event{
		Type: types.EventTypeMessageEnd,
		Message: types.UserMessage{
			Role:    types.RoleUser,
			Content: "hello",
		},
	}
	if got := messagesFromAgentEvent(user); len(got) != 0 {
		t.Fatalf("user message_end should not render because submit already appended it: %#v", got)
	}

	assistant := types.Event{
		Type: types.EventTypeMessageEnd,
		Message: types.AssistantMessage{
			Role:    types.RoleAssistant,
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "reply"}},
		},
	}
	got := messagesFromAgentEvent(assistant)
	if len(got) != 1 || got[0].Text != "reply" {
		t.Fatalf("assistant message_end should render, got %#v", got)
	}
}

func TestMessageFromSessionEventIncludesPermissionDenied(t *testing.T) {
	msg, ok := messageFromSessionEvent(coding_agent.SessionEvent{
		Type:     coding_agent.SessionEventPermissionDeny,
		ToolName: "bash",
		Reason:   "dangerous command",
	})
	if !ok {
		t.Fatal("expected permission denied event to render")
	}
	if !strings.Contains(msg.Text, "bash") || !strings.Contains(msg.Text, "dangerous command") {
		t.Fatalf("unexpected message text: %q", msg.Text)
	}
}

func TestMessagesFromAgentEventFormatsBashToolAsSingleClaudeStyleBlock(t *testing.T) {
	start := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Args:       map[string]any{"command": "go test ./pkg/modu-tui"},
	})
	if len(start) != 1 {
		t.Fatalf("start messages len = %d, want 1", len(start))
	}
	if got := start[0]; got.Summary != "Running shell command" || got.ToolInput != "go test ./pkg/modu-tui" {
		t.Fatalf("unexpected start message: %#v", got)
	}

	end := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "bash",
		Result:     types.ToolResult{Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}}},
	})
	if len(end) != 1 {
		t.Fatalf("end messages len = %d, want 1", len(end))
	}
	if got := end[0]; got.Summary != "Ran 1 shell command" || got.ToolOutput != "ok" || !got.ToolDone {
		t.Fatalf("unexpected end message: %#v", got)
	}
}

func TestMessagesFromAgentEventFormatsReadToolLikeClaudeCode(t *testing.T) {
	path := "/Users/ityike/Code/go/src/github.com/openmodu/modu/cmd/tuipoc2/main.go"
	start := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "read",
		Args: map[string]any{
			"path":   path,
			"offset": float64(205),
			"limit":  float64(14),
		},
	})
	if len(start) != 1 {
		t.Fatalf("start messages len = %d, want 1", len(start))
	}
	if got, want := start[0].ToolInput, path+" · lines 205-218"; got != want {
		t.Fatalf("read input = %q, want %q", got, want)
	}

	end := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "read",
		Result: types.ToolResult{Content: []types.ContentBlock{&types.TextContent{
			Type: "text",
			Text: "205\tfunc a() {}\n206\tfunc b() {}\n",
		}}},
	})
	if len(end) != 1 {
		t.Fatalf("end messages len = %d, want 1", len(end))
	}
	if got := end[0]; got.Summary != "Read 2 lines" || got.ToolOutput != "Read 2 lines" || !got.ToolDone {
		t.Fatalf("unexpected read end message: %#v", got)
	}
}

func TestToolApprovalDecisionToTypes(t *testing.T) {
	tests := map[modutui.ToolApprovalDecision]types.ToolApprovalDecision{
		modutui.ToolApprovalAllow:       types.ToolApprovalAllow,
		modutui.ToolApprovalAllowAlways: types.ToolApprovalAllowAlways,
		modutui.ToolApprovalDeny:        types.ToolApprovalDeny,
		modutui.ToolApprovalDenyAlways:  types.ToolApprovalDenyAlways,
	}
	for input, want := range tests {
		if got := toolApprovalDecisionToTypes(input); got != want {
			t.Fatalf("decision %q mapped to %q, want %q", input, got, want)
		}
	}
}

func TestInitialTerminalSizeFallsBackWhenSizeIsUnavailable(t *testing.T) {
	width, height := initialTerminalSize(-1, 120, 35)
	if width != 120 || height != 35 {
		t.Fatalf("initialTerminalSize = %dx%d, want 120x35", width, height)
	}
}

func TestModuTUIPrompterApproveToolUsesModuTUIRequest(t *testing.T) {
	requests := make(chan modutui.RequestToolApprovalMsg, 1)
	prompter := &moduTUIPrompter{
		ctx: context.Background(),
		send: func(msg tea.Msg) {
			req, ok := msg.(modutui.RequestToolApprovalMsg)
			if !ok {
				t.Fatalf("unexpected message type %T", msg)
			}
			requests <- req
			req.Respond <- modutui.ToolApprovalAllowAlways
		},
	}

	decision, err := prompter.ApproveTool("bash", "call-1", map[string]any{"command": "go test ./..."})
	if err != nil {
		t.Fatalf("ApproveTool returned error: %v", err)
	}
	if decision != types.ToolApprovalAllowAlways {
		t.Fatalf("decision = %q, want %q", decision, types.ToolApprovalAllowAlways)
	}
	req := <-requests
	if req.Request.ID != "call-1" || req.Request.ToolName != "bash" || req.Request.Detail != "go test ./..." {
		t.Fatalf("unexpected approval request: %#v", req.Request)
	}
}

func TestModuTUISlashCommandsIncludeBaseAndSessionCommands(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test", Name: "Test", ProviderID: "test"},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	commands := moduTUISlashCommands(session)
	seen := map[string]bool{}
	for _, cmd := range commands {
		if seen[cmd.Name] {
			t.Fatalf("duplicate slash command %q in %#v", cmd.Name, commands)
		}
		seen[cmd.Name] = true
	}
	for _, want := range []string{"/help", "/clear", "/tokens", "/compact"} {
		if !seen[want] {
			t.Fatalf("missing slash command %q in %#v", want, commands)
		}
	}
}

func TestModuTUIInfoCardLinesIncludeStartupContext(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test-model", Name: "Test Model", ProviderID: "test-provider"},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Join(moduTUIInfoCardLines(session, session.GetModel()), "\n")
	for _, want := range []string{
		"modu_code",
		"model: Test Model (test-provider / test-model)",
		"cwd: " + session.RuntimeState().Cwd,
		"session: " + shortModuTUISessionID(session.GetSessionID()),
		"commands: type /",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("info card lines missing %q:\n%s", want, lines)
		}
	}
}

func TestModuTUISlashPrinterCapturesSectionsAndClear(t *testing.T) {
	var printer moduTUISlashPrinter
	printer.PrintInfo("alpha")
	printer.PrintSection("Beta", []string{"one", "two"})
	printer.PrintError(context.Canceled)
	printer.ClearScreen()

	if !printer.clear {
		t.Fatal("expected clear flag")
	}
	text := printer.Text()
	for _, want := range []string{"alpha", "Beta", "one", "two", "error: context canceled"} {
		if !strings.Contains(text, want) {
			t.Fatalf("printer text missing %q:\n%s", want, text)
		}
	}
}

func TestRunModuTUISlashSendsPreformattedHelpOutput(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test", Name: "Test", ProviderID: "test"},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var messages []tea.Msg
	runModuTUISlash(context.Background(), "/help", session, session.GetModel(), func(msg tea.Msg) {
		messages = append(messages, msg)
	})

	var got *modutui.Message
	for _, msg := range messages {
		if appendMsg, ok := msg.(modutui.AppendMessageMsg); ok {
			next := appendMsg.Message
			got = &next
			break
		}
	}
	if got == nil {
		t.Fatalf("expected AppendMessageMsg in %#v", messages)
	}
	if !got.Preformatted {
		t.Fatalf("slash help output should be preformatted: %#v", got)
	}
	for _, want := range []string{"Help", "/help, /h", "/quit, /exit", "tool approval"} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("help output missing %q:\n%s", want, got.Text)
		}
	}
}
