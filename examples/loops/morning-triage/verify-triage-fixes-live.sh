#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
STATE_FILE="$ROOT/state/triage.md"
SESSION_ROOT="${MODU_SESSION_ROOT:-$HOME/.modu/sessions}"
SKIP_GH_PR_CHECK="${SKIP_GH_PR_CHECK:-0}"
GITHUB_REPO_SLUG="${GITHUB_REPO_SLUG:-}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_file() {
  [[ -f "$1" ]] || fail "missing file: $1"
}

github_repo_slug() {
  local root="$1"
  local remote
  remote="$(git -C "$root" remote get-url origin 2>/dev/null || true)"
  case "$remote" in
    git@github.com:*.git)
      remote="${remote#git@github.com:}"
      printf '%s\n' "${remote%.git}"
      ;;
    git@github.com:*)
      printf '%s\n' "${remote#git@github.com:}"
      ;;
    https://github.com/*.git)
      remote="${remote#https://github.com/}"
      printf '%s\n' "${remote%.git}"
      ;;
    https://github.com/*)
      printf '%s\n' "${remote#https://github.com/}"
      ;;
  esac
}

need_file "$STATE_FILE"
[[ -d "$SESSION_ROOT" ]] || fail "missing session root: $SESSION_ROOT"

if [[ -z "$GITHUB_REPO_SLUG" ]]; then
  GITHUB_REPO_SLUG="$(github_repo_slug "$ROOT")"
fi
[[ "$GITHUB_REPO_SLUG" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] ||
  fail "cannot determine GitHub repo slug; set GITHUB_REPO_SLUG=owner/repo"
PR_PREFIX="https://github.com/$GITHUB_REPO_SLUG/pull/"

file_has_state_pr_link() {
  local file="$1"
  local pr
  for pr in "${state_pr_links[@]}"; do
    if grep -Fq "$pr" "$file"; then
      return 0
    fi
  done
  return 1
}

is_triage_fixes_session() {
  local file="$1"
  grep -Fq "\"cwd\":\"$ROOT\"" "$file" &&
    (
      grep -Fq '"name":"workflow:triage-fixes"' "$file" ||
        grep -Fq 'workflow named triage-fixes' "$file" ||
        grep -Fq '"name":"triage-fixes"' "$file"
    )
}

is_triage_fixes_snapshot() {
  local file="$1"
  [[ "$(basename "$file")" == "snapshot.json" ]] &&
    grep -Fq '"name": "triage-fixes"' "$file" &&
    grep -Fq "$ROOT" "$file"
}

created_pr_links_from_file() {
  local file="$1"
  if [[ "$(basename "$file")" == "snapshot.json" ]]; then
    if command -v jq >/dev/null 2>&1; then
      jq -r '
        .agents[]?.recentToolCalls[]?
        | select((.argsPreview // "") | contains("gh pr create --draft"))
        | (.resultPreview // "")
      ' "$file"
    else
      awk '
        /"argsPreview": .*gh pr create --draft/ {window = 8}
        window > 0 {print; window--}
      ' "$file"
    fi
  else
    grep -F 'gh pr create --draft' "$file" || true
  fi |
    grep -Eho 'https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[0-9]+' ||
    true
}

mapfile -t state_pr_links < <(
  awk -F '|' '
    function trim(s) {
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
      return s
    }
    /^\|/ {
      status = tolower(trim($(NF - 1)))
      if (status == "pr-open") {
        print
      }
    }
  ' "$STATE_FILE" |
    grep -Eho 'https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/pull/[0-9]+' |
    awk -v prefix="$PR_PREFIX" 'index($0, prefix) == 1' |
    awk '!seen[$0]++'
)
[[ "${#state_pr_links[@]}" -gt 0 ]] ||
  fail "state/triage.md needs at least one pr-open row carrying a $GITHUB_REPO_SLUG draft PR URL"

mapfile -t candidate_snapshots < <(
  find "$SESSION_ROOT" -type f -name 'snapshot.json' -print 2>/dev/null
)
mapfile -t candidate_files < <(
  find "$SESSION_ROOT" -type f \( -name '*.jsonl' -o -name '*.json' \) -print 2>/dev/null
)
mapfile -t evidence_files < <(
  for file in "${candidate_snapshots[@]}"; do
    if is_triage_fixes_snapshot "$file" &&
      grep -Eq 'adversarial code reviewer|ASSUME the work is|review:' "$file" &&
      grep -Fq 'gh pr create --draft' "$file" &&
      grep -Fq 'state/triage.md' "$file"; then
      printf '%s\n' "$file"
    fi
  done
)
if [[ "${#evidence_files[@]}" -eq 0 ]]; then
  mapfile -t evidence_files < <(
    for file in "${candidate_files[@]}"; do
      if is_triage_fixes_session "$file" &&
        grep -Eq 'adversarial code reviewer|ASSUME the work is|review:' "$file" &&
        grep -Fq 'gh pr create --draft' "$file" &&
        grep -Fq 'state/triage.md' "$file"; then
        printf '%s\n' "$file"
      fi
    done
  )
fi
[[ "${#evidence_files[@]}" -gt 0 ]] ||
  fail "missing triage-fixes workflow session evidence in this repo with reviewer, draft PR, state/triage.md update path, and the state PR URL"

mapfile -t created_pr_links < <(
  for file in "${evidence_files[@]}"; do
    created_pr_links_from_file "$file"
  done |
    awk -v prefix="$PR_PREFIX" 'index($0, prefix) == 1' |
    awk '!seen[$0]++'
)
[[ "${#created_pr_links[@]}" -gt 0 ]] ||
  fail "triage-fixes evidence includes a draft PR command but no created PR URL"

mapfile -t pr_links < <(
  awk 'NR == FNR {state[$0] = 1; next} state[$0]' \
    <(printf '%s\n' "${state_pr_links[@]}") \
    <(printf '%s\n' "${created_pr_links[@]}") |
    awk '!seen[$0]++'
)
[[ "${#pr_links[@]}" -gt 0 ]] ||
  fail "triage-fixes evidence does not include a created draft PR URL persisted in state/triage.md"

file_has_persisted_created_pr_link() {
  local file="$1"
  local pr
  for pr in "${pr_links[@]}"; do
    if grep -Fq "$pr" "$file"; then
      return 0
    fi
  done
  return 1
}

mapfile -t state_evidence_files < <(
  for file in "${evidence_files[@]}"; do
    if file_has_persisted_created_pr_link "$file" &&
      grep -Eq 'adversarial code reviewer|ASSUME the work is|review:' "$file" &&
      grep -Fq 'gh pr create --draft' "$file" &&
      grep -Fq 'state/triage.md' "$file"; then
      printf '%s\n' "$file"
    fi
  done
)
[[ "${#state_evidence_files[@]}" -gt 0 ]] ||
  fail "triage-fixes evidence does not include the draft PR URL persisted in state/triage.md"

if [[ "$SKIP_GH_PR_CHECK" == "1" ]]; then
  printf 'triage-fixes live evidence ok (offline PR check skipped): pr=%s\n' "${pr_links[0]}"
  exit 0
fi

command -v gh >/dev/null 2>&1 || fail "gh CLI is required to verify draft PR status"

for pr in "${pr_links[@]}"; do
  status="$(gh pr view "$pr" --json isDraft,state --jq '.state + " " + (.isDraft|tostring)' 2>/dev/null || true)"
  if [[ "$status" == "OPEN true" ]]; then
    printf 'triage-fixes live evidence ok: draft PR waiting for review: %s\n' "$pr"
    exit 0
  fi
done

fail "no extracted PR URL is currently an open draft PR"
