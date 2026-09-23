#!/usr/bin/env bash
# harness-stub.sh — mock-only harness e2e (NEV-45, per NEV-44 §2/§6).
# Exercises the harness contract: OpenAI-compatible baseURL + local client key.
# Covers: chat/completions, responses (stream + non-stream), messages + alias,
# x-opencode-session affinity, X-PayLess-Request-ID, /api/requests stats.
# No real secrets: all provider keys are throwaway mock values.
set -euo pipefail

APP="${APP_BASE_URL:-http://127.0.0.1:9472}"
MOCK_OR="${MOCK_OPENROUTER_URL:-http://127.0.0.1:19474}"
MOCK_SUR="${MOCK_SURPLUS_URL:-http://127.0.0.1:19475}"
MOCK_OC="${MOCK_OPENCODE_URL:-http://127.0.0.1:19476}"
MODEL="stub-model"
EXPECT_TEXT="stub hello"

pass() { echo "ok: $1"; }
fail() { echo "FAIL: $1" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || fail "missing required tool: $1"; }
need curl
need jq
need python3

wait_for() {
  local url="$1" name="$2" i
  for i in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then pass "$name ready ($url)"; return 0; fi
    sleep 2
  done
  fail "$name not ready: $url"
}

wait_for "$MOCK_OR/healthz" "mock-openrouter"
wait_for "$MOCK_SUR/healthz" "mock-surplus"
wait_for "$MOCK_OC/healthz" "mock-opencode"
wait_for "$APP/healthz" "app healthz"
wait_for "$APP/readyz" "app readyz"

echo "--- create local client key (harness contract: baseURL <listener>/v1 + Bearer key) ---"
KEY_RESP="$(curl -fsS -X POST "$APP/api/client-keys" -H 'Content-Type: application/json' \
  -d '{"label":"harness-stub","harness":"Other"}')"
SECRET="$(printf '%s' "$KEY_RESP" | jq -r '.secret // empty')"
[ -n "$SECRET" ] || fail "no .secret in client-keys response: $KEY_RESP"
AUTH="Authorization: Bearer $SECRET"

echo "--- point mock upstreams at deterministic scenario ---"
for mock in "$MOCK_OR" "$MOCK_SUR" "$MOCK_OC"; do
  curl -fsS -X POST "$mock/__mock/scenario" -H 'Content-Type: application/json' -d "$(jq -n \
    --arg id "$MODEL" --arg text "$EXPECT_TEXT" \
    '{models: [{id: $id, name: "Stub Model", prompt_price: "0.000001", completion_price: "0.000002",
      context_length: 128000, max_completion_tokens: 4096,
      supported_parameters: ["tools", "response_format"],
      input_modalities: ["text"], output_modalities: ["text"],
      supported_features: ["streaming"]}],
     response_text: $text, input_tokens: 3, output_tokens: 2}')" >/dev/null
  curl -fsS -X POST "$mock/__mock/fixtures" -H 'Content-Type: application/json' -d '{"files":[]}' >/dev/null
  curl -fsS -X POST "$mock/__mock/reset" >/dev/null
done
pass "mock scenarios configured"

echo "--- register mock provider credentials (throwaway keys, mock-only) ---"
OR_RESP="$(curl -sS -w '\n%{http_code}' -X POST "$APP/api/providers/credentials" -H 'Content-Type: application/json' \
  -d "{\"provider\":\"openrouter\",\"label\":\"stub-openrouter\",\"api_key\":\"mock-key-not-secret\",\"base_url\":\"$MOCK_OR/openrouter/api/v1\"}")"
[ "$(printf '%s' "$OR_RESP" | tail -n1)" = "201" ] || fail "openrouter credential rejected: $OR_RESP"
SUR_RESP="$(curl -sS -w '\n%{http_code}' -X POST "$APP/api/providers/credentials" -H 'Content-Type: application/json' \
  -d "{\"provider\":\"surplus\",\"label\":\"stub-surplus\",\"api_key\":\"mock-surplus-not-secret\",\"base_url\":\"$MOCK_SUR/surplus/v1\"}")"
[ "$(printf '%s' "$SUR_RESP" | tail -n1)" = "201" ] || fail "surplus credential rejected: $SUR_RESP"
OC_RESP="$(curl -sS -w '\n%{http_code}' -X POST "$APP/api/providers/credentials" -H 'Content-Type: application/json' \
  -d "{\"provider\":\"opencode-go\",\"label\":\"stub-opencode\",\"api_key\":\"mock-opencode-not-secret\",\"base_url\":\"$MOCK_OC/zen/go/v1\",\"access_mode\":\"api\"}")"
[ "$(printf '%s' "$OC_RESP" | tail -n1)" = "201" ] || fail "opencode-go credential rejected: $OC_RESP"
pass "provider credentials registered"

echo "--- GET /v1/models lists stub model ---"
MODELS="$(curl -fsS "$APP/v1/models" -H "$AUTH")"
printf '%s' "$MODELS" | jq -e --arg m "$MODEL" '.data | map(.id) | contains([$m])' >/dev/null \
  || fail "models missing $MODEL: $MODELS"
pass "models list contains $MODEL"

echo "--- POST /v1/chat/completions (non-stream) ---"
CHAT_HEADERS="$(mktemp)"
CHAT_BODY="$(curl -fsS -D "$CHAT_HEADERS" "$APP/v1/chat/completions" -H "$AUTH" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}")"
printf '%s' "$CHAT_BODY" | jq -e --arg t "$EXPECT_TEXT" '.choices[0].message.content == $t' >/dev/null \
  || fail "chat content mismatch: $CHAT_BODY"
grep -qi 'x-payless-request-id' "$CHAT_HEADERS" || fail "chat missing X-PayLess-Request-ID header"
REQ_ID="$(grep -i 'x-payless-request-id' "$CHAT_HEADERS" | tr -d '\r' | awk '{print $2}')"
[ -n "$REQ_ID" ] || fail "empty X-PayLess-Request-ID"
pass "chat/completions non-stream text + request-id ($REQ_ID)"
rm -f "$CHAT_HEADERS"

echo "--- POST /v1/chat/completions (stream) ---"
STREAM_OUT="$(curl -fsSN "$APP/v1/chat/completions" -H "$AUTH" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}],\"stream\":true}")"
printf '%s' "$STREAM_OUT" | grep -q 'data:' || fail "chat stream has no SSE data frames"
printf '%s' "$STREAM_OUT" | python3 -c "import sys,json
frames=[l[5:].strip() for l in sys.stdin.read().splitlines() if l.startswith('data:')]
frames=[f for f in frames if f and f != '[DONE]']
text=''
for f in frames:
  try: p=json.loads(f)
  except Exception: continue
  ch=(p.get('choices') or [{}])[0]
  d=ch.get('delta') or {}
  if isinstance(d.get('content'),str): text+=d['content']
  if isinstance(ch.get('text'),str): text+=ch['text']
assert '$EXPECT_TEXT' in text, 'stream text missing: %r' % text
" || fail "chat stream text mismatch"
printf '%s' "$STREAM_OUT" | grep -q '\[DONE\]' || fail "chat stream missing [DONE] terminator"
pass "chat/completions stream SSE + [DONE]"

echo "--- POST /v1/responses (non-stream + stream) ---"
RESP_BODY="$(curl -fsS "$APP/v1/responses" -H "$AUTH" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$MODEL\",\"input\":\"hello\"}")"
printf '%s' "$RESP_BODY" | jq -e '.object == "response"' >/dev/null || fail "responses object mismatch: $RESP_BODY"
printf '%s' "$RESP_BODY" | grep -q "$EXPECT_TEXT" || fail "responses text missing: $RESP_BODY"
pass "responses non-stream"
RESP_STREAM="$(curl -fsSN "$APP/v1/responses" -H "$AUTH" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$MODEL\",\"input\":\"hello\",\"stream\":true}")"
printf '%s' "$RESP_STREAM" | grep -q 'response.completed' || fail "responses stream missing response.completed"
printf '%s' "$RESP_STREAM" | grep -q "$EXPECT_TEXT" || fail "responses stream text missing"
pass "responses stream"

echo "--- POST /v1/messages + /anthropic alias (x-api-key auth) ---"
for path in "/v1/messages" "/anthropic/v1/messages"; do
  MSG_BODY="$(curl -fsS "$APP$path" -H "x-api-key: $SECRET" -H 'Content-Type: application/json' -H 'anthropic-version: 2023-06-01' \
    -d "{\"model\":\"$MODEL\",\"max_tokens\":32,\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}")"
  printf '%s' "$MSG_BODY" | jq -e '.type == "message"' >/dev/null || fail "$path type mismatch: $MSG_BODY"
  printf '%s' "$MSG_BODY" | grep -q "$EXPECT_TEXT" || fail "$path text missing: $MSG_BODY"
  pass "messages $path"
done

echo "--- x-opencode-session affinity ---"
SESSION="stubsession123"
SESSION_RESP="$(curl -fsS -w '\n%{http_code}' "$APP/v1/chat/completions" -H "$AUTH" -H 'Content-Type: application/json' -H "x-opencode-session: $SESSION" \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"session check\"}]}")"
[ "$(printf '%s' "$SESSION_RESP" | tail -n1)" = "200" ] || fail "session request failed: $SESSION_RESP"
STATS="$(curl -fsS "$APP/api/requests?limit=100")"
printf '%s' "$STATS" | jq -e --arg s "$SESSION" '[.data[] | select(.session_id == $s)] | length >= 1' >/dev/null \
  || fail "stats missing session_id $SESSION: $STATS"
printf '%s' "$STATS" | jq -e --arg m "$MODEL" --arg t "$EXPECT_TEXT" \
  '[.data[] | select(.model == $m)] | length >= 5' >/dev/null \
  || fail "stats missing expected request rows for $MODEL"
printf '%s' "$STATS" | jq -e --arg m "$MODEL" \
  '[.data[] | select(.model == $m and (.attempts // 1) >= 1)] | length >= 1' >/dev/null \
  || fail "stats missing attempts accounting"
UPSTREAM_CALLS="$(curl -fsS "$MOCK_OR/__mock/requests")"
printf '%s' "$UPSTREAM_CALLS" | jq -e '[.data[] | select(.method == "POST" and (.path | test("chat/completions|responses|messages") | not | not))] | length >= 1' >/dev/null \
  || fail "mock saw no inference calls: $UPSTREAM_CALLS"
pass "stats + session affinity + upstream calls recorded"

echo "ALL HARNESS-STUB CHECKS PASSED (mock-only, no real secrets)"
