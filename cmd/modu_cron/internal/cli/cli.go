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
	rows := make([][]string, 0, len(cfg.Tasks))
	for _, t := range cfg.Tasks {
		enabled := "no"
		if t.Enabled {
			enabled = "yes"
		}
		rows = append(rows, []string{t.ID, t.Cron, enabled, t.Prompt})
	}
	writeTable(out, []tableColumn{
		{Header: "ID", Max: 24},
		{Header: "CRON", Max: 24},
		{Header: "ENABLED", Max: 7},
		{Header: "PROMPT", Max: 72},
	}, rows)
	return nil
}
