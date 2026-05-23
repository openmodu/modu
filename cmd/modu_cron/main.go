// modu_cron is a cron-driven agent runner built on modu's CodingAgent.
//
// Dual-form CLI:
//
//	modu_cron daemon              run the scheduler loop
//	modu_cron list                list configured tasks
//	modu_cron logs <id> [flags]   inspect a task's run history
//	modu_cron add                 interactively add a task
//	modu_cron rm   <id> [--yes]   remove a task
//	modu_cron run  <id>           [stub] fire a task immediately
//
// Default config: ~/.modu_cron/config.yaml (override with -c).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

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
	case "daemon":
		return cli.Daemon(context.Background(), cfgPath)
	case "list":
		return cli.List(cfgPath, os.Stdout)
	case "logs":
		return runLogs(args)
	case "add":
		return cli.Add(cfgPath, os.Stdin, os.Stderr)
	case "rm":
		return runRm(args, cfgPath)
	case "run":
		return cli.NotImplemented(cmd)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand: %s", cmd)
	}
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
  daemon            run the scheduler loop
  list              list configured tasks
  logs <id>         inspect a task's run history (--tail / --file / --json)
  add               interactively add a task
  rm   <id>         remove a task (--yes to skip prompt)
  run  <id>         [stub] fire a task immediately

Flags:
  -c <path>         config file (default: ~/.modu_cron/config.yaml)`)
}
