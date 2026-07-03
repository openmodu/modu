package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

// Main is the cron CLI entrypoint: `modu_code cron [-c <config>]` starts the
// scheduler daemon and blocks until SIGINT/SIGTERM. There are no
// subcommands — task management (add / list / remove) is not a CLI concern
// here, it's the builtin 'cron' extension's cron_add/cron_list/cron_remove
// tools, available in any modu_code session (interactive or its own
// Telegram bot). progName is only used in help/error output.
func Main(progName string, args []string, defaultCfgPath string) error {
	fs := flag.NewFlagSet(progName, flag.ContinueOnError)
	cfgPath := fs.String("c", defaultCfgPath, "path to config.yaml")
	fs.Usage = func() { Usage(os.Stderr, progName) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if rest := fs.Args(); len(rest) > 0 {
		switch rest[0] {
		case "help", "-h", "--help":
			Usage(os.Stderr, progName)
			return nil
		default:
			Usage(os.Stderr, progName)
			return fmt.Errorf("%s takes no subcommands (got %q) — it starts the scheduler daemon; manage tasks by talking to modu_code", progName, rest[0])
		}
	}
	return Daemon(context.Background(), *cfgPath)
}

// Usage prints the cron CLI help, phrased for the invoking program.
func Usage(w io.Writer, progName string) {
	fmt.Fprintf(w, `%[1]s — cron scheduler daemon

Usage:
  %[1]s [-c <config>]

Starts the scheduler loop: loads tasks from the config, fires each on its
cron schedule, hot-reloads on config changes (fsnotify + SIGHUP), runs until
Ctrl+C.

There are no subcommands. To add, list, or remove tasks, talk to modu_code —
the builtin 'cron' extension registers cron_add / cron_list / cron_remove on
any session (interactive or its own Telegram bot), and the daemon picks up
changes automatically. Missing config/task files are fine: the daemon starts
with zero tasks and the working directory it was launched from.

Flags:
  -c <path>         config file (default: ~/.modu_cron/config.yaml)
`, progName)
}
