#!/usr/bin/env bash
# verify-triage-fixes-contract.sh
#
# Validates that the triage-fixes workflow's Deliver phase satisfies:
# 1. PASS path instructs the agent to push the branch first (`git push -u origin`)
# 2. PASS path specifies `--base feat/loop` on the PR creation command
# 3. Deliver agent tools are restricted to `['read', 'bash']`
#
# Usage: ./scripts/verify-triage-fixes-contract.sh
# Exit 0 if all checks pass, 1 otherwise.

set -euo pipefail

WORKFLOW="examples/loops/morning-triage/triage-fixes.workflow.js"
errors=0

check() {
  local label="$1"
  local pattern="$2"
  if grep -qF -e "$pattern" "$WORKFLOW"; then
    echo "PASS  $label"
  else
    echo "FAIL  $label — pattern not found: $pattern"
    errors=$((errors + 1))
  fi
}

echo "=== triage-fixes Deliver phase contract ==="
echo "File: $WORKFLOW"
echo ""

# 1. Must push the branch before creating the PR
check "git push -u origin" "git push -u origin"

# 2. Must specify --base feat/loop in the PR create command
check "--base feat/loop" "--base feat/loop"

# 3. Deliver tools must be restricted to read and bash only
check "Deliver tools: ['read', 'bash']" "tools: ['read', 'bash']"

echo ""
if [ "$errors" -eq 0 ]; then
  echo "✓ All contract checks passed."
  exit 0
else
  echo "✗ $errors contract check(s) failed."
  exit 1
fi
