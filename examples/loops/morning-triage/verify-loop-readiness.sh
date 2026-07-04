#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
CRON_CONFIG="${MODU_CRON_CONFIG:-$HOME/.modu/cron/config.yaml}"
EXTENSIONS_CONFIG="${MODU_EXTENSIONS_CONFIG:-$HOME/.modu/extensions.yaml}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_file() {
  [[ -f "$1" ]] || fail "missing file: $1"
}

need_text() {
  local file="$1"
  local needle="$2"
  local message="$3"
  grep -Fq "$needle" "$file" || fail "$message in $file"
}

task_file_from_config() {
  local cfg="$1"
  local tasks_file
  tasks_file="$(awk -F: '$1 ~ /^[[:space:]]*tasks_file[[:space:]]*$/ {gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2); print $2; exit}' "$cfg")"
  if [[ -z "$tasks_file" ]]; then
    printf '%s\n' "$(dirname "$cfg")/tasks.yaml"
  elif [[ "$tasks_file" = /* ]]; then
    printf '%s\n' "$tasks_file"
  else
    printf '%s\n' "$(dirname "$cfg")/$tasks_file"
  fi
}

task_block() {
  local task_file="$1"
  local task_id="$2"
  awk -v id="$task_id" '
    $0 ~ "^[[:space:]]*- id:[[:space:]]*" id "[[:space:]]*$" {in_task=1; print; next}
    in_task && $0 ~ "^[[:space:]]*- id:" {exit}
    in_task {print}
  ' "$task_file"
}

go_parse_check() {
  local cfg="$1"
  local tmp
  tmp="$(mktemp -d)"
  cat >"$tmp/check_cron_config.go" <<'GO'
package main

import (
	"context"
	"fmt"
	"os"

	cronconfig "github.com/openmodu/modu/pkg/cron/config"
	"github.com/openmodu/modu/pkg/cron/scheduler"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: check_cron_config <config>")
		os.Exit(2)
	}
	cfg, err := cronconfig.Load(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "load cron config: %v\n", err)
		os.Exit(1)
	}
	s := scheduler.New(func(context.Context, cronconfig.Task) error { return nil })
	if err := s.LoadAll(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "schedule cron tasks: %v\n", err)
		os.Exit(1)
	}
}
GO
  set +e
  (cd "$ROOT" && go run "$tmp/check_cron_config.go" "$cfg") >/dev/null
  local rc=$?
  set -e
  rm -rf "$tmp"
  return "$rc"
}

extension_block() {
  local config_file="$1"
  local extension_name="$2"
  awk -v name="$extension_name" '
    $0 ~ "^[[:space:]]*- name:[[:space:]]*" name "[[:space:]]*$" {in_ext=1; print; next}
    in_ext && $0 ~ "^[[:space:]]*- name:" {exit}
    in_ext {print}
  ' "$config_file"
}

need_file "$ROOT/.coding_agent/skills/morning-triage/SKILL.md"
need_file "$ROOT/.coding_agent/workflows/triage-fixes.js"
need_file "$ROOT/.coding_agent/agents/reviewer.md"
need_file "$CRON_CONFIG"
need_file "$EXTENSIONS_CONFIG"
go_parse_check "$CRON_CONFIG"

need_text "$ROOT/.coding_agent/skills/morning-triage/SKILL.md" 'state/triage.md' "morning-triage skill must persist triage state"
need_text "$ROOT/.coding_agent/skills/morning-triage/SKILL.md" 'No actionable findings' "morning-triage skill must write a no-action heartbeat row"
need_text "$ROOT/.coding_agent/workflows/triage-fixes.js" "isolation: 'worktree'" "triage-fixes must isolate work"
need_text "$ROOT/.coding_agent/workflows/triage-fixes.js" 'gh pr create --draft' "triage-fixes must open draft PRs only"
need_text "$ROOT/.coding_agent/agents/reviewer.md" 'ASSUME: this work is BROKEN' "reviewer must be adversarial"

goal_ext="$(extension_block "$EXTENSIONS_CONFIG" goal)"
[[ -n "$goal_ext" ]] || fail "missing goal extension config in $EXTENSIONS_CONFIG"
printf '%s\n' "$goal_ext" | grep -Fq 'verifier:' || fail "goal extension must configure verifier in $EXTENSIONS_CONFIG"
printf '%s\n' "$goal_ext" | grep -Eq '^[[:space:]]*enabled:[[:space:]]*true[[:space:]]*$' ||
  fail "goal verifier must be enabled in $EXTENSIONS_CONFIG"

configured_root="$(awk -F: '$1 ~ /^[[:space:]]*working_dir[[:space:]]*$/ {sub(/^[[:space:]]+/, "", $2); print $2; exit}' "$CRON_CONFIG")"
[[ "$configured_root" == "$ROOT" ]] || fail "cron working_dir is $configured_root, want $ROOT"

task_file="$(task_file_from_config "$CRON_CONFIG")"
need_file "$task_file"

triage="$(task_block "$task_file" morning-triage)"
[[ -n "$triage" ]] || fail "missing morning-triage task in $task_file"
printf '%s\n' "$triage" | grep -Eq '^[[:space:]]*enabled:[[:space:]]*true[[:space:]]*$' || fail "morning-triage task must be enabled"
printf '%s\n' "$triage" | grep -Fq 'prompt: /morning-triage' || fail "morning-triage task must invoke /morning-triage"
printf '%s\n' "$triage" | grep -Fq 'timezone: Asia/Shanghai' || fail "morning-triage task must run in Asia/Shanghai timezone"
printf '%s\n' "$triage" | grep -Fq 'goal:' || fail "morning-triage task must declare goal:"
printf '%s\n' "$triage" | grep -Fq 'state/triage.md' || fail "morning-triage goal must mention state/triage.md"
printf '%s\n' "$triage" | grep -Fq 'timeout: 30m' || fail "morning-triage task must keep timeout cap"
printf '%s\n' "$triage" | grep -Fq 'max_tokens_per_run: 400000' || fail "morning-triage task must keep token cap"

market="$(task_block "$task_file" morning-market-daily)"
[[ -n "$market" ]] || fail "missing morning-market-daily task in $task_file"
printf '%s\n' "$market" | grep -Eq '^[[:space:]]*enabled:[[:space:]]*true[[:space:]]*$' || fail "morning-market-daily task must be enabled"
printf '%s\n' "$market" | grep -Fq 'goal:' || fail "morning-market-daily task must declare goal:"
printf '%s\n' "$market" | grep -Fq 'timezone: Asia/Shanghai' || fail "morning-market-daily task must run in Asia/Shanghai timezone"
printf '%s\n' "$market" | grep -Fq 'state/watchlist.md' || fail "morning-market-daily task must maintain state/watchlist.md"
printf '%s\n' "$market" | grep -Fq 'Skills: a-stock-data' || fail "market task prompt must activate a-stock-data skill"
printf '%s\n' "$market" | grep -Fq '腾讯财经API' || fail "market task prompt must prefer Tencent market data"
printf '%s\n' "$market" | grep -Fq '东财请求必须串行' || fail "market task prompt must require serial Eastmoney requests"
printf '%s\n' "$market" | grep -Fq 'User-Agent/Referer' || fail "market task prompt must require Eastmoney UA/Referer"
printf '%s\n' "$market" | grep -Fq '不要编造涨跌幅、成交额、题材或标的' || fail "market task prompt must forbid fabricated market observations"
printf '%s\n' "$market" | grep -Fq '不要创建或更新 state/watchlist.md' || fail "market task prompt must preserve watchlist on non-trading/no-data runs"
printf '%s\n' "$market" | grep -Fq '读取仓库里的 `state/watchlist.md`' || fail "market task prompt must read prior watchlist"
printf '%s\n' "$market" | grep -Fq '更新 `state/watchlist.md`' || fail "market task prompt must write next watchlist"
printf '%s\n' "$market" | grep -Fq '# A股观察清单' || fail "market task prompt must require watchlist title"
printf '%s\n' "$market" | grep -Fq '| 题材 | 强度 | 证据 | 代表标的 | 明日验证点 |' || fail "market task prompt must require discovered themes table"
printf '%s\n' "$market" | grep -Fq '| 代码 | 名称 | 触发原因 | 所属题材 | 风险 | 明日验证点 |' || fail "market task prompt must require discovered stocks table"
printf '%s\n' "$market" | grep -Fq '| 题材/标的 | 移出原因 | 日期 |' || fail "market task prompt must require removed watchlist table"
printf '%s\n' "$market" | grep -Fq '写完后读回 state/watchlist.md' || fail "market task prompt must read back watchlist after writing"
printf '%s\n' "$market" | grep -Fq 'max_tokens_per_run: 300000' || fail "market task must keep token cap"

printf 'loop readiness ok: repo=%s task_file=%s\n' "$ROOT" "$task_file"
