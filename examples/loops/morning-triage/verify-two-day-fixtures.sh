#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

write_state() {
  local root="$1"
  local day1="$2"
  local day2="$3"
  mkdir -p "$root/state"
  cat >"$root/state/triage.md" <<EOF
# Triage State

| date | finding | source | priority | status |
|------|---------|--------|----------|--------|
| $day1 | No actionable findings | fixture | - | closed |
| $day2 | No actionable findings | fixture | - | closed |
EOF
}

valid_session_body() {
  local day="$1"
  cat <<EOF
{"type":"message","message":{"role":"assistant","content":"ran gh run list --status failure; ran gh issue list; ran git log --since=yesterday; read state/triage.md; ran ls ./inbox; wrote triage row for $day"}}
EOF
}

write_log_and_session() {
  local logs="$1"
  local sessions="$2"
  local root="$3"
  local day="$4"
  local session_id="$5"
  local trigger="$6"
  local session_body="$7"

  mkdir -p "$logs/morning-triage" "$sessions/project"
  cat >"$logs/morning-triage/${day}-${session_id}.log" <<EOF
{"type":"run_start","started_at":"${day}T06:00:00+08:00","trigger":"$trigger","timezone":"Asia/Shanghai","has_goal":true,"goal":"Triage and update state/triage.md","goal_verifier":true}
{"type":"session_start","session_id":"$session_id"}
{"type":"run_end","status":"ok","goal_status":"complete"}
EOF
  cat >"$sessions/project/$session_id.jsonl" <<EOF
{"type":"session_info","name":"cron:morning-triage","cwd":"$root"}
$session_body
EOF
}

run_fixture() {
  local day1="$1"
  local day2="$2"
  local trigger1="$3"
  local trigger2="$4"
  local session_body1="$5"
  local session_body2="$6"
  local tmp
  local rc
  tmp="$(mktemp -d)"
  local session_root1="${7:-$tmp/root}"
  local session_root2="${8:-$tmp/root}"
  write_state "$tmp/root" "$day1" "$day2"
  write_log_and_session "$tmp/logs" "$tmp/sessions" "$session_root1" "$day1" "session-one" "$trigger1" "$session_body1"
  write_log_and_session "$tmp/logs" "$tmp/sessions" "$session_root2" "$day2" "session-two" "$trigger2" "$session_body2"
  REQUIRE_SCHEDULER_TRIGGER=1 MODU_CRON_LOG_ROOT="$tmp/logs" MODU_SESSION_ROOT="$tmp/sessions" \
    bash "$SCRIPT_DIR/verify-two-day.sh" "$tmp/root" || rc=$?
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

DAY1="${FIXTURE_DAY1:-2026-07-06}"
DAY2="${FIXTURE_DAY2:-2026-07-07}"
VALID1="$(valid_session_body "$DAY1")"
VALID2="$(valid_session_body "$DAY2")"

run_fixture "$DAY1" "$DAY2" "scheduler" "scheduler" "$VALID1" "$VALID2"

expect_fail "manual trigger rejected in strict mode" \
  'strict natural-cron acceptance needs two scheduler-triggered' \
  "$DAY1" "$DAY2" "manual" "scheduler" "$VALID1" "$VALID2"

expect_fail "same run date rejected" \
  'strict natural-cron acceptance needs two distinct run dates' \
  "$DAY1" "$DAY1" "scheduler" "scheduler" "$VALID1" "$VALID1"

expect_fail "missing issue scan rejected" \
  'session did not scan issues' \
  "$DAY1" "$DAY2" "scheduler" "scheduler" \
  "{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":\"ran gh run list --status failure; ran git log --since=yesterday; read state/triage.md; ran ls ./inbox; wrote triage row for $DAY1\"}}" \
  "$VALID2"

expect_fail "wrong session cwd rejected" \
  'session cwd is not this repo' \
  "$DAY1" "$DAY2" "scheduler" "scheduler" "$VALID1" "$VALID2" \
  "/tmp/other-repo" \
  "/tmp/other-repo"

printf 'two-day verifier fixtures ok: verifier=%s\n' "$SCRIPT_DIR/verify-two-day.sh"
