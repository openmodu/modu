#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
CRON_CONFIG="${MODU_CRON_CONFIG:-$HOME/.modu/cron/config.yaml}"
export EXPECTED_TASKS="${EXPECTED_TASKS-morning-triage morning-market-daily}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

[[ -f "$CRON_CONFIG" ]] || fail "missing cron config: $CRON_CONFIG"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/next_run_windows.go" <<'GO'
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	cronconfig "github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/scheduler"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: next_run_windows <config>")
		os.Exit(2)
	}
	cfg, err := cronconfig.Load(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load cron config: %v\n", err)
		os.Exit(1)
	}
	now := time.Now()
	expected := expectedTasks()
	seen := make(map[string]bool)
	count := 0
	for _, task := range cfg.Tasks {
		if !task.Enabled {
			continue
		}
		next, err := scheduler.Next(task, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "task %s: next run: %v\n", task.ID, err)
			os.Exit(1)
		}
		hasGoal := strings.TrimSpace(task.Goal) != ""
		if _, ok := expected[task.ID]; ok {
			seen[task.ID] = true
			if !hasGoal {
				fmt.Fprintf(os.Stderr, "expected task %s is enabled but has no goal\n", task.ID)
				os.Exit(1)
			}
			if err := checkExpectedWindow(task, next); err != nil {
				fmt.Fprintf(os.Stderr, "expected task %s has invalid next window: %v\n", task.ID, err)
				os.Exit(1)
			}
		}
		fmt.Printf("%s next=%s timezone=%s has_goal=%t cron=%q\n",
			task.ID, next.Format(time.RFC3339), tzOrLocal(task.Timezone), hasGoal, task.Cron)
		count++
	}
	if count == 0 {
		fmt.Fprintln(os.Stderr, "no enabled tasks")
		os.Exit(1)
	}
	for id := range expected {
		if !seen[id] {
			fmt.Fprintf(os.Stderr, "expected task %s is not enabled or missing\n", id)
			os.Exit(1)
		}
	}
}

func checkExpectedWindow(task cronconfig.Task, next time.Time) error {
	wantHour, wantMinute, ok := expectedShanghaiClock(task.ID)
	if !ok {
		return nil
	}
	if strings.TrimSpace(task.Timezone) != "Asia/Shanghai" {
		return fmt.Errorf("timezone=%q, want Asia/Shanghai", task.Timezone)
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return err
	}
	local := next.In(loc)
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday {
		return fmt.Errorf("next=%s is not a Shanghai workday", local.Format(time.RFC3339))
	}
	if local.Hour() != wantHour || local.Minute() != wantMinute || local.Second() != 0 {
		return fmt.Errorf("next=%s, want %02d:%02d:00 Asia/Shanghai", local.Format(time.RFC3339), wantHour, wantMinute)
	}
	return nil
}

func expectedShanghaiClock(id string) (int, int, bool) {
	switch id {
	case "morning-triage":
		return 6, 0, true
	case "morning-market-daily":
		return 10, 20, true
	default:
		return 0, 0, false
	}
}

func tzOrLocal(tz string) string {
	if strings.TrimSpace(tz) != "" {
		return strings.TrimSpace(tz)
	}
	return time.Local.String()
}

func expectedTasks() map[string]struct{} {
	out := make(map[string]struct{})
	for _, id := range strings.Fields(os.Getenv("EXPECTED_TASKS")) {
		out[id] = struct{}{}
	}
	return out
}
GO

(cd "$ROOT" && go run "$tmp/next_run_windows.go" "$CRON_CONFIG")
