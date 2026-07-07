package cron

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/types"
)

// fakeAPI implements just enough of extension.ExtensionAPI to capture tool
// registrations.
type fakeAPI struct {
	tools      []types.Tool
	commands   map[string]extension.CommandHandler
	notifies   []string
	sent       []string
	sentOption []extension.MessageOptions
}

func (f *fakeAPI) RegisterTool(t types.Tool) { f.tools = append(f.tools, t) }
func (f *fakeAPI) RegisterCommand(name string, _ string, h extension.CommandHandler) {
	if f.commands == nil {
		f.commands = make(map[string]extension.CommandHandler)
	}
	f.commands[name] = h
}
func (f *fakeAPI) AddHook(extension.ToolHook)        {}
func (f *fakeAPI) On(string, extension.EventHandler) {}
func (f *fakeAPI) SendMessage(text string) error {
	return f.SendMessageWithOptions(text, extension.MessageOptions{})
}
func (f *fakeAPI) SetActiveTools([]string)          {}
func (f *fakeAPI) SetModel(string, string) error    { return nil }
func (f *fakeAPI) GetCommands() []extension.Command { return nil }
func (f *fakeAPI) SessionID() string                { return "s" }
func (f *fakeAPI) SessionDir() string               { return "" }
func (f *fakeAPI) AgentDir() string                 { return "" }
func (f *fakeAPI) Cwd() string                      { return "" }
func (f *fakeAPI) IsIdle() bool                     { return true }
func (f *fakeAPI) HasPendingMessages() bool         { return false }
func (f *fakeAPI) PermissionMode() string           { return "" }
func (f *fakeAPI) SendFollowUpMessage(text string) error {
	return f.SendMessageWithOptions(text, extension.MessageOptions{DeliverAs: "followUp"})
}
func (f *fakeAPI) SendMessageWithOptions(text string, opts extension.MessageOptions) error {
	f.sent = append(f.sent, text)
	f.sentOption = append(f.sentOption, opts)
	return nil
}
func (f *fakeAPI) Notify(_ string, text string) {
	f.notifies = append(f.notifies, text)
}
func (f *fakeAPI) Confirm(_, _ string, defaultYes bool) bool { return defaultYes }
func (f *fakeAPI) Select(_ string, options []string) string {
	if len(options) > 0 {
		return options[0]
	}
	return ""
}
func (f *fakeAPI) BackgroundTasks() []extension.TaskSnapshot { return nil }
func (f *fakeAPI) InterruptBackgroundTask(string, string) (extension.TaskSnapshot, bool) {
	return extension.TaskSnapshot{}, false
}
func (f *fakeAPI) AddPending(int) {}
func (f *fakeAPI) DonePending()   {}
func (f *fakeAPI) ForkSession(context.Context, extension.ForkOptions) (string, error) {
	return "", nil
}

func TestInitRegistersCronTools(t *testing.T) {
	e := New()
	api := &fakeAPI{}
	if err := e.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	want := map[string]bool{"cron_add": false, "cron_list": false, "cron_remove": false, "cron_update": false}
	for _, tool := range api.tools {
		if _, ok := want[tool.Name()]; ok {
			want[tool.Name()] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %s not registered", name)
		}
	}
	if len(api.tools) != len(want) {
		t.Errorf("registered %d tools, want %d", len(api.tools), len(want))
	}
	if api.commands["cron"] == nil {
		t.Fatal("cron slash command not registered")
	}
}

func TestApplyConfig(t *testing.T) {
	e := New()
	if err := e.ApplyConfig(map[string]any{"config_path": " /tmp/x.yaml "}); err != nil {
		t.Fatalf("ApplyConfig: %v", err)
	}
	if e.cfgPath != "/tmp/x.yaml" {
		t.Fatalf("cfgPath=%q", e.cfgPath)
	}
	if err := e.ApplyConfig(map[string]any{"config_path": 5}); err == nil {
		t.Error("non-string config_path should error")
	}
	if err := e.ApplyConfig(map[string]any{"typo": true}); err == nil {
		t.Error("unknown key should error")
	}
}

func TestCronSlashListAndRemoveUseDirectTools(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	task := config.Task{UUID: "11111111-1111-1111-1111-111111111111", Name: "daily", Cron: "@every 1m", Prompt: "p", Enabled: true}
	if err := config.SaveTasks(config.DefaultTasksPath(cfgPath), []config.Task{task}); err != nil {
		t.Fatalf("SaveTasks: %v", err)
	}
	e := &Extension{cfgPath: cfgPath}
	api := &fakeAPI{}
	if err := e.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := api.commands["cron"]("list"); err != nil {
		t.Fatalf("cron list: %v", err)
	}
	if got := strings.Join(api.notifies, "\n"); !strings.Contains(got, task.UUID) || !strings.Contains(got, "\"daily\"") {
		t.Fatalf("list notify missing task:\n%s", got)
	}
	if len(api.sent) != 0 {
		t.Fatalf("list should not send natural-language prompt: %#v", api.sent)
	}
	if err := api.commands["cron"]("rm " + task.UUID); err != nil {
		t.Fatalf("cron rm: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Tasks) != 0 {
		t.Fatalf("rm should delete task by uuid: %+v", cfg.Tasks)
	}
}

func TestCronSlashAddAndUpdateSendNaturalLanguage(t *testing.T) {
	e := &Extension{cfgPath: filepath.Join(t.TempDir(), "config.yaml")}
	api := &fakeAPI{}
	if err := e.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := api.commands["cron"]("add every morning summarize inbox"); err != nil {
		t.Fatalf("cron add: %v", err)
	}
	if err := api.commands["cron"]("update 11111111-1111-1111-1111-111111111111 to run at 9am"); err != nil {
		t.Fatalf("cron update: %v", err)
	}
	if len(api.sent) != 2 {
		t.Fatalf("expected two natural-language prompts, got %#v", api.sent)
	}
	if !strings.Contains(api.sent[0], "cron add") || !strings.Contains(api.sent[0], "every morning") {
		t.Fatalf("bad add prompt: %q", api.sent[0])
	}
	if !strings.Contains(api.sent[1], "cron update") || !strings.Contains(api.sent[1], "9am") {
		t.Fatalf("bad update prompt: %q", api.sent[1])
	}
	for _, opts := range api.sentOption {
		if opts.CustomType != "cron_slash" || !opts.Display {
			t.Fatalf("bad message options: %+v", opts)
		}
	}
}
