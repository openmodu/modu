// modu_cron is a cron-driven agent runner built on modu's CodingAgent.
//
// Dual-form CLI:
//
//	modu_cron daemon              run the scheduler loop
//	modu_cron list                list configured tasks
//	modu_cron add                 [stub] add a task
//	modu_cron run  <id>           [stub] fire a task immediately
//	modu_cron rm   <id>           [stub] remove a task
//	modu_cron logs <id>           [stub] tail a task's logs
//
// Default config: ~/.modu_cron/config.yaml (override with -c).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

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

func dispatch(cmd string, _ []string, cfgPath string) error {
	switch cmd {
	case "daemon":
		return cli.Daemon(context.Background(), cfgPath)
	case "list":
		return cli.List(cfgPath, os.Stdout)
	case "add", "run", "rm", "logs":
		return cli.NotImplemented(cmd)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand: %s", cmd)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `modu_cron — cron-driven agent runner

Usage:
  modu_cron [-c <config>] <subcommand> [args]

Subcommands:
  daemon            run the scheduler loop
  list              list configured tasks
  add               [stub] add a task
  run  <id>         [stub] fire a task immediately
  rm   <id>         [stub] remove a task
  logs <id>         [stub] tail a task's logs

Flags:
  -c <path>         config file (default: ~/.modu_cron/config.yaml)`)
}
