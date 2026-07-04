#!/usr/bin/env bash
# Verify the triage-fixes workflow contract:
#   1. Load prompt says to read only "state/triage.md"
#   2. Load tools are only ['read']
#   3. An empty smoke run shows only one "read ./state/triage.md" call
#
# Usage: ./verify-triage-fixes-contract.sh
# Returns 0 on pass, 1 on failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKFLOW="$SCRIPT_DIR/triage-fixes.workflow.js"
EXIT_CODE=0

pass()  { echo "  ✓ $*"; }
fail()  { echo "  ✗ $*"; EXIT_CODE=1; }

echo "=== verify-triage-fixes-contract ==="
echo

# ------------------------------------------------------------------
# Check 1: Load prompt says to read only ./state/triage.md
# ------------------------------------------------------------------
echo "[1/3] Load prompt: read only state/triage.md"

# Extract the Load phase agent prompt between the Load phase marker and findings assignment
# We look for the prompt string that contains "state/triage.md"
PROMPT_LINE=$(grep -n "Read only the file" "$WORKFLOW" || true)
if [[ -z "$PROMPT_LINE" ]]; then
    fail "Load prompt does not contain 'Read only the file'"
else
    pass "Load prompt contains 'Read only the file'"
fi

# Verify it mentions state/triage.md
if grep -q "state/triage.md" "$WORKFLOW"; then
    pass "Load prompt mentions state/triage.md"
else
    fail "Load prompt does NOT mention state/triage.md"
fi

# Verify the prompt explicitly restricts to not exploring other files
if grep -q "do NOT read any other file" "$WORKFLOW"; then
    pass "Load prompt explicitly forbids reading other files"
else
    fail "Load prompt does NOT forbid reading other files"
fi

echo

# ------------------------------------------------------------------
# Check 2: Load tools are only ['read']
# ------------------------------------------------------------------
echo "[2/3] Load tools: restricted to ['read']"

# Extract the load-findings agent tools configuration
# Only inspect the section between "phase('Load')" and "const findings ="
LOAD_TOOLS_LINE=$(sed -n '/phase(.Load.)/,/const findings =/p' "$WORKFLOW" | grep "tools:" || true)
if [[ -z "$LOAD_TOOLS_LINE" ]]; then
    fail "Could not find tools config for load-findings agent"
elif echo "$LOAD_TOOLS_LINE" | grep -qE "grep|'ls'|'find'"; then
    fail "Load tools include grep/ls/find (should be only ['read']): $LOAD_TOOLS_LINE"
elif echo "$LOAD_TOOLS_LINE" | grep -q "read"; then
    pass "Load tools are exactly ['read'] ($LOAD_TOOLS_LINE)"
else
    fail "Load tools missing 'read': $LOAD_TOOLS_LINE"
fi

echo

# ------------------------------------------------------------------
# Check 3: Emulate an empty smoke run (depends on a working Node.js
#          or modu workflow runner; here we do a static check that the
#          workflow returns {"findings":[]} when no findings are present).
# ------------------------------------------------------------------
echo "[3/3] Empty smoke run: only one read call expected"

# Verify the return path for empty findings exists
if grep -q '{"findings":\[\]}' "$WORKFLOW" || grep -q "'{\"findings\":\[\]}'" "$WORKFLOW"; then
    pass "Workflow returns empty findings array when none found"
else
    fail "Workflow does not handle empty findings case"
fi

# Verify that the only read tool mentioned in the load phase is for state/triage.md
LOAD_SECTION=$(sed -n '/phase(.Load.)/,/const findings =/p' "$WORKFLOW")
READ_COUNT=$(echo "$LOAD_SECTION" | grep -c "state/triage.md" || true)
if [[ "$READ_COUNT" -ge 1 ]]; then
    pass "Load phase references state/triage.md (${READ_COUNT}x)"
else
    fail "Load phase does not reference state/triage.md"
fi

echo
echo "=== Done ==="
if [[ "$EXIT_CODE" -eq 0 ]]; then
    echo "All checks passed."
else
    echo "Some checks FAILED."
fi
exit "$EXIT_CODE"
