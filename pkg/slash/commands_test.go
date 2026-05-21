package slash

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	sessionpkg "github.com/openmodu/modu/pkg/coding_agent/session"
	"github.com/openmodu/modu/pkg/providers"
	"github.com/openmodu/modu/pkg/types"
)

type capturePrinter struct {
	lines []string
}

func (p *capturePrinter) PrintInfo(s string) {
	p.lines = append(p.lines, s)
}

func (p *capturePrinter) PrintError(err error) {
	p.lines = append(p.lines, err.Error())
}

func (p *capturePrinter) PrintSection(title string, lines []string) {
	p.lines = append(p.lines, title)
	p.lines = append(p.lines, lines...)
}

func (p *capturePrinter) String() string {
	return strings.Join(p.lines, "\n")
}

func TestHandleContextShowsPromptSources(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".coding_agent")
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("project instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := &types.Model{
		ID:         "mimo-v2.5-pro",
		Name:       "MiMo V2.5 Pro",
		ProviderID: "xiaomi-mimo",
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/context", session, printer, model)

	if !handled || exit {
		t.Fatalf("expected /context to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	for _, want := range []string{
		"Context",
		"model: MiMo V2.5 Pro (xiaomi-mimo / mimo-v2.5-pro)",
		"cwd: " + cwd,
		"context files (1):",
		"AGENTS.md",
		"prompt templates: none",
		"resource packages: none",
		"system prompt:",
		"memory: empty",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestHandlePromptsListsPromptTemplates(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".coding_agent")
	promptDir := filepath.Join(cwd, ".coding_agent", "prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "review.md"), []byte("---\ndescription: review target\n---\nReview {{input}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := &types.Model{
		ID:         "mimo-v2.5-pro",
		Name:       "MiMo V2.5 Pro",
		ProviderID: "xiaomi-mimo",
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/prompts", session, printer, model)

	if !handled || exit {
		t.Fatalf("expected /prompts to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	for _, want := range []string{
		"available prompt templates (1):",
		"/review",
		"review target",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestHandleLeavesTUIOnlyCommandsUnhandled(t *testing.T) {
	cwd := t.TempDir()
	model := &types.Model{ID: "test", Name: "Test", ProviderID: "test"}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  filepath.Join(cwd, ".coding_agent"),
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"/settings", "/scoped-models", "/retry", "/hotkeys"} {
		t.Run(cmd, func(t *testing.T) {
			printer := &capturePrinter{}
			handled, exit := Handle(context.Background(), cmd, session, printer, model)
			if handled || exit {
				t.Fatalf("expected %s to be left to TUI, handled=%v exit=%v output=%q", cmd, handled, exit, printer.String())
			}
		})
	}
}

func TestHandleSessionCommands(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".coding_agent")
	model := &types.Model{
		ID:         "mimo-v2.5-pro",
		Name:       "MiMo V2.5 Pro",
		ProviderID: "xiaomi-mimo",
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/session name demo", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /session name to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	if session.GetSessionName() != "demo" {
		t.Fatalf("expected session name demo, got %q", session.GetSessionName())
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/session", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /session to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	for _, want := range []string{
		"Session",
		"name: demo",
		"cwd: " + cwd,
		"model: MiMo V2.5 Pro (xiaomi-mimo / mimo-v2.5-pro)",
		"messages: 0",
		"tokens: 0",
		"duration:",
		"plan mode: off",
		"worktree: none",
		"context files:",
		"skills:",
		"prompt templates:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected session output to contain %q, got:\n%s", want, output)
		}
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/sessions", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /sessions to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	if output := printer.String(); !strings.Contains(output, "Sessions (1)") || !strings.Contains(output, "demo") {
		t.Fatalf("expected sessions output with demo, got:\n%s", output)
	}

	other, err := sessionpkg.NewManager(agentDir, filepath.Join(cwd, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Append(sessionpkg.NewEntry(sessionpkg.EntryTypeMessage, "", sessionpkg.MessageData{Role: "user", Content: "other"})); err != nil {
		t.Fatal(err)
	}
	otherPath := other.FilePath()
	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/sessions delete "+otherPath, session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /sessions delete to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	if _, err := os.Stat(otherPath); !os.IsNotExist(err) {
		t.Fatalf("expected other session deleted, stat err=%v output=%s", err, printer.String())
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/session delete "+session.GetSessionFile(), session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /session delete active to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	if output := printer.String(); !strings.Contains(output, "refusing to delete the active session") {
		t.Fatalf("expected active delete refusal, got:\n%s", output)
	}
}

func TestHandleWorktreeStatusShowsLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	cwd := t.TempDir()
	initSlashGitRepo(t, cwd)
	agentDir := filepath.Join(cwd, ".coding_agent")
	model := &types.Model{
		ID:         "mimo-v2.5-pro",
		Name:       "MiMo V2.5 Pro",
		ProviderID: "xiaomi-mimo",
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/worktree status", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /worktree status handled without exit, handled=%v exit=%v", handled, exit)
	}
	if output := printer.String(); !strings.Contains(output, "Worktree") || !strings.Contains(output, "active: no") || !strings.Contains(output, "cwd: "+cwd) {
		t.Fatalf("expected inactive worktree status, got:\n%s", output)
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/worktree on", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /worktree on handled without exit, handled=%v exit=%v", handled, exit)
	}
	active := session.WorktreeStatus()
	if !active.Active {
		t.Fatal("expected active worktree")
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/worktree status", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected active /worktree status handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	for _, want := range []string{
		"Worktree",
		"active: yes",
		"path: " + active.Path,
		"cwd: " + active.Cwd,
		"original cwd: " + cwd,
		"exists: yes",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected active worktree status to contain %q, got:\n%s", want, output)
		}
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/worktree list", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /worktree list handled without exit, handled=%v exit=%v", handled, exit)
	}
	output = printer.String()
	for _, want := range []string{
		"Worktrees",
		"active exists=yes " + active.Path,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected worktree list to contain %q, got:\n%s", want, output)
		}
	}

	stalePath := filepath.Join(agentDir, "worktrees", "wt-stale")
	if err := os.MkdirAll(stalePath, 0o755); err != nil {
		t.Fatal(err)
	}
	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/worktree cleanup", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /worktree cleanup handled without exit, handled=%v exit=%v", handled, exit)
	}
	output = printer.String()
	if !strings.Contains(output, "Worktree cleanup") || !strings.Contains(output, "removed "+stalePath) {
		t.Fatalf("expected worktree cleanup output, got:\n%s", output)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("expected stale worktree removed, stat err=%v", err)
	}
	if _, err := os.Stat(active.Path); err != nil {
		t.Fatalf("expected active worktree kept: %v", err)
	}

	if err := os.WriteFile(filepath.Join(active.Path, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/worktree diff", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /worktree diff handled without exit, handled=%v exit=%v", handled, exit)
	}
	output = printer.String()
	for _, want := range []string{
		"Worktree diff",
		"README.md",
		"changed",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected worktree diff to contain %q, got:\n%s", want, output)
		}
	}
}

func TestHandlePlanStatusShowsArtifactAndTodos(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".coding_agent")
	model := &types.Model{
		ID:         "mimo-v2.5-pro",
		Name:       "MiMo V2.5 Pro",
		ProviderID: "xiaomi-mimo",
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session.EnterPlanMode()
	session.ExitPlanMode("approved slash plan", []string{"first", "second"})
	status := session.PlanStatus()

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/plan status", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /plan status handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	for _, want := range []string{
		"Plan",
		"active: no",
		"latest plan: " + status.PlanFile,
		"latest plan exists: yes",
		"revisions: 1",
		"todos: total=2 pending=2 in_progress=0 completed=0",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected plan status to contain %q, got:\n%s", want, output)
		}
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/plan show", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /plan show handled without exit, handled=%v exit=%v", handled, exit)
	}
	output = printer.String()
	if !strings.Contains(output, "Plan") || !strings.Contains(output, "approved slash plan") {
		t.Fatalf("expected plan show output with plan content, got:\n%s", output)
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/plan history", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /plan history handled without exit, handled=%v exit=%v", handled, exit)
	}
	output = printer.String()
	if !strings.Contains(output, "Plan history") || !strings.Contains(output, "revision-") {
		t.Fatalf("expected plan history output with revision, got:\n%s", output)
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/plan clear", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /plan clear handled without exit, handled=%v exit=%v", handled, exit)
	}
	if output := printer.String(); !strings.Contains(output, "cleared latest plan and todos") {
		t.Fatalf("expected plan clear output, got:\n%s", output)
	}
	if status := session.PlanStatus(); status.PlanExists || status.TodoTotal != 0 {
		t.Fatalf("expected cleared plan status, got %#v", status)
	}
}

func TestHandleTreeAndForkCommands(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".coding_agent")
	model := &types.Model{
		ID:         "mimo-v2.5-pro",
		Name:       "MiMo V2.5 Pro",
		ProviderID: "xiaomi-mimo",
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
		StreamFn: func(ctx context.Context, _ *types.Model, _ *types.LLMContext, _ *types.SimpleStreamOptions) (types.EventStream, error) {
			stream := types.NewEventStream()
			go func() {
				defer stream.Close()
				msg := &types.AssistantMessage{
					Role:       "assistant",
					ProviderID: model.ProviderID,
					Model:      model.ID,
					StopReason: "stop",
					Content:    []types.ContentBlock{&types.TextContent{Type: "text", Text: "ok"}},
					Timestamp:  time.Now().UnixMilli(),
				}
				stream.Push(types.StreamEvent{Type: "done", Reason: "stop", Message: msg})
				stream.Resolve(msg, nil)
			}()
			return stream, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Prompt(context.Background(), "first forkable message"); err != nil {
		t.Fatal(err)
	}

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/tree", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /tree to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	if !strings.Contains(output, "Forkable Messages") || !strings.Contains(output, "first forkable message") {
		t.Fatalf("expected forkable messages output, got:\n%s", output)
	}

	msgs := session.GetForkMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected one fork message, got %#v", msgs)
	}
	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/fork "+msgs[0].EntryID, session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /fork to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	if output := printer.String(); !strings.Contains(output, "forked from entry") {
		t.Fatalf("expected fork output, got:\n%s", output)
	}
}

func TestHandleExportCommand(t *testing.T) {
	cwd := t.TempDir()
	model := &types.Model{ID: "test", Name: "Test", ProviderID: "test"}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  filepath.Join(cwd, ".coding_agent"),
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "export this"})

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/export exports/session.html", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /export to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	outPath := filepath.Join(cwd, "exports", "session.html")
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("expected export file: %v", err)
	}
	if !strings.Contains(string(data), "export this") {
		t.Fatalf("expected exported message, got %s", string(data))
	}
	if !strings.Contains(printer.String(), outPath) {
		t.Fatalf("expected output path, got %s", printer.String())
	}
}

func TestHandleCopyCommand(t *testing.T) {
	cwd := t.TempDir()
	model := &types.Model{ID: "test", Name: "Test", ProviderID: "test"}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  filepath.Join(cwd, ".coding_agent"),
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session.GetAgent().AppendMessage(types.AssistantMessage{
		Role:    "assistant",
		Content: []types.ContentBlock{&types.TextContent{Type: "text", Text: "copy me"}},
	})

	oldCopy := copyTextToClipboard
	defer func() { copyTextToClipboard = oldCopy }()
	var copied string
	copyTextToClipboard = func(text string) error {
		copied = text
		return nil
	}

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/copy", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /copy to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	if copied != "copy me" {
		t.Fatalf("expected copied assistant text, got %q", copied)
	}
	if !strings.Contains(printer.String(), "copied last assistant message") {
		t.Fatalf("expected copy confirmation, got %s", printer.String())
	}
}

func TestHandleChangelogCommand(t *testing.T) {
	cwd := t.TempDir()
	runGit(t, cwd, "init")
	runGit(t, cwd, "config", "user.email", "test@example.com")
	runGit(t, cwd, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(cwd, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, cwd, "add", "README.md")
	runGit(t, cwd, "commit", "-m", "initial changelog entry")

	model := &types.Model{ID: "test", Name: "Test", ProviderID: "test"}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  filepath.Join(cwd, ".coding_agent"),
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/changelog", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /changelog to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	if output := printer.String(); !strings.Contains(output, "Changelog") || !strings.Contains(output, "initial changelog entry") {
		t.Fatalf("expected changelog output, got:\n%s", output)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, string(out))
	}
}

func TestHandleDoctorShowsDiagnostics(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".coding_agent")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	model := &types.Model{
		ID:         "mimo-v2.5-pro",
		Name:       "MiMo V2.5 Pro",
		ProviderID: "test-unregistered-doctor",
		BaseURL:    server.URL + "/v1",
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		Model:           model,
		ModelConfigPath: filepath.Join(agentDir, "config.json"),
		GetAPIKey:       func(string) (string, error) { return "secret", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/doctor", session, printer, model)

	if !handled || exit {
		t.Fatalf("expected /doctor to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	for _, want := range []string{
		"Doctor",
		"model config: " + filepath.Join(agentDir, "config.json"),
		"model: MiMo V2.5 Pro (test-unregistered-doctor / mimo-v2.5-pro)",
		"baseUrl: " + server.URL + "/v1",
		"baseUrl status: reachable (HTTP 404)",
		"provider registered: no",
		"api key: set",
		"problems (1):",
		"provider is not registered: test-unregistered-doctor",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestHandleDoctorReportsUnreachableBaseURL(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".coding_agent")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	baseURL := server.URL
	server.Close()
	model := &types.Model{
		ID:         "mimo-v2.5-pro",
		Name:       "MiMo V2.5 Pro",
		ProviderID: "test-unreachable-doctor",
		BaseURL:    baseURL,
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:       cwd,
		AgentDir:  agentDir,
		Model:     model,
		GetAPIKey: func(string) (string, error) { return "secret", nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/doctor", session, printer, model)

	if !handled || exit {
		t.Fatalf("expected /doctor to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	for _, want := range []string{
		"baseUrl: " + baseURL,
		"baseUrl status:",
		"baseUrl not reachable:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestHandleModelSwitchReportsClearedContext(t *testing.T) {
	cwd := t.TempDir()
	agentDir := filepath.Join(cwd, ".coding_agent")
	providers.Models["slash-model-feedback"] = map[string]*types.Model{
		"model-a": {ID: "model-a", Name: "Slash Model A", ProviderID: "slash-model-feedback"},
		"model-b": {ID: "model-b", Name: "Slash Model B", ProviderID: "slash-model-feedback"},
	}
	session, err := coding_agent.NewCodingSession(coding_agent.CodingSessionOptions{
		Cwd:          cwd,
		AgentDir:     agentDir,
		Model:        providers.Models["slash-model-feedback"]["model-a"],
		ScopedModels: []string{"model-a", "model-b"},
		GetAPIKey:    func(string) (string, error) { return "", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session.GetAgent().AppendMessage(types.UserMessage{Role: "user", Content: "old context"})

	printer := &capturePrinter{}
	handled, exit := Handle(context.Background(), "/model Slash Model B", session, printer, session.GetModel())

	if !handled || exit {
		t.Fatalf("expected /model to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	output := printer.String()
	for _, want := range []string{
		"switched model: Slash Model B (slash-model-feedback / model-b)",
		"active entry: Slash Model B",
		"conversation context cleared",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
	if got := len(session.GetMessages()); got != 0 {
		t.Fatalf("expected model switch to clear messages, got %d", got)
	}
}

func initSlashGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
}
