package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/openmodu/modu/pkg/cron/config"
)

// RmOptions controls confirmation behavior for the rm subcommand.
//
// Confirmation matrix:
//
//	Yes=true                  → skip confirmation
//	Yes=false, IsTTY=true     → prompt "remove X? [y/N]" on Out, default no
//	Yes=false, IsTTY=false    → refuse with error (non-interactive script
//	                            must opt in via --yes to delete data)
type RmOptions struct {
	Yes   bool
	IsTTY bool
	In    io.Reader
	Out   io.Writer
}

// Rm removes the task with id taskID from cfgPath's task file.
func Rm(cfgPath, taskID string, opts RmOptions) error {
	if taskID == "" {
		return fmt.Errorf("rm: task id required")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	idx := -1
	for i, t := range cfg.Tasks {
		if t.ID == taskID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("rm: task %q not found in %s", taskID, cfgPath)
	}

	if !opts.Yes {
		if !opts.IsTTY {
			return fmt.Errorf("rm: refusing to remove %q without --yes (stdin is not a terminal)", taskID)
		}
		ok, err := confirmRemove(opts.In, opts.Out, cfg.Tasks[idx])
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(opts.Out, "cancelled")
			return nil
		}
	}

	cfg.Tasks = append(cfg.Tasks[:idx], cfg.Tasks[idx+1:]...)
	taskPath := config.ResolveTasksPath(cfgPath, cfg)
	if err := config.SaveTasks(taskPath, cfg.Tasks); err != nil {
		return err
	}
	fmt.Fprintf(opts.Out, "removed task %q from %s\n", taskID, taskPath)
	return nil
}

func confirmRemove(in io.Reader, out io.Writer, task config.Task) (bool, error) {
	fmt.Fprintf(out, "remove %q (cron=%q prompt=%q)? [y/N]: ", task.ID, task.Cron, truncate(task.Prompt, 60))
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	v := strings.ToLower(strings.TrimSpace(line))
	return v == "y" || v == "yes", nil
}
