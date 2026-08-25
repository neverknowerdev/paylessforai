# PayLessForAI v1 Implementation Plan

Status: in progress
Last updated: 2026-08-25

## Current implementation status

The repository now contains the first working vertical slice:

- One deployable Go binary: the local `paylessforai-app`.
- Local app entrypoint with embedded UI and embedded SQLite migrations.
- Pure deterministic `MatchEngine` and `RetryEngine` with decision-focused tests.
- OpenRouter and Surplus HTTP catalog/inference client foundations.
- Catalog snapshots with same-model OpenRouter/Surplus alias merging.
- OpenRouter free variants with free-first routing and immediate paid-provider failover.
- OpenAI Chat Completions, Responses, and Anthropic Messages proxy paths.
- Local client-key creation, listing, revocation, and authentication.
- AES-256-GCM encrypted provider credential storage and UI management APIs.
- Request usage normalization, persisted request state, and streaming partial-state handling.
- Recent request statistics API and embedded UI summary for tokens and cost.
- Provider-specific fixed-point pricing for input/output, request, cache, and reasoning components; provider-reported response cost is authoritative with usage-based fallback.
- Fault-injection tests for retry/failover and the no-failover-after-stream-bytes invariant.

Still pending from this plan: durable provider-health/circuit persistence, richer statistics visualizations, provider-specific translation/parameter compatibility, hosted server deployment, and release packaging.

## 1. Objective

PayLessForAI is a local LLM proxy that exposes OpenAI- and Anthropic-compatible APIs, discovers routes from multiple upstream services, and selects the cheapest healthy compatible route for the requested model.

The first release must:

- Run as one dependency-free Go binary on the user's machine.
- Embed the web UI, static assets, and database migrations.
- Use an embedded SQLite database.
- Support OpenRouter and Surplus Intelligence.
- Support OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages, including streaming.
- Expose an OpenAI-compatible models endpoint.
- Discover models and prices at startup and refresh them periodically.
- Persist request, attempt, routing, error, token, cache, cost, and timing statistics.
- Provide a local UI for configuration, keys, provider health, models, requests, usage, cost, and estimated savings.
- Make matching and retry behavior deterministic, explainable, and extensively tested.
- Provide browser E2E coverage using deterministic provider mock servers, without spending real tokens.

The v1 default is provider arbitrage for the same logical model. PayLessForAI must not silently replace a requested model with a cheaper, potentially weaker model. Cross-model selection may later be offered through an explicit virtual model such as `payless/auto`, with separate quality and capability constraints.

## 2. V1 API contract

Default local addresses:

```text
UI:                 http://127.0.0.1:9472
OpenAI base URL:    http://127.0.0.1:9472/v1
Anthropic base URL: http://127.0.0.1:9472
```

Public inference endpoints:

```text
GET  /v1/models
POST /v1/chat/completions
POST /v1/responses
POST /v1/messages
POST /anthropic/v1/messages  # compatibility alias
GET  /healthz
GET  /readyz
```

Authentication:

- OpenAI-compatible calls accept `Authorization: Bearer <local-client-key>`.
- Anthropic Messages accepts `x-api-key: <local-client-key>` and also Bearer authentication for clients that support it.
- The UI and management API are local-only by default.
- Local client keys and upstream provider credentials are separate entities.

Compatibility principles:

- Preserve unknown request fields instead of decoding into a narrow struct and dropping them.
- Parse only the fields needed for authentication, validation, matching, model rewriting, streaming, and accounting.
- Prefer protocol-native upstream endpoints.
- Do not translate between Chat Completions, Responses, and Messages unless a tested adapter explicitly supports the translation.
- Return the client-facing protocol's native success, streaming, and error shape.
- Attach `X-PayLess-Request-ID` to every response.

## 3. Provider discovery strategy

### 3.1 OpenRouter

Use the authenticated `GET /api/v1/models/user` endpoint when a user key is available because it reflects account provider preferences, privacy settings, and guardrails. The general `GET /api/v1/models` catalog remains useful for unauthenticated discovery and diagnostics.

The documented model response includes effective model prices and supported parameters, but there is no documented consumer-side `discounted` filter or discount percentage field. The provider-side `discount_to_user` field is an OpenRouter ingestion detail. V1 must therefore:

- Route using documented API prices rather than scraping UI discount badges.
- Recognize documented variants such as `:free`.
- Allow the user to opt into lower-availability service tiers such as `flex`; never enable them silently.
- Never enable a privacy/data-sharing discount automatically.
- Use actual `usage.cost` returned by inference as the accounting authority.
- Optionally reconcile a completed request through `/generation?id=...` when final usage is missing.

When no user-specific provider override is required, request OpenRouter's own price-aware routing:

```json
{
  "provider": {
    "sort": "price",
    "allow_fallbacks": true,
    "require_parameters": true
  }
}
```

References:

- [OpenRouter model API](https://openrouter.ai/docs/api/api-reference/models/get-models)
- [OpenRouter free variants](https://openrouter.ai/docs/guides/routing/model-variants/free)
- [OpenRouter free-model router](https://openrouter.ai/docs/guides/routing/routers/free-router)
- [OpenRouter provider routing](https://openrouter.ai/docs/guides/routing/provider-selection)
- [OpenRouter provider discount ingestion](https://openrouter.ai/docs/guides/community/for-providers)
- [OpenRouter usage accounting](https://openrouter.ai/docs/cookbook/administration/usage-accounting)

### 3.2 Surplus Intelligence

Use the Surplus endpoints according to their distinct purposes:

- `GET /v1/models`: catalog and capability metadata.
- `GET /api/markets`: active marketplace models, best ask, seller count, and volume.
- `GET /api/markets/:model`: current order book, health, price, and capacity for one model.
- `GET /v1/prices`: reference comparison matrix; useful for display and baselines, but not the authority for current active marketplace liquidity.

Surplus already selects the cheapest healthy seller and performs internal failover. PayLessForAI decides whether Surplus as an aggregate route is preferable to OpenRouter, while Surplus decides among its own eligible sellers.

References:

- [Surplus model catalog](https://www.surplusintelligence.ai/docs/api-reference/models)
- [Surplus market API](https://www.surplusintelligence.ai/docs/api-reference/markets)
- [Surplus price comparison API](https://www.surplusintelligence.ai/docs/api-reference/prices)
- [Surplus health and routing](https://www.surplusintelligence.ai/docs/marketplace/health-routing)
- [Surplus Responses API](https://www.surplusintelligence.ai/docs/api-reference/responses)
- [Surplus Anthropic Messages API](https://www.surplusintelligence.ai/docs/api-reference/anthropic-messages)

## 4. High-level architecture

PayLessForAI v1 is a local modular monolith. A hosted server is deliberately
deferred until the local product is stable:

```text
IDE / local API client
        |
        v
cmd/paylessforai-app       (local machine, localhost:9472)
app/controlplane            (UI and management API)
app/gateway                 (public models and inference handlers)
app/runtime                 (startup, catalog refresh, provider wiring)
internal/{catalog,matcher,proxy,retry,store,...}
```

The app stores provider credentials locally, refreshes catalogs/prices, selects
routes, retries/fails over, and records usage. A future hosted deployment can
reuse these packages, but is not part of the current binary.

Repository layout:

```text
cmd/paylessforai-app/         local app CLI
app/controlplane/             local UI and management handlers
app/gateway/                  local public models and inference handlers
app/runtime/                  local startup and graceful shutdown
internal/config/              validated runtime configuration
internal/protocol/openai/     Chat Completions and Responses wire handling
internal/protocol/anthropic/  Messages wire handling
internal/providers/           provider contracts and shared types
internal/providers/openrouter/
internal/providers/surplus/
internal/catalog/             refresh, normalization, snapshots, aliases
internal/matcher/             pure candidate matching and ranking engine
internal/retry/               pure retry/failover decision engine
internal/proxy/               request executor and streaming bridge
internal/usage/               usage and cost normalization
internal/health/              route/credential health and circuit state
internal/store/               SQLite repositories and transactions
internal/secrets/             credential encryption and key hashing
internal/web/                 embedded UI handlers and assets
migrations/                   embedded ordered SQL migrations
web/                          Go templates, CSS, and small local JavaScript
test/mockprovider/            deterministic upstream provider simulator
test/e2e/                     browser E2E suite
```

Runtime choices:

- Pure-Go SQLite driver so release builds can use `CGO_ENABLED=0`.
- SQLite WAL mode, foreign keys, busy timeout, and explicit transactions.
- `go:embed` for migrations, templates, CSS, JavaScript, and icons.
- Server-rendered Go templates with small embedded JavaScript and SVG charts.
- No CDN or runtime-loaded web dependencies.
- Playwright may be a development/test dependency; it is not part of the shipped binary.

## 5. Model catalog and identity

Keep a logical model separate from the routes that can serve it:

```text
Logical model: anthropic/claude-sonnet-x
  - OpenRouter route -> anthropic/claude-sonnet-x
  - Surplus route    -> claude-sonnet-x
```

Rules:

- Use explicit provider mappings and aliases; never fuzzy-match during inference.
- Preserve OpenRouter-style IDs as public logical IDs when a safe mapping exists.
- Treat OpenRouter `:free` variants as free routes while preserving their upstream IDs; normalize the suffix for logical-model matching so a paid route can fail over for the same model.
- Rank free routes ahead of paid routes. A free route does not retry itself after a pre-response provider error; it fails over immediately to the next eligible route because free capacity is commonly rate-limited or variable.
- Namespace ambiguous or Surplus-only models as `surplus/<model>`.
- Merge routes only when they represent the same model and compatible modality/capability set.
- Keep raw provider model metadata for forward compatibility.
- Mark prices and routes with observed time, refresh generation, and stale-at time.
- Atomically publish a new in-memory catalog only after a complete successful refresh.
- Retain the last successful snapshot when a provider refresh fails.

Startup sequence:

1. Validate configuration and filesystem permissions.
2. Open SQLite and run migrations.
3. Load the last successful catalog and provider-health snapshot.
4. Perform an initial refresh for configured providers.
5. Start the HTTP server and UI.
6. Refresh configured providers periodically and publish snapshots atomically.

Suggested refresh cadence:

- Surplus active market: every 60 seconds plus jitter.
- Provider model catalogs: every 5 minutes plus jitter.
- Immediate refresh after credentials change.
- Targeted refresh after an upstream model-not-found response.
- Configurable stale threshold; stale routes are visible but not eligible by default.

## 6. MatchEngine

### 6.1 Responsibility

`MatchEngine` is the single authority for determining which route is best for one request. All handlers and protocols must use it. No handler may implement its own price comparison, health filtering, or tie-breaking.

The engine must be:

- Pure: no database, network, logging, environment, sleeping, or mutable global state.
- Deterministic: identical inputs always produce identical results.
- Explainable: every candidate is selected or rejected with structured reasons.
- Protocol-neutral: Chat Completions, Responses, and Messages use the same route model.
- Numerically safe: fixed-point monetary arithmetic, never binary floating point.

### 6.2 Proposed input and output

Keep the public engine interface small:

```go
type MatchEngine interface {
    Match(MatchInput) MatchResult
}

type MatchInput struct {
    Request MatchRequest
    Routes  []Route
    Now     time.Time
}

type MatchRequest struct {
    Protocol            Protocol
    LogicalModel        string
    Required            Capabilities
    EstimatedInput      int64
    ExpectedOutput      int64
    AllowStale          bool
    AllowUntrusted      bool
    AllowedProviders    []string
    ExcludedProviders   []string
    MaximumCostPicoUSD  *int64
}

type MatchResult struct {
    Selected   *RankedRoute
    Ranked     []RankedRoute
    Rejections []RouteRejection
    Error      *MatchError
}
```

`Route` is a fully materialized input DTO. The caller, not the matcher, loads catalog data and health state. A route contains:

- Stable route and logical-model IDs.
- Provider and upstream model ID.
- Supported protocols, modalities, parameters, and tools.
- Context and output limits.
- Current credential and circuit health.
- Trust/privacy classification.
- Price components and snapshot timestamps.
- Stable tie-break metadata.

### 6.3 Matching algorithm

The engine performs these steps in a fixed order:

1. Select routes for the exact logical model.
2. Require the incoming wire protocol.
3. Require all request capabilities and parameters.
4. Enforce context and output limits.
5. Enforce allow/deny provider constraints.
6. Enforce trust and privacy constraints.
7. Exclude disabled credentials and open circuits.
8. Exclude missing or stale prices unless explicitly allowed.
9. Calculate expected cost using fixed-point arithmetic.
10. Enforce the maximum-cost constraint.
11. Rank explicitly free routes first, then by expected cost.
12. Break ties by fresher price, higher observed success rate, lower latency, then stable route ID.

Initial expected-cost calculation:

```text
estimated cost =
    estimated input tokens * uncached input price
  + expected output tokens * output price
  + known cache/reasoning components when available
  + known fixed request charges
```

V1 intentionally assumes uncached input during selection. Cache hits are not known reliably before execution. Actual cached tokens and charges are recorded afterward.

Every rejection uses a stable code, for example:

```text
wrong_model
unsupported_protocol
missing_capability
context_too_small
output_limit_too_small
provider_not_allowed
provider_excluded
untrusted_route
credential_disabled
circuit_open
missing_price
stale_price
over_maximum_cost
price_overflow
```

### 6.4 MatchEngine test requirements

The matcher is critical financial logic and receives the strongest test gate:

- Table tests for every filter and rejection code.
- Table tests for every price component and tie-break level.
- Boundary tests for zero tokens, zero price, maximum values, overflow, exact context limits, and exact cost limits.
- Tests proving input route order cannot affect output.
- Tests proving equivalent aliases resolve before matching, never inside it.
- Tests for protocol-specific capability differences.
- Tests for stale/fresh price boundaries with injected `Now`.
- Property tests for deterministic output and monotonic cost behavior.
- Fuzz tests for arbitrary prices, token counts, candidate order, and malformed optional data.
- Golden explanation tests so selection reasons cannot change accidentally.
- 100% statement coverage for `internal/matcher`, supplemented by the explicit decision matrix rather than relying on coverage alone.

No MatchEngine change may merge unless its changed decisions are represented in tests.

## 7. RetryEngine

### 7.1 Responsibility

`RetryEngine` is the single authority for deciding whether to retry the same route, fail over to another matched route, wait, or terminate. The request executor performs I/O but may not invent retry rules.

Like MatchEngine, RetryEngine must be pure and deterministic. It receives facts and returns a decision; it does not sleep, update the database, or make HTTP requests.

### 7.2 Proposed interface

```go
type RetryEngine interface {
    Decide(RetryInput) RetryDecision
}

type RetryInput struct {
    AttemptNumber       int
    MaximumAttempts     int
    StartedAt           time.Time
    Now                 time.Time
    Deadline            time.Time
    Error               ClassifiedError
    Delivery            DeliveryState
    RetryAfter          *time.Duration
    SameRouteAvailable  bool
    FallbacksRemaining  int
}

type RetryDecision struct {
    Action       RetryAction
    Delay        time.Duration
    HealthEffect HealthEffect
    Reason       RetryReason
}
```

Actions:

```text
retry_same_route
fail_over
return_terminal_error
terminate_partial_stream
return_client_cancelled
```

Delivery state is explicit:

```text
nothing_sent
headers_sent
body_started
stream_completed
```

Core safety invariant: once response body bytes have been emitted to the client, retry or failover is forbidden.

### 7.3 Error classification and default behavior

| Condition | Default action before client bytes |
|---|---|
| Invalid client request or unsupported parameter | Terminal 400 |
| Invalid local API key | Terminal 401 |
| Upstream credential 401/403 | Back off credential and fail over |
| Upstream balance/payment 402 | Back off credential and fail over |
| Upstream model 404 | Mark route stale, request refresh, and fail over |
| Rate limit 429 | Respect bounded `Retry-After`; retry or fail over |
| Connect error, timeout, 408, or 5xx | Bounded retry/failover |
| Malformed upstream response | Mark route unhealthy and fail over |
| Client cancellation | Cancel upstream; do not retry |
| Unknown upstream failure | Fail over if safe, otherwise sanitized 502 |

After headers/body are sent, any retryable upstream error becomes `terminate_partial_stream` and the request is persisted as `partial`.

The executor enforces:

- Request-wide deadline.
- Maximum attempt count.
- Connect and response-header timeouts.
- Exponential backoff with full jitter.
- Bounded use of upstream `Retry-After`.
- Immediate propagation of client cancellation.
- Persisted route and credential circuit state.

Random jitter is produced by an injected delay calculator with a seeded source in tests. RetryEngine itself may return the allowed delay range or accept the already calculated deterministic delay.

### 7.4 RetryEngine test requirements

- An explicit decision table crossing error class, delivery state, remaining attempts, remaining fallbacks, deadline, and `Retry-After`.
- Tests proving no decision can retry after `headers_sent` or `body_started`.
- Tests for attempt and deadline exhaustion.
- Tests for negative, zero, valid, excessive, and malformed `Retry-After` values.
- Tests for retry-same versus failover selection.
- Tests for every health/circuit side effect.
- Tests for client cancellation at each delivery stage.
- Deterministic tests for exponential backoff and seeded jitter.
- Property tests proving attempt counts and delays are bounded.
- Fuzz tests over state combinations to detect panics and illegal transitions.
- 100% statement coverage for `internal/retry`, plus explicit invariant tests.

No retry-policy change may merge without updating the decision table.

## 8. Request execution and streaming

Persist the request before the first upstream call:

```text
received
  -> routing
  -> upstream_started
  -> streaming
  -> succeeded | failed | cancelled | partial
```

For every attempt persist:

- Provider, route, credential reference, and upstream model.
- Catalog and price snapshot used for selection.
- Expected cost and match rank.
- Start/end time, status, and normalized error.
- Whether headers or body bytes reached the client.
- Upstream request/generation ID.
- Usage and actual cost when available.

Streaming implementation:

- Forward upstream bytes without reconstructing payloads.
- Parse a copy with a protocol-specific streaming observer.
- Support arbitrarily large valid events without `bufio.Scanner`'s default token limit.
- Flush promptly and preserve event ordering and terminal markers.
- Capture final usage from Chat Completions, Responses events, and Anthropic message deltas.
- Stop the upstream request immediately when the client disconnects.
- Never fail over after the first client-visible response bytes.

Durability means durable request state, attempts, accounting, catalog, and circuit health. It does not mean an HTTP stream can be resumed after the process or client connection is lost.

## 9. Usage, cost, and savings

Normalize all available fields while retaining sanitized raw provider usage JSON:

- Input/prompt tokens.
- Output/completion tokens.
- Total tokens.
- Cache-read and cache-write tokens.
- Reasoning/thinking tokens.
- Audio, image, or media units where present.
- Estimated route cost.
- Provider-reported charged cost.
- Upstream inference cost where exposed.
- Finish reason and upstream finish reason.
- Latency, time to response headers, time to first token, and total duration.

Use fixed-point monetary storage, such as integer pico-USD per unit and per request. Preserve original price strings for audit/debugging.

Actual cost reported by the provider is authoritative. If a provider returns token usage but no monetary total, PayLessForAI calculates a fixed-point actual-cost fallback from the selected provider's price snapshot, including cache, reasoning, and fixed-request components. Estimated savings are computed after completion by applying the request's actual token mix to the next-cheapest eligible route from the persisted price snapshot. The UI must label savings as estimated.

Do not store prompt or response bodies by default. Store only sizes, hashes where useful, usage, routing facts, and sanitized error snippets. Add configurable retention and purge controls.

## 10. Database outline

Core tables:

```text
schema_migrations
settings
provider_credentials
client_api_keys
catalog_refreshes
models
model_aliases
model_routes
price_observations
provider_health
proxy_requests
proxy_attempts
request_usage
```

Migration requirements:

- Ordered, embedded migrations with checksums.
- Transactional application where SQLite permits it.
- Fresh-database and every-supported-upgrade-path tests.
- A pre-migration database backup for non-empty production databases.
- Clear startup failure without partially publishing a new schema version.

## 11. Secret handling

Client keys:

- Generate at least 256 bits of randomness.
- Store only a SHA-256 hash because the source key is high entropy.
- Show the full key once at creation.
- Store label, prefix, scopes, creation time, last-used time, and revocation time.

Provider credentials:

- Encrypt with AES-256-GCM and a unique nonce.
- Store the generated master key separately in a `0600` data-directory file.
- Show only a masked prefix and health/status in normal UI views.
- Decrypt only for provider calls, validation, or an explicit local reveal action.
- Redact secrets from URLs, headers, logs, errors, test snapshots, and panic output.

Bind to `127.0.0.1` by default. Listening on a non-loopback address requires an explicit option and visible security warning.

## 12. UI scope

Pages:

1. Dashboard
   - Base URLs with copy actions.
   - Provider and catalog health.
   - Request count, tokens, spend, savings, failures, and latency.
2. Provider credentials
   - Add, validate, replace, disable, and delete credentials.
   - Last validation and sanitized failure reason.
3. Client API keys
   - Create, copy-once, list, and revoke.
4. Models
   - Logical model, routes, capabilities, price, freshness, and eligibility.
   - Match explanation preview for representative token counts.
5. Requests
   - Request state, protocol, model, selected route, attempts, usage, cost, and error.
   - No prompt/response content by default.
6. Settings
   - Refresh intervals, stale-price policy, deadlines, attempt limits, retention, trust/privacy, and optional service tiers.

## 13. Test strategy

Testing is a product requirement, not a final cleanup phase. Every implementation milestone includes tests at the same time as production code.

### 13.1 Unit tests

Use table-driven tests throughout:

- MatchEngine and RetryEngine decision matrices.
- Fixed-point price parsing, conversion, arithmetic, and overflow.
- Model alias and identity normalization.
- Capability extraction and compatibility checks.
- Provider error classification.
- Protocol request inspection and model rewriting.
- Chat Completions, Responses, and Messages usage normalization.
- SSE/event parsing, including fragmented network reads and large events.
- Secret redaction.
- Request state-machine transitions.
- Savings calculations using actual token mixes.

### 13.2 Property and fuzz tests

Fuzz targets:

- Provider catalog JSON parsers.
- All three request and response protocols.
- SSE and Anthropic event framing.
- Model alias inputs.
- Fixed-point price strings and arithmetic.
- Match and retry input combinations.
- Error bodies and `Retry-After` headers.

Short deterministic fuzz corpora run in normal CI. Longer fuzzing may run nightly.

### 13.3 Store and migration tests

- Fresh database migration.
- Upgrade from every released schema fixture.
- Failed migration rollback.
- Concurrent request/attempt writes under WAL.
- Catalog snapshot atomicity.
- Provider-health persistence across restart.
- API-key revocation and uniqueness.
- Encrypted credential round trips and wrong-master-key failures.
- Retention and purge behavior.

### 13.4 Provider adapter contract tests

Each adapter gets captured and hand-authored fixtures for:

- Catalog success, empty catalog, partial fields, new unknown fields, and malformed data.
- Price parsing across input, output, cached, reasoning, fixed, and free pricing.
- Non-streaming success.
- Streaming success and final usage.
- Tool calls and structured output.
- Provider-native error formats.
- Missing final usage and reconciliation behavior.

### 13.5 Integration and fault-injection tests

Use Go `httptest` servers to simulate:

- Connection refusal and DNS-style errors through injected transports.
- TLS and authentication failures.
- 400, 401, 402, 403, 404, 408, 409, 429, and representative 5xx responses.
- `Retry-After` in seconds and HTTP-date form.
- Slow connection, delayed headers, idle stream, and total deadline exhaustion.
- Malformed JSON and malformed SSE.
- Disconnect before headers, after headers, and mid-event.
- Duplicate and missing terminal events.
- Client cancellation before and during streaming.
- Provider recovery after circuit backoff.
- Process restart with persisted catalog and health state.

### 13.6 Mock LLM provider server

Create `test/mockprovider` as a deterministic Go server capable of emulating both OpenRouter and Surplus.

It must implement:

```text
GET  /openrouter/api/v1/models
GET  /openrouter/api/v1/models/user
GET  /openrouter/api/v1/generation
POST /openrouter/api/v1/chat/completions
POST /openrouter/api/v1/responses
POST /openrouter/api/v1/messages

GET  /surplus/v1/models
GET  /surplus/api/markets
GET  /surplus/api/markets/:model
GET  /surplus/v1/prices
POST /surplus/v1/chat/completions
POST /surplus/v1/responses
POST /surplus/anthropic/v1/messages
```

Test-only control API:

```text
POST /__mock/reset
PUT  /__mock/scenario
GET  /__mock/requests
GET  /__mock/state
```

Scenarios configure deterministic:

- Catalogs, mappings, capabilities, prices, and active liquidity.
- Response text, tool calls, usage, cache/reasoning tokens, and charged cost.
- Streaming event timing and chunk boundaries.
- Status failures and provider-native errors.
- `Retry-After`, delays, malformed bodies, abrupt disconnects, and recovery after N attempts.

The mock records received protocol, model, headers, body, call count, and timing so tests can prove which route was used and whether a forbidden retry occurred. It must never require or accept real provider credentials.

### 13.7 Browser E2E tests

Use Playwright against the real compiled PayLessForAI binary and the Go mock provider. Each test receives a temporary data directory and isolated ports.

Required E2E journeys:

1. First run shows base URLs and empty provider state.
2. Add mock OpenRouter and Surplus credentials through the UI.
3. Refresh catalogs and display merged logical models and routes.
4. Create a local client API key and verify copy-once behavior.
5. Send Chat Completions and prove MatchEngine chooses the cheaper compatible route.
6. Send a Responses request and verify response/events and UI usage statistics.
7. Send an Anthropic Messages request and verify response/events and UI usage statistics.
8. Simulate a retryable pre-header failure and prove fallback succeeds.
9. Simulate a mid-stream failure and prove no fallback occurs after client-visible bytes.
10. Simulate terminal validation/auth/balance errors and verify API plus UI diagnostics.
11. Verify cached, cache-write, reasoning, input, output, total tokens, and cost appear correctly.
12. Verify request attempt history and match explanation in the UI.
13. Revoke a local client key and prove subsequent API calls fail.
14. Restart the binary and prove configuration, catalog, statistics, and health state persist.
15. Delete/disable provider credentials and prove their routes become ineligible.

E2E tests must assert persisted state through management APIs or UI state, not only screenshots or transient toasts.

### 13.8 Compatibility tests

Run protocol-level tests using representative official or widely used clients:

- OpenAI-compatible client for Chat Completions.
- OpenAI-compatible client for Responses.
- Anthropic client for Messages.

Cover streaming, non-streaming, tools, tool results, structured output where applicable, cancellation, custom headers, and unknown parameter preservation.

### 13.9 Coverage and CI gates

- `internal/matcher`: 100% statement coverage plus decision-table and invariant tests.
- `internal/retry`: 100% statement coverage plus decision-table and invariant tests.
- Other critical routing, protocol, accounting, and store packages: target at least 90% statement coverage.
- Repository aggregate: target at least 80%, excluding generated files and trivial entrypoint wiring.
- Coverage thresholds never replace behavioral assertions.
- Run `go test ./...`, `go test -race ./...`, static analysis, frontend lint/tests, integration tests, and browser E2E in CI.
- Run clean cross-platform builds with `CGO_ENABLED=0`.
- Live provider smoke tests are optional, manual, and use dedicated capped credentials; ordinary CI never spends tokens.

## 14. Implementation milestones and acceptance gates

### Milestone 1: Foundation

Deliver:

- Go module and package structure.
- Configuration and data directory.
- SQLite, embedded migrations, structured logs, health endpoints, and graceful shutdown.
- Embedded UI shell and base URL display.

Gate:

- Fresh/upgrade migration tests pass.
- Binary starts offline with no providers.
- Embedded UI/assets work from a clean binary.

### Milestone 2: Keys and provider configuration

Deliver:

- Provider credential encryption and lifecycle.
- Local client-key lifecycle and middleware.
- Provider connectivity validation in UI.

Gate:

- Encryption, redaction, revocation, and UI E2E tests pass.
- Secrets do not appear in logs or test artifacts.

### Milestone 3: Catalog and MatchEngine

Deliver:

- OpenRouter and Surplus discovery adapters.
- Model identity, snapshots, periodic refresh, route health inputs.
- Pure MatchEngine and explainable results.
- `/v1/models` and models UI.

Gate:

- MatchEngine decision matrix and 100% coverage pass.
- Mock-provider catalog E2E proves deterministic cheapest-compatible selection.

### Milestone 4: Chat Completions and RetryEngine

Deliver:

- Non-streaming and streaming Chat Completions.
- Pure RetryEngine, executor, attempt persistence, circuit health, and error mapping.
- Usage and cost accounting.

Gate:

- RetryEngine decision matrix and 100% coverage pass.
- Fault-injection integration suite passes.
- E2E proves safe fallback before bytes and no fallback after bytes.

### Milestone 5: Responses and Anthropic Messages

Deliver:

- Non-streaming and streaming `/v1/responses`.
- Non-streaming and streaming `/v1/messages` plus compatibility alias.
- Protocol-native errors, usage normalization, tools, and request preservation.

Gate:

- Protocol contract, streaming, mock-provider, and browser E2E tests pass for both formats.
- Compatibility clients successfully complete tool-call round trips.

### Milestone 6: Statistics and operational UI

Deliver:

- Dashboard, request drill-down, attempt history, route explanations, token categories, spend, savings, latency, and failures.
- Retention and purge controls.

Gate:

- UI E2E verifies all normalized accounting categories and persisted request states.
- Savings calculations are reproducible from stored price and usage data.

### Milestone 7: Packaging and release

Deliver:

- macOS, Linux, and Windows builds.
- Version metadata, release archive, checksums, and startup documentation.
- Clean-install and database-upgrade smoke tests.

Gate:

- `CGO_ENABLED=0` builds pass.
- No runtime assets or migrations are missing.
- A clean machine can run the downloaded binary without external dependencies.
- Full unit, race, integration, compatibility, and E2E suites pass.

## 15. V1 completion scenario

V1 is complete when a clean downloaded binary can:

1. Start locally with an embedded UI and SQLite database.
2. Add OpenRouter and Surplus credentials through the UI.
3. Discover and periodically refresh their models, active routes, capabilities, and prices.
4. Create a local API key and display the correct base URLs.
5. Accept Chat Completions, Responses, and Anthropic Messages requests, streaming and non-streaming.
6. Use MatchEngine to choose the cheapest healthy compatible route for the exact requested logical model.
7. Use RetryEngine to perform bounded safe retries/failover without ever duplicating an already-started client stream.
8. Persist and display attempts, failures, latency, token categories, actual cost, and estimated savings.
9. Survive restart without losing configuration, catalog snapshots, statistics, or health state.
10. Pass the complete deterministic mock-provider and browser E2E suite without making a paid LLM request.
