package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coding_agent "github.com/openmodu/modu/pkg/coding_agent"
	sessionpkg "github.com/openmodu/modu/pkg/coding_agent/services/session"
	"github.com/openmodu/modu/pkg/slash"
	"github.com/openmodu/modu/pkg/tui"
	"github.com/openmodu/modu/pkg/types"
)

func TestRunConfigCommandExample(t *testing.T) {
	var out bytes.Buffer
	if err := runConfigCommand([]string{"example"}, &out, nil); err != nil {
		t.Fatalf("runConfigCommand example: %v", err)
	}
	if !strings.Contains(out.String(), `"models"`) || !strings.Contains(out.String(), `"description": "local coding model"`) {
		t.Fatalf("unexpected example output:\n%s", out.String())
	}
}

func TestRunConfigCommandShowWhenMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out bytes.Buffer
	if err := runConfigCommand(nil, &out, nil); err != nil {
		t.Fatalf("runConfigCommand show: %v", err)
	}
	got := out.String()
	for _, want := range []string{"status: missing", "TUI:", "/config"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in output:\n%s", want, got)
		}
	}
}

func TestRunConfigCommandInitAndValidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var initOut bytes.Buffer
	if err := runConfigCommand([]string{"init"}, &initOut, nil); err != nil {
		t.Fatalf("runConfigCommand init: %v", err)
	}
	path := filepath.Join(home, ".coding_agent", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config file: %v", err)
	}
	if !strings.Contains(initOut.String(), path) {
		t.Fatalf("expected init output to include path, got %q", initOut.String())
	}

	var validateOut bytes.Buffer
	if err := runConfigCommand([]string{"validate"}, &validateOut, nil); err != nil {
		t.Fatalf("runConfigCommand validate: %v\n%s", err, validateOut.String())
	}
	if !strings.Contains(validateOut.String(), "status: ok") {
		t.Fatalf("expected validate ok, got:\n%s", validateOut.String())
	}
}

func TestRunConfigCommandAddListUseAndRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var addOut bytes.Buffer
	err := runConfigCommand([]string{
		"add", "local-qwen", "lmstudio", "qwen", "127.0.0.1:1234/v1", "local-key",
		"--description", "local", "coding", "model",
	}, &addOut, nil)
	if err != nil {
		t.Fatalf("runConfigCommand add: %v\n%s", err, addOut.String())
	}
	if !strings.Contains(addOut.String(), "added model: local-qwen") {
		t.Fatalf("unexpected add output:\n%s", addOut.String())
	}

	var listOut bytes.Buffer
	if err := runConfigCommand([]string{"list"}, &listOut, nil); err != nil {
		t.Fatalf("runConfigCommand list: %v", err)
	}
	if got := listOut.String(); !strings.Contains(got, "* local-qwen") || !strings.Contains(got, "local coding model") {
		t.Fatalf("unexpected list output:\n%s", got)
	}

	var useOut bytes.Buffer
	if err := runConfigCommand([]string{"use", "lmstudio/qwen"}, &useOut, nil); err != nil {
		t.Fatalf("runConfigCommand use: %v", err)
	}
	if !strings.Contains(useOut.String(), "active: local-qwen") {
		t.Fatalf("unexpected use output:\n%s", useOut.String())
	}

	var removeOut bytes.Buffer
	if err := runConfigCommand([]string{"remove", "local-qwen"}, &removeOut, nil); err != nil {
		t.Fatalf("runConfigCommand remove: %v", err)
	}
	if !strings.Contains(removeOut.String(), "removed model: local-qwen") {
		t.Fatalf("unexpected remove output:\n%s", removeOut.String())
	}
}

func TestRunConfigCommandAddWritesV2ProviderSchema(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var out bytes.Buffer
	if err := runConfigCommand([]string{
		"add", "local-qwen", "lmstudio", "qwen", "127.0.0.1:1234/v1", "local-key",
	}, &out, nil); err != nil {
		t.Fatalf("runConfigCommand add: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".coding_agent", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"version": 2`, `"providers"`, `"baseUrl": "127.0.0.1:1234/v1"`, `"apiKey": "local-key"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in config:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"models": [
    {
      "name": "local-qwen",
      "provider": "lmstudio",
      "model": "qwen",
      "baseUrl"`) {
		t.Fatalf("expected model entry not to own baseUrl:\n%s", got)
	}
}

func TestConfigProviderEntriesIncludePresets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	entries, err := configProviderEntries()
	if err != nil {
		t.Fatalf("configProviderEntries: %v", err)
	}
	names := map[string]bool{}
	for _, entry := range entries {
		names[entry.Name] = true
	}
	for _, want := range []string{"openai", "deepseek", "lmstudio", "ollama"} {
		if !names[want] {
			t.Fatalf("expected provider preset %q in %#v", want, entries)
		}
	}
}

func TestSplitConfigArgsSupportsQuotedDescription(t *testing.T) {
	got, err := splitConfigArgs(`add local lmstudio qwen http://127.0.0.1:1234/v1 --description "local coding model"`)
	if err != nil {
		t.Fatalf("splitConfigArgs: %v", err)
	}
	want := []string{"add", "local", "lmstudio", "qwen", "http://127.0.0.1:1234/v1", "--description", "local coding model"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("split args = %#v, want %#v", got, want)
	}
}

func TestRunConfigCommandValidateFailsInvalidConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".coding_agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"models":[{"provider":"","model":"","baseUrl":""}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runConfigCommand([]string{"validate"}, &out, nil)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(out.String(), "problems") {
		t.Fatalf("expected problems output, got:\n%s", out.String())
	}
}

func TestMainTUISessionFlows(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	var initOut bytes.Buffer
	if err := runConfigCommand([]string{"init"}, &initOut, nil); err != nil {
		t.Fatalf("runConfigCommand init: %v", err)
	}

	oldArgs := os.Args
	oldCommandLine := flag.CommandLine
	oldRunTUI := runTUI
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
		runTUI = oldRunTUI
		if err := os.Chdir(oldwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}

	called := false
	runTUI = func(ctx context.Context, session *coding_agent.CodingSession, model *types.Model, noApprove bool, opts tui.RunOptions) error {
		called = true
		if noApprove {
			t.Fatal("expected noApprove false")
		}
		if opts.CommandHooks.Config == nil {
			t.Fatal("expected config hook")
		}
		if out, err := opts.CommandHooks.Config("validate"); err != nil || !strings.Contains(out, "status: ok") {
			t.Fatalf("expected config hook validate ok, out=%q err=%v", out, err)
		}
		session.SetSessionName("active")
		agentDir := filepath.Dir(filepath.Dir(filepath.Dir(session.GetSessionFile())))
		sourceFile := writeMainTestSessionFile(t, agentDir, filepath.Join(project, "source"), "source session", "resume from main tui")
		deleteFile := writeMainTestSessionFile(t, agentDir, filepath.Join(project, "delete"), "delete session", "delete from main tui")

		printer := &mainCapturePrinter{}
		runSlash := func(line string) {
			t.Helper()
			handled, exit := slash.Handle(ctx, line, session, printer, model)
			if !handled || exit {
				t.Fatalf("slash %q handled=%v exit=%v output=%s", line, handled, exit, printer.String())
			}
		}

		runSlash("/sessions all")
		if output := printer.String(); !strings.Contains(output, sourceFile) || !strings.Contains(output, "Sessions") {
			t.Fatalf("expected /sessions all to include source file, got:\n%s", output)
		}

		runSlash("/fork-session " + sourceFile)
		forkedFile := session.GetSessionFile()
		if forkedFile == sourceFile {
			t.Fatalf("expected fork to switch to a new file, got source %q", sourceFile)
		}
		if _, err := os.Stat(forkedFile); err != nil {
			t.Fatalf("expected forked file to exist: %v", err)
		}

		runSlash("/resume " + sourceFile)
		if got := session.GetSessionFile(); got != sourceFile {
			t.Fatalf("expected resumed source file, got %q want %q", got, sourceFile)
		}
		if got := session.GetMessages(); len(got) != 1 {
			t.Fatalf("expected resumed messages, got %#v", got)
		}

		runSlash("/tree")
		forkMessages := session.GetForkMessages()
		if len(forkMessages) != 1 {
			t.Fatalf("expected one forkable message, got %#v", forkMessages)
		}
		runSlash("/fork " + forkMessages[0].EntryID)

		runSlash("/session delete " + deleteFile)
		if _, err := os.Stat(deleteFile); !os.IsNotExist(err) {
			t.Fatalf("expected delete file removed, stat err=%v", err)
		}
		return nil
	}

	os.Args = []string{"modu_code"}
	flag.CommandLine = flag.NewFlagSet("modu_code", flag.ContinueOnError)
	main()
	if !called {
		t.Fatal("expected TUI runner to be called")
	}
}

type mainCapturePrinter struct {
	lines []string
}

func (p *mainCapturePrinter) PrintInfo(s string) {
	p.lines = append(p.lines, s)
}

func (p *mainCapturePrinter) PrintError(err error) {
	p.lines = append(p.lines, err.Error())
}

func (p *mainCapturePrinter) PrintSection(title string, lines []string) {
	p.lines = append(p.lines, title)
	p.lines = append(p.lines, lines...)
}

func (p *mainCapturePrinter) String() string {
	return strings.Join(p.lines, "\n")
}

func writeMainTestSessionFile(t *testing.T, agentDir, cwd, name, prompt string) string {
	t.Helper()
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	mgr, err := sessionpkg.NewManager(agentDir, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Append(sessionpkg.NewEntry(sessionpkg.EntryTypeMessage, "", sessionpkg.MessageData{
		Role:    "user",
		Content: prompt,
	})); err != nil {
		t.Fatal(err)
	}
	if err := mgr.AppendSessionInfo(name); err != nil {
		t.Fatal(err)
	}
	return mgr.FilePath()
}
