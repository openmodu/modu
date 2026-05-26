// modu_cron is a cron-driven agent runner built on modu's CodingAgent.
//
// Dual-form CLI:
//
//	modu_cron daemon              run the scheduler loop
//	modu_cron init                create config.yaml + tasks.yaml
//	modu_cron list                list configured tasks
//	modu_cron logs <id> [flags]   inspect a task's run history
//	modu_cron add "<desc>"        add a task from a natural-language description
//	modu_cron rm   <id> [--yes]   remove a task
//	modu_cron run  <id>           fire a task once, ignoring schedule + enabled
//
// Default config: ~/.modu_cron/config.yaml (override with -c).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/openmodu/modu/cmd/modu_cron/internal/cli"
	"github.com/openmodu/modu/cmd/modu_cron/internal/config"
)

func main() {
	cfgPath := flag.String("c", config.DefaultPath(), "path to config.yaml")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	if err := dispatch(args[0], args[1:], *cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func dispatch(cmd string, args []string, cfgPath string) error {
	switch cmd {
	case "init":
		return runInit(args, cfgPath)
	case "daemon":
		return cli.Daemon(context.Background(), cfgPath)
	case "list":
		return cli.List(cfgPath, os.Stdout)
	case "logs":
		return runLogs(args)
	case "add":
		if len(args) == 0 {
			return fmt.Errorf(`add: usage: modu_cron add "<task description>"`)
		}
		// Accept either modu_cron add "<text>" (one arg, quoted) or
		// modu_cron add <text...> (multiple unquoted args).
		return cli.Add(context.Background(), cfgPath, strings.Join(args, " "), os.Stdout)
	case "rm":
		return runRm(args, cfgPath)
	case "run":
		if len(args) == 0 {
			return fmt.Errorf("run: task id required")
		}
		return cli.Run(context.Background(), cfgPath, args[0], os.Stdout)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand: %s", cmd)
	}
}

func runInit(args []string, cfgPath string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage:
  modu_cron [-c <config>] init [flags]

Creates an isolated runtime config and task file. By default the task file is
tasks.yaml next to config.yaml, working_dir is the current directory, and the
model points to the shared LM Studio endpoint.`)
	}
	force := fs.Bool("force", false, "overwrite existing config and task file")
	nonInteractive := fs.Bool("non-interactive", false, "use defaults/flags without prompting")
	workdir := fs.String("workdir", "", "agent working directory (default: current directory)")
	tasksFile := fs.String("tasks-file", "", "task file path, relative to config dir unless absolute (default: tasks.yaml)")
	modelProvider := fs.String("model-provider", "", "LLM provider id (default: lmstudio)")
	model := fs.String("model", "", "LLM model id (default: qwen/qwen3.6-35b-a3b)")
	modelBaseURL := fs.String("model-base-url", "", "OpenAI-compatible base URL (default: http://192.168.5.149:1234/v1)")
	modelAPIKey := fs.String("model-api-key", "", "inline LLM API key")
	modelAPIKeyEnv := fs.String("model-api-key-env", "", "env var containing LLM API key")
	noTelegram := fs.Bool("no-telegram", false, "skip creating the default Telegram channel")
	tgChannel := fs.String("telegram-channel", "", "Telegram channel name (default: telegram-home)")
	tgToken := fs.String("telegram-token", "", "inline Telegram bot token")
	tgTokenEnv := fs.String("telegram-token-env", "", "env var containing Telegram bot token (default: MODU_CRON_TG_TOKEN)")
	tgChatID := fs.String("telegram-chat-id", "", "inline Telegram chat id")
	tgChatIDEnv := fs.String("telegram-chat-id-env", "", "env var containing Telegram chat id (default: MODU_CRON_TG_CHAT_ID)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return cli.Init(cfgPath, cli.InitOptions{
		Force:             *force,
		Interactive:       !*nonInteractive,
		In:                os.Stdin,
		WorkingDir:        *workdir,
		TasksFile:         *tasksFile,
		ModelProvider:     *modelProvider,
		Model:             *model,
		ModelBaseURL:      *modelBaseURL,
		ModelAPIKey:       *modelAPIKey,
		ModelAPIKeyEnv:    *modelAPIKeyEnv,
		TelegramChannel:   *tgChannel,
		DisableTelegram:   *noTelegram,
		TelegramToken:     *tgToken,
		TelegramTokenEnv:  *tgTokenEnv,
		TelegramChatID:    *tgChatID,
		TelegramChatIDEnv: *tgChatIDEnv,
	}, os.Stdout)
}

func runRm(args []string, cfgPath string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage:
  modu_cron rm <task-id> [--yes]

Without --yes, an interactive confirmation prompt is shown when stdin is
a terminal; scripts running outside a TTY must pass --yes explicitly.`)
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
	return cli.Rm(cfgPath, taskID, cli.RmOptions{
		Yes:   *yes,
		IsTTY: term.IsTerminal(int(os.Stdin.Fd())),
		In:    os.Stdin,
		Out:   os.Stderr,
	})
}

func runLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `Usage:
  modu_cron logs <task-id>                    list runs for the task
  modu_cron logs <task-id> --tail [--json]    show the most recent run
  modu_cron logs <task-id> --file <name>      show a specific run file
                                              (add --json for raw NDJSON)`)
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
	return cli.Logs(taskID, cli.LogsOptions{
		File: *file,
		Tail: *tail,
		JSON: *asJSON,
	}, os.Stdout)
}

func usage() {
	fmt.Fprintln(os.Stderr, `modu_cron — cron-driven agent runner

Usage:
  modu_cron [-c <config>] <subcommand> [args]

Subcommands:
  init              create config.yaml and tasks.yaml
  daemon            run the scheduler loop
  list              list configured tasks
  logs <id>         inspect a task's run history (--tail / --file / --json)
  add "<desc>"      add a task from a natural-language description
  rm   <id>         remove a task (--yes to skip prompt)
  run  <id>         fire a task once (ignores schedule + enabled flag)

Flags:
  -c <path>         config file (default: ~/.modu_cron/config.yaml)`)
}
