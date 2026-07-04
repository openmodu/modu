#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
WORKFLOW="${A_STOCK_WORKFLOW:-$ROOT/examples/workflows/a-stock-daily-report.js}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_text() {
  local needle="$1"
  local message="$2"
  grep -Fq "$needle" "$WORKFLOW" || fail "$message"
}

[[ -f "$WORKFLOW" ]] || fail "missing workflow: $WORKFLOW"

need_text 'timeZone: "Asia/Shanghai"' "workflow must anchor dates to Asia/Shanghai"
need_text 'DATA_SOURCE_RULES' "workflow must carry explicit data-source rules"
need_text 'isWeekend' "workflow must guard weekend runs"
need_text '不更新 state/watchlist.md' "weekend guard must avoid writing false market state"
need_text '核心接口全部返回空' "workflow must treat all-empty market data as non-trading/no-data evidence"
need_text '不要创建或更新 state/watchlist.md' "workflow must preserve prior watchlist on non-trading/no-data runs"
need_text '题材/标的表不得为空' "workflow must forbid empty discovered theme/stock tables"
need_text '东财请求必须串行' "workflow must state Eastmoney requests are serial"
need_text 'state/watchlist.md' "workflow must read/write state/watchlist.md"
need_text '读取watchlist' "workflow must read prior watchlist before collection"
need_text '更新watchlist' "workflow must write the next watchlist state"
need_text '无历史 watchlist，本轮建立基线' "workflow must support baseline creation"
need_text '写完后读回 state/watchlist.md' "workflow must verify watchlist state by reading it back after writing"

if grep -Fq 'parallel([' "$WORKFLOW"; then
  fail "workflow must not parallelize A-stock collectors; Eastmoney endpoints need serial throttling"
fi

printf 'a-stock workflow contract ok: %s\n' "$WORKFLOW"
