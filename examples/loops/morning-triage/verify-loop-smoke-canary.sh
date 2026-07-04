#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
TASK_ID="${SMOKE_TASK_ID:-loop-smoke-fast}"
LOG_ROOT="${MODU_CRON_LOG_ROOT:-$HOME/.modu/cron/logs}"
SESSION_ROOT="${MODU_SESSION_ROOT:-$HOME/.modu/sessions}"
CRON_CONFIG="${MODU_CRON_CONFIG:-$HOME/.modu/cron/config.yaml}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
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

session_id_from_log() {
  local log="$1"
  local line
  line="$(grep -m1 '"type":"session_start"' "$log" || true)"
  [[ -n "$line" ]] || fail "missing session_start in log: $log"
  printf '%s\n' "$line" | sed -E 's/.*"session_id":"([^"]+)".*/\1/'
}

session_file_for_id() {
  local session_id="$1"
  find "$SESSION_ROOT" -type f -name "$session_id.jsonl" -print -quit 2>/dev/null
}

[[ -d "$LOG_ROOT/$TASK_ID" ]] || fail "missing smoke log dir: $LOG_ROOT/$TASK_ID"
[[ -d "$SESSION_ROOT" ]] || fail "missing session root: $SESSION_ROOT"

mapfile -t logs < <(
  while IFS= read -r log; do
    start_line="$(head -n 1 "$log")"
    tail_line="$(tail -n 1 "$log")"
    if printf '%s' "$start_line" | grep -q '"type":"run_start"' &&
      printf '%s' "$start_line" | grep -q "\"task_id\":\"$TASK_ID\"" &&
      printf '%s' "$start_line" | grep -q '"trigger":"scheduler"' &&
      printf '%s' "$start_line" | grep -q '"timezone":"Asia/Shanghai"' &&
      printf '%s' "$start_line" | grep -q '"has_goal":true' &&
      printf '%s' "$start_line" | grep -q '"goal_verifier":true' &&
      printf '%s' "$tail_line" | grep -q '"type":"run_end"' &&
      printf '%s' "$tail_line" | grep -q '"status":"ok"' &&
      printf '%s' "$tail_line" | grep -q '"goal_status":"complete"'; then
      printf '%s\n' "$log"
    fi
  done < <(find "$LOG_ROOT/$TASK_ID" -type f -name '*.log' -print | sort) | tail -n 1
)
[[ "${#logs[@]}" -ge 1 ]] || fail "missing successful scheduler-triggered $TASK_ID smoke log"

log="${logs[0]}"
session_id="$(session_id_from_log "$log")"
session_file="$(session_file_for_id "$session_id")"
[[ -n "$session_file" ]] || fail "missing full session JSONL for session_id=$session_id from $log"

head -n 1 "$session_file" | grep -Fq "\"cwd\":\"$ROOT\"" ||
  fail "smoke session cwd is not this repo: $session_file"
grep -Fq '"name":"cron:loop-smoke-fast"' "$session_file" ||
  fail "smoke session is not named cron:loop-smoke-fast: $session_file"
grep -Fq 'state/loop-smoke.md' "$session_file" ||
  fail "smoke session did not touch state/loop-smoke.md: $session_file"
grep -Fq 'trigger=scheduler' "$session_file" ||
  fail "smoke session did not write/read trigger=scheduler: $session_file"
grep -Fq '"name":"update_goal"' "$session_file" ||
  fail "smoke session did not call update_goal: $session_file"
grep -Fq '"status":"complete"' "$session_file" ||
  fail "smoke session does not show goal completion: $session_file"

if [[ -f "$CRON_CONFIG" ]]; then
  task_file="$(task_file_from_config "$CRON_CONFIG")"
  if [[ -f "$task_file" ]] && grep -Fq "$TASK_ID" "$task_file"; then
    fail "temporary smoke task is still present in live task file: $task_file"
  fi
fi
[[ ! -e "$ROOT/state/loop-smoke.md" ]] ||
  fail "temporary smoke state file still exists: $ROOT/state/loop-smoke.md"

printf 'loop smoke canary ok: log=%s session=%s\n' "$log" "$session_file"
