#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

previous_trading_day() {
  python3 <<'PY'
import datetime

today = datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).date()
day = today
while day.weekday() >= 5:
    day -= datetime.timedelta(days=1)
print(day.isoformat())
PY
}

write_watchlist() {
  local root="$1"
  local day="$2"
  mkdir -p "$root/state"
  cat >"$root/state/watchlist.md" <<EOF
# A股观察清单

## 最新日期
$day

## 上一轮复盘
- 无历史 watchlist，本轮建立基线，后续逐日复盘延续、走弱、证伪和待观察。

## 今日发现的题材
| 题材 | 强度 | 证据 | 代表标的 | 明日验证点 |
| --- | --- | --- | --- | --- |
| AI应用 | 高 | 腾讯行情与同花顺热点共同显示强势 | 300000 测试股份 | 明日验证成交额是否延续 |

## 今日发现的标的
| 代码 | 名称 | 触发原因 | 所属题材 | 风险 | 明日验证点 |
| --- | --- | --- | --- | --- | --- |
| 300000 | 测试股份 | 热点归因与成交放量 | AI应用 | 样本数据仅用于 verifier fixture | 明日验证是否继续放量 |

## 明日观察假设
- AI应用题材如果成交额延续放大，则保留观察；否则标记走弱或证伪。

## 已移出观察
| 题材/标的 | 移出原因 | 日期 |
| --- | --- | --- |
EOF
}

write_log_and_session() {
  local root="$1"
  local logs="$2"
  local sessions="$3"
  local day="$4"
  local session_id="$5"
  local session_body="$6"

  mkdir -p "$logs/morning-market-daily" "$sessions/project"
  cat >"$logs/morning-market-daily/$day.log" <<EOF
{"type":"run_start","started_at":"${day}T10:20:00+08:00","trigger":"scheduler","timezone":"Asia/Shanghai","has_goal":true,"goal":"Update state/watchlist.md with discovered themes and stocks","goal_verifier":true}
{"type":"session_start","session_id":"$session_id"}
{"type":"run_end","status":"ok","goal_status":"complete"}
EOF
  cat >"$sessions/project/$session_id.jsonl" <<EOF
{"type":"session_info","name":"cron:morning-market-daily","cwd":"$root"}
$session_body
EOF
}

valid_session_body() {
  local day="$1"
  cat <<EOF
{"type":"message","message":{"role":"assistant","content":"读取 state/watchlist.md，日期 ${day}；通过 https://qt.gtimg.cn/q=sh000001 和 https://push2.eastmoney.com/api/qt/clist/get 采集数据；更新watchlist 并写回 state/watchlist.md，今日发现的题材包括 AI应用。写完后读回 state/watchlist.md，确认标题、最新日期、题材表、标的表和已移出观察表存在。"}}
EOF
}

run_fixture() {
  local session_body="$1"
  local log_day="${2:-$DAY}"
  local session_cwd="${3:-}"
  local tmp
  local rc
  tmp="$(mktemp -d)"
  write_watchlist "$tmp/root" "$DAY"
  write_log_and_session "${session_cwd:-$tmp/root}" "$tmp/logs" "$tmp/sessions" "$log_day" "fixture-session" "$session_body"
  MODU_CRON_LOG_ROOT="$tmp/logs" MODU_SESSION_ROOT="$tmp/sessions" \
    bash "$SCRIPT_DIR/verify-a-stock-loop.sh" "$tmp/root" || rc=$?
  rm -rf "$tmp"
  return "${rc:-0}"
}

expect_fail() {
  local name="$1"
  local session_body="$2"
  local message_pattern="$3"
  local log_day="${4:-$DAY}"
  local session_cwd="${5:-}"
  local out
  out="$(mktemp)"
  if run_fixture "$session_body" "$log_day" "$session_cwd" >"$out" 2>&1; then
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

DAY="${FIXTURE_DAY:-$(previous_trading_day)}"
[[ "$DAY" =~ ^20[0-9]{2}-[0-9]{2}-[0-9]{2}$ ]] || fail "invalid fixture day: $DAY"

run_fixture "$(valid_session_body "$DAY")"
expect_fail "missing direct market data source" \
  "{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":\"读取 state/watchlist.md，日期 ${DAY}；更新watchlist 并写回 state/watchlist.md，今日发现的题材包括 AI应用。\"}}" \
  'session lacks direct market data source evidence'
expect_fail "missing watchlist update evidence" \
  "{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":\"读取 state/watchlist.md，日期 ${DAY}；通过 https://qt.gtimg.cn/q=sh000001 采集数据，仅生成报告。\"}}" \
  'session lacks watchlist update evidence'
expect_fail "missing watchlist read-back evidence" \
  "{\"type\":\"message\",\"message\":{\"role\":\"assistant\",\"content\":\"读取 state/watchlist.md，日期 ${DAY}；通过 https://qt.gtimg.cn/q=sh000001 采集数据；更新watchlist 并写回 state/watchlist.md，今日发现的题材包括 AI应用。\"}}" \
  'session lacks post-write watchlist read-back evidence'
expect_fail "log date does not match watchlist date" \
  "$(valid_session_body "$DAY")" \
  'need a scheduler-triggered, Asia/Shanghai, verifier-enabled, goal-complete morning-market-daily log' \
  "2099-01-04"
expect_fail "wrong session cwd" \
  "$(valid_session_body "$DAY")" \
  'session cwd is not this repo' \
  "$DAY" \
  "/tmp/other-repo"

printf 'a-stock loop fixtures ok: day=%s verifier=%s\n' "$DAY" "$SCRIPT_DIR/verify-a-stock-loop.sh"
