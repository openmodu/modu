#!/usr/bin/env bash
set -uo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

failures=0

run_check() {
  local name="$1"
  shift
  local out
  out="$(mktemp)"
  printf '\n== %s ==\n' "$name"
  if "$@" >"$out" 2>&1; then
    cat "$out"
    rm -f "$out"
    printf 'OK: %s\n' "$name"
    return 0
  else
    local rc=$?
    cat "$out"
    rm -f "$out"
    printf 'FAIL: %s (rc=%d)\n' "$name" "$rc" >&2
    failures=$((failures + 1))
    return 0
  fi
}

run_check "installed loop readiness" \
  bash "$ROOT/examples/loops/morning-triage/verify-loop-readiness.sh" "$ROOT"

run_check "embedded scheduler armed" \
  env MODU_CRON_TMUX_SESSION="${MODU_CRON_TMUX_SESSION:-modu-loop-cron}" \
    bash "$ROOT/examples/loops/morning-triage/verify-scheduler-running.sh" "$ROOT"

run_check "next natural run windows" \
  bash "$ROOT/examples/loops/morning-triage/verify-next-run-windows.sh" "$ROOT"

run_check "next natural run window fixtures" \
  bash "$ROOT/examples/loops/morning-triage/verify-next-run-windows-fixtures.sh"

run_check "high-frequency loop smoke canary" \
  bash "$ROOT/examples/loops/morning-triage/verify-loop-smoke-canary.sh" "$ROOT"

run_check "high-frequency loop smoke canary fixtures" \
  bash "$ROOT/examples/loops/morning-triage/verify-loop-smoke-canary-fixtures.sh"

run_check "triage-fixes empty queue smoke" \
  bash "$ROOT/examples/loops/morning-triage/verify-triage-fixes-empty-smoke.sh" "$ROOT"

run_check "triage-fixes and human-review notification contract" \
  bash "$ROOT/examples/loops/morning-triage/verify-triage-fixes-contract.sh" "$ROOT"

run_check "triage-fixes live verifier fixtures" \
  bash "$ROOT/examples/loops/morning-triage/verify-triage-fixes-live-fixtures.sh"

run_check "triage-fixes live draft PR evidence" \
  bash "$ROOT/examples/loops/morning-triage/verify-triage-fixes-live.sh" "$ROOT"

run_check "A-stock workflow state contract" \
  bash "$ROOT/examples/workflows/verify-a-stock-workflow-contract.sh" "$ROOT"

run_check "A-stock loop verifier fixtures" \
  bash "$ROOT/examples/workflows/verify-a-stock-loop-fixtures.sh"

run_check "morning-triage two-day verifier fixtures" \
  bash "$ROOT/examples/loops/morning-triage/verify-two-day-fixtures.sh"

if [[ "${ALLOW_BOOTSTRAP:-0}" == "1" ]]; then
  run_check "morning-triage bootstrap two-day evidence" \
    bash "$ROOT/examples/loops/morning-triage/verify-two-day.sh" "$ROOT"
else
  run_check "morning-triage strict natural-cron evidence" \
    env REQUIRE_SCHEDULER_TRIGGER=1 bash "$ROOT/examples/loops/morning-triage/verify-two-day.sh" "$ROOT"
fi

run_check "A-stock real cron watchlist loop" \
  bash "$ROOT/examples/workflows/verify-a-stock-loop.sh" "$ROOT"

if [[ "$failures" -gt 0 ]]; then
  printf '\n%d live-loop check(s) failed.\n' "$failures" >&2
  exit 1
fi

printf '\nall live-loop checks passed\n'
