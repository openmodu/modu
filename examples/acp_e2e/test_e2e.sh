#!/usr/bin/env bash
# End-to-end test for cmd/acp-gateway driven by a scripted mock ACP agent.
# No external dependencies (LLM API keys, npx) — self-contained.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'cleanup' EXIT

PORT=${PORT:-17080}
TOKEN=${TOKEN:-e2e-token}
BASE_URL="http://127.0.0.1:${PORT}"

cleanup() {
    if [[ -n "${GATEWAY_PID:-}" ]] && kill -0 "$GATEWAY_PID" 2>/dev/null; then
        kill "$GATEWAY_PID" 2>/dev/null || true
        wait "$GATEWAY_PID" 2>/dev/null || true
    fi
    rm -rf "$WORK_DIR"
}

echo "[e2e] building binaries..."
cd "$REPO_ROOT"
go build -o "$WORK_DIR/mock_acp_agent" ./examples/acp_e2e/mock_acp_agent/
go build -o "$WORK_DIR/acp-gateway"    ./cmd/acp-gateway/

cat > "$WORK_DIR/acp.config.json" <<EOF
{
  "version": 1,
  "agents": [
    {
      "id": "mock",
      "name": "mock",
      "command": "$WORK_DIR/mock_acp_agent"
    }
  ],
  "defaultAgent": "mock"
}
EOF

echo "[e2e] starting gateway on :$PORT..."
MODU_ACP_TOKEN="$TOKEN" \
    "$WORK_DIR/acp-gateway" \
    -addr ":$PORT" \
    -config "$WORK_DIR/acp.config.json" \
    > "$WORK_DIR/gateway.log" 2>&1 &
GATEWAY_PID=$!

# Wait for gateway to come up.
for _ in $(seq 1 40); do
    if curl -sf "$BASE_URL/healthz" > /dev/null; then
        break
    fi
    sleep 0.1
done
if ! curl -sf "$BASE_URL/healthz" > /dev/null; then
    echo "[e2e] gateway never came up" >&2
    cat "$WORK_DIR/gateway.log" >&2
    exit 1
fi

echo "[e2e] /healthz ok"

echo "[e2e] listing agents..."
agents=$(curl -sf -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/agents")
echo "  → $agents"
echo "$agents" | grep -q '"mock"' || { echo "[e2e] mock agent not listed" >&2; exit 1; }

echo "[e2e] case 1 — simple prompt → three chunks + completed"
task=$(curl -sf -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"agent":"mock","prompt":"hello","cwd":"/tmp"}' \
    "$BASE_URL/api/tasks")
task_id=$(printf '%s' "$task" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
if [[ -z "$task_id" ]]; then echo "[e2e] no task id: $task" >&2; exit 1; fi
echo "  taskId=$task_id"

# Stream until we see a completed status; capture to file.
stream_log="$WORK_DIR/stream1.log"
curl -sNf -H "Authorization: Bearer $TOKEN" \
    --max-time 10 \
    "$BASE_URL/api/tasks/$task_id/stream" > "$stream_log" &
CURL_PID=$!

# Poll task status up to 5s.
for _ in $(seq 1 50); do
    status=$(curl -sf -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/tasks/$task_id" \
        | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')
    if [[ "$status" == "completed" || "$status" == "failed" ]]; then break; fi
    sleep 0.1
done

# Give SSE a beat to drain.
sleep 0.3
kill $CURL_PID 2>/dev/null || true
wait $CURL_PID 2>/dev/null || true

if [[ "$status" != "completed" ]]; then
    echo "[e2e] task status=$status, expected completed" >&2
    cat "$WORK_DIR/gateway.log" >&2
    exit 1
fi

# Verify result text.
result=$(curl -sf -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/tasks/$task_id" \
    | sed -n 's/.*"result":"\([^"]*\)".*/\1/p')
if [[ "$result" != "Hello, world" ]]; then
    echo "[e2e] result=$result, expected Hello, world" >&2
    exit 1
fi

# SSE should have seen at least one text_delta.
if ! grep -q 'text_delta' "$stream_log"; then
    echo "[e2e] SSE stream missing text_delta events" >&2
    cat "$stream_log" >&2
    exit 1
fi
echo "  ✓ completed, result=\"$result\", SSE had text_delta frames"

echo "[e2e] case 2 — permission flow"
task=$(curl -sf -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"agent":"mock","prompt":"please permission to delete","cwd":"/tmp"}' \
    "$BASE_URL/api/tasks")
task_id=$(printf '%s' "$task" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
echo "  taskId=$task_id"

stream_log="$WORK_DIR/stream2.log"
curl -sNf -H "Authorization: Bearer $TOKEN" \
    --max-time 10 \
    "$BASE_URL/api/tasks/$task_id/stream" > "$stream_log" &
CURL_PID=$!

# Wait for the permission event to arrive in the SSE log.
for _ in $(seq 1 50); do
    if grep -q 'event: permission' "$stream_log"; then break; fi
    sleep 0.1
done
if ! grep -q 'event: permission' "$stream_log"; then
    echo "[e2e] no permission SSE frame" >&2
    cat "$stream_log" >&2
    exit 1
fi

# Approve with optionId=allow.
curl -sf -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"toolCallId":"mock-tool-1","optionId":"allow"}' \
    "$BASE_URL/api/tasks/$task_id/approve" > /dev/null

# Wait for completion.
for _ in $(seq 1 50); do
    status=$(curl -sf -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/tasks/$task_id" \
        | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')
    if [[ "$status" == "completed" || "$status" == "failed" ]]; then break; fi
    sleep 0.1
done

sleep 0.3
kill $CURL_PID 2>/dev/null || true
wait $CURL_PID 2>/dev/null || true

if [[ "$status" != "completed" ]]; then
    echo "[e2e] permission task status=$status" >&2
    exit 1
fi

result=$(curl -sf -H "Authorization: Bearer $TOKEN" "$BASE_URL/api/tasks/$task_id" \
    | sed -n 's/.*"result":"\([^"]*\)".*/\1/p')
if [[ "$result" != "permission-selected" ]]; then
    echo "[e2e] result=$result, expected permission-selected" >&2
    exit 1
fi
echo "  ✓ permission approved, task completed with \"$result\""

echo "[e2e] case 3 — unauthorized request"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/api/agents")
if [[ "$code" != "401" ]]; then
    echo "[e2e] unauthenticated /api/agents = $code, want 401" >&2
    exit 1
fi
echo "  ✓ auth enforced"

echo
echo "[e2e] ALL CASES PASSED"
