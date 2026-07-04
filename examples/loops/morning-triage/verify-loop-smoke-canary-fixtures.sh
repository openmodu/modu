#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERIFIER="$SCRIPT_DIR/verify-loop-smoke-canary.sh"
TASK_ID="loop-smoke-fast"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

write_fixture() {
  local tmp="$1"
  local trigger="${2:-scheduler}"
  local include_update_goal="${3:-yes}"
  local task_present="${4:-no}"
  local state_present="${5:-no}"

  local root="$tmp/root"
  local logs="$tmp/logs/$TASK_ID"
  local sessions="$tmp/sessions/project"
  local cron="$tmp/cron"
  local session_id="fixture-session"

  mkdir -p "$root/state" "$logs" "$sessions" "$cron"
  cat >"$cron/config.yaml" <<EOF
tasks_file: task.yaml
EOF
  if [[ "$task_present" == "yes" ]]; then
    cat >"$cron/task.yaml" <<EOF
tasks:
  - id: $TASK_ID
    cron: "*/30 * * * * *"
    prompt: "temporary smoke"
EOF
  else
    cat >"$cron/task.yaml" <<EOF
tasks: []
EOF
  fi

  if [[ "$state_present" == "yes" ]]; then
    printf 'temporary smoke state\n' >"$root/state/loop-smoke.md"
  fi

  cat >"$logs/2026-07-04T10-21-30.log" <<EOF
{"type":"run_start","task_id":"$TASK_ID","trigger":"$trigger","timezone":"Asia/Shanghai","has_goal":true,"goal_verifier":true}
{"type":"session_start","session_id":"$session_id"}
{"type":"run_end","status":"ok","goal_status":"complete"}
EOF

  {
    printf '{"type":"session_info","name":"cron:%s","cwd":"%s"}\n' "$TASK_ID" "$root"
    printf '{"type":"message","message":{"role":"assistant","content":"wrote state/loop-smoke.md with trigger=scheduler and read it back"}}\n'
    if [[ "$include_update_goal" == "yes" ]]; then
      printf '{"type":"tool_call","name":"update_goal","arguments":{"status":"complete"}}\n'
    fi
  } >"$sessions/$session_id.jsonl"
}

run_fixture() {
  local trigger="${1:-scheduler}"
  local include_update_goal="${2:-yes}"
  local task_present="${3:-no}"
  local state_present="${4:-no}"
  local tmp
  local rc
  tmp="$(mktemp -d)"
  write_fixture "$tmp" "$trigger" "$include_update_goal" "$task_present" "$state_present"
  set +e
  SMOKE_TASK_ID="$TASK_ID" \
    MODU_CRON_LOG_ROOT="$tmp/logs" \
    MODU_SESSION_ROOT="$tmp/sessions" \
    MODU_CRON_CONFIG="$tmp/cron/config.yaml" \
    bash "$VERIFIER" "$tmp/root"
  rc=$?
  set -e
  rm -rf "$tmp"
  return "$rc"
}

expect_reject() {
  local name="$1"
  shift
  local out
  out="$(mktemp)"
  if run_fixture "$@" >"$out" 2>&1; then
    cat "$out"
    rm -f "$out"
    fail "fixture should have been rejected: $name"
  fi
  rm -f "$out"
  printf 'fixture rejected as expected: %s\n' "$name"
}

run_fixture
expect_reject "manual trigger" "manual" "yes" "no" "no"
expect_reject "missing update_goal" "scheduler" "no" "no" "no"
expect_reject "temporary task still present" "scheduler" "yes" "yes" "no"
expect_reject "temporary state still present" "scheduler" "yes" "no" "yes"

printf 'loop smoke canary fixtures ok: verifier=%s\n' "$VERIFIER"
