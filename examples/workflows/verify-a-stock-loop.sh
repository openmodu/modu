#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TASK_ID="${TASK_ID:-morning-market-daily}"
LOG_ROOT="${MODU_CRON_LOG_ROOT:-$HOME/.modu/cron/logs}"
TASK_LOG_DIR="$LOG_ROOT/$TASK_ID"
SESSION_ROOT="${MODU_SESSION_ROOT:-$HOME/.modu/sessions}"
STATE_FILE="${WATCHLIST_STATE_FILE:-$ROOT/state/watchlist.md}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_file() {
  [[ -f "$1" ]] || fail "missing file: $1"
}

latest_watchlist_date() {
  awk '
    /^## 最新日期[[:space:]]*$/ {getline; gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); print; exit}
  ' "$STATE_FILE"
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

run_date_from_start_line() {
  local start_line="$1"
  printf '%s\n' "$start_line" | sed -E 's/.*"started_at":"(20[0-9]{2}-[0-9]{2}-[0-9]{2})T.*/\1/'
}

bash "$SCRIPT_DIR/verify-a-stock-watchlist.sh" "$ROOT"
need_file "$STATE_FILE"
[[ -d "$TASK_LOG_DIR" ]] || fail "missing cron log dir: $TASK_LOG_DIR"
[[ -d "$SESSION_ROOT" ]] || fail "missing session root: $SESSION_ROOT"

watchlist_date="$(latest_watchlist_date)"
[[ "$watchlist_date" =~ ^20[0-9]{2}-[0-9]{2}-[0-9]{2}$ ]] || fail "cannot parse latest watchlist date from $STATE_FILE"

mapfile -t logs < <(
  while IFS= read -r log; do
    start_line="$(head -n 1 "$log")"
    tail_line="$(tail -n 1 "$log")"
    run_date="$(run_date_from_start_line "$start_line")"
    if [[ "$run_date" == "$watchlist_date" ]] &&
      printf '%s' "$start_line" | grep -q '"type":"run_start"' &&
      printf '%s' "$start_line" | grep -q '"trigger":"scheduler"' &&
      printf '%s' "$start_line" | grep -q '"timezone":"Asia/Shanghai"' &&
      printf '%s' "$start_line" | grep -q '"has_goal":true' &&
      printf '%s' "$start_line" | grep -q '"goal":' &&
      printf '%s' "$start_line" | grep -Fq 'state/watchlist.md' &&
      printf '%s' "$start_line" | grep -q '"goal_verifier":true' &&
      printf '%s' "$tail_line" | grep -q '"type":"run_end"' &&
      printf '%s' "$tail_line" | grep -q '"status":"ok"' &&
      printf '%s' "$tail_line" | grep -q '"goal_status":"complete"'; then
      printf '%s\n' "$log"
    fi
  done < <(find "$TASK_LOG_DIR" -type f -name '*.log' -print | sort) | tail -n 1
)
[[ "${#logs[@]}" -ge 1 ]] || fail "need a scheduler-triggered, Asia/Shanghai, verifier-enabled, goal-complete $TASK_ID log with goal mentioning state/watchlist.md for watchlist date $watchlist_date"

log="${logs[0]}"
session_id="$(session_id_from_log "$log")"
session_file="$(session_file_for_id "$session_id")"
[[ -n "$session_file" ]] || fail "missing full session JSONL for session_id=$session_id from $log"
grep -q '"type":"session_info"' "$session_file" || fail "session lacks session_info: $session_file"
grep -q "\"name\":\"cron:$TASK_ID\"" "$session_file" || fail "session is not named cron:$TASK_ID: $session_file"
grep -Fq "\"cwd\":\"$ROOT\"" "$session_file" || fail "session cwd is not this repo: $session_file"
grep -Fq "$watchlist_date" "$session_file" || fail "session does not mention watchlist date $watchlist_date: $session_file"
grep -Fq 'state/watchlist.md' "$session_file" || fail "session did not read/write state/watchlist.md: $session_file"
grep -Eq 'qt\.gtimg\.cn|push2\.eastmoney\.com|10jqka|hexin\.cn' "$session_file" ||
  fail "session lacks direct market data source evidence: $session_file"
grep -Eq '更新watchlist|写回 state/watchlist\.md|今日发现的题材' "$session_file" ||
  fail "session lacks watchlist update evidence: $session_file"
grep -Eq '读回 state/watchlist\.md|写完后读回 state/watchlist\.md|确认标题|确认.*最新日期' "$session_file" ||
  fail "session lacks post-write watchlist read-back evidence: $session_file"

printf 'a-stock loop evidence ok: state=%s date=%s log=%s session=%s\n' "$STATE_FILE" "$watchlist_date" "$log" "$session_file"
