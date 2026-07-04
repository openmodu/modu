#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_SLUG="${GITHUB_REPO_SLUG:-openmodu/modu}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

write_state() {
  local root="$1"
  local status="$2"
  local pr_url="${3-https://github.com/${REPO_SLUG}/pull/123}"
  mkdir -p "$root/state"
  cat >"$root/state/triage.md" <<EOF
# Triage State

| date | finding | source | priority | status |
|------|---------|--------|----------|--------|
| 2026-07-03 | Fix fixture failure | fixture CI $pr_url | P1 | $status |
EOF
}

valid_evidence_body() {
  cat <<EOF
{"type":"message","message":{"role":"assistant","content":"ROLE: adversarial code reviewer. ASSUME the work is BROKEN until proven otherwise. review:fix-fixture PASS. Deliver step ran gh pr create --draft --head fix/fixture --title fix:fixture. Updated state/triage.md to pr-open. PR: https://github.com/${REPO_SLUG}/pull/123"}}
EOF
}

write_evidence() {
  local sessions="$1"
  local body="$2"
  local session_name="${3:-workflow:triage-fixes}"
  local cwd="${4:-$CURRENT_FIXTURE_ROOT}"
  mkdir -p "$sessions/project"
  cat >"$sessions/project/fixture-session.jsonl" <<EOF
{"type":"session_info","name":"$session_name","cwd":"$cwd"}
$body
EOF
}

run_fixture() {
  local status="$1"
  local evidence_body="$2"
  local state_pr_url="${3-https://github.com/${REPO_SLUG}/pull/123}"
  local session_name="${4:-workflow:triage-fixes}"
  local cwd_override="${5:-}"
  local tmp
  local rc
  tmp="$(mktemp -d)"
  CURRENT_FIXTURE_ROOT="$tmp/root"
  write_state "$tmp/root" "$status" "$state_pr_url"
  write_evidence "$tmp/sessions" "$evidence_body" "$session_name" "${cwd_override:-$tmp/root}"
  mkdir -p "$tmp/logs"
  SKIP_GH_PR_CHECK=1 GITHUB_REPO_SLUG="$REPO_SLUG" \
    MODU_SESSION_ROOT="$tmp/sessions" MODU_CRON_LOG_ROOT="$tmp/logs" \
    bash "$SCRIPT_DIR/verify-triage-fixes-live.sh" "$tmp/root" || rc=$?
  rm -rf "$tmp"
  return "${rc:-0}"
}

expect_fail() {
  local name="$1"
  local status="$2"
  local evidence_body="$3"
  local message_pattern="$4"
  local state_pr_url="${5-https://github.com/${REPO_SLUG}/pull/123}"
  local session_name="${6:-workflow:triage-fixes}"
  local cwd_override="${7:-}"
  local out
  out="$(mktemp)"
  if run_fixture "$status" "$evidence_body" "$state_pr_url" "$session_name" "$cwd_override" >"$out" 2>&1; then
    cat "$out"
    rm -f "$out"
    fail "$name unexpectedly passed"
  fi
  if ! grep -Eq "$message_pattern" "$out"; then
    cat "$out"
    rm -f "$out"
    fail "$name failed for the wrong reason; expected pattern: $message_pattern"
  fi
  printf 'fixture rejected as expected: %s\n' "$name"
  rm -f "$out"
}

run_fixture "pr-open" "$(valid_evidence_body)"
expect_fail "missing pr-open state row" \
  "open" \
  "$(valid_evidence_body)" \
  'state/triage\.md needs at least one pr-open row carrying'
expect_fail "missing PR URL in pr-open state row" \
  "pr-open" \
  "$(valid_evidence_body)" \
  'state/triage\.md needs at least one pr-open row carrying' \
  ""
expect_fail "created PR not persisted in state" \
  "pr-open" \
  "$(valid_evidence_body)" \
  'created draft PR URL persisted in state/triage\.md' \
  "https://github.com/${REPO_SLUG}/pull/122"
expect_fail "missing reviewer evidence" \
  "pr-open" \
  "{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":\"Deliver step ran gh pr create --draft. Updated state/triage.md. PR: https://github.com/${REPO_SLUG}/pull/123\"}}" \
  'missing triage-fixes workflow session evidence'
expect_fail "missing draft PR command" \
  "pr-open" \
  "{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":\"ROLE: adversarial code reviewer. ASSUME the work is BROKEN. review:fix-fixture PASS. Updated state/triage.md. PR: https://github.com/${REPO_SLUG}/pull/123\"}}" \
  'missing triage-fixes workflow session evidence'
expect_fail "wrong repository PR URL" \
  "pr-open" \
  "{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":\"ROLE: adversarial code reviewer. ASSUME the work is BROKEN. review:fix-fixture PASS. Deliver step ran gh pr create --draft. Updated state/triage.md. PR: https://github.com/other/repo/pull/123\"}}" \
  'draft PR command but no created PR URL'
expect_fail "wrong workflow session" \
  "pr-open" \
  "$(valid_evidence_body)" \
  'missing triage-fixes workflow session evidence' \
  "https://github.com/${REPO_SLUG}/pull/123" \
  "cron:morning-triage"
expect_fail "wrong workflow cwd" \
  "pr-open" \
  "$(valid_evidence_body)" \
  'missing triage-fixes workflow session evidence' \
  "https://github.com/${REPO_SLUG}/pull/123" \
  "workflow:triage-fixes" \
  "/tmp/other-repo"

printf 'triage-fixes live fixtures ok: repo=%s verifier=%s\n' "$REPO_SLUG" "$SCRIPT_DIR/verify-triage-fixes-live.sh"
