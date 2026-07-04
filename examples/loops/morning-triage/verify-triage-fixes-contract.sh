#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
FILES=(
  "$ROOT/examples/loops/morning-triage/triage-fixes.workflow.js"
  "$ROOT/.coding_agent/workflows/triage-fixes.js"
)

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
  grep -Fq -- "$needle" "$file" || fail "$message in $file"
}

for file in "${FILES[@]}"; do
  need_file "$file"
  need_text "$file" "Read only ./state/triage.md" "Load phase must only parse persisted triage state"
  need_text "$file" "tools: ['read']" "Load phase must not fan out into repository discovery"
  need_text "$file" "isolation: 'worktree'" "handoff must isolate fixes in worktrees"
  need_text "$file" "ASSUME the work is " "reviewer must be adversarial"
  need_text "$file" "schema: VERDICT_SCHEMA" "reviewer must return a structured verdict"
  need_text "$file" "gh pr create --draft" "PASS path must open a draft PR"
  need_text "$file" "git push -u origin" "PASS path must push the fix branch before opening a PR"
  need_text "$file" "--base feat/loop" "PASS path must specify a non-interactive PR base"
  need_text "$file" "tools: ['read', 'bash']" "Deliver phase must only use read/bash"
  need_text "$file" "NEVER merge" "workflow must forbid auto-merge"
  need_text "$file" "./inbox/" "REJECT path must write an inbox item"
  need_text "$file" "status pr-open" "PASS path must update triage state"
  need_text "$file" "Capture the PR URL" "PASS path must capture the draft PR URL"
  need_text "$file" "same row" "PASS path must persist the draft PR URL in the triage state row"
  need_text "$file" "findings.slice(0, 3)" "workflow must cap batch size for human review"
done

need_file "$ROOT/pkg/cron/notify/notify.go"
need_file "$ROOT/pkg/cron/notify/notify_test.go"
need_text "$ROOT/pkg/cron/notify/notify.go" "InboxNew []string" "completion notification must surface new inbox items"
need_text "$ROOT/pkg/cron/notify/notify.go" "PRLinks []string" "completion notification must surface draft PR links"
need_text "$ROOT/pkg/cron/notify/notify.go" "pr: %s" "notification text must print PR links"
need_text "$ROOT/pkg/cron/notify/notify.go" "inbox: %d new item(s) waiting for you" "notification text must print new inbox items"
need_text "$ROOT/pkg/cron/notify/notify_test.go" "TestBuildPayloadSurfacesInboxAndPRLinks" "notification contract must have unit coverage"

printf 'triage-fixes + human-review notification contract ok\n'
