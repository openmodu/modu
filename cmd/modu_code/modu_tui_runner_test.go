package main

import (
	"context"
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
					"phases": []map[string]any{
						{"title": "Scope", "agentCount": 1, "doneCount": 1},
						{"title": "Research", "agentCount": 2, "doneCount": 1, "runningCount": 1},
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
		"orchestration map",
		"[Scope] 1/1 done",
		"#1 [done] scope",
		"result: DOMAIN finance/markets; angles selected",
		"[Research] 1/2 running=1",
		"#2 [done] primary sources tokens=1200 tools=2",
		"tools: web_search -> market close data",
		"#3 [running] watch tomorrow",
		"prompt: Find tomorrow's catalysts",
		"latest run",
		"/workflows agent latest <agent-id>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("workflow cockpit missing %q:\n%s", want, text)
		}
	}
}

func TestModuTUIWorkflowCockpitRowsOpenWorkflowDetails(t *testing.T) {
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
	if rows[0].Value != "run-2" || rows[0].Command != moduTUIWorkflowPanelDetailPrefix+"run-2" {
		t.Fatalf("first row should open run-2 details: %#v", rows[0])
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
		"orchestration",
		"[Research] 1/1 done",
		"#1 [done] collect",
		"result: market data ok",
		"[Report] 1/1 done",
		"#2 [done] write report",
		"/workflows agent run-detail <agent-id>",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("detail panel missing %q:\n%s", want, text)
		}
	}
	if len(panel.Rows) != 5 || panel.Rows[0].Command != moduTUIWorkflowPanelControlPrefix+"restart:run-detail" || panel.Rows[1].Command != moduTUIWorkflowPanelAgentsPrefix+"run-detail" || panel.Rows[2].Command != moduTUIWorkflowPanelResultPrefix+"run-detail" || panel.Rows[3].Command != moduTUIWorkflowPanelScriptPrefix+"run-detail" || panel.Rows[4].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("detail panel should expose control, agents, result, script, and back rows: %#v", panel.Rows)
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

	if len(panel.Rows) < 3 {
		t.Fatalf("detail panel rows = %#v", panel.Rows)
	}
	if panel.Rows[0].Label != "Pause" || panel.Rows[0].Command != moduTUIWorkflowPanelControlPrefix+"pause:run-running" {
		t.Fatalf("expected first row to pause running workflow: %#v", panel.Rows[0])
	}
	if panel.Rows[1].Label != "Stop" || panel.Rows[1].Command != moduTUIWorkflowPanelControlPrefix+"stop:run-running" {
		t.Fatalf("expected second row to stop running workflow: %#v", panel.Rows[1])
	}
	text := strings.Join(panel.Lines, "\n")
	if !strings.Contains(text, "Pause, Stop") {
		t.Fatalf("detail panel should list running controls:\n%s", text)
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
	if len(agentsPanel.Rows) != 4 {
		t.Fatalf("agents panel rows = %#v", agentsPanel.Rows)
	}
	if agentsPanel.Rows[0].Command != moduTUIWorkflowPanelAgentPrefix+"run-agents:1" || !strings.Contains(agentsPanel.Rows[0].Label, "#1 [done] collect") || !strings.Contains(agentsPanel.Rows[0].Detail, "Research") || !strings.Contains(agentsPanel.Rows[0].Detail, "1200 tokens") || !strings.Contains(agentsPanel.Rows[0].Detail, "1 tools") {
		t.Fatalf("first agent row = %#v", agentsPanel.Rows[0])
	}
	if agentsPanel.Rows[2].Command != moduTUIWorkflowPanelDetailPrefix+"run-agents" || agentsPanel.Rows[3].Command != moduTUIWorkflowPanelBackCommand {
		t.Fatalf("agents panel should expose detail and cockpit back rows: %#v", agentsPanel.Rows)
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
	if len(agentPanel.Rows) != 3 || agentPanel.Rows[0].Command != moduTUIWorkflowPanelTranscriptPrefix+"run-agents:1" || agentPanel.Rows[1].Command != moduTUIWorkflowPanelAgentsPrefix+"run-agents" || agentPanel.Rows[2].Command != moduTUIWorkflowPanelDetailPrefix+"run-agents" {
		t.Fatalf("agent panel back rows = %#v", agentPanel.Rows)
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
	if len(panel.Rows) != 2 || panel.Rows[0].Command != moduTUIWorkflowPanelAgentPrefix+"run-transcript:1" || panel.Rows[1].Command != moduTUIWorkflowPanelAgentsPrefix+"run-transcript" {
		t.Fatalf("transcript panel rows = %#v", panel.Rows)
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
				"updatedAt":    1,
			}},
		},
	}

	resultPanel := moduTUIWorkflowResultPanelFromStates(states, "run-artifact")
	if resultPanel.ID != moduTUIWorkflowResultPanelID || resultPanel.Title != "Workflow Result" {
		t.Fatalf("unexpected result panel: %#v", resultPanel)
	}
	resultText := strings.Join(resultPanel.Lines, "\n")
	for _, want := range []string{`"report": "full final result"`, `"items": [`} {
		if !strings.Contains(resultText, want) {
			t.Fatalf("result panel missing %q:\n%s", want, resultText)
		}
	}
	if len(resultPanel.Rows) != 2 || resultPanel.Rows[0].Command != moduTUIWorkflowPanelDetailPrefix+"run-artifact" {
		t.Fatalf("result panel rows = %#v", resultPanel.Rows)
	}

	scriptPanel := moduTUIWorkflowScriptPanelFromStates(states, "run-artifact")
	if scriptPanel.ID != moduTUIWorkflowScriptPanelID || scriptPanel.Title != "Workflow Script" {
		t.Fatalf("unexpected script panel: %#v", scriptPanel)
	}
	scriptText := strings.Join(scriptPanel.Lines, "\n")
	for _, want := range []string{"meta({ name: \"market_watch\" })", "return \"ok\""} {
		if !strings.Contains(scriptText, want) {
			t.Fatalf("script panel missing %q:\n%s", want, scriptText)
		}
	}
	if len(scriptPanel.Rows) != 2 || scriptPanel.Rows[0].Command != moduTUIWorkflowPanelDetailPrefix+"run-artifact" {
		t.Fatalf("script panel rows = %#v", scriptPanel.Rows)
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
