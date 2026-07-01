package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	modutui "github.com/openmodu/modu/pkg/modu-tui"
	"github.com/openmodu/modu/pkg/providers"
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

func TestMessagesFromAssistantMessageGroupsThinkingAtTop(t *testing.T) {
	messages := messagesFromAgentMessage(types.AssistantMessage{
		Role: types.RoleAssistant,
		Content: []types.ContentBlock{
			&types.TextContent{Type: "text", Text: "final answer"},
			&types.ThinkingContent{Type: "thinking", Thinking: "first thought"},
			&types.ToolCallContent{Type: "toolCall", ID: "call-1", Name: "read", Arguments: map[string]any{"path": "main.go"}},
			&types.ThinkingContent{Type: "thinking", Thinking: "second thought"},
		},
	})

	if len(messages) != 3 {
		t.Fatalf("messages len = %d, want 3: %#v", len(messages), messages)
	}
	if !messages[0].Thinking {
		t.Fatalf("first message should be grouped thinking block: %#v", messages)
	}
	if !strings.Contains(messages[0].Text, "first thought") || !strings.Contains(messages[0].Text, "second thought") {
		t.Fatalf("thinking block should contain all thinking text: %#v", messages[0])
	}
	if messages[1].Text != "final answer" {
		t.Fatalf("assistant text should follow thinking, got %#v", messages[1])
	}
	if !messages[2].Tool || messages[2].ToolName != "read" {
		t.Fatalf("tool call should stay after thinking and text, got %#v", messages[2])
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

type fakeDebounceTimer struct{}

func (fakeDebounceTimer) Stop() bool { return true }

func TestModuTUIAgentDurationTrackerEmitsSingleTotalAcrossRounds(t *testing.T) {
	now := time.Unix(100, 0)
	var emitted []modutui.Message
	tracker := newModuTUIAgentDurationTracker(
		func() time.Time { return now },
		func(msg modutui.Message) { emitted = append(emitted, msg) },
	)
	var pending func()
	tracker.schedule = func(_ time.Duration, f func()) moduTUIDebounceTimer {
		pending = f
		return fakeDebounceTimer{}
	}

	// AgentEnd without an active task does nothing.
	tracker.Handle(types.Event{Type: types.EventTypeAgentEnd})
	if pending != nil || len(emitted) != 0 {
		t.Fatal("AgentEnd without AgentStart should not arm a timer or emit")
	}

	// Round 1: the long "real work" round.
	tracker.Handle(types.Event{Type: types.EventTypeAgentStart})
	now = now.Add(60 * time.Second)
	tracker.Handle(types.Event{Type: types.EventTypeAgentEnd})
	if pending == nil {
		t.Fatal("AgentEnd should arm the debounce finalize")
	}
	if len(emitted) != 0 {
		t.Fatal("nothing should be emitted before the debounce fires")
	}
	stale := pending

	// Round 2: the hidden goal continuation re-prompts before debounce fires,
	// cancelling round 1's finalize and extending the same task.
	now = now.Add(2 * time.Second)
	tracker.Handle(types.Event{Type: types.EventTypeAgentStart})
	now = now.Add(5*time.Second + 400*time.Millisecond)
	tracker.Handle(types.Event{Type: types.EventTypeAgentEnd})

	// The stale round-1 finalize must be a no-op (gen mismatch).
	stale()
	if len(emitted) != 0 {
		t.Fatalf("cancelled finalize should not emit: %#v", emitted)
	}

	// The live finalize reports one total spanning both rounds: 100 -> 167.4s.
	pending()
	if len(emitted) != 1 {
		t.Fatalf("want one completion message, got %d: %#v", len(emitted), emitted)
	}
	msg := emitted[0]
	if msg.Role != modutui.RoleAssistant || !msg.Preformatted || !msg.Plain {
		t.Fatalf("completion message should be assistant preformatted plain text: %#v", msg)
	}
	if got, want := msg.Text, "✓ Completed (1min 07s)"; got != want {
		t.Fatalf("completion text = %q, want %q", got, want)
	}

	// After finalizing, a lone AgentEnd does not re-emit.
	tracker.Handle(types.Event{Type: types.EventTypeAgentEnd})
	if len(emitted) != 1 {
		t.Fatalf("tracker should be idle after completion, got %d messages", len(emitted))
	}
}

func TestFormatModuTUIActivityDuration(t *testing.T) {
	tests := map[time.Duration]string{
		-1 * time.Second:                      "0s",
		400 * time.Millisecond:                "0s",
		59*time.Second + 600*time.Millisecond: "1min",
		60 * time.Second:                      "1min",
		65 * time.Second:                      "1min 05s",
	}
	for input, want := range tests {
		if got := formatModuTUIActivityDuration(input); got != want {
			t.Fatalf("formatModuTUIActivityDuration(%s) = %q, want %q", input, got, want)
		}
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

func TestMessageFromSessionEventIncludesContextCompactDivider(t *testing.T) {
	msg, ok := messageFromSessionEvent(coding_agent.SessionEvent{
		Type: coding_agent.SessionEventCompactionDone,
	})
	if !ok {
		t.Fatal("expected compaction done event to render")
	}
	if msg.Text != moduTUIContextCompactDivider || !msg.Preformatted || !msg.Plain {
		t.Fatalf("unexpected compact message: %#v", msg)
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
	if got, want := start[0].Summary, "Read 1 file"; got != want {
		t.Fatalf("read start summary = %q, want %q", got, want)
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

func TestToolRunningSummaryCountsReadFiles(t *testing.T) {
	tests := []struct {
		name string
		args any
		want string
	}{
		{name: "path", args: map[string]any{"path": "a.go"}, want: "Read 1 file"},
		{name: "file_path", args: map[string]any{"file_path": "a.go"}, want: "Read 1 file"},
		{name: "paths", args: map[string]any{"paths": []any{"a.go", "b.go"}}, want: "Read 2 files"},
		{name: "file_paths", args: map[string][]string{"file_paths": []string{"a.go", "b.go", "c.go"}}, want: "Read 3 files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolRunningSummaryFromArgs("read", tt.args); got != tt.want {
				t.Fatalf("summary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToolSummariesForReadSearchAndListTools(t *testing.T) {
	running := map[string]string{
		"read": "Read 1 file",
		"grep": "Search files",
		"find": "Find files",
		"ls":   "List directory",
	}
	for tool, want := range running {
		if got := toolRunningSummaryFromArgs(tool, map[string]any{"path": "main.go"}); got != want {
			t.Fatalf("%s running summary = %q, want %q", tool, got, want)
		}
	}

	done := []struct {
		name   string
		tool   string
		output string
		want   string
	}{
		{name: "grep files", tool: "grep", output: "Found 2 file(s)\na.go\nb.go", want: "Found 2 files"},
		{name: "grep count", tool: "grep", output: "a.go:2\nb.go:3\n\nFound 5 total occurrence(s) across 2 file(s).", want: "Found 5 matches"},
		{name: "grep content", tool: "grep", output: "a.go:10:needle\nb.go:20:needle", want: "Found 2 matches"},
		{name: "grep empty", tool: "grep", output: "No matches found.", want: "Found 0 matches"},
		{name: "find files", tool: "find", output: "a.go\nb.go\n\n(Results are truncated. Consider using a more specific path or pattern.)", want: "Found 2 files"},
		{name: "find empty", tool: "find", output: "No files found", want: "Found 0 files"},
		{name: "ls entries", tool: "ls", output: "cmd/\ngo.mod\n\n... (20 entries total, showing first 2)", want: "Listed 2 entries"},
		{name: "ls empty", tool: "ls", output: "(empty directory)", want: "Listed 0 entries"},
	}
	for _, tt := range done {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolDoneSummary(tt.tool, false, tt.output); got != tt.want {
				t.Fatalf("done summary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMessagesFromAgentEventFormatsWriteToolAsExpandedCodeBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	start := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "write",
		Args: map[string]any{
			"path":    path,
			"content": "package main\nfunc main() {}\n",
		},
	})
	if len(start) != 1 {
		t.Fatalf("start messages len = %d, want 1", len(start))
	}
	if got := start[0]; !got.ToolNoCollapse || !got.Expanded || got.ToolInput != path || got.ToolCode == "" || got.ToolLanguage != "go" {
		t.Fatalf("unexpected write start message: %#v", got)
	}
	if !strings.Contains(start[0].ToolOutput, "Wrote 2 lines") {
		t.Fatalf("write summary missing line count: %#v", start[0])
	}
	if !strings.Contains(start[0].ToolCode, "1  package main") || !strings.Contains(start[0].ToolCode, "2  func main() {}") {
		t.Fatalf("write code should include line numbers: %#v", start[0])
	}

	end := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "write",
		Result: types.ToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "The file main.go has been updated successfully."}},
			Details: map[string]any{"type": "update"},
		},
	})
	if len(end) != 1 {
		t.Fatalf("end messages len = %d, want 1", len(end))
	}
	if got := end[0]; got.ToolName != "update" || !got.ToolNoCollapse || !got.Expanded || got.ToolOutput != "" {
		t.Fatalf("unexpected write end message: %#v", got)
	}
}

func TestMessagesFromAgentEventFormatsExistingWriteAsUpdateDiff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"old\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	start := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "write",
		Args: map[string]any{
			"path":    path,
			"content": "package main\n\nfunc main() {\n\tprintln(\"new\")\n}\n",
		},
	})
	if len(start) != 1 {
		t.Fatalf("start messages len = %d, want 1", len(start))
	}
	got := start[0]
	if got.ToolName != "update" || got.ToolLanguage != "diff" || !got.ToolNoCollapse || !got.Expanded {
		t.Fatalf("unexpected update write message: %#v", got)
	}
	for _, want := range []string{"Added 1 lines, removed 1 lines", "@@ -4,1 +4,1 @@", "  3  func main() {", "- 4  \tprintln(\"old\")", "+ 4  \tprintln(\"new\")", "  5  }"} {
		if !strings.Contains(got.ToolOutput+"\n"+got.ToolCode, want) {
			t.Fatalf("update write message missing %q: %#v", want, got)
		}
	}
}

func TestMessagesFromAgentEventUsesSessionCwdForRelativeUpdateDiff(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "main.go"), []byte("before\nold\nafter\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	start := messagesFromAgentEventWithCwd(types.Event{
		Type:       types.EventTypeToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "edit",
		Args: map[string]any{
			"path":     "main.go",
			"old_text": "old\n",
			"new_text": "new\n",
		},
	}, cwd)
	if len(start) != 1 {
		t.Fatalf("start messages len = %d, want 1", len(start))
	}
	for _, want := range []string{"@@ -2,1 +2,1 @@", "  1  before", "- 2  old", "+ 2  new", "  3  after"} {
		if !strings.Contains(start[0].ToolCode, want) {
			t.Fatalf("relative edit diff missing %q: %#v", want, start[0])
		}
	}
}

func TestMessagesFromAgentEventFormatsEditToolAsExpandedDiffBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tfmt.Println(\"old\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	start := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionStart,
		ToolCallID: "call-1",
		ToolName:   "edit",
		Args: map[string]any{
			"path":     path,
			"old_text": "\tfmt.Println(\"old\")\n",
			"new_text": "\tfmt.Println(\"new\")\n",
		},
	})
	if len(start) != 1 {
		t.Fatalf("start messages len = %d, want 1", len(start))
	}
	got := start[0]
	if got.ToolName != "update" || !got.ToolNoCollapse || got.ToolLanguage != "diff" {
		t.Fatalf("unexpected edit message: %#v", got)
	}
	for _, want := range []string{"Added 1 lines, removed 1 lines", "@@ -4,1 +4,1 @@", "  3  func main() {", "- 4  \tfmt.Println(\"old\")", "+ 4  \tfmt.Println(\"new\")", "  5  }"} {
		if !strings.Contains(got.ToolOutput+"\n"+got.ToolCode, want) {
			t.Fatalf("edit message missing %q: %#v", want, got)
		}
	}

	end := messagesFromAgentEvent(types.Event{
		Type:       types.EventTypeToolExecutionEnd,
		ToolCallID: "call-1",
		ToolName:   "edit",
		Result: types.ToolResult{Content: []types.ContentBlock{&types.TextContent{
			Type: "text",
			Text: "Successfully edited main.go (1 replacement(s))\n\n--- main.go\n+++ main.go\n@@ -10,1 +10,1 @@\n  9  before\n- 10  old\n+ 10  new\n  11  after\n",
		}}},
	})
	if len(end) != 1 {
		t.Fatalf("end messages len = %d, want 1", len(end))
	}
	for _, want := range []string{"@@ -10,1 +10,1 @@", "  9  before", "- 10  old", "+ 10  new", "  11  after"} {
		if !strings.Contains(end[0].ToolCode, want) {
			t.Fatalf("edit end diff missing %q: %#v", want, end[0])
		}
	}
	if end[0].ToolOutput != "" || end[0].ToolLanguage != "diff" {
		t.Fatalf("edit end should hide raw success text and keep diff language: %#v", end[0])
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

func TestModuTUIPrompterSelectUsesHumanPromptCard(t *testing.T) {
	requests := make(chan modutui.RequestHumanPromptMsg, 1)
	prompter := &moduTUIPrompter{
		ctx: context.Background(),
		send: func(msg tea.Msg) {
			req, ok := msg.(modutui.RequestHumanPromptMsg)
			if !ok {
				t.Fatalf("unexpected message type %T", msg)
			}
			requests <- req
			req.Respond <- "2 commits"
		},
	}

	got := prompter.Select("Choose commit shape", []string{"2 commits", "1 commit"})
	if got != "2 commits" {
		t.Fatalf("Select returned %q", got)
	}
	req := <-requests
	if req.Request.Title != "Choose commit shape" || len(req.Request.Options) != 2 || req.Request.DefaultIndex != 0 {
		t.Fatalf("unexpected human prompt request: %#v", req.Request)
	}
}

func TestModuTUIPrompterConfirmUsesHumanPromptCard(t *testing.T) {
	requests := make(chan modutui.RequestHumanPromptMsg, 1)
	prompter := &moduTUIPrompter{
		ctx: context.Background(),
		send: func(msg tea.Msg) {
			req, ok := msg.(modutui.RequestHumanPromptMsg)
			if !ok {
				t.Fatalf("unexpected message type %T", msg)
			}
			requests <- req
			req.Respond <- "no"
		},
	}

	if got := prompter.Confirm("Overwrite?", "file exists", true); got {
		t.Fatal("Confirm should return false for no")
	}
	req := <-requests
	if req.Request.DefaultIndex != 0 || len(req.Request.Options) != 2 || req.Request.Options[0].Value != "yes" || req.Request.Options[1].Value != "no" {
		t.Fatalf("unexpected confirm prompt request: %#v", req.Request)
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
	for _, want := range []string{"/help", "/clear", "/config", "/model", "/tokens", "/compact"} {
		if !seen[want] {
			t.Fatalf("missing slash command %q in %#v", want, commands)
		}
	}
}

func TestMessagesFromSessionTranscriptRestoresCompactionDivider(t *testing.T) {
	streamFn := func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
		stream := types.NewEventStream()
		go func() {
			stream.Resolve(&types.AssistantMessage{
				Role:       types.RoleAssistant,
				StopReason: "stop",
				Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "compact summary"}},
				Timestamp:  time.Now().UnixMilli(),
			}, nil)
			stream.Close()
		}()
		return stream, nil
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test", Name: "Test", ProviderID: "test", ContextWindow: 32768},
		GetAPIKey: func(string) (string, error) { return "", nil },
		StreamFn:  streamFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		session.GetAgent().AppendMessage(types.UserMessage{Role: types.RoleUser, Content: "msg"})
	}
	if err := session.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}

	messages := messagesFromSessionTranscript(session)
	for _, msg := range messages {
		if msg.Text == moduTUIContextCompactDivider {
			return
		}
	}
	t.Fatalf("expected compact divider in transcript: %#v", messages)
}

func TestModuTUIQueueCommandParsesSteerAndFollowUp(t *testing.T) {
	tests := []struct {
		line     string
		wantKind modutui.SubmitKind
		wantText string
		wantOK   bool
	}{
		{line: "/steer change direction", wantKind: modutui.SubmitKindSteer, wantText: "change direction", wantOK: true},
		{line: "/s quick", wantKind: modutui.SubmitKindSteer, wantText: "quick", wantOK: true},
		{line: "/followup next", wantKind: modutui.SubmitKindFollowUp, wantText: "next", wantOK: true},
		{line: "/f later", wantKind: modutui.SubmitKindFollowUp, wantText: "later", wantOK: true},
		{line: "/model list", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			gotKind, gotText, gotOK := moduTUIQueueCommand(tt.line)
			if gotOK != tt.wantOK || gotKind != tt.wantKind || gotText != tt.wantText {
				t.Fatalf("moduTUIQueueCommand(%q) = %q, %q, %v; want %q, %q, %v", tt.line, gotKind, gotText, gotOK, tt.wantKind, tt.wantText, tt.wantOK)
			}
		})
	}
}

func TestModuTUIInputHistoryPersistenceTrimsTo100(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history", "input_history")
	history := make([]string, 105)
	for i := range history {
		history[i] = "prompt"
	}
	history[0] = ""
	history[104] = "latest"

	if err := saveModuTUIInputHistory(path, history); err != nil {
		t.Fatalf("saveModuTUIInputHistory returned error: %v", err)
	}
	got, err := loadModuTUIInputHistory(path)
	if err != nil {
		t.Fatalf("loadModuTUIInputHistory returned error: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("loaded history len = %d, want 100", len(got))
	}
	if got[len(got)-1] != "latest" {
		t.Fatalf("loaded newest history = %q, want latest", got[len(got)-1])
	}
}

func TestModuTUIInputHistoryPersistencePreservesMultilinePrompts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history", "input_history")
	history := []string{
		"single line",
		"first line\nsecond line\nthird line",
	}

	if err := saveModuTUIInputHistory(path, history); err != nil {
		t.Fatalf("saveModuTUIInputHistory returned error: %v", err)
	}
	got, err := loadModuTUIInputHistory(path)
	if err != nil {
		t.Fatalf("loadModuTUIInputHistory returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded history len = %d, want 2: %#v", len(got), got)
	}
	if got[1] != history[1] {
		t.Fatalf("multiline prompt = %q, want %q", got[1], history[1])
	}
}

func TestModuTUIInputHistoryLoadsLegacyLineFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history", "input_history")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := loadModuTUIInputHistory(path)
	if err != nil {
		t.Fatalf("loadModuTUIInputHistory returned error: %v", err)
	}
	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("legacy history = %#v, want [first second]", got)
	}
}

func TestModuTUIInfoCardLinesIncludeStartupContext(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test-model", Name: "Test Model", ProviderID: "test-provider", ContextWindow: 32768},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Join(moduTUIInfoCardLines(session, session.GetModel()), "\n")
	for _, want := range []string{
		"modu_code",
		"model: Test Model",
		"cwd: " + session.RuntimeState().Cwd,
		"session: " + shortModuTUISessionID(session.GetSessionID()),
		"commands: type /",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("info card lines missing %q:\n%s", want, lines)
		}
	}
}

func TestModuTUIFooterIncludesContextModelAndCwd(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test-model", Name: "Test Model", ProviderID: "test-provider", ContextWindow: 32768},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	footer := moduTUIFooter(session)
	for _, want := range []string{
		"ctx 0/33K",
		"Test Model",
		compactModuTUICwd(session.RuntimeState().Cwd),
	} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q:\n%s", want, footer)
		}
	}
}

func TestFormatModuTUITokens(t *testing.T) {
	tests := map[int]string{
		0:       "0",
		999:     "999",
		1200:    "1.2K",
		32768:   "33K",
		262144:  "262K",
		1000000: "1M",
	}
	for input, want := range tests {
		if got := formatModuTUITokens(input); got != want {
			t.Fatalf("formatModuTUITokens(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestModuTUITodosConvertsSessionTodos(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test", Name: "Test", ProviderID: "test"},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session.SetTodos([]coding_agent.TodoItem{
		{Content: "first", Status: "in_progress"},
		{Content: "second", Status: "pending"},
	})

	got := moduTUITodos(session)
	if len(got) != 2 {
		t.Fatalf("todos len = %d, want 2: %#v", len(got), got)
	}
	if got[0].Content != "first" || got[0].Status != "in_progress" || got[1].Content != "second" {
		t.Fatalf("unexpected converted todos: %#v", got)
	}
}

func TestModuTUIMouseEnabledForSSHUnlessDisabled(t *testing.T) {
	if moduTUIMouseDisabledFromEnv([]string{"SSH_TTY=/dev/pts/1"}) {
		t.Fatal("SSH_TTY should keep mouse reporting by default")
	}
	if moduTUIMouseDisabledFromEnv([]string{"SSH_CONNECTION=1.1.1.1 22 2.2.2.2 33333"}) {
		t.Fatal("SSH_CONNECTION should keep mouse reporting by default")
	}
	if moduTUIMouseDisabledFromEnv([]string{"SSH_TTY=/dev/pts/1", "MODU_TUI_MOUSE=on"}) {
		t.Fatal("MODU_TUI_MOUSE=on should force mouse reporting on")
	}
	if !moduTUIMouseDisabledFromEnv([]string{"MODU_TUI_MOUSE=off"}) {
		t.Fatal("MODU_TUI_MOUSE=off should force mouse reporting off")
	}
	if moduTUIMouseDisabledFromEnv([]string{"TERM=xterm-256color"}) {
		t.Fatal("non-SSH terminal should keep mouse reporting by default")
	}
}

func TestModuTUIArrowKeysScrollForSSHAndMouseDisabled(t *testing.T) {
	if !moduTUIArrowKeysScrollFromEnv([]string{"SSH_TTY=/dev/pts/1"}) {
		t.Fatal("SSH sessions should keep empty-input arrow key transcript scrolling")
	}
	if !moduTUIArrowKeysScrollFromEnv([]string{"MODU_TUI_MOUSE=off"}) {
		t.Fatal("mouse-disabled sessions should use arrow key transcript scrolling")
	}
	if moduTUIArrowKeysScrollFromEnv([]string{"TERM=xterm-256color"}) {
		t.Fatal("plain local terminal should keep normal input history arrow behavior")
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

func joinedModuTUIAppendMessages(messages []tea.Msg) string {
	var parts []string
	for _, msg := range messages {
		if appendMsg, ok := msg.(modutui.AppendMessageMsg); ok {
			parts = append(parts, appendMsg.Message.Text)
		}
	}
	return strings.Join(parts, "\n")
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
	runModuTUISlash(context.Background(), "/help", session, session.GetModel(), CommandHooks{}, func(msg tea.Msg) {
		messages = append(messages, msg)
	}, nil, nil, nil)

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

func TestRunModuTUISlashRoutesConfigHook(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test", Name: "Test", ProviderID: "test"},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	called := false
	var messages []tea.Msg
	runModuTUISlash(context.Background(), "/config validate", session, session.GetModel(), CommandHooks{
		Config: func(args string) (string, error) {
			called = true
			if args != "validate" {
				t.Fatalf("config args = %q, want validate", args)
			}
			return "config: test\nstatus: missing", nil
		},
	}, func(msg tea.Msg) {
		messages = append(messages, msg)
	}, nil, nil, nil)

	if !called {
		t.Fatal("expected config hook to be called")
	}
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
	if !got.Preformatted || !strings.Contains(got.Text, "status: missing") {
		t.Fatalf("unexpected config output: %#v", got)
	}
	if strings.Contains(got.Text, "unknown command") {
		t.Fatalf("/config should not fall through to unknown command: %q", got.Text)
	}
}

func TestRunModuTUISlashExactConfigStartsWizard(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     &types.Model{ID: "test", Name: "Test", ProviderID: "test"},
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	started := false
	configCalled := false
	runModuTUISlash(context.Background(), "/config", session, session.GetModel(), CommandHooks{
		Config: func(args string) (string, error) {
			configCalled = true
			return "should not be called", nil
		},
	}, func(msg tea.Msg) {}, nil, func() {
		started = true
	}, nil)

	if !started {
		t.Fatal("expected exact /config to start wizard")
	}
	if configCalled {
		t.Fatal("exact /config should not call config status hook")
	}
}

func TestRunModuTUISlashExactModelStartsSelector(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     providers.GetModel("deepseek", "deepseek-chat"),
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	started := false
	runModuTUISlash(context.Background(), "/model", session, session.GetModel(), CommandHooks{}, func(msg tea.Msg) {}, nil, nil, func() {
		started = true
	})

	if !started {
		t.Fatal("expected exact /model to start selector")
	}
}

func TestModuTUIWorkflowCockpitShowsOrchestrationMap(t *testing.T) {
	text := moduTUIWorkflowCockpitTextFromStates(map[string]any{
		"workflow": map[string]any{
			"runningCount":   1,
			"completedCount": 2,
			"indicator":      "workflow deep_research 2/3 running: Research",
			"runs": []map[string]any{
				{
					"id":                "20260630T130648.520375000Z",
					"name":              "deep_research",
					"status":            "running",
					"agentCount":        3,
					"doneCount":         2,
					"runningAgentCount": 1,
					"currentPhase":      "Research",
					"updatedAt":         200,
					"logs": []string{
						"scope complete",
						"research fanout started",
					},
					"phases": []map[string]any{
						{"title": "Scope", "agentCount": 1, "doneCount": 1},
						{"title": "Research", "agentCount": 2, "doneCount": 1, "runningCount": 1, "durationMs": 42000, "estimatedTokens": 1200},
					},
					"agents": []map[string]any{
						{
							"id":            1,
							"label":         "scope",
							"phase":         "Scope",
							"status":        "done",
							"resultPreview": "DOMAIN finance/markets; angles selected",
						},
						{
							"id":              2,
							"label":           "primary sources",
							"phase":           "Research",
							"status":          "done",
							"turnTokens":      1200,
							"recentToolCalls": 2,
							"recentToolCallPreviews": []map[string]any{
								{"toolName": "web_search", "resultPreview": "market close data"},
							},
						},
						{
							"id":            3,
							"label":         "watch tomorrow",
							"phase":         "Research",
							"status":        "running",
							"promptPreview": "Find tomorrow's catalysts",
						},
					},
				},
			},
		},
	})

	for _, want := range []string{
		"Workflow Cockpit",
		"overview",
		"workflow deep_research 2/3 running: Research",
		"board",
		"1. [done] Scope 1/1 complete",
		"2. [running] Research 1/2 running now",
		"> #3 watch tomorrow running: Find tomorrow's catalysts",
		"flow",
		"phases: Scope:done -> Research:run",
		"now: Research 1/2 running=1",
		"updates",
		"- scope complete",
		"- research fanout started",
		"timeline",
		"[done] Scope 1/1",
		"[running] Research 1/2 · 1 running · est 1200 · 42s",
		"latest run",
		"/workflows guide latest",
		"/workflows feed latest",
		"/workflows map latest",
		"/workflows show latest",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("workflow cockpit missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{
		"orchestration map",
		"result: DOMAIN finance/markets; angles selected",
		"tools: web_search -> market close data",
	} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("workflow cockpit should keep full map out of overview, found %q:\n%s", unwanted, text)
		}
	}
}

func TestModuTUIWorkflowCockpitRowsOpenFeedForRuns(t *testing.T) {
	rows := moduTUIWorkflowCockpitRowsFromStates(map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-2",
				"name":         "market_watch",
				"status":       "completed",
				"agentCount":   5,
				"doneCount":    5,
				"durationMs":   65000,
				"currentPhase": "Report",
				"updatedAt":    200,
			}, {
				"id":         "run-1",
				"status":     "running",
				"agentCount": 3,
				"doneCount":  1,
				"updatedAt":  100,
			}},
		},
	})

	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].Value != "run-2" || rows[0].Command != moduTUIWorkflowPanelFeedPrefix+"run-2" {
		t.Fatalf("completed row should open run-2 feed: %#v", rows[0])
	}
	if rows[1].Value != "run-1" || rows[1].Command != moduTUIWorkflowPanelFeedPrefix+"run-1" {
		t.Fatalf("running row should open run-1 feed: %#v", rows[1])
	}
	for _, want := range []string{"market_watch", "completed", "5/5"} {
		if !strings.Contains(rows[0].Label, want) {
			t.Fatalf("row label missing %q: %#v", want, rows[0])
		}
	}
	if !strings.Contains(rows[0].Detail, "Report") || !strings.Contains(rows[0].Detail, "1min 05s") {
		t.Fatalf("row detail should include phase and duration: %#v", rows[0])
	}
}

func TestModuTUIWorkflowCockpitPanelSelectsLatestRunningRun(t *testing.T) {
	panel := moduTUIWorkflowCockpitPanelFromStates(map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "latest-complete",
				"name":         "latest_complete",
				"status":       "completed",
				"updatedAt":    30,
				"agentCount":   2,
				"doneCount":    2,
				"currentPhase": "Report",
				"snapshotPath": "/tmp/workflow/latest/snapshot.json",
				"scriptPath":   "/tmp/workflow/latest/script.js",
				"phases": []map[string]any{{
					"title":      "Research",
					"agentCount": 1,
					"doneCount":  1,
				}, {
					"title":      "Report",
					"agentCount": 1,
					"doneCount":  1,
				}},
			}, {
				"id":        "active-run",
				"name":      "active",
				"status":    "running",
				"updatedAt": 20,
			}, {
				"id":        "old-run",
				"name":      "old",
				"status":    "failed",
				"updatedAt": 10,
			}},
		},
	})

	if panel.Selected != 1 {
		t.Fatalf("cockpit selected row = %d, want active running row 1: %#v", panel.Selected, panel.Rows)
	}
	if panel.Rows[panel.Selected].Value != "active-run" {
		t.Fatalf("cockpit selected row = %#v, want active-run", panel.Rows[panel.Selected])
	}
	text := strings.Join(panel.Lines, "\n")
	for _, want := range []string{
		"latest run preview",
		"+-- Status",
		"latest_complete [completed]",
		"+-- Path",
		"path: Research:done -> Report:done",
		"+-- Outcome",
		"result: open Result view",
		"script: open Script view",
		"recent runs",
		"1. latest_complete [completed] | 2/2 | @Report",
		"2. active [running]",
		"3. old [failed]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cockpit preview missing %q:\n%s", want, text)
		}
	}
	if !moduTUIWorkflowPanelHasShortcut(panel, "?", moduTUIWorkflowPanelGuidePrefix+"latest-complete") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "f", moduTUIWorkflowPanelFeedPrefix+"latest-complete") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "m", moduTUIWorkflowPanelMapPrefix+"latest-complete") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "d", moduTUIWorkflowPanelDetailPrefix+"latest-complete") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "o", moduTUIWorkflowPanelResultPrefix+"latest-complete") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "s", moduTUIWorkflowPanelScriptPrefix+"latest-complete") {
		t.Fatalf("cockpit shortcuts should target latest run: %#v", panel.Shortcuts)
	}
	if !strings.Contains(panel.Footer, "[?] Guide") || !strings.Contains(panel.Footer, "[f] Feed") ||
		!strings.Contains(panel.Footer, "[o] Result") || !strings.Contains(panel.Footer, "[s] Script") ||
		strings.Contains(panel.Footer, "details") {
		t.Fatalf("cockpit footer should expose latest shortcuts and generic open copy: %q", panel.Footer)
	}
	next, ok := moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowCockpitPanelID,
		Command: panel.Rows[panel.Selected].Command,
		Row:     panel.Rows[panel.Selected],
	})
	if !ok || next.ID != moduTUIWorkflowFeedPanelID {
		t.Fatalf("cockpit selected running row should open feed, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowCockpitPanelID,
		Command: moduTUIWorkflowPanelGuidePrefix + "latest-complete",
	})
	if !ok || next.ID != moduTUIWorkflowGuidePanelID {
		t.Fatalf("cockpit guide shortcut should open guide, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowCockpitPanelID,
		Command: moduTUIWorkflowPanelMapPrefix + "latest-complete",
	})
	if !ok || next.ID != moduTUIWorkflowMapPanelID {
		t.Fatalf("cockpit map shortcut should open map, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowCockpitPanelID,
		Command: moduTUIWorkflowPanelResultPrefix + "latest-complete",
	})
	if !ok || next.ID != moduTUIWorkflowResultPanelID {
		t.Fatalf("cockpit result shortcut should open result panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowCockpitPanelID,
		Command: moduTUIWorkflowPanelScriptPrefix + "latest-complete",
	})
	if !ok || next.ID != moduTUIWorkflowScriptPanelID {
		t.Fatalf("cockpit script shortcut should open script panel, got ok=%v panel=%#v", ok, next)
	}
}

func TestModuTUIWorkflowRunDetailPanelShowsRunInPanel(t *testing.T) {
	panel := moduTUIWorkflowRunDetailPanelFromStates(map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":                "run-detail",
				"name":              "market_watch",
				"status":            "completed",
				"agentCount":        2,
				"doneCount":         2,
				"runningAgentCount": 0,
				"durationMs":        65000,
				"currentPhase":      "Report",
				"phases": []map[string]any{{
					"title":      "Research",
					"agentCount": 1,
					"doneCount":  1,
				}, {
					"title":      "Report",
					"agentCount": 1,
					"doneCount":  1,
				}},
				"agents": []map[string]any{{
					"id":            1,
					"label":         "collect",
					"phase":         "Research",
					"status":        "done",
					"resultPreview": "market data ok",
				}, {
					"id":            2,
					"label":         "write report",
					"phase":         "Report",
					"status":        "done",
					"resultPreview": "report ok",
				}},
			}},
		},
	}, "run-detail")

	if panel.ID != moduTUIWorkflowRunDetailPanelID || panel.Title != "Workflow Run" {
		t.Fatalf("unexpected detail panel header: %#v", panel)
	}
	text := strings.Join(panel.Lines, "\n")
	for _, want := range []string{
		"summary",
		"id: run-detail",
		"progress: 2/2 done, 0 running, 0 errors",
		"duration: 1min 05s",
		"board",
		"Enter Map to inspect the full phase and agent tree",
		"/workflows agent run-detail <agent-id>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("detail panel missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "[Research] 1/1 done") || strings.Contains(text, "result: market data ok") {
		t.Fatalf("detail panel should keep full orchestration in the map panel:\n%s", text)
	}
	if len(panel.Rows) != 11 ||
		panel.Rows[0].Command != moduTUIWorkflowPanelFeedPrefix+"run-detail" ||
		panel.Rows[1].Command != moduTUIWorkflowPanelPhasePrefix+"run-detail:Report" ||
		panel.Rows[2].Command != moduTUIWorkflowPanelControlPrefix+"restart:run-detail" ||
		panel.Rows[3].Command != moduTUIWorkflowPanelPhasePrefix+"run-detail:Research" ||
		panel.Rows[4].Command != moduTUIWorkflowPanelPhasePrefix+"run-detail:Report" ||
		panel.Rows[5].Command != moduTUIWorkflowPanelGuidePrefix+"run-detail" ||
		panel.Rows[6].Command != moduTUIWorkflowPanelMapPrefix+"run-detail" ||
		panel.Rows[7].Command != moduTUIWorkflowPanelAgentsPrefix+"run-detail" ||
		panel.Rows[8].Command != moduTUIWorkflowPanelResultPrefix+"run-detail" ||
		panel.Rows[9].Command != moduTUIWorkflowPanelScriptPrefix+"run-detail" ||
		panel.Rows[10].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("detail panel should expose control, phase, guide, agents, result, script, and back rows: %#v", panel.Rows)
	}
	if panel.Selected != 1 {
		t.Fatalf("detail panel selected row = %d, want current phase quick row 1: %#v", panel.Selected, panel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(panel, "f", moduTUIWorkflowPanelFeedPrefix+"run-detail") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "?", moduTUIWorkflowPanelGuidePrefix+"run-detail") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "m", moduTUIWorkflowPanelMapPrefix+"run-detail") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-detail") {
		t.Fatalf("detail panel should expose view navigation shortcuts: %#v", panel.Shortcuts)
	}
	if !strings.Contains(panel.Footer, "[?] Guide") || !strings.Contains(panel.Footer, "[f] Feed") || !strings.Contains(panel.Footer, "[m] Map") || !strings.Contains(panel.Footer, "[a] Agents") {
		t.Fatalf("detail footer should expose view navigation shortcuts: %q", panel.Footer)
	}

	mapPanel := moduTUIWorkflowMapPanelFromStates(map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-detail",
				"name":         "market_watch",
				"status":       "completed",
				"currentPhase": "Research",
				"phases": []map[string]any{{
					"title":      "Research",
					"agentCount": 1,
					"doneCount":  1,
				}},
				"agents": []map[string]any{{
					"id":            1,
					"label":         "collect",
					"phase":         "Research",
					"status":        "done",
					"resultPreview": "market data ok",
				}},
			}},
		},
	}, "run-detail")
	mapText := strings.Join(mapPanel.Lines, "\n")
	if mapPanel.ID != moduTUIWorkflowMapPanelID || !strings.Contains(mapText, "orchestration map") || !strings.Contains(mapText, "result: market data ok") {
		t.Fatalf("map panel should contain full orchestration tree: %#v\n%s", mapPanel, mapText)
	}
	for _, want := range []string{
		"topology",
		"01 Research [done] 1/1",
		"path: start -> Research -> finish",
		"agents: done #1 collect",
		"tree",
	} {
		if !strings.Contains(mapText, want) {
			t.Fatalf("map panel missing topology line %q:\n%s", want, mapText)
		}
	}
	if len(mapPanel.Rows) != 6 ||
		mapPanel.Rows[0].Command != moduTUIWorkflowPanelPhasePrefix+"run-detail:Research" ||
		mapPanel.Rows[1].Command != moduTUIWorkflowPanelGuidePrefix+"run-detail" ||
		mapPanel.Rows[2].Command != moduTUIWorkflowPanelDetailPrefix+"run-detail" ||
		mapPanel.Rows[3].Command != moduTUIWorkflowPanelFeedPrefix+"run-detail" ||
		mapPanel.Rows[4].Command != moduTUIWorkflowPanelAgentsPrefix+"run-detail" ||
		mapPanel.Rows[5].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("map panel rows = %#v", mapPanel.Rows)
	}
	if mapPanel.Selected != 0 {
		t.Fatalf("map panel selected row = %d, want current phase row 0: %#v", mapPanel.Selected, mapPanel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(mapPanel, "f", moduTUIWorkflowPanelFeedPrefix+"run-detail") ||
		!moduTUIWorkflowPanelHasShortcut(mapPanel, "?", moduTUIWorkflowPanelGuidePrefix+"run-detail") ||
		!moduTUIWorkflowPanelHasShortcut(mapPanel, "d", moduTUIWorkflowPanelDetailPrefix+"run-detail") ||
		!moduTUIWorkflowPanelHasShortcut(mapPanel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-detail") {
		t.Fatalf("map panel should expose view navigation shortcuts: %#v", mapPanel.Shortcuts)
	}
	next, ok := moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowRunDetailPanelID,
		Command: moduTUIWorkflowPanelResultPrefix + "run-detail",
	})
	if !ok || next.ID != moduTUIWorkflowResultPanelID {
		t.Fatalf("detail result action should open result panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowRunDetailPanelID,
		Command: moduTUIWorkflowPanelScriptPrefix + "run-detail",
	})
	if !ok || next.ID != moduTUIWorkflowScriptPanelID {
		t.Fatalf("detail script action should open script panel, got ok=%v panel=%#v", ok, next)
	}
	guidePanel := moduTUIWorkflowGuidePanelFromStates(map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-detail",
				"name":         "market_watch",
				"status":       "completed",
				"currentPhase": "Research",
				"phases": []map[string]any{{
					"title":      "Research",
					"agentCount": 1,
					"doneCount":  1,
				}},
				"agents": []map[string]any{{
					"id":     1,
					"label":  "collect",
					"phase":  "Research",
					"status": "done",
				}},
			}},
		},
	}, "run-detail")
	guideText := strings.Join(guidePanel.Lines, "\n")
	for _, want := range []string{
		"workflow guide",
		"Feed: live Status/Plan/Metrics cards, board, lanes, updates, timeline",
		"Map: topology, phase path edges, agent lanes, detailed tree",
		"Phase: one orchestration stage with position and neighbors",
		"Agent: phase context, peer lanes, status, tools, result/error, transcript",
		"Result: final workflow output with run and plan context",
		"Script: generated or resumed workflow script with run context",
		"current route",
		"/workflows -> running run -> Feed",
		"Feed cards -> current phase, attention agent, active agent",
		"Map topology -> Phase/Agent for structure",
		"Result/Script -> Feed/Map to return to execution context",
		"current phase",
		"Research 1/1 done",
	} {
		if !strings.Contains(guideText, want) {
			t.Fatalf("guide panel missing %q:\n%s", want, guideText)
		}
	}
	if guidePanel.Rows[0].Command != moduTUIWorkflowPanelPhasePrefix+"run-detail:Research" ||
		guidePanel.Rows[1].Command != moduTUIWorkflowPanelFeedPrefix+"run-detail" ||
		guidePanel.Rows[2].Command != moduTUIWorkflowPanelMapPrefix+"run-detail" ||
		guidePanel.Rows[3].Command != moduTUIWorkflowPanelDetailPrefix+"run-detail" ||
		guidePanel.Rows[4].Command != moduTUIWorkflowPanelResultPrefix+"run-detail" ||
		guidePanel.Rows[5].Command != moduTUIWorkflowPanelScriptPrefix+"run-detail" {
		t.Fatalf("guide panel rows = %#v", guidePanel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(guidePanel, "o", moduTUIWorkflowPanelResultPrefix+"run-detail") ||
		!moduTUIWorkflowPanelHasShortcut(guidePanel, "s", moduTUIWorkflowPanelScriptPrefix+"run-detail") ||
		!strings.Contains(guidePanel.Footer, "[o] Result") ||
		!strings.Contains(guidePanel.Footer, "[s] Script") {
		t.Fatalf("guide panel should expose artifact shortcuts: shortcuts=%#v footer=%q", guidePanel.Shortcuts, guidePanel.Footer)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowMapPanelID,
		Command: moduTUIWorkflowPanelFeedPrefix + "run-detail",
	})
	if !ok || next.ID != moduTUIWorkflowFeedPanelID {
		t.Fatalf("map shortcut action should open feed panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowMapPanelID,
		Command: moduTUIWorkflowPanelPhasePrefix + "run-detail:Research",
	})
	if !ok || next.ID != moduTUIWorkflowPhasePanelID {
		t.Fatalf("map phase action should open phase panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowMapPanelID,
		Command: moduTUIWorkflowPanelGuidePrefix + "run-detail",
	})
	if !ok || next.ID != moduTUIWorkflowGuidePanelID {
		t.Fatalf("map guide shortcut action should open guide panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowGuidePanelID,
		Command: moduTUIWorkflowPanelMapPrefix + "run-detail",
	})
	if !ok || next.ID != moduTUIWorkflowMapPanelID {
		t.Fatalf("guide map action should open map panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowGuidePanelID,
		Command: moduTUIWorkflowPanelResultPrefix + "run-detail",
	})
	if !ok || next.ID != moduTUIWorkflowResultPanelID {
		t.Fatalf("guide result action should open result panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowGuidePanelID,
		Command: moduTUIWorkflowPanelScriptPrefix + "run-detail",
	})
	if !ok || next.ID != moduTUIWorkflowScriptPanelID {
		t.Fatalf("guide script action should open script panel, got ok=%v panel=%#v", ok, next)
	}
}

func moduTUIWorkflowPanelHasShortcut(panel modutui.Panel, key, command string) bool {
	for _, shortcut := range panel.Shortcuts {
		if shortcut.Key == key && shortcut.Command == command {
			return true
		}
	}
	return false
}

func moduTUIWorkflowPanelHasRowCommand(panel modutui.Panel, command string) bool {
	for _, row := range panel.Rows {
		if row.Command == command {
			return true
		}
	}
	return false
}

func TestModuTUIWorkflowRunDetailPanelShowsLiveFlowSummary(t *testing.T) {
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":                "run-flow",
				"name":              "market_watch",
				"status":            "running",
				"agentCount":        4,
				"doneCount":         1,
				"runningAgentCount": 1,
				"errorCount":        1,
				"currentPhase":      "Research",
				"durationMs":        65000,
				"cost":              0.012345,
				"logs": []string{
					"old update",
					"scoped market domain",
					"primary sources done",
					"started cross-check",
					"risk retry",
					"writing final summary",
				},
				"phases": []map[string]any{{
					"title":      "Scope",
					"agentCount": 1,
					"doneCount":  1,
				}, {
					"title":           "Research",
					"agentCount":      2,
					"doneCount":       1,
					"runningCount":    1,
					"errorCount":      1,
					"durationMs":      42000,
					"estimatedTokens": 500,
				}, {
					"title":      "Report",
					"agentCount": 1,
				}},
				"agents": []map[string]any{{
					"id":     1,
					"label":  "scope",
					"phase":  "Scope",
					"status": "done",
				}, {
					"id":              3,
					"label":           "verify",
					"phase":           "Research",
					"status":          "running",
					"promptPreview":   "cross-check catalysts",
					"recentToolCalls": 2,
				}, {
					"id":     4,
					"label":  "risk",
					"phase":  "Research",
					"status": "failed",
					"error":  "source unavailable",
				}, {
					"id":     5,
					"label":  "draft",
					"phase":  "Report",
					"status": "queued",
				}},
			}},
		},
	}
	panel := moduTUIWorkflowRunDetailPanelFromStates(states, "run-flow")

	text := strings.Join(panel.Lines, "\n")
	for _, want := range []string{
		"board",
		"1. [done] Scope 1/1 complete",
		"2. [error] Research 1/2 needs attention",
		"! #4 risk failed: source unavailable",
		"> #3 verify running · 2 tools: cross-check catalysts",
		"3. [waiting] Report 0/1 waits for Research",
		"flow",
		"phases: Scope:done -> Research:error -> Report:wait",
		"now: Research 1/2 errors=1",
		"active: #3 verify [running] @Research 2 tools",
		"prompt: cross-check catalysts",
		"attention: #4 risk [failed] @Research",
		"error: source unavailable",
		"next: Report",
		"updates",
		"- scoped market domain",
		"- primary sources done",
		"- started cross-check",
		"- risk retry",
		"- writing final summary",
		"timeline",
		"[done] Scope 1/1",
		"[error] Research 1/2 · 1 running · 1 errors · est 500 · 42s",
		"attention: #4 risk [failed] @Research",
		"active: #3 verify [running] @Research 2 tools",
		"[waiting] Report 0/1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("detail flow summary missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "old update") {
		t.Fatalf("updates should only show recent entries:\n%s", text)
	}
	if len(panel.Rows) < 4 ||
		panel.Rows[0].Command != moduTUIWorkflowPanelFeedPrefix+"run-flow" ||
		panel.Rows[1].Command != moduTUIWorkflowPanelPhasePrefix+"run-flow:Research" ||
		panel.Rows[2].Command != moduTUIWorkflowPanelAgentPrefix+"run-flow:4" ||
		panel.Rows[3].Command != moduTUIWorkflowPanelAgentPrefix+"run-flow:3" {
		t.Fatalf("detail panel should expose feed, current phase, attention agent, and active agent quick rows: %#v", panel.Rows)
	}
	if !strings.Contains(panel.Rows[2].Label, "Attention agent: #4 risk") || !strings.Contains(panel.Rows[2].Detail, "failed") {
		t.Fatalf("attention quick row = %#v", panel.Rows[2])
	}
	if !strings.Contains(panel.Rows[3].Label, "Active agent: #3 verify") || !strings.Contains(panel.Rows[3].Detail, "running") {
		t.Fatalf("active quick row = %#v", panel.Rows[3])
	}
	if panel.Selected != 1 {
		t.Fatalf("detail panel selected row = %d, want current phase quick row 1: %#v", panel.Selected, panel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(panel, "!", moduTUIWorkflowPanelAgentPrefix+"run-flow:4") {
		t.Fatalf("detail panel should expose attention shortcut: %#v", panel.Shortcuts)
	}
	if !strings.Contains(panel.Footer, "[!] Attention") {
		t.Fatalf("detail footer should expose attention shortcut: %q", panel.Footer)
	}

	feed := moduTUIWorkflowFeedPanelFromStates(states, "run-flow")
	if feed.ID != moduTUIWorkflowFeedPanelID || feed.Title != "Workflow Feed" {
		t.Fatalf("unexpected feed panel: %#v", feed)
	}
	feedText := strings.Join(feed.Lines, "\n")
	for _, want := range []string{
		"cards",
		"+-- Status",
		"market_watch [running]",
		"progress: 1/4 done | 1 running | 1 error",
		"current: Research",
		"duration: 1min 05s",
		"+-- Plan",
		"route: 1 Scope -> 2 Research -> 3 Report",
		"now: 2/3 Research 1/2 | 1 running | 1 error | needs attention",
		"next: 3/3 Report waits for Research",
		"stage 1: done Scope 1/1",
		"stage 2: attention Research 1/2",
		"stage 3: next Report 0/1",
		"+-- Metrics",
		"agents: 4 total | 1 done | 1 running | 1 error",
		"phases: 3 total | 1 done | 1 attention | 1 waiting",
		"estimated tokens: 500",
		"cost: 0.012345",
		"elapsed: 1min 05s",
		"+-- Path",
		"path: Scope:done -> Research:error -> Report:wait",
		"now: Research 1/2 errors=1",
		"next: Report",
		"+-- Attention",
		"#4 risk [failed] @Research",
		"error: source unavailable",
		"+-- Active",
		"#3 verify [running] @Research 2 tools",
		"prompt: cross-check catalysts",
		"+-- Next",
		"phase: Report",
		"board",
		"2. [error] Research 1/2 needs attention",
		"lanes",
		"Scope: done #1 scope",
		"Research: run #3 verify 2 tools | err #4 risk",
		"Report: wait #5 draft",
		"legend: run active | done complete | err attention | wait queued",
		"flow",
		"updates",
		"timeline",
		"phases: Scope:done -> Research:error -> Report:wait",
		"- writing final summary",
		"[error] Research 1/2 · 1 running · 1 errors · est 500 · 42s",
	} {
		if !strings.Contains(feedText, want) {
			t.Fatalf("feed panel missing %q:\n%s", want, feedText)
		}
	}
	if len(feed.Rows) < 8 ||
		feed.Rows[0].Command != moduTUIWorkflowPanelPhasePrefix+"run-flow:Research" ||
		feed.Rows[1].Command != moduTUIWorkflowPanelAgentPrefix+"run-flow:4" ||
		feed.Rows[2].Command != moduTUIWorkflowPanelAgentPrefix+"run-flow:3" ||
		feed.Rows[3].Command != moduTUIWorkflowPanelGuidePrefix+"run-flow" ||
		feed.Rows[4].Command != moduTUIWorkflowPanelDetailPrefix+"run-flow" ||
		feed.Rows[5].Command != moduTUIWorkflowPanelMapPrefix+"run-flow" ||
		feed.Rows[6].Command != moduTUIWorkflowPanelAgentsPrefix+"run-flow" ||
		feed.Rows[7].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("feed panel rows = %#v", feed.Rows)
	}
	if feed.Selected != 0 {
		t.Fatalf("feed panel selected row = %d, want current phase row 0: %#v", feed.Selected, feed.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(feed, "p", moduTUIWorkflowPanelControlPrefix+"pause:run-flow") ||
		!moduTUIWorkflowPanelHasShortcut(feed, "x", moduTUIWorkflowPanelControlPrefix+"stop:run-flow") ||
		!moduTUIWorkflowPanelHasShortcut(feed, "!", moduTUIWorkflowPanelAgentPrefix+"run-flow:4") ||
		!moduTUIWorkflowPanelHasShortcut(feed, "?", moduTUIWorkflowPanelGuidePrefix+"run-flow") ||
		!moduTUIWorkflowPanelHasShortcut(feed, "d", moduTUIWorkflowPanelDetailPrefix+"run-flow") ||
		!moduTUIWorkflowPanelHasShortcut(feed, "m", moduTUIWorkflowPanelMapPrefix+"run-flow") ||
		!moduTUIWorkflowPanelHasShortcut(feed, "a", moduTUIWorkflowPanelAgentsPrefix+"run-flow") {
		t.Fatalf("feed panel shortcuts = %#v", feed.Shortcuts)
	}
	if !strings.Contains(feed.Footer, "[p] Pause") || !strings.Contains(feed.Footer, "[x] Stop") ||
		!strings.Contains(feed.Footer, "[!] Attention") ||
		!strings.Contains(feed.Footer, "[?] Guide") ||
		!strings.Contains(feed.Footer, "[d] Detail") || !strings.Contains(feed.Footer, "[m] Map") || !strings.Contains(feed.Footer, "[a] Agents") {
		t.Fatalf("feed panel footer should expose shortcuts: %q", feed.Footer)
	}
	next, ok := moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowFeedPanelID,
		Command: moduTUIWorkflowPanelAgentPrefix + "run-flow:4",
	})
	if !ok || next.ID != moduTUIWorkflowAgentPanelID {
		t.Fatalf("feed attention shortcut action should open agent panel, got ok=%v panel=%#v", ok, next)
	}
}

func TestModuTUIWorkflowFeedPanelShowsOutcomeForCompletedRun(t *testing.T) {
	snapshotPath := filepath.Join(t.TempDir(), "snapshot.json")
	scriptPath := filepath.Join(t.TempDir(), "script.js")
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-done",
				"name":         "market_watch",
				"status":       "completed",
				"scriptPath":   scriptPath,
				"snapshotPath": snapshotPath,
				"agentCount":   2,
				"doneCount":    2,
				"currentPhase": "Report",
				"cost":         0.0042,
				"phases": []map[string]any{{
					"title":      "Research",
					"agentCount": 1,
					"doneCount":  1,
					"cost":       0.0012,
				}, {
					"title":      "Report",
					"agentCount": 1,
					"doneCount":  1,
					"cost":       0.0030,
				}},
				"agents": []map[string]any{{
					"id":            1,
					"label":         "collect",
					"phase":         "Research",
					"status":        "done",
					"resultPreview": "market data ok",
				}, {
					"id":            2,
					"label":         "write",
					"phase":         "Report",
					"status":        "done",
					"resultPreview": "report ok",
				}},
			}},
		},
	}

	panel := moduTUIWorkflowFeedPanelFromStates(states, "run-done")
	text := strings.Join(panel.Lines, "\n")
	for _, want := range []string{
		"+-- Plan",
		"route: 1 Research -> 2 Report",
		"now: workflow completed",
		"next: inspect outcome",
		"stage 1: done Research 1/1",
		"stage 2: done Report 1/1",
		"+-- Metrics",
		"cost: 0.004200",
		"+-- Path",
		"path: Research:done -> Report:done",
		"+-- Outcome",
		"status: completed",
		"result: open Result view",
		"snapshot: " + snapshotPath,
		"script: open Script view",
		"next: Result, Script, or Restart",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("completed feed missing %q:\n%s", want, text)
		}
	}
	if !moduTUIWorkflowPanelHasRowCommand(panel, moduTUIWorkflowPanelResultPrefix+"run-done") ||
		!moduTUIWorkflowPanelHasRowCommand(panel, moduTUIWorkflowPanelScriptPrefix+"run-done") {
		t.Fatalf("completed feed should expose result and script rows: %#v", panel.Rows)
	}
	if panel.Selected < 0 || panel.Selected >= len(panel.Rows) ||
		panel.Rows[panel.Selected].Command != moduTUIWorkflowPanelResultPrefix+"run-done" {
		t.Fatalf("completed feed should select result row by default, selected=%d rows=%#v", panel.Selected, panel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(panel, "o", moduTUIWorkflowPanelResultPrefix+"run-done") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "s", moduTUIWorkflowPanelScriptPrefix+"run-done") ||
		!strings.Contains(panel.Footer, "[o] Result") ||
		!strings.Contains(panel.Footer, "[s] Script") {
		t.Fatalf("completed feed should expose artifact shortcuts/footer: shortcuts=%#v footer=%q", panel.Shortcuts, panel.Footer)
	}
	next, ok := moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowFeedPanelID,
		Command: moduTUIWorkflowPanelResultPrefix + "run-done",
	})
	if !ok || next.ID != moduTUIWorkflowResultPanelID {
		t.Fatalf("feed result action should open result panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowFeedPanelID,
		Command: moduTUIWorkflowPanelScriptPrefix + "run-done",
	})
	if !ok || next.ID != moduTUIWorkflowScriptPanelID {
		t.Fatalf("feed script action should open script panel, got ok=%v panel=%#v", ok, next)
	}
}

func TestModuTUIWorkflowPhasePanelShowsOneStage(t *testing.T) {
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-phase",
				"name":         "market_watch",
				"status":       "running",
				"currentPhase": "Research",
				"phases": []map[string]any{{
					"title":        "Scope",
					"agentCount":   1,
					"doneCount":    1,
					"runningCount": 0,
				}, {
					"title":        "Research",
					"agentCount":   2,
					"doneCount":    1,
					"runningCount": 1,
					"durationMs":   42000,
				}},
				"agents": []map[string]any{{
					"id":            1,
					"label":         "scope",
					"phase":         "Scope",
					"status":        "done",
					"resultPreview": "domain ok",
				}, {
					"id":              2,
					"label":           "collect",
					"phase":           "Research",
					"status":          "done",
					"turnTokens":      1200,
					"recentToolCalls": 1,
					"resultPreview":   "market data ok",
				}, {
					"id":            3,
					"label":         "verify",
					"phase":         "Research",
					"status":        "running",
					"promptPreview": "cross-check sources",
				}},
			}},
		},
	}

	panel := moduTUIWorkflowPhasePanelFromStates(states, "run-phase", "Research")
	if panel.ID != moduTUIWorkflowPhasePanelID || panel.Title != "Workflow Phase" {
		t.Fatalf("unexpected phase panel: %#v", panel)
	}
	text := strings.Join(panel.Lines, "\n")
	for _, want := range []string{
		"workflow: market_watch",
		"phase: Research",
		"progress: 1/2 running=1",
		"duration: 42s",
		"position",
		"stage: 2/2",
		"path: Scope -> Research -> finish",
		"previous: Scope",
		"#2 [done] collect",
		"result: market data ok",
		"#3 [running] verify",
		"prompt: cross-check sources",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("phase panel missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "domain ok") {
		t.Fatalf("phase panel should not show agents from other phases:\n%s", text)
	}
	if len(panel.Rows) != 8 ||
		panel.Rows[0].Command != moduTUIWorkflowPanelAgentPrefix+"run-phase:2" ||
		panel.Rows[1].Command != moduTUIWorkflowPanelAgentPrefix+"run-phase:3" ||
		panel.Rows[2].Command != moduTUIWorkflowPanelGuidePrefix+"run-phase" ||
		panel.Rows[3].Command != moduTUIWorkflowPanelFeedPrefix+"run-phase" ||
		panel.Rows[4].Command != moduTUIWorkflowPanelMapPrefix+"run-phase" ||
		panel.Rows[5].Command != moduTUIWorkflowPanelDetailPrefix+"run-phase" ||
		panel.Rows[5].Value != "Research" ||
		panel.Rows[6].Command != moduTUIWorkflowPanelAgentsPrefix+"run-phase" ||
		panel.Rows[7].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("phase panel rows = %#v", panel.Rows)
	}
	if panel.Selected != 1 {
		t.Fatalf("phase panel selected row = %d, want running agent row 1: %#v", panel.Selected, panel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(panel, "?", moduTUIWorkflowPanelGuidePrefix+"run-phase") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "f", moduTUIWorkflowPanelFeedPrefix+"run-phase") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "m", moduTUIWorkflowPanelMapPrefix+"run-phase") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "d", moduTUIWorkflowPanelDetailPrefix+"run-phase") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-phase") {
		t.Fatalf("phase panel shortcuts = %#v", panel.Shortcuts)
	}
	if !strings.Contains(panel.Footer, "[?] Guide") ||
		!strings.Contains(panel.Footer, "[f] Feed") ||
		!strings.Contains(panel.Footer, "[m] Map") ||
		!strings.Contains(panel.Footer, "[d] Detail") ||
		!strings.Contains(panel.Footer, "[a] Agents") {
		t.Fatalf("phase panel footer should expose navigation shortcuts: %q", panel.Footer)
	}

	next, ok := moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowRunDetailPanelID,
		Command: moduTUIWorkflowPanelPhasePrefix + "run-phase:Research",
	})
	if !ok || next.ID != moduTUIWorkflowPhasePanelID {
		t.Fatalf("phase action should open phase panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowPhasePanelID,
		Command: moduTUIWorkflowPanelGuidePrefix + "run-phase",
	})
	if !ok || next.ID != moduTUIWorkflowGuidePanelID {
		t.Fatalf("phase guide action should open guide panel, got ok=%v panel=%#v", ok, next)
	}
}

func TestModuTUIWorkflowRunDetailPanelShowsRunningControls(t *testing.T) {
	panel := moduTUIWorkflowRunDetailPanelFromStates(map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":                "run-running",
				"name":              "market_watch",
				"status":            "running",
				"agentCount":        3,
				"doneCount":         1,
				"runningAgentCount": 2,
			}},
		},
	}, "run-running")

	if len(panel.Rows) < 4 {
		t.Fatalf("detail panel rows = %#v", panel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(panel, "p", moduTUIWorkflowPanelControlPrefix+"pause:run-running") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "x", moduTUIWorkflowPanelControlPrefix+"stop:run-running") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "?", moduTUIWorkflowPanelGuidePrefix+"run-running") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "f", moduTUIWorkflowPanelFeedPrefix+"run-running") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "m", moduTUIWorkflowPanelMapPrefix+"run-running") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-running") {
		t.Fatalf("running detail shortcuts = %#v", panel.Shortcuts)
	}
	if !strings.Contains(panel.Footer, "[p] Pause") || !strings.Contains(panel.Footer, "[x] Stop") ||
		!strings.Contains(panel.Footer, "[?] Guide") ||
		!strings.Contains(panel.Footer, "[f] Feed") || !strings.Contains(panel.Footer, "[m] Map") || !strings.Contains(panel.Footer, "[a] Agents") {
		t.Fatalf("running detail footer should expose shortcuts: %q", panel.Footer)
	}
	if panel.Rows[0].Label != "Execution feed" || panel.Rows[0].Command != moduTUIWorkflowPanelFeedPrefix+"run-running" {
		t.Fatalf("expected first row to open execution feed: %#v", panel.Rows[0])
	}
	if panel.Rows[1].Label != "Pause" || panel.Rows[1].Command != moduTUIWorkflowPanelControlPrefix+"pause:run-running" {
		t.Fatalf("expected second row to pause running workflow: %#v", panel.Rows[1])
	}
	if panel.Rows[2].Label != "Stop" || panel.Rows[2].Command != moduTUIWorkflowPanelControlPrefix+"stop:run-running" {
		t.Fatalf("expected third row to stop running workflow: %#v", panel.Rows[2])
	}
	text := strings.Join(panel.Lines, "\n")
	if !strings.Contains(text, "Pause, Stop") {
		t.Fatalf("detail panel should list running controls:\n%s", text)
	}
	if !strings.Contains(text, "active: no agent snapshot yet (running)") {
		t.Fatalf("detail panel should show empty live snapshot state:\n%s", text)
	}
	if panel.Selected != 5 {
		t.Fatalf("running detail selected row = %d, want Agents row 5 when no phases exist: %#v", panel.Selected, panel.Rows)
	}
}

func TestModuTUIWorkflowControlActionBuildsSlashCommand(t *testing.T) {
	command, runID, status, ok := moduTUIWorkflowControlAction(modutui.PanelAction{
		PanelID: moduTUIWorkflowRunDetailPanelID,
		Command: moduTUIWorkflowPanelControlPrefix + "pause:run-123",
	})
	if !ok {
		t.Fatal("expected workflow control action")
	}
	if command != "/workflows pause run-123" || runID != "run-123" || status != "workflow pause requested" {
		t.Fatalf("unexpected control action: command=%q runID=%q status=%q", command, runID, status)
	}

	if _, _, _, ok := moduTUIWorkflowControlAction(modutui.PanelAction{Command: moduTUIWorkflowPanelControlPrefix + "delete:run-123"}); ok {
		t.Fatal("unsupported control verb should not be accepted")
	}
}

func TestModuTUIWorkflowRunShortcutsFollowStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   []string
	}{
		{name: "running", status: "running", want: []string{"p:workflow-panel:control:pause:run-short", "x:workflow-panel:control:stop:run-short"}},
		{name: "paused", status: "paused", want: []string{"p:workflow-panel:control:resume:run-short", "r:workflow-panel:control:restart:run-short"}},
		{name: "completed", status: "completed", want: []string{"r:workflow-panel:control:restart:run-short"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shortcuts := moduTUIWorkflowRunShortcuts(moduTUIWorkflowRun{ID: "run-short", Status: tt.status})
			got := make([]string, 0, len(shortcuts))
			for _, shortcut := range shortcuts {
				got = append(got, shortcut.Key+":"+shortcut.Command)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("shortcuts = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestModuTUIWorkflowAgentControlActionBuildsSlashCommand(t *testing.T) {
	command, runID, agentID, status, ok := moduTUIWorkflowAgentControlAction(modutui.PanelAction{
		PanelID: moduTUIWorkflowAgentPanelID,
		Command: moduTUIWorkflowPanelAgentControlPrefix + "restart:run-123:7",
	})
	if !ok {
		t.Fatal("expected workflow agent control action")
	}
	if command != "/workflows agent-restart run-123 7" || runID != "run-123" || agentID != 7 || status != "workflow agent restart requested" {
		t.Fatalf("unexpected agent control action: command=%q runID=%q agentID=%d status=%q", command, runID, agentID, status)
	}

	command, runID, agentID, status, ok = moduTUIWorkflowAgentControlAction(modutui.PanelAction{
		Command: moduTUIWorkflowPanelAgentControlPrefix + "stop:run-123:7",
	})
	if !ok || command != "/workflows agent-stop run-123 7" || runID != "run-123" || agentID != 7 || status != "workflow agent stop requested" {
		t.Fatalf("unexpected stop action: command=%q runID=%q agentID=%d status=%q ok=%v", command, runID, agentID, status, ok)
	}

	if _, _, _, _, ok := moduTUIWorkflowAgentControlAction(modutui.PanelAction{Command: moduTUIWorkflowPanelAgentControlPrefix + "pause:run-123:7"}); ok {
		t.Fatal("unsupported agent control verb should not be accepted")
	}
}

func TestModuTUIWorkflowPanelRefFromPanel(t *testing.T) {
	tests := []struct {
		name  string
		panel modutui.Panel
		want  moduTUIWorkflowPanelRef
	}{
		{
			name:  "cockpit",
			panel: modutui.Panel{ID: moduTUIWorkflowCockpitPanelID},
			want:  moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowCockpitPanelID},
		},
		{
			name: "detail",
			panel: modutui.Panel{
				ID: moduTUIWorkflowRunDetailPanelID,
				Rows: []modutui.PanelRow{{
					Command: moduTUIWorkflowPanelAgentsPrefix + "run-1",
				}},
			},
			want: moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowRunDetailPanelID, RunID: "run-1"},
		},
		{
			name: "feed",
			panel: modutui.Panel{
				ID: moduTUIWorkflowFeedPanelID,
				Rows: []modutui.PanelRow{{
					Command: moduTUIWorkflowPanelDetailPrefix + "run-feed",
				}},
			},
			want: moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowFeedPanelID, RunID: "run-feed"},
		},
		{
			name: "map",
			panel: modutui.Panel{
				ID: moduTUIWorkflowMapPanelID,
				Rows: []modutui.PanelRow{{
					Command: moduTUIWorkflowPanelDetailPrefix + "run-map",
				}},
			},
			want: moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowMapPanelID, RunID: "run-map"},
		},
		{
			name: "guide",
			panel: modutui.Panel{
				ID: moduTUIWorkflowGuidePanelID,
				Rows: []modutui.PanelRow{{
					Command: moduTUIWorkflowPanelFeedPrefix + "run-guide",
				}},
			},
			want: moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowGuidePanelID, RunID: "run-guide"},
		},
		{
			name: "agent",
			panel: modutui.Panel{
				ID: moduTUIWorkflowAgentPanelID,
				Rows: []modutui.PanelRow{{
					Command: moduTUIWorkflowPanelAgentControlPrefix + "stop:run-2:7",
				}},
			},
			want: moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowAgentPanelID, RunID: "run-2", AgentID: 7},
		},
		{
			name: "transcript",
			panel: modutui.Panel{
				ID: moduTUIWorkflowTranscriptPanelID,
				Rows: []modutui.PanelRow{{
					Command: moduTUIWorkflowPanelAgentPrefix + "run-3:9",
				}},
			},
			want: moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowTranscriptPanelID, RunID: "run-3", AgentID: 9},
		},
		{
			name: "phase",
			panel: modutui.Panel{
				ID: moduTUIWorkflowPhasePanelID,
				Rows: []modutui.PanelRow{{
					Value:   "Research",
					Command: moduTUIWorkflowPanelDetailPrefix + "run-phase",
				}},
			},
			want: moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowPhasePanelID, RunID: "run-phase", Phase: "Research"},
		},
		{
			name: "result",
			panel: modutui.Panel{
				ID: moduTUIWorkflowResultPanelID,
				Rows: []modutui.PanelRow{{
					Command: moduTUIWorkflowPanelDetailPrefix + "run-4",
				}},
			},
			want: moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowResultPanelID, RunID: "run-4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := moduTUIWorkflowPanelRefFromPanel(tt.panel)
			if !ok {
				t.Fatal("expected workflow panel ref")
			}
			if got != tt.want {
				t.Fatalf("panel ref = %#v, want %#v", got, tt.want)
			}
		})
	}

	if _, ok := moduTUIWorkflowPanelRefFromPanel(modutui.Panel{ID: "config"}); ok {
		t.Fatal("non-workflow panel should not produce a workflow panel ref")
	}
}

func TestModuTUIWorkflowPanelRefMatchesRun(t *testing.T) {
	tests := []struct {
		name  string
		ref   moduTUIWorkflowPanelRef
		runID string
		want  bool
	}{
		{
			name:  "cockpit matches any workflow run update",
			ref:   moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowCockpitPanelID},
			runID: "run-1",
			want:  true,
		},
		{
			name:  "feed matches same run",
			ref:   moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowFeedPanelID, RunID: "run-1"},
			runID: "run-1",
			want:  true,
		},
		{
			name:  "phase matches same run",
			ref:   moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowPhasePanelID, RunID: "run-1", Phase: "Research"},
			runID: "run-1",
			want:  true,
		},
		{
			name:  "agent does not match other run",
			ref:   moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowAgentPanelID, RunID: "run-1", AgentID: 7},
			runID: "run-2",
			want:  false,
		},
		{
			name:  "run-specific panel needs run id",
			ref:   moduTUIWorkflowPanelRef{PanelID: moduTUIWorkflowMapPanelID, RunID: "run-1"},
			runID: "",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.MatchesRun(tt.runID); got != tt.want {
				t.Fatalf("MatchesRun(%q) = %v, want %v", tt.runID, got, tt.want)
			}
		})
	}
}

func TestModuTUIWorkflowAgentsAndAgentPanels(t *testing.T) {
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":     "run-agents",
				"name":   "market_watch",
				"status": "completed",
				"agents": []map[string]any{{
					"id":              1,
					"label":           "collect",
					"phase":           "Research",
					"status":          "done",
					"turnTokens":      1200,
					"recentToolCalls": 1,
					"resultPreview":   "market data ok",
					"promptPreview":   "collect market data",
					"recentToolCallPreviews": []map[string]any{{
						"toolName":      "web_search",
						"argsPreview":   `{"q":"A股"}`,
						"resultPreview": "search result ok",
					}},
				}, {
					"id":            3,
					"label":         "verify",
					"phase":         "Research",
					"status":        "running",
					"promptPreview": "cross-check data",
				}, {
					"id":              2,
					"label":           "write report",
					"phase":           "Report",
					"status":          "done",
					"estimatedTokens": 800,
				}},
			}},
		},
	}

	agentsPanel := moduTUIWorkflowAgentsPanelFromStates(states, "run-agents")
	if agentsPanel.ID != moduTUIWorkflowAgentsPanelID || agentsPanel.Title != "Workflow Agents" {
		t.Fatalf("unexpected agents panel: %#v", agentsPanel)
	}
	agentsText := strings.Join(agentsPanel.Lines, "\n")
	for _, want := range []string{
		"phase lanes",
		"Research: done #1 collect 1 tools | run #3 verify",
		"Report: done #2 write report",
		"agent list",
	} {
		if !strings.Contains(agentsText, want) {
			t.Fatalf("agents panel missing %q:\n%s", want, agentsText)
		}
	}
	if len(agentsPanel.Rows) != 8 {
		t.Fatalf("agents panel rows = %#v", agentsPanel.Rows)
	}
	if agentsPanel.Rows[0].Command != moduTUIWorkflowPanelAgentPrefix+"run-agents:1" || !strings.Contains(agentsPanel.Rows[0].Label, "#1 [done] collect") || !strings.Contains(agentsPanel.Rows[0].Detail, "Research") || !strings.Contains(agentsPanel.Rows[0].Detail, "1200 tokens") || !strings.Contains(agentsPanel.Rows[0].Detail, "1 tools") {
		t.Fatalf("first agent row = %#v", agentsPanel.Rows[0])
	}
	if agentsPanel.Rows[3].Command != moduTUIWorkflowPanelGuidePrefix+"run-agents" ||
		agentsPanel.Rows[4].Command != moduTUIWorkflowPanelFeedPrefix+"run-agents" ||
		agentsPanel.Rows[5].Command != moduTUIWorkflowPanelMapPrefix+"run-agents" ||
		agentsPanel.Rows[6].Command != moduTUIWorkflowPanelDetailPrefix+"run-agents" ||
		agentsPanel.Rows[7].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("agents panel should expose guide/feed/map/detail/cockpit rows: %#v", agentsPanel.Rows)
	}
	if agentsPanel.Selected != 2 {
		t.Fatalf("agents panel selected row = %d, want running agent row 2: %#v", agentsPanel.Selected, agentsPanel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(agentsPanel, "?", moduTUIWorkflowPanelGuidePrefix+"run-agents") ||
		!moduTUIWorkflowPanelHasShortcut(agentsPanel, "f", moduTUIWorkflowPanelFeedPrefix+"run-agents") ||
		!moduTUIWorkflowPanelHasShortcut(agentsPanel, "m", moduTUIWorkflowPanelMapPrefix+"run-agents") ||
		!moduTUIWorkflowPanelHasShortcut(agentsPanel, "d", moduTUIWorkflowPanelDetailPrefix+"run-agents") {
		t.Fatalf("agents panel shortcuts = %#v", agentsPanel.Shortcuts)
	}
	next, ok := moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowAgentsPanelID,
		Command: moduTUIWorkflowPanelGuidePrefix + "run-agents",
	})
	if !ok || next.ID != moduTUIWorkflowGuidePanelID {
		t.Fatalf("agents guide action should open guide panel, got ok=%v panel=%#v", ok, next)
	}

	agentPanel := moduTUIWorkflowAgentPanelFromStates(states, "run-agents", 1)
	if agentPanel.ID != moduTUIWorkflowAgentPanelID || agentPanel.Title != "Workflow Agent" {
		t.Fatalf("unexpected agent panel: %#v", agentPanel)
	}
	text := strings.Join(agentPanel.Lines, "\n")
	for _, want := range []string{
		"id: 1",
		"label: collect",
		"status: done",
		"phase: Research",
		"tokens: 1200",
		"context",
		"stage: 1/2",
		"path: start -> Research -> Report",
		"next: Report",
		"agent: 1/2 in Research",
		"peers: done #1 collect 1 tools | run #3 verify",
		"result preview",
		"market data ok",
		"prompt preview",
		"collect market data",
		"recent tool calls",
		"- web_search [ok]",
		`args: {"q":"A股"}`,
		"result: search result ok",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("agent panel missing %q:\n%s", want, text)
		}
	}
	if len(agentPanel.Rows) != 7 ||
		agentPanel.Rows[0].Command != moduTUIWorkflowPanelTranscriptPrefix+"run-agents:1" ||
		agentPanel.Rows[1].Command != moduTUIWorkflowPanelPhasePrefix+"run-agents:Research" ||
		agentPanel.Rows[2].Command != moduTUIWorkflowPanelGuidePrefix+"run-agents" ||
		agentPanel.Rows[3].Command != moduTUIWorkflowPanelFeedPrefix+"run-agents" ||
		agentPanel.Rows[4].Command != moduTUIWorkflowPanelMapPrefix+"run-agents" ||
		agentPanel.Rows[5].Command != moduTUIWorkflowPanelAgentsPrefix+"run-agents" ||
		agentPanel.Rows[6].Command != moduTUIWorkflowPanelDetailPrefix+"run-agents" {
		t.Fatalf("agent panel back rows = %#v", agentPanel.Rows)
	}
	if agentPanel.Selected != 0 {
		t.Fatalf("agent panel selected row = %d, want transcript row: %#v", agentPanel.Selected, agentPanel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(agentPanel, "?", moduTUIWorkflowPanelGuidePrefix+"run-agents") ||
		!moduTUIWorkflowPanelHasShortcut(agentPanel, "f", moduTUIWorkflowPanelFeedPrefix+"run-agents") ||
		!moduTUIWorkflowPanelHasShortcut(agentPanel, "m", moduTUIWorkflowPanelMapPrefix+"run-agents") ||
		!moduTUIWorkflowPanelHasShortcut(agentPanel, "d", moduTUIWorkflowPanelDetailPrefix+"run-agents") ||
		!moduTUIWorkflowPanelHasShortcut(agentPanel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-agents") {
		t.Fatalf("agent panel shortcuts = %#v", agentPanel.Shortcuts)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowAgentPanelID,
		Command: moduTUIWorkflowPanelGuidePrefix + "run-agents",
	})
	if !ok || next.ID != moduTUIWorkflowGuidePanelID {
		t.Fatalf("agent guide action should open guide panel, got ok=%v panel=%#v", ok, next)
	}
}

func TestModuTUIWorkflowAgentsPanelSelectsRunningAgent(t *testing.T) {
	panel := moduTUIWorkflowAgentsPanelFromStates(map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":     "run-agents-focus",
				"name":   "market_watch",
				"status": "running",
				"agents": []map[string]any{{
					"id":     1,
					"label":  "scope",
					"status": "done",
				}, {
					"id":     2,
					"label":  "verify",
					"status": "running",
				}},
			}},
		},
	}, "run-agents-focus")

	if panel.Selected != 1 {
		t.Fatalf("agents panel selected row = %d, want running agent row 1: %#v", panel.Selected, panel.Rows)
	}
	if panel.Rows[panel.Selected].Value != "2" {
		t.Fatalf("selected agent row = %#v, want agent 2", panel.Rows[panel.Selected])
	}
}

func TestModuTUIWorkflowRunningAgentPanelShowsControlRows(t *testing.T) {
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":     "run-agent-control",
				"name":   "market_watch",
				"status": "running",
				"agents": []map[string]any{{
					"id":            3,
					"label":         "verify",
					"phase":         "Research",
					"status":        "running",
					"promptPreview": "cross-check sources",
				}},
			}},
		},
	}

	panel := moduTUIWorkflowAgentPanelFromStates(states, "run-agent-control", 3)
	if panel.ID != moduTUIWorkflowAgentPanelID {
		t.Fatalf("unexpected agent panel: %#v", panel)
	}
	text := strings.Join(panel.Lines, "\n")
	if !strings.Contains(text, "status: running") || !strings.Contains(text, "cross-check sources") {
		t.Fatalf("running agent panel missing summary:\n%s", text)
	}
	if len(panel.Rows) != 9 {
		t.Fatalf("running agent panel rows = %#v", panel.Rows)
	}
	if panel.Rows[0].Label != "Stop agent" || panel.Rows[0].Command != moduTUIWorkflowPanelAgentControlPrefix+"stop:run-agent-control:3" {
		t.Fatalf("first row should stop agent: %#v", panel.Rows[0])
	}
	if panel.Rows[1].Label != "Restart agent" || panel.Rows[1].Command != moduTUIWorkflowPanelAgentControlPrefix+"restart:run-agent-control:3" {
		t.Fatalf("second row should restart agent: %#v", panel.Rows[1])
	}
	if panel.Rows[2].Command != moduTUIWorkflowPanelTranscriptPrefix+"run-agent-control:3" {
		t.Fatalf("transcript row should follow controls: %#v", panel.Rows)
	}
	if panel.Rows[3].Command != moduTUIWorkflowPanelPhasePrefix+"run-agent-control:Research" ||
		panel.Rows[4].Command != moduTUIWorkflowPanelGuidePrefix+"run-agent-control" ||
		panel.Rows[5].Command != moduTUIWorkflowPanelFeedPrefix+"run-agent-control" ||
		panel.Rows[6].Command != moduTUIWorkflowPanelMapPrefix+"run-agent-control" {
		t.Fatalf("agent panel should expose guide, feed, and map rows: %#v", panel.Rows)
	}
	if panel.Selected != 2 {
		t.Fatalf("running agent selected row = %d, want transcript row after controls: %#v", panel.Selected, panel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(panel, "x", moduTUIWorkflowPanelAgentControlPrefix+"stop:run-agent-control:3") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "r", moduTUIWorkflowPanelAgentControlPrefix+"restart:run-agent-control:3") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "?", moduTUIWorkflowPanelGuidePrefix+"run-agent-control") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "f", moduTUIWorkflowPanelFeedPrefix+"run-agent-control") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "m", moduTUIWorkflowPanelMapPrefix+"run-agent-control") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "d", moduTUIWorkflowPanelDetailPrefix+"run-agent-control") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-agent-control") {
		t.Fatalf("running agent shortcuts = %#v", panel.Shortcuts)
	}
	if !strings.Contains(panel.Footer, "[x] Stop agent") || !strings.Contains(panel.Footer, "[r] Restart agent") ||
		!strings.Contains(panel.Footer, "[?] Guide") || !strings.Contains(panel.Footer, "[f] Feed") ||
		!strings.Contains(panel.Footer, "[m] Map") || !strings.Contains(panel.Footer, "[d] Detail") ||
		!strings.Contains(panel.Footer, "[a] Agents") {
		t.Fatalf("running agent footer should expose shortcuts: %q", panel.Footer)
	}
}

func TestModuTUIWorkflowTranscriptPanelReadsSnapshotTranscript(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(snapshotPath, []byte(`{
	  "agents": [{
	    "id": 1,
	    "label": "collect",
	    "transcript": [{
	      "role": "user",
	      "text": "collect market data"
	    }, {
	      "role": "assistant",
	      "text": "calling search",
	      "toolCalls": [{"id":"call-1","name":"web_search","args":"{\"q\":\"A股\"}"}],
	      "usage": {"input": 10, "output": 20, "totalTokens": 30}
	    }, {
	      "role": "tool",
	      "toolName": "web_search",
	      "text": "search result ok"
	    }]
	  }]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-transcript",
				"name":         "market_watch",
				"status":       "completed",
				"snapshotPath": snapshotPath,
				"agents": []map[string]any{{
					"id":     1,
					"label":  "collect",
					"phase":  "Research",
					"status": "done",
				}},
				"updatedAt": 1,
			}},
		},
	}

	panel := moduTUIWorkflowTranscriptPanelFromStates(states, "run-transcript", 1)
	if panel.ID != moduTUIWorkflowTranscriptPanelID || panel.Title != "Workflow Transcript" {
		t.Fatalf("unexpected transcript panel: %#v", panel)
	}
	text := strings.Join(panel.Lines, "\n")
	for _, want := range []string{
		"## 1. USER",
		"collect market data",
		"## 2. ASSISTANT",
		"calling search",
		"ToolCall: web_search (call-1)",
		`Args: {"q":"A股"}`,
		"Usage: input=10 output=20 total=30",
		"## 3. TOOL web_search",
		"search result ok",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("transcript panel missing %q:\n%s", want, text)
		}
	}
	if len(panel.Rows) != 7 ||
		panel.Rows[0].Command != moduTUIWorkflowPanelAgentPrefix+"run-transcript:1" ||
		panel.Rows[1].Command != moduTUIWorkflowPanelPhasePrefix+"run-transcript:Research" ||
		panel.Rows[2].Command != moduTUIWorkflowPanelGuidePrefix+"run-transcript" ||
		panel.Rows[3].Command != moduTUIWorkflowPanelFeedPrefix+"run-transcript" ||
		panel.Rows[4].Command != moduTUIWorkflowPanelMapPrefix+"run-transcript" ||
		panel.Rows[5].Command != moduTUIWorkflowPanelAgentsPrefix+"run-transcript" ||
		panel.Rows[6].Command != moduTUIWorkflowPanelDetailPrefix+"run-transcript" {
		t.Fatalf("transcript panel rows = %#v", panel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(panel, "?", moduTUIWorkflowPanelGuidePrefix+"run-transcript") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "f", moduTUIWorkflowPanelFeedPrefix+"run-transcript") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "m", moduTUIWorkflowPanelMapPrefix+"run-transcript") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "d", moduTUIWorkflowPanelDetailPrefix+"run-transcript") ||
		!moduTUIWorkflowPanelHasShortcut(panel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-transcript") {
		t.Fatalf("transcript panel shortcuts = %#v", panel.Shortcuts)
	}
	next, ok := moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowTranscriptPanelID,
		Command: moduTUIWorkflowPanelGuidePrefix + "run-transcript",
	})
	if !ok || next.ID != moduTUIWorkflowGuidePanelID {
		t.Fatalf("transcript guide action should open guide panel, got ok=%v panel=%#v", ok, next)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowTranscriptPanelID,
		Command: moduTUIWorkflowPanelPhasePrefix + "run-transcript:Research",
	})
	if !ok || next.ID != moduTUIWorkflowPhasePanelID {
		t.Fatalf("transcript parent phase action should open phase panel, got ok=%v panel=%#v", ok, next)
	}
}

func TestModuTUIWorkflowResultAndScriptPanelsReadArtifacts(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "snapshot.json")
	scriptPath := filepath.Join(dir, "script.js")
	if err := os.WriteFile(snapshotPath, []byte(`{"result":{"report":"full final result","items":[1,2]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("meta({ name: \"market_watch\" })\nreturn \"ok\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-artifact",
				"name":         "market_watch",
				"status":       "completed",
				"scriptPath":   scriptPath,
				"snapshotPath": snapshotPath,
				"agentCount":   2,
				"doneCount":    2,
				"currentPhase": "Report",
				"updatedAt":    1,
				"phases": []map[string]any{{
					"title":      "Research",
					"agentCount": 1,
					"doneCount":  1,
				}, {
					"title":      "Report",
					"agentCount": 1,
					"doneCount":  1,
				}},
			}},
		},
	}

	resultPanel := moduTUIWorkflowResultPanelFromStates(states, "run-artifact")
	if resultPanel.ID != moduTUIWorkflowResultPanelID || resultPanel.Title != "Workflow Result" {
		t.Fatalf("unexpected result panel: %#v", resultPanel)
	}
	resultText := strings.Join(resultPanel.Lines, "\n")
	for _, want := range []string{
		"context",
		"workflow: market_watch",
		"status: completed",
		"progress: 2/2 done, 0 running, 0 errors",
		"current phase: Report",
		"plan: 1 Research -> 2 Report",
		"snapshot: " + snapshotPath,
		`"report": "full final result"`,
		`"items": [`,
	} {
		if !strings.Contains(resultText, want) {
			t.Fatalf("result panel missing %q:\n%s", want, resultText)
		}
	}
	if len(resultPanel.Rows) != 6 ||
		resultPanel.Rows[0].Command != moduTUIWorkflowPanelGuidePrefix+"run-artifact" ||
		resultPanel.Rows[1].Command != moduTUIWorkflowPanelFeedPrefix+"run-artifact" ||
		resultPanel.Rows[2].Command != moduTUIWorkflowPanelMapPrefix+"run-artifact" ||
		resultPanel.Rows[3].Command != moduTUIWorkflowPanelAgentsPrefix+"run-artifact" ||
		resultPanel.Rows[4].Command != moduTUIWorkflowPanelDetailPrefix+"run-artifact" ||
		resultPanel.Rows[5].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("result panel rows = %#v", resultPanel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(resultPanel, "?", moduTUIWorkflowPanelGuidePrefix+"run-artifact") ||
		!moduTUIWorkflowPanelHasShortcut(resultPanel, "f", moduTUIWorkflowPanelFeedPrefix+"run-artifact") ||
		!moduTUIWorkflowPanelHasShortcut(resultPanel, "m", moduTUIWorkflowPanelMapPrefix+"run-artifact") ||
		!moduTUIWorkflowPanelHasShortcut(resultPanel, "d", moduTUIWorkflowPanelDetailPrefix+"run-artifact") ||
		!moduTUIWorkflowPanelHasShortcut(resultPanel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-artifact") {
		t.Fatalf("result panel shortcuts = %#v", resultPanel.Shortcuts)
	}
	next, ok := moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowResultPanelID,
		Command: moduTUIWorkflowPanelGuidePrefix + "run-artifact",
	})
	if !ok || next.ID != moduTUIWorkflowGuidePanelID {
		t.Fatalf("result guide action should open guide panel, got ok=%v panel=%#v", ok, next)
	}

	scriptPanel := moduTUIWorkflowScriptPanelFromStates(states, "run-artifact")
	if scriptPanel.ID != moduTUIWorkflowScriptPanelID || scriptPanel.Title != "Workflow Script" {
		t.Fatalf("unexpected script panel: %#v", scriptPanel)
	}
	scriptText := strings.Join(scriptPanel.Lines, "\n")
	for _, want := range []string{
		"context",
		"workflow: market_watch",
		"plan: 1 Research -> 2 Report",
		"path: " + scriptPath,
		"meta({ name: \"market_watch\" })",
		"return \"ok\"",
	} {
		if !strings.Contains(scriptText, want) {
			t.Fatalf("script panel missing %q:\n%s", want, scriptText)
		}
	}
	if len(scriptPanel.Rows) != 6 ||
		scriptPanel.Rows[0].Command != moduTUIWorkflowPanelGuidePrefix+"run-artifact" ||
		scriptPanel.Rows[1].Command != moduTUIWorkflowPanelFeedPrefix+"run-artifact" ||
		scriptPanel.Rows[2].Command != moduTUIWorkflowPanelMapPrefix+"run-artifact" ||
		scriptPanel.Rows[3].Command != moduTUIWorkflowPanelAgentsPrefix+"run-artifact" ||
		scriptPanel.Rows[4].Command != moduTUIWorkflowPanelDetailPrefix+"run-artifact" ||
		scriptPanel.Rows[5].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("script panel rows = %#v", scriptPanel.Rows)
	}
	if !moduTUIWorkflowPanelHasShortcut(scriptPanel, "?", moduTUIWorkflowPanelGuidePrefix+"run-artifact") ||
		!moduTUIWorkflowPanelHasShortcut(scriptPanel, "f", moduTUIWorkflowPanelFeedPrefix+"run-artifact") ||
		!moduTUIWorkflowPanelHasShortcut(scriptPanel, "m", moduTUIWorkflowPanelMapPrefix+"run-artifact") ||
		!moduTUIWorkflowPanelHasShortcut(scriptPanel, "d", moduTUIWorkflowPanelDetailPrefix+"run-artifact") ||
		!moduTUIWorkflowPanelHasShortcut(scriptPanel, "a", moduTUIWorkflowPanelAgentsPrefix+"run-artifact") {
		t.Fatalf("script panel shortcuts = %#v", scriptPanel.Shortcuts)
	}
	next, ok = moduTUIWorkflowPanelAction(nil, modutui.PanelAction{
		PanelID: moduTUIWorkflowScriptPanelID,
		Command: moduTUIWorkflowPanelGuidePrefix + "run-artifact",
	})
	if !ok || next.ID != moduTUIWorkflowGuidePanelID {
		t.Fatalf("script guide action should open guide panel, got ok=%v panel=%#v", ok, next)
	}
}

func TestModuTUIWorkflowArtifactPanelsCapLongContent(t *testing.T) {
	dir := t.TempDir()
	snapshotPath := filepath.Join(dir, "snapshot.json")
	scriptPath := filepath.Join(dir, "script.js")
	resultLines := make([]string, 0, moduTUIWorkflowArtifactLineLimit+5)
	scriptLines := make([]string, 0, moduTUIWorkflowArtifactLineLimit+5)
	for i := 0; i < moduTUIWorkflowArtifactLineLimit+5; i++ {
		resultLines = append(resultLines, fmt.Sprintf("result line %03d", i+1))
		scriptLines = append(scriptLines, fmt.Sprintf("// script line %03d", i+1))
	}
	resultJSON, err := json.Marshal(map[string]any{"result": strings.Join(resultLines, "\n")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(snapshotPath, resultJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte(strings.Join(scriptLines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-long-artifact",
				"name":         "long_artifact",
				"status":       "completed",
				"scriptPath":   scriptPath,
				"snapshotPath": snapshotPath,
				"updatedAt":    1,
			}},
		},
	}

	resultPanel := moduTUIWorkflowResultPanelFromStates(states, "run-long-artifact")
	resultText := strings.Join(resultPanel.Lines, "\n")
	if !strings.Contains(resultText, "result line 200") || strings.Contains(resultText, "result line 201") || !strings.Contains(resultText, "full artifact: "+snapshotPath) {
		t.Fatalf("result artifact preview should be capped with path:\n%s", resultText)
	}
	scriptPanel := moduTUIWorkflowScriptPanelFromStates(states, "run-long-artifact")
	scriptText := strings.Join(scriptPanel.Lines, "\n")
	if !strings.Contains(scriptText, "// script line 200") || strings.Contains(scriptText, "// script line 201") || !strings.Contains(scriptText, "full artifact: "+scriptPath) {
		t.Fatalf("script artifact preview should be capped with path:\n%s", scriptText)
	}
}

func TestModuTUIWorkflowRunBySelectorUsesLatestByUpdatedAt(t *testing.T) {
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":        "old-run",
				"name":      "old",
				"status":    "completed",
				"updatedAt": 10,
			}, {
				"id":        "new-run",
				"name":      "new",
				"status":    "running",
				"updatedAt": 20,
			}},
		},
	}

	latest, ok := moduTUIWorkflowRunBySelectorFromStates(states, "latest")
	if !ok || latest.ID != "new-run" {
		t.Fatalf("latest run = %#v ok=%v, want new-run", latest, ok)
	}
	exact, ok := moduTUIWorkflowRunBySelectorFromStates(states, "old-run")
	if !ok || exact.ID != "old-run" {
		t.Fatalf("exact run = %#v ok=%v, want old-run", exact, ok)
	}
	if _, ok := moduTUIWorkflowRunBySelectorFromStates(states, "missing"); ok {
		t.Fatal("missing selector should not resolve")
	}
}

func TestModuTUIWorkflowPanelFromSlashRoutesReadOnlySubcommands(t *testing.T) {
	states := map[string]any{
		"workflow": map[string]any{
			"runningCount":   1,
			"completedCount": 1,
			"runs": []map[string]any{{
				"id":         "run-old",
				"name":       "old",
				"status":     "completed",
				"agentCount": 1,
				"doneCount":  1,
				"updatedAt":  10,
				"agents": []map[string]any{{
					"id":     1,
					"label":  "old-agent",
					"status": "done",
				}},
			}, {
				"id":                "run-new",
				"name":              "new",
				"status":            "running",
				"agentCount":        2,
				"doneCount":         1,
				"runningAgentCount": 1,
				"updatedAt":         20,
				"agents": []map[string]any{{
					"id":     1,
					"label":  "collect",
					"phase":  "Research",
					"status": "done",
				}, {
					"id":     2,
					"label":  "verify",
					"phase":  "Research",
					"status": "running",
				}},
			}},
		},
	}

	tests := []struct {
		line string
		id   string
	}{
		{line: "/workflows list", id: moduTUIWorkflowCockpitPanelID},
		{line: "/workflows show latest", id: moduTUIWorkflowRunDetailPanelID},
		{line: "/workflows show run-old", id: moduTUIWorkflowRunDetailPanelID},
		{line: "/workflows feed latest", id: moduTUIWorkflowFeedPanelID},
		{line: "/workflows guide latest", id: moduTUIWorkflowGuidePanelID},
		{line: "/workflows map latest", id: moduTUIWorkflowMapPanelID},
		{line: "/workflows agent latest 2", id: moduTUIWorkflowAgentPanelID},
		{line: "/workflows transcript latest 2", id: moduTUIWorkflowTranscriptPanelID},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			panel, ok := moduTUIWorkflowPanelFromSlashStates(states, tt.line)
			if !ok {
				t.Fatal("expected workflow slash panel")
			}
			if panel.ID != tt.id {
				t.Fatalf("panel ID = %q, want %q: %#v", panel.ID, tt.id, panel)
			}
		})
	}

	panel, ok := moduTUIWorkflowPanelFromSlashStates(states, "/workflows show missing")
	if !ok || panel.ID != moduTUIWorkflowRunDetailPanelID || !strings.Contains(panel.Subtitle, "missing") {
		t.Fatalf("missing show should return missing-run panel, got ok=%v panel=%#v", ok, panel)
	}
	if _, ok := moduTUIWorkflowPanelFromSlashStates(states, "/workflows pause run-new"); ok {
		t.Fatal("control subcommands should fall through to normal slash execution")
	}
	if _, ok := moduTUIWorkflowPanelFromSlashStates(states, "/help"); ok {
		t.Fatal("non-workflow slash should not route to workflow panel")
	}
}

func TestModuTUIWorkflowPanelFromNotifyRoutesLifecycleMessages(t *testing.T) {
	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":         "run-started",
				"name":       "started",
				"status":     "running",
				"agentCount": 2,
				"doneCount":  0,
				"updatedAt":  20,
			}, {
				"id":           "run-old",
				"name":         "old",
				"status":       "completed",
				"agentCount":   1,
				"doneCount":    1,
				"snapshotPath": "/tmp/workflow/run-old/snapshot.json",
				"scriptPath":   "/tmp/workflow/run-old/script.js",
				"updatedAt":    10,
			}},
		},
	}

	panel, status, ok := moduTUIWorkflowPanelFromNotifyStates(states, coding_agent.SessionEvent{
		Type:          coding_agent.SessionEventExtensionNotify,
		ExtensionName: "workflow",
		Message:       "Workflow started in background.\nRun: run-started\nUse /workflows show run-started",
	})
	if !ok || panel.ID != moduTUIWorkflowFeedPanelID || !strings.Contains(panel.Subtitle, "started") || !strings.Contains(status, "started in background") {
		t.Fatalf("started notify = ok=%v status=%q panel=%#v", ok, status, panel)
	}

	panel, status, ok = moduTUIWorkflowPanelFromNotifyStates(states, coding_agent.SessionEvent{
		Type:          coding_agent.SessionEventExtensionNotify,
		ExtensionName: "workflow",
		Message:       "Workflow run run-old restarted in background.\nNew run: run-started",
	})
	if !ok || panel.ID != moduTUIWorkflowFeedPanelID || !strings.Contains(panel.Subtitle, "started") || !strings.Contains(status, "restarted") {
		t.Fatalf("restart notify = ok=%v status=%q panel=%#v", ok, status, panel)
	}

	completionStates := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":           "run-started",
				"name":         "started",
				"status":       "completed",
				"agentCount":   2,
				"doneCount":    2,
				"snapshotPath": "/tmp/workflow/run-started/snapshot.json",
				"scriptPath":   "/tmp/workflow/run-started/script.js",
				"updatedAt":    30,
			}},
		},
	}
	panel, status, ok = moduTUIWorkflowPanelFromNotifyStates(completionStates, coding_agent.SessionEvent{
		Type:          coding_agent.SessionEventExtensionNotify,
		ExtensionName: "workflow",
		Message:       "Workflow started completed with 2 agent(s).\n\n## Execution flow\n...",
	})
	if !ok || panel.ID != moduTUIWorkflowFeedPanelID || !strings.Contains(panel.Subtitle, "started") || !strings.Contains(status, "completed") {
		t.Fatalf("completion notify = ok=%v status=%q panel=%#v", ok, status, panel)
	}
	if panel.Selected < 0 || panel.Selected >= len(panel.Rows) ||
		panel.Rows[panel.Selected].Command != moduTUIWorkflowPanelResultPrefix+"run-started" {
		t.Fatalf("completion notify feed should select result row: selected=%d rows=%#v", panel.Selected, panel.Rows)
	}

	panel, status, ok = moduTUIWorkflowPanelFromNotifyStates(states, coding_agent.SessionEvent{
		Type:          coding_agent.SessionEventExtensionNotify,
		ExtensionName: "workflow",
		Message:       "Stop requested for workflow run run-started",
	})
	if !ok || panel.ID != "" || !strings.Contains(status, "Stop requested") {
		t.Fatalf("control notify = ok=%v status=%q panel=%#v", ok, status, panel)
	}

	if _, _, ok := moduTUIWorkflowPanelFromNotifyStates(states, coding_agent.SessionEvent{
		Type:          coding_agent.SessionEventExtensionNotify,
		ExtensionName: "goal",
		Message:       "Workflow started in background.\nRun: run-started",
	}); ok {
		t.Fatal("non-workflow extension notify should not route to workflow panel")
	}
}

func TestModuTUIWorkflowPanelFromToolEventRoutesWorkflowResults(t *testing.T) {
	type snapshotDetails struct {
		RunDir string `json:"runDir,omitempty"`
	}

	states := map[string]any{
		"workflow": map[string]any{
			"runs": []map[string]any{{
				"id":         "run-tool",
				"name":       "tool_workflow",
				"status":     "running",
				"agentCount": 2,
				"doneCount":  0,
				"updatedAt":  30,
			}, {
				"id":           "run-complete",
				"name":         "complete_workflow",
				"status":       "completed",
				"agentCount":   2,
				"doneCount":    2,
				"snapshotPath": "/tmp/workflow/run-complete/snapshot.json",
				"scriptPath":   "/tmp/workflow/run-complete/script.js",
				"updatedAt":    40,
			}},
		},
	}

	panel, status, ok := moduTUIWorkflowPanelFromToolEventStates(states, types.Event{
		Type:     types.EventTypeToolExecutionStart,
		ToolName: "workflow",
	})
	if !ok || panel.ID != moduTUIWorkflowCockpitPanelID || status != "workflow started" {
		t.Fatalf("workflow tool start event = ok=%v status=%q panel=%#v", ok, status, panel)
	}

	panel, status, ok = moduTUIWorkflowPanelFromToolEventStates(states, types.Event{
		Type:     types.EventTypeToolExecutionEnd,
		ToolName: "workflow",
		Result: types.ToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Workflow started in background.\nRun: run-tool"}},
			Details: map[string]any{
				"runID": "run-tool",
			},
		},
	})
	if !ok || panel.ID != moduTUIWorkflowFeedPanelID || !strings.Contains(panel.Subtitle, "tool_workflow") || status != "workflow started: run-tool" {
		t.Fatalf("workflow tool async event = ok=%v status=%q panel=%#v", ok, status, panel)
	}

	panel, status, ok = moduTUIWorkflowPanelFromToolEventStates(states, types.Event{
		Type:     types.EventTypeToolExecutionUpdate,
		ToolName: "workflow",
		Result: types.ToolResult{
			Details: snapshotDetails{RunDir: "/tmp/workflow/runs/run-tool"},
		},
	})
	if !ok || panel.ID != moduTUIWorkflowFeedPanelID || !strings.Contains(panel.Subtitle, "tool_workflow") || status != "workflow updated: run-tool" {
		t.Fatalf("workflow tool update event = ok=%v status=%q panel=%#v", ok, status, panel)
	}

	panel, status, ok = moduTUIWorkflowPanelFromToolEventStates(states, types.Event{
		Type:     types.EventTypeToolExecutionEnd,
		ToolName: "workflow",
		Result: types.ToolResult{
			Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "Workflow complete_workflow completed with 2 agent(s).\n\n## Execution flow\n...\n\n## Final result\nlarge"}},
		},
	})
	if !ok || panel.ID != moduTUIWorkflowFeedPanelID || !strings.Contains(panel.Subtitle, "complete_workflow") || !strings.Contains(status, "completed") {
		t.Fatalf("workflow tool completion event = ok=%v status=%q panel=%#v", ok, status, panel)
	}
	if panel.Selected < 0 || panel.Selected >= len(panel.Rows) ||
		panel.Rows[panel.Selected].Command != moduTUIWorkflowPanelResultPrefix+"run-complete" {
		t.Fatalf("workflow tool completion feed should select result row: selected=%d rows=%#v", panel.Selected, panel.Rows)
	}
	if runID := moduTUIWorkflowRunIDFromPanel(panel); runID != "run-complete" {
		t.Fatalf("workflow completion panel run id = %q, want run-complete", runID)
	}

	if _, _, ok := moduTUIWorkflowPanelFromToolEventStates(states, types.Event{
		Type:     types.EventTypeToolExecutionEnd,
		ToolName: "bash",
		Result:   types.ToolResult{},
	}); ok {
		t.Fatal("non-workflow tool event should not route to workflow panel")
	}
	if _, _, ok := moduTUIWorkflowPanelFromToolEventStates(states, types.Event{
		Type:     types.EventTypeToolExecutionStart,
		ToolName: "bash",
	}); ok {
		t.Fatal("non-workflow tool start should not route to workflow panel")
	}
}

func TestModuTUIWorkflowToolResultRendersCompactOutput(t *testing.T) {
	output := "Workflow report completed with 2 agent(s).\n\n## Execution flow\nvery long\n\n## Final result\nlarge payload"
	if got := toolDoneSummary("workflow", false, output); got != "Workflow report completed with 2 agent(s)." {
		t.Fatalf("workflow summary = %q", got)
	}
	if got := toolDisplayOutput("workflow", false, output); got != "Opened workflow run panel: latest" {
		t.Fatalf("workflow display output = %q", got)
	}
	started := "Workflow started in background.\nRun: run-123\nUse /workflows show run-123"
	if got := toolDoneSummary("workflow", false, started); got != "Workflow started: run-123" {
		t.Fatalf("workflow started summary = %q", got)
	}
	if got := toolDisplayOutput("workflow", false, started); got != "Opened workflow run panel: run-123" {
		t.Fatalf("workflow started display output = %q", got)
	}
}

func TestRunModuTUISlashExactWorkflowsShowsCockpit(t *testing.T) {
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
	runModuTUISlash(context.Background(), "/workflows", session, session.GetModel(), CommandHooks{}, func(msg tea.Msg) {
		messages = append(messages, msg)
	}, nil, nil, nil)

	var got *modutui.Panel
	for _, msg := range messages {
		if panelMsg, ok := msg.(modutui.SetPanelMsg); ok {
			next := panelMsg.Panel
			got = &next
			break
		}
	}
	if got == nil {
		t.Fatalf("expected SetPanelMsg in %#v", messages)
	}
	if got.Title != "Workflow Cockpit" || !strings.Contains(strings.Join(got.Lines, "\n"), "overview") {
		t.Fatalf("unexpected workflows cockpit output: %#v", got)
	}
	if strings.Contains(strings.Join(got.Lines, "\n"), "unknown command") {
		t.Fatalf("/workflows should not fall through to unknown command: %#v", got)
	}
}

func TestRunModuTUIModelSelectSwitchesModel(t *testing.T) {
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       t.TempDir(),
		AgentDir:  t.TempDir(),
		Model:     providers.GetModel("deepseek", "deepseek-chat"),
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var messages []tea.Msg
	done := make(chan struct{})
	go func() {
		runModuTUIModelSelect(context.Background(), session, func(msg tea.Msg) {
			messages = append(messages, msg)
			if prompt, ok := msg.(modutui.RequestHumanPromptMsg); ok {
				prompt.Respond <- "openai/gpt-4o"
			}
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("model selector did not finish")
	}
	if got := session.GetModel(); got == nil || got.ProviderID != "openai" || got.ID != "gpt-4o" {
		t.Fatalf("expected selected model openai/gpt-4o, got %#v", got)
	}
	var sawPrompt bool
	for _, msg := range messages {
		if prompt, ok := msg.(modutui.RequestHumanPromptMsg); ok {
			sawPrompt = true
			if prompt.Request.Title != "Model" || !strings.Contains(prompt.Request.Body, "Choose active model") {
				t.Fatalf("unexpected model prompt: %#v", prompt.Request)
			}
		}
	}
	if !sawPrompt {
		t.Fatalf("expected model selection prompt in %#v", messages)
	}
}

func TestModuTUIConfigWizardProviderFlow(t *testing.T) {
	var messages []tea.Msg
	var prompts []modutui.HumanPromptRequest
	var saved ConfigProviderInput
	responses := []string{"setup", "deepseek", "api-key"}
	textResponses := []string{"sk-test", "https://api.deepseek.com/v1"}
	wizard := newModuTUIConfigWizard(CommandHooks{
		ConfigProviders: func() ([]ConfigProviderEntry, error) {
			return []ConfigProviderEntry{{
				Name:      "deepseek",
				Type:      "openai-compatible",
				BaseURL:   "https://api.deepseek.com/v1",
				APIKeyEnv: "DEEPSEEK_API_KEY",
			}}, nil
		},
		ConfigSetProvider: func(input ConfigProviderInput) (string, error) {
			saved = input
			return "saved provider: " + input.Provider, nil
		},
	}, func(msg tea.Msg) {
		messages = append(messages, msg)
		if prompt, ok := msg.(modutui.RequestHumanPromptMsg); ok {
			prompts = append(prompts, prompt.Request)
			if len(responses) == 0 {
				prompt.Respond <- ""
				return
			}
			next := responses[0]
			responses = responses[1:]
			prompt.Respond <- next
		}
		if prompt, ok := msg.(modutui.RequestHumanTextMsg); ok {
			if len(textResponses) == 0 {
				t.Fatalf("unexpected text prompt: %#v", prompt.Request)
			}
			next := textResponses[0]
			textResponses = textResponses[1:]
			prompt.Respond <- next
		}
	})

	done := make(chan struct{})
	go func() {
		wizard.Start(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wizard did not finish provider card selection")
	}
	wizard.handleInput(context.Background(), "-")
	wizard.handleInput(context.Background(), "-")

	if len(prompts) < 2 {
		t.Fatalf("expected top and provider prompts, got %#v", prompts)
	}
	if prompts[0].Title != "Config" || len(prompts[0].Options) != 2 || prompts[0].Options[0].Label != "Setup with provider or add model manually" || prompts[0].Options[1].Label != "Show config status" {
		t.Fatalf("unexpected config menu prompt: %#v", prompts[0])
	}
	if prompts[1].Title != "Config: provider" || len(prompts[1].Options) != 4 {
		t.Fatalf("unexpected provider prompt: %#v", prompts[1])
	}
	for i, want := range []struct {
		label string
		value string
	}{
		{label: "DeepSeek", value: "deepseek"},
		{label: "LMStudio", value: "lmstudio"},
		{label: "Ollama", value: "ollama"},
		{label: "Custom OpenAI-Compatible", value: "custom"},
	} {
		if prompts[1].Options[i].Label != want.label || prompts[1].Options[i].Value != want.value {
			t.Fatalf("provider option %d = %#v, want %#v", i, prompts[1].Options[i], want)
		}
	}
	if saved.Provider != "deepseek" || saved.APIKey != "sk-test" || saved.APIKeyEnv != "" || saved.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("unexpected saved provider: %#v", saved)
	}
	text := joinedModuTUIAppendMessages(messages)
	for _, want := range []string{"saved provider: deepseek"} {
		if !strings.Contains(text, want) {
			t.Fatalf("wizard output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "sk-test") {
		t.Fatalf("API key should not be echoed to transcript:\n%s", text)
	}
}

func TestModuTUIConfigWizardHandleInputReportsActive(t *testing.T) {
	done := make(chan struct{})
	wizard := newModuTUIConfigWizard(CommandHooks{}, func(msg tea.Msg) {})
	wizard.mu.Lock()
	wizard.active = true
	wizard.step = "menu"
	wizard.mu.Unlock()

	if !wizard.HandleInput(context.Background(), "q") {
		t.Fatal("expected active wizard to handle input")
	}
	go func() {
		for {
			wizard.mu.Lock()
			active := wizard.active
			wizard.mu.Unlock()
			if !active {
				close(done)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wizard did not handle cancel input")
	}
	if wizard.HandleInput(context.Background(), "hello") {
		t.Fatal("inactive wizard should not handle input")
	}
}

func TestRunModuTUISlashDoesNotResetStatusWhenAgentRunStarted(t *testing.T) {
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
	runModuTUISlash(context.Background(), "/help", session, session.GetModel(), CommandHooks{}, func(msg tea.Msg) {
		messages = append(messages, msg)
	}, func() bool { return true }, nil, nil)

	for _, msg := range messages {
		switch msg := msg.(type) {
		case modutui.SetBusyMsg:
			if !msg.Busy {
				t.Fatalf("slash cleanup should not clear busy while agent run is active: %#v", messages)
			}
		case modutui.SetStatusMsg:
			if msg.Status == "idle" {
				t.Fatalf("slash cleanup should not reset status to idle while agent run is active: %#v", messages)
			}
		}
	}
}

func TestModuTUISlashRunningStatusUsesCommandName(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{line: "/goal fix the failing test", want: "running /goal"},
		{line: "/help", want: "running /help"},
		{line: "", want: "running slash command"},
	}
	for _, tt := range tests {
		if got := moduTUISlashRunningStatus(tt.line); got != tt.want {
			t.Fatalf("moduTUISlashRunningStatus(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}
