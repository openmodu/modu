package cron

import (
	"context"
	"testing"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/types"
)

// fakeAPI implements just enough of extension.ExtensionAPI to capture tool
// registrations.
type fakeAPI struct {
	tools []types.Tool
}

func (f *fakeAPI) RegisterTool(t types.Tool) { f.tools = append(f.tools, t) }
func (f *fakeAPI) RegisterCommand(string, string, extension.CommandHandler) {
}
func (f *fakeAPI) AddHook(extension.ToolHook)        {}
func (f *fakeAPI) On(string, extension.EventHandler) {}
func (f *fakeAPI) SendMessage(string) error          { return nil }
func (f *fakeAPI) SetActiveTools([]string)           {}
func (f *fakeAPI) SetModel(string, string) error     { return nil }
func (f *fakeAPI) GetCommands() []extension.Command  { return nil }
func (f *fakeAPI) SessionID() string                 { return "s" }
func (f *fakeAPI) SessionDir() string                { return "" }
func (f *fakeAPI) AgentDir() string                  { return "" }
func (f *fakeAPI) Cwd() string                       { return "" }
func (f *fakeAPI) IsIdle() bool                      { return true }
func (f *fakeAPI) HasPendingMessages() bool          { return false }
func (f *fakeAPI) PermissionMode() string            { return "" }
func (f *fakeAPI) SendFollowUpMessage(string) error  { return nil }
func (f *fakeAPI) SendMessageWithOptions(string, extension.MessageOptions) error {
	return nil
}
func (f *fakeAPI) Notify(string, string)                     {}
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
func (f *fakeAPI) ForkSession(context.Context, extension.ForkOptions) (string, error) {
	return "", nil
}

func TestInitRegistersCronTools(t *testing.T) {
	e := New()
	api := &fakeAPI{}
	if err := e.Init(api); err != nil {
		t.Fatalf("Init: %v", err)
	}
	want := map[string]bool{"cron_add": false, "cron_list": false, "cron_remove": false}
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
