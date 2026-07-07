// Package cron is a builtin extension that exposes modu_cron's task
// management tools (cron_add / cron_list / cron_remove / cron_update) to any
// CodingSession, so an interactive modu_code session can schedule and
// manage cron tasks in natural language.
//
// This is the management surface only: tasks land in ~/.modu/cron's
// config/tasks files and are executed by the long-lived modu_cron daemon,
// which hot-reloads the task file (fsnotify) — changes made here take
// effect without restarting it. Writes are serialized against the daemon
// and CLI via the advisory flock in crontools.
//
// Optional configuration via extensions.yaml:
//
//	extensions:
//	  - name: cron
//	    config:
//	      config_path: /custom/path/config.yaml   # default ~/.modu/cron/config.yaml
package cron

import (
	"context"
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/crontools"
	"github.com/openmodu/modu/pkg/types"
)

// Extension registers the cron management tools bound to a config path.
type Extension struct {
	cfgPath string
	api     extension.ExtensionAPI
}

// New constructs the cron extension with the default config path.
func New() *Extension { return &Extension{} }

func init() {
	extension.Register("cron", func() extension.Extension { return New() })
}

func (e *Extension) Name() string { return "cron" }

// ApplyConfig accepts an optional config_path override.
func (e *Extension) ApplyConfig(cfg map[string]any) error {
	for key, value := range cfg {
		switch key {
		case "config_path":
			s, ok := value.(string)
			if !ok {
				return fmt.Errorf("cron: config_path must be a string, got %T", value)
			}
			e.cfgPath = strings.TrimSpace(s)
		default:
			return fmt.Errorf("cron: unknown config key %q", key)
		}
	}
	return nil
}

func (e *Extension) Init(api extension.ExtensionAPI) error {
	e.api = api
	path := e.cfgPath
	if path == "" {
		path = config.DefaultPath()
	}
	for _, tool := range crontools.New(path) {
		api.RegisterTool(tool)
	}
	api.RegisterCommand("cron", "Manage cron tasks: /cron add <request>, /cron list, /cron rm <uuid>, /cron update <request>", e.cmdCron)
	return nil
}

func (e *Extension) cmdCron(args string) error {
	args = strings.TrimSpace(args)
	if args == "" {
		e.tell("usage: /cron add <request> | /cron list | /cron rm <uuid> | /cron update <request>")
		return nil
	}
	parts := strings.Fields(args)
	switch strings.ToLower(parts[0]) {
	case "list", "ls":
		text, err := e.runCronTool("cron_list", nil)
		if err != nil {
			return err
		}
		e.tell(text)
		return nil
	case "rm", "remove", "delete":
		if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
			e.tell("usage: /cron rm <uuid>")
			return nil
		}
		text, err := e.runCronTool("cron_remove", map[string]any{"uuid": strings.TrimSpace(parts[1])})
		if err != nil {
			return err
		}
		e.tell(text)
		return nil
	case "add":
		return e.askAgentForCronChange("add", strings.TrimSpace(strings.TrimPrefix(args, parts[0])))
	case "update":
		return e.askAgentForCronChange("update", strings.TrimSpace(strings.TrimPrefix(args, parts[0])))
	default:
		e.tell("unknown /cron subcommand: " + parts[0])
		return nil
	}
}

func (e *Extension) askAgentForCronChange(action, request string) error {
	if e.api == nil {
		return fmt.Errorf("cron extension API unavailable")
	}
	if strings.TrimSpace(request) == "" {
		request = "Start an interactive natural-language exchange to collect the cron task details."
	}
	prompt := fmt.Sprintf("The user invoked /cron %s. Manage modu cron via the cron tools using natural language. Request: %s", action, request)
	return e.api.SendMessageWithOptions(prompt, extension.MessageOptions{
		CustomType: "cron_slash",
		Display:    true,
	})
}

func (e *Extension) runCronTool(name string, args map[string]any) (string, error) {
	path := e.cfgPath
	if path == "" {
		path = config.DefaultPath()
	}
	for _, tool := range crontools.New(path) {
		if tool.Name() != name {
			continue
		}
		res, err := tool.Execute(context.Background(), "cron-slash", args, nil)
		if err != nil {
			return "", err
		}
		if len(res.Content) == 0 {
			return "", nil
		}
		if text, ok := res.Content[0].(*types.TextContent); ok {
			return text.Text, nil
		}
		return "", nil
	}
	return "", fmt.Errorf("cron tool %s not found", name)
}

func (e *Extension) tell(text string) {
	if e.api != nil {
		e.api.Notify(e.Name(), text)
	}
}
