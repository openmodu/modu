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
		"system prompt:",
		"memory: empty",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
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
