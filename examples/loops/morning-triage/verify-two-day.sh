#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
TASK_ID="${TASK_ID:-morning-triage}"
LOG_ROOT="${MODU_CRON_LOG_ROOT:-$HOME/.modu/cron/logs}"
TASK_LOG_DIR="$LOG_ROOT/$TASK_ID"
SESSION_ROOT="${MODU_SESSION_ROOT:-$HOME/.modu/sessions}"
STATE_FILE="$ROOT/state/triage.md"
INBOX_DIR="$ROOT/inbox"
MANUAL_DAY1_SESSION="${MANUAL_DAY1_SESSION:-}"
REQUIRE_SCHEDULER_TRIGGER="${REQUIRE_SCHEDULER_TRIGGER:-0}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_file() {
  [[ -f "$1" ]] || fail "missing file: $1"
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

check_session_discovery_inputs() {
  local session="$1"
  grep -Fq 'gh run list --status failure' "$session" || fail "session did not scan failed CI: $session"
  grep -Fq 'gh issue list' "$session" || fail "session did not scan issues: $session"
  grep -Fq 'git log --since=yesterday' "$session" || fail "session did not inspect recent commits: $session"
  grep -Fq 'state/triage.md' "$session" || fail "session did not read/write state/triage.md: $session"
  grep -Fq 'ls ./inbox' "$session" || fail "session did not inspect inbox/: $session"
}

run_date_from_start_line() {
  local start_line="$1"
  printf '%s\n' "$start_line" | sed -E 's/.*"started_at":"(20[0-9]{2}-[0-9]{2}-[0-9]{2})T.*/\1/'
}

check_manual_day1_session() {
  local session="$1"
  need_file "$session"
  head -n 1 "$session" | grep -q '"type":"session"' || fail "manual day-1 file does not look like a session JSONL: $session"
  head -n 1 "$session" | grep -Fq "\"cwd\":\"$ROOT\"" || fail "manual day-1 session cwd is not this repo: $session"
  check_session_discovery_inputs "$session"
}

check_closed_findings_not_reopened() {
  awk -F '|' '
    function trim(s) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
      return s
    }
    function is_separator(s) {
      return s ~ /^[-[:space:]]+$/
    }
    /^\|/ {
      if (NF >= 7) {
        finding = trim($3)
        status = tolower(trim($6))
      } else {
        finding = trim($2)
        status = tolower(trim($5))
      }
      if (finding == "finding" || is_separator(finding) || finding == "") {
        next
      }
      if (status == "closed" || status == "done") {
        closed[finding] = 1
      } else if (status == "open" && closed[finding]) {
        printf "finding reopened after closed/done: %s\n", finding > "/dev/stderr"
        exit 1
      }
    }
  ' "$STATE_FILE" || fail "state/triage.md re-opened an exact finding that was already closed/done"
}

state_has_row_date() {
  local want="$1"
  awk -F '|' -v want="$want" '
    function trim(s) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
      return s
    }
    /^\|/ {
      date = trim($2)
      if (date == want) {
        found = 1
      }
    }
    END { exit found ? 0 : 1 }
  ' "$STATE_FILE"
}

state_row_dates() {
  awk -F '|' '
    function trim(s) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
      return s
    }
    /^\|/ {
      date = trim($2)
      if (date ~ /^20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/) {
        print date
      }
    }
  ' "$STATE_FILE" | sort -u
}

need_file "$STATE_FILE"
[[ -d "$TASK_LOG_DIR" ]] || fail "missing cron log dir: $TASK_LOG_DIR"
[[ -d "$SESSION_ROOT" ]] || fail "missing session root: $SESSION_ROOT"

if [[ "$REQUIRE_SCHEDULER_TRIGGER" == "1" ]]; then
  mapfile -t logs < <(
    while IFS= read -r log; do
      start_line="$(head -n 1 "$log")"
      tail_line="$(tail -n 1 "$log")"
      if printf '%s' "$start_line" | grep -q '"type":"run_start"' &&
        printf '%s' "$start_line" | grep -q '"trigger":"scheduler"' &&
        printf '%s' "$start_line" | grep -q '"timezone":"Asia/Shanghai"' &&
        printf '%s' "$start_line" | grep -q '"has_goal":true' &&
        printf '%s' "$start_line" | grep -q '"goal":' &&
        printf '%s' "$start_line" | grep -Fq 'state/triage.md' &&
        printf '%s' "$start_line" | grep -q '"goal_verifier":true' &&
        printf '%s' "$tail_line" | grep -q '"goal_status":"complete"'; then
        printf '%s\n' "$log"
      fi
    done < <(find "$TASK_LOG_DIR" -type f -name '*.log' -print | sort) | tail -n 2
  )
else
  mapfile -t logs < <(find "$TASK_LOG_DIR" -type f -name '*.log' -print | sort | tail -n 2)
fi
if [[ "$REQUIRE_SCHEDULER_TRIGGER" == "1" && "${#logs[@]}" -lt 2 ]]; then
  fail "strict natural-cron acceptance needs two scheduler-triggered, Asia/Shanghai, verifier-enabled, goal-complete $TASK_ID logs, found ${#logs[@]}"
fi
if [[ "${#logs[@]}" -lt 2 ]]; then
  if [[ -z "$MANUAL_DAY1_SESSION" ]]; then
    fail "need at least two $TASK_ID run logs, found ${#logs[@]}"
  fi
  [[ "${#logs[@]}" -ge 1 ]] || fail "manual day-1 bootstrap still needs at least one $TASK_ID cron log"
  check_manual_day1_session "$MANUAL_DAY1_SESSION"
  printf 'manual day-1 bootstrap session: %s\n' "$MANUAL_DAY1_SESSION"
fi

for log in "${logs[@]}"; do
  start_line="$(head -n 1 "$log")"
  tail_line="$(tail -n 1 "$log")"
  if [[ "$REQUIRE_SCHEDULER_TRIGGER" == "1" ]]; then
    printf '%s' "$start_line" | grep -q '"type":"run_start"' || fail "first line is not run_start: $log"
    printf '%s' "$start_line" | grep -q '"trigger":"scheduler"' || fail "run was not scheduler-triggered: $log"
    printf '%s' "$start_line" | grep -q '"timezone":"Asia/Shanghai"' || fail "run was not scheduled in Asia/Shanghai timezone: $log"
    printf '%s' "$start_line" | grep -q '"has_goal":true' || fail "run did not declare a task goal: $log"
    printf '%s' "$start_line" | grep -q '"goal":' || fail "run did not record the task goal text: $log"
    printf '%s' "$start_line" | grep -Fq 'state/triage.md' || fail "task goal does not mention state/triage.md: $log"
    printf '%s' "$start_line" | grep -q '"goal_verifier":true' || fail "run did not enable the goal verifier: $log"
  fi
  printf 'log: %s\n  %s\n' "$log" "$tail_line"
  printf '%s' "$tail_line" | grep -q '"type":"run_end"' || fail "last line is not run_end: $log"
  printf '%s' "$tail_line" | grep -q '"status":"ok"' || fail "run did not finish ok: $log"
  if [[ "$REQUIRE_SCHEDULER_TRIGGER" == "1" ]]; then
    printf '%s' "$tail_line" | grep -q '"goal_status":"complete"' || fail "task goal did not complete: $log"
  fi
  session_id="$(session_id_from_log "$log")"
  session_file="$(session_file_for_id "$session_id")"
  [[ -n "$session_file" ]] || fail "missing full session JSONL for session_id=$session_id from $log"
  grep -q '"type":"session_info"' "$session_file" || fail "session lacks session_info: $session_file"
  grep -q "\"name\":\"cron:$TASK_ID\"" "$session_file" || fail "session is not named cron:$TASK_ID: $session_file"
  grep -Fq "\"cwd\":\"$ROOT\"" "$session_file" || fail "session cwd is not this repo: $session_file"
  check_session_discovery_inputs "$session_file"
  printf '  session: %s\n' "$session_file"
  run_date="$(run_date_from_start_line "$start_line")"
  [[ "$run_date" =~ ^20[0-9]{2}-[0-9]{2}-[0-9]{2}$ ]] || fail "cannot parse run date from run_start: $log"
  state_has_row_date "$run_date" || fail "state/triage.md has no table row for run date $run_date from $log"
done

if [[ "$REQUIRE_SCHEDULER_TRIGGER" == "1" ]]; then
  mapfile -t log_dates < <(
    for log in "${logs[@]}"; do
      run_date_from_start_line "$(head -n 1 "$log")"
    done | sort -u
  )
  [[ "${#log_dates[@]}" -ge 2 ]] ||
    fail "strict natural-cron acceptance needs two distinct run dates, found ${#log_dates[@]} (${log_dates[*]:-none})"
fi

mapfile -t dates < <(state_row_dates)
[[ "${#dates[@]}" -ge 2 ]] || fail "state/triage.md needs at least two distinct dates, found ${#dates[@]}"
check_closed_findings_not_reopened

printf 'state dates: %s\n' "${dates[*]}"
if [[ -d "$INBOX_DIR" ]]; then
  printf 'inbox entries:\n'
  find "$INBOX_DIR" -maxdepth 1 -type f -name '*.md' -print | sort || true
else
  printf 'inbox entries: none\n'
fi

cat <<'EOF'

Manual checks still required:
- Confirm completed findings were not re-done under new wording.
- Compare the latest notification with inbox/: it should mention only items created in the latest run, not old inbox files.
- If MANUAL_DAY1_SESSION was used, treat this as bootstrap evidence; two scheduled cron logs are still the stricter acceptance.
- For strict natural-cron acceptance, rerun without MANUAL_DAY1_SESSION and with REQUIRE_SCHEDULER_TRIGGER=1 after two logs include run_start trigger=scheduler, timezone=Asia/Shanghai, has_goal=true, goal text mentioning state/triage.md, goal_verifier=true, and run_end goal_status=complete.
EOF
