// Package cli implements the modu_cron subcommands.
package cli

import (
	"fmt"
	"io"

	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

// List prints all configured tasks.
func List(cfgPath string, out io.Writer) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if len(cfg.Tasks) == 0 {
		fmt.Fprintln(out, "(no tasks)")
		return nil
	}
	fmt.Fprintf(out, "%-20s %-15s %-8s %s\n", "ID", "CRON", "ENABLED", "PROMPT")
	for _, t := range cfg.Tasks {
		enabled := "no"
		if t.Enabled {
			enabled = "yes"
		}
		fmt.Fprintf(out, "%-20s %-15s %-8s %s\n", t.ID, t.Cron, enabled, t.Prompt)
	}
	return nil
}
