package slash

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
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
	if output := printer.String(); !strings.Contains(output, "Session") || !strings.Contains(output, "name: demo") {
		t.Fatalf("expected session output with name, got:\n%s", output)
	}

	printer = &capturePrinter{}
	handled, exit = Handle(context.Background(), "/sessions", session, printer, model)
	if !handled || exit {
		t.Fatalf("expected /sessions to be handled without exit, handled=%v exit=%v", handled, exit)
	}
	if output := printer.String(); !strings.Contains(output, "Sessions (1)") || !strings.Contains(output, "demo") {
		t.Fatalf("expected sessions output with demo, got:\n%s", output)
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
