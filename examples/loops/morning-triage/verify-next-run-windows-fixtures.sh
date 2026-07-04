#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

write_config() {
  local tmp="$1"
  cat >"$tmp/config.yaml" <<EOF
working_dir: $ROOT
tasks_file: $tmp/tasks.yaml
EOF
}

write_tasks() {
  local tmp="$1"
  local triage_cron="$2"
  local triage_timezone="$3"
  local triage_goal="$4"
  local market_cron="$5"
  local market_timezone="$6"
  local market_goal="$7"

  cat >"$tmp/tasks.yaml" <<EOF
tasks:
  - id: morning-triage
    cron: "$triage_cron"
    timezone: $triage_timezone
    prompt: "/morning-triage"
$(goal_yaml "$triage_goal")
    enabled: true
  - id: morning-market-daily
    cron: "$market_cron"
    timezone: $market_timezone
    prompt: "market"
$(goal_yaml "$market_goal")
    enabled: true
EOF
}

goal_yaml() {
  local goal="$1"
  if [[ -n "$goal" ]]; then
    printf '    goal: "%s"\n' "$goal"
  fi
}

run_fixture() {
  local triage_cron="$1"
  local triage_timezone="$2"
  local triage_goal="$3"
  local market_cron="$4"
  local market_timezone="$5"
  local market_goal="$6"
  local tmp
  local rc
  tmp="$(mktemp -d)"
  write_config "$tmp"
  write_tasks "$tmp" "$triage_cron" "$triage_timezone" "$triage_goal" "$market_cron" "$market_timezone" "$market_goal"
  MODU_CRON_CONFIG="$tmp/config.yaml" EXPECTED_TASKS="morning-triage morning-market-daily" \
    bash "$SCRIPT_DIR/verify-next-run-windows.sh" "$ROOT" || rc=$?
  rm -rf "$tmp"
  return "${rc:-0}"
}

expect_fail() {
  local name="$1"
  local pattern="$2"
  shift 2
  local out
  out="$(mktemp)"
  if run_fixture "$@" >"$out" 2>&1; then
    cat "$out"
    rm -f "$out"
    fail "$name unexpectedly passed"
  fi
  if ! grep -Eq "$pattern" "$out"; then
    cat "$out"
    rm -f "$out"
    fail "$name failed for the wrong reason; expected pattern: $pattern"
  fi
  printf 'fixture rejected as expected: %s\n' "$name"
  rm -f "$out"
}

run_fixture \
  "0 0 6 * * 1-5" "Asia/Shanghai" "Update state/triage.md" \
  "0 20 10 * * 1-5" "Asia/Shanghai" "Update state/watchlist.md"

expect_fail "missing triage goal" 'expected task morning-triage is enabled but has no goal' \
  "0 0 6 * * 1-5" "Asia/Shanghai" "" \
  "0 20 10 * * 1-5" "Asia/Shanghai" "Update state/watchlist.md"

expect_fail "wrong triage clock" 'expected task morning-triage has invalid next window: .*want 06:00:00 Asia/Shanghai' \
  "0 30 6 * * 1-5" "Asia/Shanghai" "Update state/triage.md" \
  "0 20 10 * * 1-5" "Asia/Shanghai" "Update state/watchlist.md"

expect_fail "wrong market timezone" 'expected task morning-market-daily has invalid next window: timezone="UTC", want Asia/Shanghai' \
  "0 0 6 * * 1-5" "Asia/Shanghai" "Update state/triage.md" \
  "0 20 10 * * 1-5" "UTC" "Update state/watchlist.md"

expect_fail "weekend triage schedule" 'expected task morning-triage has invalid next window: .*not a Shanghai workday' \
  "0 0 6 * * 0" "Asia/Shanghai" "Update state/triage.md" \
  "0 20 10 * * 1-5" "Asia/Shanghai" "Update state/watchlist.md"

printf 'next-run window fixtures ok: verifier=%s\n' "$SCRIPT_DIR/verify-next-run-windows.sh"
