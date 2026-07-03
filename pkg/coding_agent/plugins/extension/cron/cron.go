// Package cron is a builtin extension that exposes modu_cron's task
// management tools (cron_add / cron_list / cron_remove) to any
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
	"fmt"
	"strings"

	"github.com/openmodu/modu/pkg/coding_agent/plugins/extension"
	"github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/crontools"
)

// Extension registers the cron management tools bound to a config path.
type Extension struct {
	cfgPath string
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
	path := e.cfgPath
	if path == "" {
		path = config.DefaultPath()
	}
	for _, tool := range crontools.New(path) {
		api.RegisterTool(tool)
	}
	return nil
}
