# Reliable request usage capture across three wire formats

Status: design and implementation notes; initial implementation included in this branch.
Date: 2026-09-11.
Investigated revision: `d96a362a9f52c4099ee92cddd6bc4316ebf9a68f`.

## Outcome and scope

Capture provider-reported statistics before response translation, normalize their meaning once, preserve additional usage fields, and persist the result independently of successful delivery to the client. Apply this to OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages, for both JSON and SSE, including all nine client/upstream format combinations.

Use one shared usage package, explicit upstream-format selection, presence-aware values, an accumulator per upstream attempt, and a request-level projection. Missing statistics must be displayed as **Not reported**, never silently converted into measured zero. Cache and reasoning counts are breakdowns, not extra tokens to add indiscriminately to totals or charges.

This design includes collection, translated usage emission, persistence, accounting correctness, API/UI presentation, migrations, and verification. It does not require a tokenizer, billing dashboard scraping, an additional inference request to retrieve statistics, or a wholesale streaming architecture rewrite. Provider extensions remain inspectable even when PayLessForAI cannot interpret or price them. Supporting three formats cannot guarantee every provider reports every metric.

## Investigation: confirmed defects and limits

The supplied screenshot shows a successful request through **Chat Completions → Responses**, with all token fields zero. The actual provider response and streaming mode are not available from that screenshot. The following code defects are confirmed; attributing that specific production request to one defect still requires its usage payload or a synthetic reproduction against the same provider contract.

| Current seam | Confirmed behavior | Consequence |
| --- | --- | --- |
| `internal/wire/canonical.go`: `decodeUsage` | Reads basic counts and reasoning, but never fills cached-read/cache-write fields | Translation loses cache information even for JSON |
| `decodeSSEFor` and `responses_adapter.go` | Looks for top-level `usage`; does not extract `response.completed.response.usage` | Responses streaming can deliver text and persist all-zero statistics |
| `anthropic_adapter.go` and `decodeSSEFor` | Does not extract `message_start.message.usage`; generic payload decoding accepts unrelated Anthropic events as empty responses | Input/cache data is missed; later empty response objects overwrite earlier usage |
| `internal/proxy/proxy.go`: `completeTranslated` | Replaces all six counts from each event/response; loses raw usage, cost, and input-cache semantics | Sparse events erase data and additional statistics disappear |
| `internal/usage/usage.go` | Separate parser; misses Responses `input_tokens_details`, Anthropic `message.usage`; identifies cache semantics through field presence | Pass-through and translated requests disagree |
| `observeSSE` | Parses individual lines, merges only nonzero fields, replaces raw usage on every event, drops `InputTokensNetOfCache` | Explicit zero, multipart SSE, raw data and Anthropic pricing can be wrong |
| `stream`, partial translated path, `completeTranslated` | Persistence is skipped on several read/write/decode failures; uses request context and ignores database errors | Already-consumed provider usage can be lost on cancellation or delivery failure |
| Wire response encoders | JSON writes mostly base counts; streaming encoders do not emit protocol-specific usage/final lifecycle correctly | Clients also lose usage; fixing the statistics page alone is insufficient |
| Chat upstream request construction | No explicit upstream policy for requesting usage; a client extension is not a cross-format policy | Streaming Chat usage can be absent when the client did not request it |
| `0010_request_usage.sql`, `RequestStat`, `app.js` | Counters default to zero; no per-field presence or collection status | Unknown appears as zero; old missing data cannot be reconstructed from zero alone |
| `persistUsage`, `matcher.EstimateUsageCost` | Calculated cost goes into actual-cost field; reasoning is added on top of inclusive output; cache writes are not removed from inclusive input | Misleading provenance and possible double billing in estimates |
| `app.js`: `cacheHitPercent` | Uses `cached / (input + cached)` | Wrong denominator once input includes cache, as in OpenAI usage |

The current checkout uses a monolithic `internal/proxy/proxy.go`; do not assume refactors from other worktrees have landed. No applicable `AGENTS.md` was found in the inspected worktree.

### Reproduction evidence

A temporary diagnostic test called `wire.DecodeResponse`, then applied the exact last-event overwrite logic used by `completeTranslated`. It was removed after execution; only this design document is retained.

| Synthetic fixture | Provider data | Observed current result |
| --- | --- | --- |
| Responses JSON | input 100, output 20, total 120, cached 60, reasoning 5 | 100 / 20 / 120, cached **0**, reasoning 5 |
| Responses SSE: text delta then nested completed response | Same usage as above | All six counters **0**, no decoder error |
| Anthropic SSE: start, content delta, message delta, stop | input 40, cache read 60, initial output 1, final output 20 | All six counters **0**, no decoder error |

These observations demonstrate defects; they are not passing correctness tests. Implementation must turn them into assertions on provider fixtures, persisted records, API output, and client output.

## Protocol mapping

Paths below are relative to the extracted provider usage object. A missing/null field is unknown. Parse only documented shapes for the selected upstream format, with explicit provider-specific aliases where backed by fixtures.

| Normalized metric | Chat Completions | Responses | Anthropic Messages |
| --- | --- | --- | --- |
| Input including cache | `prompt_tokens` | `input_tokens` | `input_tokens + cache_read_input_tokens + cache_creation_input_tokens` |
| Uncached input | Derive only when the required cache buckets are known | Same | `input_tokens` |
| Output including reasoning | `completion_tokens` | `output_tokens` | `output_tokens` |
| Provider total | `total_tokens` | `total_tokens` | Absent in standard usage |
| Cached read | `prompt_tokens_details.cached_tokens` | `input_tokens_details.cached_tokens` | `cache_read_input_tokens` |
| Cache write | `prompt_tokens_details.cache_write_tokens`, if supplied | `input_tokens_details.cache_write_tokens`, if supplied | `cache_creation_input_tokens` |
| Reasoning | `completion_tokens_details.reasoning_tokens` | `output_tokens_details.reasoning_tokens` | `output_tokens_details.thinking_tokens`, if supplied |
| Other statistics to preserve | Input/output modality details, accepted/rejected prediction counts, extensions | Remaining usage details and extensions | Cache TTL breakdown, server-tool counts, service tier, inference geography, extensions |

The OpenAI references document the nested token details, including currently listed cache-write fields. Capability and availability still vary by provider. [Chat usage schema](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/retrieve), [Responses usage schema](https://developers.openai.com/api/reference/cli/resources/responses/methods/create).

Anthropic input excludes both cache buckets. Cache creation can have 5-minute and 1-hour components; those components subdivide the creation total. [Cache accounting](https://platform.claude.com/docs/en/build-with-claude/prompt-caching). The current Messages reference also exposes thinking, server-tool, and service metadata; omitted thinking tokens must remain unknown, not estimated from visible thinking text. [Messages reference](https://platform.claude.com/docs/en/api/messages/create).

### Usage locations and completion

| Format | JSON | SSE collection | Terminal condition |
| --- | --- | --- | --- |
| Chat | root `usage` | Non-null root `usage`, including a chunk with `choices: []` | `[DONE]`; retain finish reason separately |
| Responses | root `usage` | `response.usage` in lifecycle events; final completed/incomplete/failed snapshot when present | Protocol terminal lifecycle event |
| Anthropic | root `usage` | `message_start.message.usage`, then `message_delta.usage` | `message_stop`; error is a separate terminal failure |

Chat streaming usage requires `stream_options.include_usage`; interrupted streams may never receive the final usage chunk. [Chat streaming option](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create). Responses completion wraps the response and its usage. [Responses events](https://developers.openai.com/api/reference/resources/responses/streaming-events). Anthropic message-delta counts are cumulative, so replace present fields rather than adding each event. [Messages streaming](https://platform.claude.com/docs/en/build-with-claude/streaming).

An HTTP 200 or EOF is not sufficient evidence of successful protocol completion. A valid terminal failure may still report authoritative usage. Track inference outcome, delivery outcome, and usage completeness separately.

## Shared usage contract

Make `internal/usage` the dependency-light owner of parsing, accumulation, normalization, and validation. It must not import `wire`, `providers`, `proxy`, or database packages. Let `wire` map its format enum to a usage source enum. Replace the independent `wire.Usage` parser with the shared representation (alias or embedding during transition).

Proposed public concepts, with exact naming left to Go conventions:

```go
type Snapshot struct {
    Version int // 2
    SourceFormat string
    InputTokens, UncachedInputTokens, OutputTokens *int64
    TotalTokens, ProviderTotalTokens *int64
    CachedReadTokens, CacheWriteTokens, ReasoningTokens *int64
    // Presence and provenance for each field: reported or derived.
    FieldSources map[string]string
    Status string // reported, partial, not_reported, invalid, legacy_unknown
    TerminalSeen bool
    RawUsage json.RawMessage
    ExtraMetadata json.RawMessage
    Diagnostics []string
}

// Observe receives one complete provider JSON object/SSE event, not a line.
// Snapshot returns a copy; callers cannot mutate the accumulator.
// Finalize takes protocol outcome and transport outcome and is idempotent.
```

Additional persistence metadata: parser version, upstream response ID if supplied, attempt ID, usage acquisition policy, actual observed service tier, reported-cost value/currency/source, and raw-retention status. Store token values as exact nonnegative int64; reject overflow, fractions, booleans, invalid strings, NaN, and negatives. Use `json.RawMessage`/`json.Number` and exact integer conversion, never a float64 round trip. Numeric strings are accepted only by explicitly configured compatibility aliases.

### Accumulator rules

1. Extract usage using upstream format and event type before semantic content decoding. Empty content, tool-only responses, refusals, errors and usage-only events are eligible for collection.
2. Absent/null usage causes no update. A present zero is a real update. A valid present field updates that field only. Never overwrite the snapshot with a zero-value response struct.
3. Standard streaming counts are snapshots/cumulative values, not additive deltas. Duplicate usage or final events are idempotent. Apply sequence information when supplied; ignore an older sequence. After finalization, duplicate terminal events do not mutate state; conflicting duplicates produce a diagnostic.
4. An authoritative final snapshot can correct an earlier value downward, including to zero. Do not implement generic `max()` merging. Unexpected intermediate decreases remain visible as diagnostics.
5. Preserve root counts and each detail bucket independently. Recompute derived fields after merging; do not carry forward an early derived total as if provider-reported.
6. Normalize Anthropic input from uncached + read + creation. Only derive inclusive input when all required terms are known; use a zero default solely when a documented provider/version contract guarantees omission means zero. Preserve a partial uncached count otherwise.
7. Derive canonical total as inclusive input + inclusive output when both are known. Retain provider total separately. If inconsistent, keep evidence, flag it, and avoid silently reconciling by adding breakdowns. If only provider total is known, preserve it with reported provenance while component totals remain unknown.
8. Output includes reasoning. Cache TTL buckets subdivide cache creation. Audio/image/text and prediction details can overlap other breakdowns: they are not an additive ledger without a documented pricing model.
9. Validate cache buckets against inclusive input and reasoning against output when comparable. Keep valid independent metrics when another field is invalid; flag the snapshot and suppress dependent derivations/cost calculation. Never clamp corrupt measurements into plausible values.
10. `reported` means terminal/JSON usage with valid required base counts; optional metrics may remain unknown. `partial` means usable statistics exist but base counts/finalization are incomplete. `not_reported` means no usable usage was supplied. `invalid` means usage was supplied but no valid base statistics could be recovered. Preserve detailed invalid-field diagnostics even when status is partial.

### Retaining other provider statistics

Preserve the merged usage object as bounded JSON, including unknown nested keys. Deep-merge objects field by field; preserve explicit zero and null in raw evidence, while null does not erase a known normalized measurement. Replace arrays as snapshots, never concatenate them. Do not sum nested iteration statistics on top of provider aggregate counts.

Keep a bounded first and final usage fragment plus the merged object in the versioned raw envelope, with event type/sequence provenance. Suggested limits: 256 KiB per retained usage document, depth 16, 128 diagnostic entries. Count dropped fragments/fields and expose `raw_truncated`; keep normalized known fields even if extra data is over limit. Do not persist content deltas, messages, prompts, tool arguments, thinking text, or arbitrary response headers. Unknown strings inside usage receive length limits and existing secret redaction. Render raw JSON as escaped text in an expandable detail view.

For statistics outside `usage`, use a narrow allowlist: response ID, model, actual service tier and documented provider cost/timing/cache-diagnostic objects. A provider adapter must specify path, units and semantics before promoting an extension to a normalized metric. Preserve unrecognized units as metadata. Never assume any field named `price` means the charge in USD.

## Integration and request behavior

### Collect once at the upstream boundary

Create one accumulator for each actual upstream attempt, including format-discovery attempts. Use the chosen provider format, not the ingress format. JSON parsing and complete SSE event decoding feed that accumulator before translating content. Return usage alongside events even when content decoding fails; accounting must not depend on finding semantic text.

`HTTPClient.readUpstreamError` currently consumes non-2xx bodies before proxy decoding. Extend its typed error/result to carry a bounded extracted usage observation and safe metadata, so reported usage on an HTTP error can reach the same attempt finalizer. Do not recover statistics later from sanitized error-message strings.

For translated responses, add a usage snapshot to `EventStream` or a decode result wrapper; `completeTranslated` consumes it directly. Remove the scan that overwrites usage from every semantic event. For legacy pass-through, feed the same collector while forwarding the original body. Retire `observeSSE` and the second independent parser once both callers are migrated.

Introduce a shared bounded SSE event framer: blank-line dispatch, joined `data:` lines, LF/CRLF, comments, `event:` names, arbitrary read splits, and trailing EOF behavior. A truncated event is not valid usage. Do not treat unknown events or usage parse errors as format-discovery evidence. Keep forwarding otherwise valid content. A full incremental rewrite of buffered translation is not required here; reuse the framer in both buffered translation and pass-through observation, with existing body limits enforced and truncation explicitly detected.

### Requesting Chat streaming statistics

At upstream Chat encoding/preparation, merge `stream_options.include_usage=true` when streaming and the route supports it; preserve other stream options. Do this for clients using any of the three ingress formats. Do not send the Chat-only option to Responses or Messages. Track the client's original preference separately so internal accounting does not force an unsolicited usage-only chunk onto clients that opted out.

Add a route/provider option with `auto`, `supported`, `unsupported` states for this parameter. In auto mode request usage. Permit one fallback without the injected option only after an explicit pre-delivery parameter rejection naming that option; count it within the existing attempt budget and persist the outcome. Do not retry ambiguous failures or a request whose response has begun. Keep this capability separate from endpoint format discovery. Explicit unsupported configuration allows compatible providers that reject the parameter to operate with `not_reported` usage. Missing usage alone is not evidence to disable the option.

### Outbound usage

All three encoders must map the normalized snapshot back into target-format fields when representable, without overwriting it or changing stored upstream evidence. Anthropic emission converts inclusive input back to its uncached convention; if necessary buckets are unknown, omit unknown optional values and do not fabricate measured counters. Standard required-field limitations must be handled explicitly: preserve partial usage in protocol-allowed locations, or omit the optional usage object where permitted; never fail successful text delivery solely for an unrepresentable statistic.

Chat emits a usage-only chunk before `[DONE]` when requested. Responses emits protocol lifecycle events and final usage under the terminal response. Messages emits named start/delta/stop events with usage in the appropriate locations. Do not emit Chat's `[DONE]` into the other formats. Emit only representable details; retain all additional upstream statistics in the statistics API rather than inventing cross-provider fields.

Test outbound streams against explicit protocol fixtures/independent parsers, not just this application's own decoder. Its current permissiveness would hide malformed output. Preserve content ordering, tool calls, finish reasons, and terminal errors while adding lifecycle/usage handling; avoid duplicating text from the final response after streaming its deltas.

### Finalization and persistence failures

Finalize every started upstream attempt on success, protocol failure, malformed content, read error, context cancellation, or downstream write failure. Observe bytes before forwarding them so a write failure cannot discard usage already read. Persist the accumulated snapshot in a deferred finalizer using `context.WithoutCancel` plus a short timeout (for example 2 seconds), never the cancelled request context alone.

Do not continue consuming an unlimited provider stream after client cancellation just to obtain usage. Preserve observed partial usage, close the body, and mark completeness honestly. A fully received final usage snapshot remains reported even if delivery subsequently fails.

Make persistence return errors. Transactionally upsert attempt usage, update the request usage projection, and record the terminal attempt/request state when appropriate. Do not mark the entire request terminal during a retry. Delivery errors and accounting errors are distinct. A storage failure must emit a structured error and a counter tagged by bounded provider/format/error class; if the database accepts it, mark accounting failure on the request. Do not claim crash-proof capture under total storage failure. Successful delivered content is not retroactively turned into a new HTTP error after headers have been sent.

## Database, API, aggregation and migration

Use a forward migration (next available number after `0019` in this checkout); do not modify old migrations. Regenerate Bob models via the existing generator. Prefer an additive schema to rewriting historical counters.

1. Add `attempt_usage`, keyed by `proxy_attempts.id` with cascading deletion. Store nullable normalized counters, status/parser version, source format, the versioned snapshot JSON, bounded raw envelope, reported cost, measured-usage cost estimate, price snapshot/provenance, and calculation coverage. Existing attempts already carry route/credential attribution; reuse it.
2. Extend `request_usage` with a versioned `normalized_usage_json`, `usage_status`, selected usage attempt ID, a nullable measured-usage cost estimate and cost provenance/coverage. Existing six integer columns remain a compatibility projection; v2 JSON is authoritative for presence. All repository readers in this application must stop interpreting a placeholder zero as known data.
3. Keep request tokens defined as the selected terminal/delivered attempt, preserving the meaning of a request row and its model. Expose `all_attempts_usage` separately for resource consumption, with known subtotals plus completeness. Failed earlier attempts must not overwrite terminal usage or disappear from consumption accounting. Do not sum snapshots within one attempt.
4. Keep existing request/group/model dashboard token metrics explicitly scoped to selected-attempt usage. Add all-attempt consumption/spend where needed, using attempt route/provider/credential attribution; provider consumption must not charge an earlier failed provider's tokens to the eventual successful provider. Keep request-success counts independent of usage-row counts. Subscription usage attribution must use the correct credential/attempt basis.
5. Add a nested nullable `usage` object and status/provenance to `RequestStat` and `AttemptStat`. Keep legacy top-level numeric fields for compatibility during migration, mark them deprecated, and switch the bundled frontend to nested values. Include additional metrics/raw details on the authenticated request-detail response; avoid expanding every list row with large raw payloads.
6. Summary endpoints return known subtotals plus per-metric known/unknown/partial request counts; never imply complete coverage when records are missing. Cache hit ratio uses cached reads / inclusive input over the same eligible records. Exclude legacy rows with unknown input semantics from this ratio and report coverage. Use null for a ratio with no known denominator.

Historical rows are `legacy_unknown` by default. Keep their stored values accessible as historical data. An optional versioned backfill can reparse retained raw usage only when source format and field meanings are unambiguous; never infer the upstream format solely from client protocol. Preserve the old snapshot and record backfill version. Rows with raw `null`/`{}` cannot recover lost usage. Old actual-cost values also have ambiguous reported-versus-calculated provenance; label them legacy, not provider-reported. Migration must not silently recalculate or relabel historical bills.

The UI displays real `0`, **Not reported**, or a value with **Partial** as appropriate. Show cache read/write, reasoning, inclusive input/output, and expandable additional statistics per request and attempt. Correct cache-hit calculations. Replace truthy cost fallbacks with null checks so a real zero cost remains zero. A successful request may have missing usage; show both states without implying inference failed.

## Cost calculation rules

Keep three distinct values: pre-request routing estimate, estimate from observed usage, and provider-reported charge. Reserve actual-cost labeling for the last. Record official-reference cost basis too; an official estimate from expected tokens is not an observed bill. Calculate reported-cost savings only when the compared costs cover the same usage and scope; label any estimated savings separately.

The common pricing path charges inclusive output once. A reasoning count is informational unless the provider price schema explicitly establishes a separate rate. If reasoning has a replacement rate, split output into non-reasoning and reasoning buckets instead of charging both the inclusive total and reasoning again. Apply the same principle to input/cache buckets. Retain provider pricing semantics so an explicitly documented surcharge can be modeled separately.

For a documented disjoint cache pricing model:

```text
input charge = uncached_input * input_rate
             + cached_read * read_rate
             + cache_write_5m * write_5m_rate
             + cache_write_1h * write_1h_rate
output charge = inclusive_output * output_rate
```

Do not treat all cache-write TTLs as one rate if they differ. Do not infer that a missing rate is free: the existing `matcher.Price` zero-valued fields need availability/provenance metadata (or a companion pricing contract). Known free routes can yield a known zero estimate, but that does not prove the provider reported a zero charge. Missing usage, unpriced modality/tool fees, conflicting counts, or unsupported overlap semantics make the calculation partial/unavailable. Expose a known subtotal only with that label. Preserve existing checked integer arithmetic and exact pico-USD conversion; record non-USD costs without silently converting them.

## Implementation sequence and handoff

Each step includes its regression assertions; do not defer correctness tests until the end.

1. **Fixtures and shared model:** add protocol JSON/SSE fixtures under `internal/usage/testdata`; implement presence, exact parsing, extraction, normalization, merge rules and raw bounds. Replace the existing test expectation that Anthropic's cache-exclusive input yields a complete inclusive total.
2. **Capture integration:** attach the collector to all upstream attempts; share SSE framing; migrate wire decoding and pass-through; reproduce the screenshot path with nested Responses terminal usage. Return usage on decoding errors and remove duplicate parsers/overwrite scans.
3. **Upstream acquisition and client encoding:** implement Chat usage-option policy and all three outbound mappings/lifecycle events. Keep client and upstream usage preferences separate. Verify content and error behavior alongside statistics.
4. **Durable accounting:** add the forward migration, attempt repository, versioned request projection, finalizer and transaction; regenerate Bob. Persist on every exit path and surface recording failures. Update cost semantics and price availability.
5. **Reporting/UI:** migrate readers, request/attempt detail, aggregate coverage, attribution, nullable formatting and cost provenance. Add the extra-statistics view and browser regression for the zero-stat screenshot.
6. **Acceptance:** run the matrix and failure tests below, migration/generator checks, normal CI, and browser E2E. Report checks and any opt-in live verification separately. Do not merge a parser-only fix while the translated or persistence paths still discard the result.

Primary files: `internal/usage/usage.go`, new accumulator/framer/fixtures; `internal/wire/canonical.go`, all three adapters and request options; `internal/proxy/proxy.go`; `internal/providers/httpclient.go` and prepared-request capability handling; `internal/matcher/matcher.go`; `internal/db/migrations`, models, repositories and Bob output; `app/controlplane/stats_handlers.go`; `internal/web/static/app.js`; `test/mockprovider` and `test/e2e/translation.spec.ts`. Exact factoring may change, but the ownership and behavior above must remain consistent.

## Test coverage and acceptance gates

### Deterministic extraction and accumulation

| Area | Required assertions |
| --- | --- |
| Three JSON formats | Every mapped count, provider total, derived total, unknown extensions, exact cost and provenance |
| Presence | Missing usage, null usage, empty object, missing field, explicit zero, known free cost, partial base counts |
| Streaming | Chat final `choices: []`; Responses nested completed/incomplete/failed usage; Anthropic start + multiple cumulative deltas + stop |
| Merge | Output 1 → 7 → 20 becomes 20; duplicates do not add; null/no-usage events do not erase; authoritative correction to zero works |
| Anthropic cache semantics | Uncached 40 + read 60 + write 10 → input 110; output 20 → total 130; TTL children are not added a second time |
| Common reasoning semantics | Input 100, output 20, reasoning 5 → total 120; reasoning is never added to output |
| Additional statistics | Cache TTL, modality, prediction, tool counts and unknown nested object/array preserved without invented normalization |
| Invalid data | Negative, fraction, overflow, greater-than-2^53 exact integer, wrong type, inconsistent cache/output breakdown, conflicting totals |
| SSE framing | LF/CRLF, comments, multiple data lines, split UTF-8/JSON across reads, usage before/after content, empty/tool-only content, oversized/truncated event |
| Robustness | Fuzz parsers/framer: no panic, bounded retained memory, no synthetic valid counts from malformed data |

Use small fixtures with explicit independent expected values. At minimum use equivalent input 110/output 20/cache read 60/cache write 10/reasoning 5 snapshots across formats, plus missing-field variants. Store the source documentation URL and verification date in a fixture manifest; fixture bodies contain synthetic content only.

### Integration matrix

Run **3 ingress formats × 3 upstream formats × 2 response modes = 18 cases**, including the diagonal through the translation-enabled provider. Separately exercise the legacy pass-through path for all three formats in JSON and SSE (6 cases). These are distinct execution paths despite identical format names.

For every case assert the outgoing provider endpoint/body, provider usage collection, attempt row, selected request projection, stats API, and downstream usage where representable. Assert text is not duplicated, tool calls survive, stream endings follow the target protocol, and unknown upstream extensions remain in stored details. Validate the downstream body with explicit fixture/schema assertions; round-tripping through the same codec alone is insufficient.

Chat acquisition cases: client omits options, requests true, requests false; cross-format ingress; existing options preserved; nonstream requests never receive stream-only options; explicit unsupported parameter rejection permits one bounded fallback; generic 400/500, cancellation or started delivery does not trigger an extra usage retry.

### Failure, retry, database and cost tests

- Transport fails before headers; body fails before/after usage; abrupt EOF lacks terminal event; malformed content contains valid usage; terminal provider error contains usage; downstream writer fails before/after final usage; cancelled request context still permits bounded finalization. Preserve every observed valid count without claiming unobserved final values.
- First attempt consumes usage then fails, second succeeds: separate rows, correct route/credential attribution, selected request usage from the second, all-attempt consumption includes both exactly once. Format probes and repeated finalizer calls are idempotent. A local failure before dispatch does not imply billable zero usage.
- Database failure rolls back related writes, surfaces an accounting error, and never produces a successful-looking fabricated usage record. Reopen SQLite and verify persisted success/partial snapshots and raw details. Verify cascade deletion and concurrent requests do not share accumulators.
- Upgrade a populated old schema; preserve historical values and mark unknown provenance. Re-running startup does not duplicate rows/backfill. Bob regeneration produces only expected changes. Exercise every stats/subscription reader using a mixture of v2, partial, missing and legacy rows.
- Price examples use hand-calculated disjoint buckets, inclusive output with reasoning, optional replacement reasoning price, distinct cache TTLs, explicit zero prices, unknown rates, fixed charges, unpriced tool fees, exact decimal charges and arithmetic overflow. Assert calculated estimates never populate provider-reported charge fields.
- UI/E2E: reproduce the screenshot route with nonzero input/output/cache/reasoning; expand request and attempt details; verify extra metrics, missing versus zero, partial badge, zero-cost display, real cache ratio and cost labels. Verify persistence after application restart. Escape malicious strings in unknown provider statistics.

### Commands and completion evidence

Use the repository's existing test infrastructure and a fresh isolated E2E data directory. CI currently runs Go race tests/vet/static build, Bob generation checks, SQLite integration tests and Playwright.

```sh
go test ./internal/usage ./internal/wire ./internal/matcher ./internal/proxy ./internal/db/repositories
go test ./internal/db/repositories -run Integration -count=1
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build -o /tmp/paylessforai-usage-check ./cmd/paylessforai-app
go generate ./internal/db
git diff --check
cd test/e2e
npm test
```

Install the pinned Bob generator as in `.github/workflows/integration-tests.yml` when required. After committing expected generated files, regeneration must leave `internal/db/bob` unchanged. The current Playwright config hardcodes `/tmp/paylessforai-e2e`, ports 19474–19477, and server reuse. Add an environment-configurable data directory and disable server reuse for CI verification before using a fresh isolated run; do not delete shared user data or accidentally test a stale running binary. The current opt-in `TestOpenCodeGoLiveRequest` checks text only; extend it to check usage presence/status if used, but deterministic CI must not require credentials or paid requests.

Acceptance requires all 18 translated cases and 6 pass-through cases, failure-path persistence, migration compatibility, pricing semantics, and UI missing-value behavior to pass. A provider that omits usage passes only if the omission is represented honestly. Tests passing without these assertions do not establish the fix.

### Checks performed for this design

- Existing usage, wire, matcher and SQLite repository test packages passed.
- Synthetic diagnostic fixtures reproduced the three losses described above.
- The initial proxy test run was blocked by sandbox loopback binding. A permitted local rerun passed (`go test ./internal/proxy`, with the opt-in live provider key unset).
- Only this Markdown design remains in the working tree; no temporary diagnostic code remains. Formatting was checked with `git diff --check` and a direct whitespace check of the new document.
- No live provider request, production database inspection, implementation migration, browser acceptance run, or production fix was performed for this design-only task.
