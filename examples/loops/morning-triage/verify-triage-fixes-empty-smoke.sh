#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
SESSION_ROOT="${MODU_SESSION_ROOT:-$HOME/.modu/sessions}"
RUN_ID="${TRIAGE_FIXES_EMPTY_RUN_ID:-20260704T030434.979690000Z}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_file() {
  [[ -f "$1" ]] || fail "missing file: $1"
}

workflow_run_dir() {
  find "$SESSION_ROOT" -path "*/extensions/workflow/runs/$RUN_ID" -type d -print -quit 2>/dev/null
}

workflow_session_file() {
  local run_dir="$1"
  local file
  while IFS= read -r file; do
    if head -n 1 "$file" | grep -Fq "\"cwd\":\"$ROOT\"" &&
      grep -Fq '"name":"workflow"' "$file" &&
      grep -Fq "$RUN_ID" "$file" &&
      grep -Fq '"name":"triage-fixes"' "$file"; then
      printf '%s\n' "$file"
      return 0
    fi
  done < <(find "$(dirname "$(dirname "$(dirname "$(dirname "$run_dir")")")")" -maxdepth 1 -type f -name '*.jsonl' -print 2>/dev/null)
}

run_dir="$(workflow_run_dir)"
[[ -n "$run_dir" ]] || fail "missing triage-fixes empty workflow run: $RUN_ID"

status_file="$run_dir/status.json"
snapshot_file="$run_dir/snapshot.json"
script_file="$run_dir/script.js"
need_file "$snapshot_file"
need_file "$script_file"
session_file="$(workflow_session_file "$run_dir")"
[[ -n "$session_file" ]] ||
  fail "missing parent session that started triage-fixes workflow run $RUN_ID from $ROOT"

if [[ -f "$status_file" ]]; then
  grep -Fq '"status": "completed"' "$status_file" ||
    fail "workflow run is not completed: $status_file"
else
  grep -Fq 'Workflow triage-fixes completed' "$session_file" ||
    fail "workflow run has no status.json and parent session does not prove completion: $session_file"
  grep -Fq '"isError":false' "$session_file" ||
    fail "workflow run has no status.json and parent session does not prove a successful tool result: $session_file"
fi
grep -Fq '"name": "triage-fixes"' "$snapshot_file" ||
  fail "snapshot is not for triage-fixes: $snapshot_file"
grep -Fq '"currentPhase": "Load"' "$snapshot_file" ||
  fail "empty smoke should stop after Load phase: $snapshot_file"
grep -Fq '"0 open finding(s)"' "$snapshot_file" ||
  fail "snapshot does not prove empty open-finding queue: $snapshot_file"
grep -Fq '"label": "load-findings"' "$snapshot_file" ||
  fail "snapshot missing load-findings agent: $snapshot_file"
grep -Fq 'Read only ./state/triage.md' "$snapshot_file" ||
  fail "load-findings prompt is not constrained to state-only loading: $snapshot_file"
grep -Fq '"toolName": "read"' "$snapshot_file" ||
  fail "load-findings agent did not read state: $snapshot_file"
grep -Fq '"argsPreview": "{\"path\":\"./state/triage.md\"}"' "$snapshot_file" ||
  fail "load-findings agent did not read ./state/triage.md: $snapshot_file"
if grep -Eq '"toolName": "(grep|ls|find|bash)"' "$snapshot_file"; then
  fail "load-findings used discovery tools during empty smoke: $snapshot_file"
fi
grep -Fq '"resultPreview": "{\"findings\":[]}"' "$snapshot_file" ||
  fail "load-findings result was not an empty findings array: $snapshot_file"
grep -Fq '"fixed": []' "$snapshot_file" ||
  fail "workflow result does not show zero fixed findings: $snapshot_file"
grep -Fq '"rejected": []' "$snapshot_file" ||
  fail "workflow result does not show zero rejected findings: $snapshot_file"
grep -Fq 'state/triage.md' "$script_file" ||
  fail "workflow script does not load state/triage.md: $script_file"

if grep -Fq 'gh pr create --draft' "$snapshot_file"; then
  fail "empty queue smoke unexpectedly includes draft PR creation evidence: $snapshot_file"
fi

printf 'triage-fixes empty smoke ok: run=%s snapshot=%s session=%s\n' "$RUN_ID" "$snapshot_file" "$session_file"
