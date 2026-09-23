#!/usr/bin/env bash
# e2e-real.sh — gated real-provider e2e spot check (NEV-46, per NEV-44 §5/§6).
#
# Board decision: ALL providers (openrouter, surplus, opencode-go, opencode-zen),
# manual (workflow_dispatch) + nightly (schedule), spend-capped.
#
# What it does, per configured provider (provider runs ONLY if its key env is set):
#   1. Registers a REAL upstream credential via POST /api/providers/credentials
#      (api_key from CI secret store, no base_url override -> real upstream).
#   2. Creates a per-provider price-ceiling group (metered-only, pinned to that
#      provider, maximum_expected_cost_pico_usd + per-token ceilings) and routes
#      exactly ONE cheap-model chat/completions call (max_tokens capped) through it.
#   3. Asserts HTTP 200, X-PayLess-Request-ID present, and cost > 0 recorded in
#      /api/requests (estimated_cost_pico_usd or actual_cost_pico_usd).
# Also runs a ZERO-SPEND negative check: a group with a 1-pico ceiling must be
# rejected with a price-limit error before any upstream call is attempted.
#
# Spend cap: 1 request/provider x 4 providers, max_tokens capped (default 16),
# price ceilings enforced server-side. Worst case is bounded by
# E2E_REAL_MAX_EXPECTED_COST_PICO_USD per call (default $0.05).
#
# Secrets handling (no key logging):
#   - Keys arrive via env (OPENROUTER_API_KEY, SURPLUS_API_KEY,
#     OPENCODE_GO_API_KEY, OPENCODE_ZEN_API_KEY). Never enable xtrace.
#   - Request/response bodies containing keys are NEVER printed; failures print
#     only HTTP status + redacted, truncated error codes.
#   - The workflow must ::add-mask:: each key before invoking this script.
#
# Ephemeral state: the workflow starts the app with a fresh mktemp -data-dir per
# run and wipes it afterwards; nothing persists between runs.
#
# Dry-run without spend: set E2E_REAL_<PROVIDER>_BASE_URL to a mockprovider URL
# and use throwaway keys; the script exercises identical code paths.
set -euo pipefail
# Intentionally NO `set -x`: secrets live in env and request bodies.

APP="${APP_BASE_URL:-http://127.0.0.1:9472}"
MAX_TOKENS="${E2E_REAL_MAX_TOKENS:-16}"
MAX_EXPECTED_COST_PICO="${E2E_REAL_MAX_EXPECTED_COST_PICO_USD:-50000000000}"
# Per-token ceilings steer to cheap models (gpt-4o-mini ~$0.15/M in,
# haiku ~$1/M, sonnet ~$3/M) while blocking flagship tiers (opus ~$15/M in).
MAX_INPUT_PICO="${E2E_REAL_MAX_INPUT_PICO_PER_TOKEN:-5000000}"
MAX_OUTPUT_PICO="${E2E_REAL_MAX_OUTPUT_PICO_PER_TOKEN:-25000000}"

OPENROUTER_MODEL="${E2E_REAL_OPENROUTER_MODEL:-}"
SURPLUS_MODEL="${E2E_REAL_SURPLUS_MODEL:-}"
OPENCODE_GO_MODEL="${E2E_REAL_OPENCODE_GO_MODEL:-}"
OPENCODE_ZEN_MODEL="${E2E_REAL_OPENCODE_ZEN_MODEL:-}"
# Blank model = auto-pick the cheapest healthy metered route for that provider
# from /api/models (uses the catalog's logical ID, immune to ID normalization).
# Set E2E_REAL_<PROVIDER>_MODEL to pin a specific model instead.
# Optional dry-run overrides (empty = real upstream). Never logged.
OPENROUTER_BASE_URL="${E2E_REAL_OPENROUTER_BASE_URL:-}"
SURPLUS_BASE_URL="${E2E_REAL_SURPLUS_BASE_URL:-}"
OPENCODE_GO_BASE_URL="${E2E_REAL_OPENCODE_GO_BASE_URL:-}"
OPENCODE_ZEN_BASE_URL="${E2E_REAL_OPENCODE_ZEN_BASE_URL:-}"

pass() { echo "ok: $1"; }
fail() { echo "FAIL: $1" >&2; exit 1; }
note() { echo "note: $1"; }

need() { command -v "$1" >/dev/null 2>&1 || fail "missing required tool: $1"; }
need curl
need jq

# Redact anything key-shaped before printing diagnostics.
redact() {
  sed -E -e 's/("api_key"[^:]*:[^"]*")[^"]*/\1***REDACTED***/g' \
         -e 's/[Ss]k-[A-Za-z0-9._-]{8,}/***REDACTED***/g' \
  | cut -c1-500
}

wait_for() {
  local url="$1" name="$2" i
  for i in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then pass "$name ready"; return 0; fi
    sleep 2
  done
  fail "$name not ready: $url"
}

wait_for "$APP/healthz" "app healthz"
wait_for "$APP/readyz" "app readyz"

# Collect configured providers (key env set -> provider under test).
PROVIDERS=()
P_BASES=()
maybe_add() { # provider, key, base_override
  if [ -n "${2:-}" ]; then PROVIDERS+=("$1"); P_BASES+=("$3"); fi
}
maybe_add "openrouter"   "${OPENROUTER_API_KEY:-}"   "$OPENROUTER_BASE_URL"
maybe_add "surplus"      "${SURPLUS_API_KEY:-}"      "$SURPLUS_BASE_URL"
maybe_add "opencode-go"  "${OPENCODE_GO_API_KEY:-}"  "$OPENCODE_GO_BASE_URL"
maybe_add "opencode-zen" "${OPENCODE_ZEN_API_KEY:-}" "$OPENCODE_ZEN_BASE_URL"

if [ "${#PROVIDERS[@]}" -eq 0 ]; then
  echo "SKIP: no real provider keys configured (board has not provisioned secret names yet); nothing spent."
  exit 0
fi
note "providers under test: ${PROVIDERS[*]} (1 capped call each, max_tokens=$MAX_TOKENS)"

echo "--- create local client key (ephemeral run) ---"
KEY_RESP="$(curl -fsS -X POST "$APP/api/client-keys" -H 'Content-Type: application/json' \
  -d '{"label":"e2e-real","harness":"Other"}')"
SECRET="$(printf '%s' "$KEY_RESP" | jq -r '.secret // empty')"
[ -n "$SECRET" ] || fail "no .secret in client-keys response"
AUTH="Authorization: Bearer $SECRET"
pass "local client key created"

# Provider keys are read by index so values never appear in logs.
key_for() {
  case "$1" in
    openrouter) printf '%s' "${OPENROUTER_API_KEY:-}" ;;
    surplus) printf '%s' "${SURPLUS_API_KEY:-}" ;;
    opencode-go) printf '%s' "${OPENCODE_GO_API_KEY:-}" ;;
    opencode-zen) printf '%s' "${OPENCODE_ZEN_API_KEY:-}" ;;
  esac
}

echo "--- register real provider credentials (keys from store, never logged) ---"
idx=0
for provider in "${PROVIDERS[@]}"; {
  key="$(key_for "$provider")"
  base="${P_BASES[$idx]}"
  body="$(jq -n --arg p "$provider" --arg k "$key" --arg b "$base" \
    '{provider: $p, label: ("e2e-real-" + $p), api_key: $k, access_mode: "api"}
     | if $b != "" then .base_url = $b else . end')"
  code="$(printf '%s' "$body" | curl -sS -o /tmp/e2e-real-cred.json -w '%{http_code}' \
    -X POST "$APP/api/providers/credentials" -H 'Content-Type: application/json' --data @-)"
  if [ "$code" != "201" ]; then
    fail "credential rejected for provider=$provider http=$code err=$(redact < /tmp/e2e-real-cred.json)"
  fi
  discovered="$(jq -r '.models_discovered // 0' /tmp/e2e-real-cred.json)"
  pass "credential registered provider=$provider http=201 models_discovered=$discovered"
  idx=$((idx + 1))
}
rm -f /tmp/e2e-real-cred.json

model_for() { # provider -> pinned model or empty
  case "$1" in
    openrouter) printf '%s' "$OPENROUTER_MODEL" ;;
    surplus) printf '%s' "$SURPLUS_MODEL" ;;
    opencode-go) printf '%s' "$OPENCODE_GO_MODEL" ;;
    opencode-zen) printf '%s' "$OPENCODE_ZEN_MODEL" ;;
  esac
}

echo "--- resolve cheap model per provider (pinned or cheapest metered route) ---"
CATALOG="$(curl -fsS "$APP/api/models")"
P_MODELS=()
idx=0
for provider in "${PROVIDERS[@]}"; {
  pinned="$(model_for "$provider")"
  if [ -n "$pinned" ]; then
    P_MODELS+=("$pinned")
    note "provider=$provider pinned model=$pinned"
  else
    pick="$(printf '%s' "$CATALOG" | jq -r --arg p "$provider" '
      ((.data // .) | map(select(.provider == $p and .billing_class == "metered"
        and .health == "healthy" and .price_available == true))
       | sort_by([(.pricing.input // 9223372036854775807),
                  (.pricing.output // 9223372036854775807)])
       | .[0] | .model // empty)')"
    [ -n "$pick" ] || fail "provider=$provider has no healthy metered route to auto-pick"
    P_MODELS+=("$pick")
    pass "provider=$provider auto-picked cheapest metered model=$pick"
  fi
  idx=$((idx + 1))
}

slug_for() { printf '%s' "e2e-real-$1" | tr '_' '-'; }

echo "--- create per-provider price-ceiling groups (spend cap, server-enforced) ---"
idx=0
for provider in "${PROVIDERS[@]}"; {
  model="${P_MODELS[$idx]}"
  slug="$(slug_for "$provider")"
  group_body="$(jq -n --arg name "e2e-real $provider" --arg slug "$slug" \
    --arg p "$provider" --arg m "$model" \
    --argjson maxcost "$MAX_EXPECTED_COST_PICO" \
    --argjson maxin "$MAX_INPUT_PICO" --argjson maxout "$MAX_OUTPUT_PICO" \
    '{name: $name, slug: $slug, enabled: true, stages: [{
      position: 0, name: "ceiling",
      sources: [{kind: "model", model_id: $m}],
      provider_names: [$p], billing_classes: ["metered"],
      selection: "lowest_expected_cost",
      maximum_expected_cost_pico_usd: $maxcost,
      maximum_input_pico_usd_per_token: $maxin,
      maximum_output_pico_usd_per_token: $maxout}]}')"
  code="$(printf '%s' "$group_body" | curl -sS -o /tmp/e2e-real-group.json -w '%{http_code}' \
    -X POST "$APP/api/groups" -H 'Content-Type: application/json' --data @-)"
  if [ "$code" != "201" ]; then
    fail "ceiling group rejected for provider=$provider http=$code err=$(redact < /tmp/e2e-real-group.json)"
  fi
  pass "ceiling group created slug=$slug provider=$provider model=$model max_expected_pico_usd=$MAX_EXPECTED_COST_PICO"
  idx=$((idx + 1))
}
rm -f /tmp/e2e-real-group.json

echo "--- zero-spend negative check: 1-pico ceiling must reject before upstream ---"
DENY_SLUG="e2e-real-ceiling-deny"
jq -n --arg slug "$DENY_SLUG" --arg m "${P_MODELS[0]}" --arg p "${PROVIDERS[0]}" \
  '{name: "e2e-real deny", slug: $slug, enabled: true, stages: [{
    position: 0, name: "deny",
    sources: [{kind: "model", model_id: $m}],
    provider_names: [$p], billing_classes: ["metered"],
    selection: "lowest_expected_cost", maximum_expected_cost_pico_usd: 1}]}' \
  | curl -sS -o /dev/null -w '%{http_code}' -X POST "$APP/api/groups" \
    -H 'Content-Type: application/json' --data @- | grep -q '201' \
  || fail "deny group creation failed"
DENY_RESP="$(curl -sS -w '\n%{http_code}' "$APP/v1/chat/completions" -H "$AUTH" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$DENY_SLUG\",\"messages\":[{\"role\":\"user\",\"content\":\"must not send\"}],\"max_tokens\":4}")"
DENY_CODE="$(printf '%s' "$DENY_RESP" | tail -n1)"
DENY_BODY="$(printf '%s' "$DENY_RESP" | head -n -1)"
if printf '%s' "$DENY_CODE" | grep -qE '^(200)$'; then
  fail "deny group unexpectedly allowed an upstream call (spend-cap breach)"
fi
printf '%s' "$DENY_BODY" | grep -qE 'group_price_limit_exceeded|no_eligible_route|over_maximum_cost' \
  || fail "deny group wrong error http=$DENY_CODE body=$(printf '%s' "$DENY_BODY" | redact)"
pass "price ceiling enforced with zero upstream spend (http=$DENY_CODE)"

echo "--- one capped real call per provider + cost>0 assertion ---"
TOTAL_EST=0
for provider in "${PROVIDERS[@]}"; {
  slug="$(slug_for "$provider")"
  HDRS="$(mktemp)"
  BODY="$(curl -fsS -D "$HDRS" "$APP/v1/chat/completions" -H "$AUTH" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$slug\",\"messages\":[{\"role\":\"user\",\"content\":\"e2e-real ping, reply in 5 words\"}],\"max_tokens\":$MAX_TOKENS}")" \
    || fail "real call failed for provider=$provider (http error; key/model/credit check needed)"
  grep -qi 'x-payless-request-id' "$HDRS" || fail "provider=$provider missing X-PayLess-Request-ID"
  rm -f "$HDRS"
  TEXT_LEN="$(printf '%s' "$BODY" | jq -r '.choices[0].message.content // empty' | wc -c)"
  [ "$TEXT_LEN" -gt 0 ] || fail "provider=$provider empty completion text"
  STATS="$(curl -fsS "$APP/api/requests?limit=25")"
  ROW="$(printf '%s' "$STATS" | jq -c --arg s "$slug" \
    '[.data[] | select(.model == $s)] | sort_by(.received_at) | last // empty')"
  [ -n "$ROW" ] || fail "provider=$provider no request row for model=$slug"
  EST="$(printf '%s' "$ROW" | jq -r '.estimated_cost_pico_usd // 0')"
  ACT="$(printf '%s' "$ROW" | jq -r '.actual_cost_pico_usd // 0')"
  GOT_PROVIDER="$(printf '%s' "$ROW" | jq -r '.provider // empty')"
  UPSTREAM="$(printf '%s' "$ROW" | jq -r '.upstream_model // empty')"
  if [ "$EST" -le 0 ] && [ "$ACT" -le 0 ]; then
    fail "provider=$provider cost>0 assertion failed (estimated=$EST actual=$ACT)"
  fi
  TOTAL_EST=$((TOTAL_EST + EST))
  pass "provider=$provider upstream=$UPSTREAM estimated_pico=$EST actual_pico=$ACT text_chars=$TEXT_LEN"
}
TOTAL_USD="$(printf '%s' "$TOTAL_EST" | python3 -c 'import sys; print(float(sys.stdin.read().strip() or 0) / 1e12)')"
echo "ALL E2E-REAL CHECKS PASSED: providers=${PROVIDERS[*]} total_estimated_cost_usd=$TOTAL_USD (capped: 1 call/provider, max_tokens=$MAX_TOKENS)"
