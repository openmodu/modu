#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
CRON_CONFIG="${MODU_CRON_CONFIG:-$HOME/.modu/cron/config.yaml}"
DAEMON_LOG="${MODU_CRON_DAEMON_LOG:-$(dirname "$CRON_CONFIG")/daemon.log}"
TMUX_SESSION="${MODU_CRON_TMUX_SESSION:-}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

find_modu_code_pid_in_tree() {
  local root_pid="$1"
  local frontier next pid cmd child
  frontier="$root_pid"
  for _ in 1 2 3 4 5 6; do
    [[ -n "$frontier" ]] || return 1
    next=""
    for pid in $frontier; do
      cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
      if printf '%s' "$cmd" | grep -Eq 'go run ./cmd/modu_code|/exe/modu_code'; then
        printf '%s\n' "$pid"
        return 0
      fi
      while IFS= read -r child; do
        [[ -n "$child" ]] || continue
        next="${next}${next:+ }$child"
      done < <(pgrep -P "$pid" 2>/dev/null || true)
    done
    frontier="$next"
  done
  return 1
}

newest_go_source_epoch() {
  if stat -f '%m' "$ROOT/pkg/cron/runner/runner.go" >/dev/null 2>&1; then
    find "$ROOT/cmd" "$ROOT/pkg" -type f -name '*.go' -print0 |
      xargs -0 stat -f '%m' |
      sort -nr |
      head -n 1
  else
    find "$ROOT/cmd" "$ROOT/pkg" -type f -name '*.go' -print0 |
      xargs -0 stat -c '%Y' |
      sort -nr |
      head -n 1
  fi
}

file_mtime_epoch() {
  local file="$1"
  if stat -f '%m' "$file" >/dev/null 2>&1; then
    stat -f '%m' "$file"
  else
    stat -c '%Y' "$file"
  fi
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

process_start_epoch() {
  local pid="$1"
  local started
  started="$(ps -p "$pid" -o lstart= 2>/dev/null || true)"
  [[ -n "$started" ]] || fail "cannot read process start time for pid=$pid"
  python3 - "$started" <<'PY'
import sys
import time

raw = " ".join(sys.argv[1].split())
try:
    parsed = time.strptime(raw, "%a %b %d %H:%M:%S %Y")
except ValueError as exc:
    print(f"cannot parse process start time {raw!r}: {exc}", file=sys.stderr)
    sys.exit(1)
print(int(time.mktime(parsed)))
PY
}

latest_scheduler_load_epoch() {
  python3 - "$DAEMON_LOG" <<'PY'
import datetime
import re
import sys

path = sys.argv[1]
latest = None
pattern = re.compile(r"^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) ")
with open(path, encoding="utf-8", errors="replace") as fh:
    current = []
    for line in fh:
        if "cron scheduler shutting down" in line:
            current = []
            continue
        current.append(line.rstrip("\n"))
for line in current:
    if "loaded " not in line and "reloaded:" not in line:
        continue
    match = pattern.match(line)
    if not match:
        continue
    parsed = datetime.datetime.strptime(match.group(1), "%Y/%m/%d %H:%M:%S")
    latest = int(parsed.timestamp())
if latest is not None:
    print(latest)
PY
}

assert_scheduler_fresh_for_go_sources() {
  local pid="$1"
  local newest_source started
  newest_source="$(newest_go_source_epoch)"
  [[ -n "$newest_source" ]] || fail "cannot find Go source files under $ROOT/cmd or $ROOT/pkg"
  started="$(process_start_epoch "$pid")" || fail "cannot parse process start time for pid=$pid"
  [[ "$started" -ge "$newest_source" ]] ||
    fail "modu_code scheduler process pid=$pid started before the newest Go source edit; restart $TMUX_SESSION before trusting future cron ticks"
}

assert_scheduler_fresh_for_cron_files() {
  local task_file newest_config loaded
  task_file="$(task_file_from_config "$CRON_CONFIG")"
  [[ -f "$task_file" ]] || fail "missing cron task file: $task_file"
  newest_config="$(printf '%s\n%s\n' "$(file_mtime_epoch "$CRON_CONFIG")" "$(file_mtime_epoch "$task_file")" | sort -nr | head -n 1)"
  [[ -n "$newest_config" ]] || fail "cannot determine cron config/task file mtimes"
  loaded="$(latest_scheduler_load_epoch)"
  [[ -n "$loaded" ]] || fail "current cron scheduler lifecycle has no task load/reload timestamp"
  [[ "$loaded" -ge "$newest_config" ]] ||
    fail "cron scheduler loaded tasks before the latest cron config/task edit; wait for reload or restart $TMUX_SESSION before trusting future cron ticks"
}

[[ -f "$CRON_CONFIG" ]] || fail "missing cron config: $CRON_CONFIG"
[[ -f "$DAEMON_LOG" ]] || fail "missing cron daemon log: $DAEMON_LOG"

modu_code_pid=""
if [[ -n "$TMUX_SESSION" ]]; then
  tmux has-session -t "$TMUX_SESSION" 2>/dev/null || fail "tmux session is not running: $TMUX_SESSION"
  found_pane=0
  while IFS='|' read -r pane_path pane_pid; do
    [[ "$pane_path" == "$ROOT" ]] || continue
    if modu_code_pid="$(find_modu_code_pid_in_tree "$pane_pid")"; then
      found_pane=1
      break
    fi
  done < <(tmux list-panes -t "$TMUX_SESSION" -F '#{pane_current_path}|#{pane_pid}')
  [[ "$found_pane" == "1" ]] ||
    fail "tmux session $TMUX_SESSION is not running modu_code from this repo: $ROOT"
else
  modu_code_pid="$(pgrep -f 'go run ./cmd/modu_code|/exe/modu_code' | head -n 1 || true)"
  [[ -n "$modu_code_pid" ]] ||
    fail "no interactive modu_code process found; embedded scheduler only runs while modu_code TUI is alive"
fi
assert_scheduler_fresh_for_go_sources "$modu_code_pid"
assert_scheduler_fresh_for_cron_files

last_lifecycle="$(
  awk '/cron scheduler started/ {event="started"} /cron scheduler shutting down/ {event="stopped"} END {print event}' "$DAEMON_LOG"
)"
[[ "$last_lifecycle" == "started" ]] || fail "cron daemon log does not show scheduler currently started"

current_lifecycle_log="$(
  awk '
    /cron scheduler shutting down/ {buf=""; next}
    {buf = buf $0 "\n"}
    END {printf "%s", buf}
  ' "$DAEMON_LOG"
)"
printf '%s' "$current_lifecycle_log" | grep -q 'loaded 2 task(s)' ||
  fail "current cron scheduler lifecycle did not load the morning loop task set"
printf '%s' "$current_lifecycle_log" | grep -q 'cron scheduler started' ||
  fail "current cron scheduler lifecycle does not show started"

printf 'scheduler running ok: config=%s log=%s\n' "$CRON_CONFIG" "$DAEMON_LOG"
