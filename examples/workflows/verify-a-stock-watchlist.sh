#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
STATE_FILE="${WATCHLIST_STATE_FILE:-$ROOT/state/watchlist.md}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

need_file() {
  [[ -f "$1" ]] || fail "missing file: $1"
}

need_line() {
  local pattern="$1"
  local message="$2"
  grep -Eq "$pattern" "$STATE_FILE" || fail "$message"
}

latest_watchlist_date() {
  awk '
    /^## 最新日期[[:space:]]*$/ {getline; gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); print; exit}
  ' "$STATE_FILE"
}

check_latest_date() {
  local date="$1"
  python3 - "$date" <<'PY'
import datetime
import sys

raw = sys.argv[1]
try:
    day = datetime.date.fromisoformat(raw)
except ValueError:
    print(f"FAIL: invalid latest date: {raw}", file=sys.stderr)
    sys.exit(1)

today = datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).date()
if day > today:
    print(f"FAIL: watchlist date {day} is in the future (Asia/Shanghai today is {today})", file=sys.stderr)
    sys.exit(1)
if day.weekday() >= 5:
    print(f"FAIL: watchlist date {day} is a weekend; do not accept non-trading-day watchlist evidence", file=sys.stderr)
    sys.exit(1)
PY
}

section_data_rows() {
  local heading="$1"
  python3 - "$STATE_FILE" "$heading" <<'PY'
import sys

path, heading = sys.argv[1], sys.argv[2]
lines = open(path, encoding="utf-8").read().splitlines()
in_section = False
table_rows = 0
data_rows = 0
for line in lines:
    if line == heading:
        in_section = True
        table_rows = 0
        data_rows = 0
        continue
    if in_section and line.startswith("## "):
        break
    if in_section and line.startswith("|"):
        table_rows += 1
        if table_rows > 2:
            data_rows += 1
print(data_rows)
PY
}

need_section_rows() {
  local section="$1"
  local min_rows="$2"
  local message="$3"
  local rows
  rows="$(section_data_rows "$section")"
  [[ "$rows" -ge "$min_rows" ]] || fail "$message"
}

need_file "$STATE_FILE"

need_line '^# A股观察清单[[:space:]]*$' 'missing title: # A股观察清单'
need_line '^## 最新日期[[:space:]]*$' 'missing section: 最新日期'
need_line '^20[0-9]{2}-[0-9]{2}-[0-9]{2}[[:space:]]*$' 'missing latest ISO date'
check_latest_date "$(latest_watchlist_date)"
need_line '^## 上一轮复盘[[:space:]]*$' 'missing section: 上一轮复盘'
need_line '^## 今日发现的题材[[:space:]]*$' 'missing section: 今日发现的题材'
need_line '^\| 题材 \| 强度 \| 证据 \| 代表标的 \| 明日验证点 \|[[:space:]]*$' 'missing themes table header'
need_line '^\| --- \| --- \| --- \| --- \| --- \|[[:space:]]*$' 'missing themes table separator'
need_section_rows '## 今日发现的题材' 1 'themes table must contain at least one discovered theme row'
need_line '^## 今日发现的标的[[:space:]]*$' 'missing section: 今日发现的标的'
need_line '^\| 代码 \| 名称 \| 触发原因 \| 所属题材 \| 风险 \| 明日验证点 \|[[:space:]]*$' 'missing stocks table header'
need_line '^\| --- \| --- \| --- \| --- \| --- \| --- \|[[:space:]]*$' 'missing stocks table separator'
need_section_rows '## 今日发现的标的' 1 'stocks table must contain at least one discovered stock row'
need_line '^## 明日观察假设[[:space:]]*$' 'missing section: 明日观察假设'
need_line '^## 已移出观察[[:space:]]*$' 'missing section: 已移出观察'
need_line '^\| 题材/标的 \| 移出原因 \| 日期 \|[[:space:]]*$' 'missing removed table header'
need_line '^\| --- \| --- \| --- \|[[:space:]]*$' 'missing removed table separator'
need_line '延续|走弱|证伪|待观察|基线' 'watchlist must explicitly review prior state or mark this run as baseline'
if grep -Eq '非交易日|暂无数据' "$STATE_FILE"; then
  fail "watchlist must not persist non-trading/no-data observations"
fi

printf 'watchlist state ok: %s\n' "$STATE_FILE"
