package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// Main is the shared cron CLI entrypoint: it parses the optional -c flag and
// dispatches the subcommand. Both `modu_cron <sub>` and `modu_code cron
// <sub>` route here; progName is only used in help output.
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
	rest := fs.Args()
	if len(rest) == 0 {
		Usage(os.Stderr, progName)
		return fmt.Errorf("subcommand required")
	}
	return Dispatch(rest[0], rest[1:], *cfgPath, progName)
}

// Dispatch routes one cron subcommand.
func Dispatch(cmd string, args []string, cfgPath, progName string) error {
	switch cmd {
	case "init":
		return runInit(args, cfgPath, progName)
	case "daemon":
		return Daemon(context.Background(), cfgPath)
	case "list":
		return List(cfgPath, os.Stdout)
	case "logs":
		return runLogs(args, progName)
	case "rm":
		return runRm(args, cfgPath, progName)
	case "run":
		if len(args) == 0 {
			return fmt.Errorf("run: task id required")
		}
		return Run(context.Background(), cfgPath, args[0], os.Stdout)
	case "help", "-h", "--help":
		Usage(os.Stderr, progName)
		return nil
	default:
		Usage(os.Stderr, progName)
		return fmt.Errorf("unknown subcommand: %s", cmd)
	}
}

func runInit(args []string, cfgPath, progName string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage:
  %s [-c <config>] init [flags]

Creates an isolated runtime config and task file. By default the task file is
tasks.yaml next to config.yaml and working_dir is the current directory. The
LLM model is not part of this config — cron tasks use whatever model is
active in 'modu_code config' (~/.modu/config.toml), same as an interactive
session.
`, progName)
	}
	force := fs.Bool("force", false, "overwrite existing config and task file")
	nonInteractive := fs.Bool("non-interactive", false, "use defaults/flags without prompting")
	workdir := fs.String("workdir", "", "agent working directory (default: current directory)")
	tasksFile := fs.String("tasks-file", "", "task file path, relative to config dir unless absolute (default: tasks.yaml)")
	noTelegram := fs.Bool("no-telegram", false, "skip creating the default Telegram channel")
	tgChannel := fs.String("telegram-channel", "", "Telegram channel name (default: telegram-home)")
	tgToken := fs.String("telegram-token", "", "inline Telegram bot token")
	tgTokenEnv := fs.String("telegram-token-env", "", "env var containing Telegram bot token (default: MODU_CRON_TG_TOKEN)")
	tgChatID := fs.String("telegram-chat-id", "", "inline Telegram chat id")
	tgChatIDEnv := fs.String("telegram-chat-id-env", "", "env var containing Telegram chat id (default: MODU_CRON_TG_CHAT_ID)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return Init(cfgPath, InitOptions{
		Force:             *force,
		Interactive:       !*nonInteractive,
		In:                os.Stdin,
		WorkingDir:        *workdir,
		TasksFile:         *tasksFile,
		TelegramChannel:   *tgChannel,
		DisableTelegram:   *noTelegram,
		TelegramToken:     *tgToken,
		TelegramTokenEnv:  *tgTokenEnv,
		TelegramChatID:    *tgChatID,
		TelegramChatIDEnv: *tgChatIDEnv,
	}, os.Stdout)
}

func runRm(args []string, cfgPath, progName string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage:
  %s rm <task-id> [--yes]

Without --yes, an interactive confirmation prompt is shown when stdin is
a terminal; scripts running outside a TTY must pass --yes explicitly.
`, progName)
	}
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	if len(args) == 0 {
		fs.Usage()
		return fmt.Errorf("rm: task id required")
	}
	taskID := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return Rm(cfgPath, taskID, RmOptions{
		Yes:   *yes,
		IsTTY: term.IsTerminal(int(os.Stdin.Fd())),
		In:    os.Stdin,
		Out:   os.Stderr,
	})
}

func runLogs(args []string, progName string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage:
  %[1]s logs <task-id>                    list runs for the task
  %[1]s logs <task-id> --tail [--json]    show the most recent run
  %[1]s logs <task-id> --file <name>      show a specific run file
                                          (add --json for raw NDJSON)
`, progName)
	}
	tail := fs.Bool("tail", false, "show the most recent run")
	file := fs.String("file", "", "show this specific run file")
	asJSON := fs.Bool("json", false, "print raw NDJSON instead of decoded transcript")

	// flag.Parse stops at the first positional argument, so peel off the
	// task id ourselves and let the FlagSet parse the rest.
	if len(args) == 0 {
		fs.Usage()
		return fmt.Errorf("logs: task id required")
	}
	taskID := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return Logs(taskID, LogsOptions{
		File: *file,
		Tail: *tail,
		JSON: *asJSON,
	}, os.Stdout)
}

// Usage prints the cron CLI help, phrased for the invoking program.
func Usage(w io.Writer, progName string) {
	fmt.Fprintf(w, `%[1]s — cron-driven agent runner

Usage:
  %[1]s [-c <config>] <subcommand> [args]

Subcommands:
  init              create config.yaml and tasks.yaml
  daemon            run the scheduler loop
  list              list configured tasks
  logs <id>         inspect a task's run history (--tail / --file / --json)
  rm   <id>         remove a task (--yes to skip prompt)
  run  <id>         fire a task once (ignores schedule + enabled flag)

To add or manage tasks in natural language, just talk to modu_code — the
builtin 'cron' extension gives any session (interactive or Telegram) the
cron_add / cron_list / cron_remove tools.

Flags:
  -c <path>         config file (default: ~/.modu_cron/config.yaml)
`, progName)
}
