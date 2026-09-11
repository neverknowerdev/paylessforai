# Streaming across all supported wire formats

Status: implementation design and gap record; audit completed 2026-09-11 against `cdb9760` after fetching and merging `origin/main`. The initial incremental SSE pump is implemented in this PR; the remaining lifecycle and multimodal items below are follow-up gates.

## Finding

Streaming is only partially supported. Requests accept `stream: true`, but the normal HTTP provider execution path reads the entire upstream response before emitting client events. This applies even when upstream and client formats match. Existing SSE output is therefore buffered playback, with incomplete protocol semantics.

“All formats” means the three formats registered in `internal/wire/codec.go` and exposed by `app/gateway/server.go`. Native Gemini, Realtime/WebSocket, and standalone audio/image APIs are not registered endpoints and are outside this implementation.

| Client format | Endpoint | Current normal provider path | Main semantic gaps |
| --- | --- | --- | --- |
| OpenAI Chat Completions | `/v1/chat/completions` | Buffers upstream, then emits text chunks | Tool fragments, reasoning output, finish reasons, usage chunks and response metadata |
| OpenAI Responses | `/v1/responses` | Buffers upstream, then emits text deltas | Response/item/content lifecycle, IDs/indexes, typed deltas, terminal status |
| Anthropic Messages | `/v1/messages`, `/anthropic/v1/messages` | Buffers upstream, then emits text deltas | Named SSE events, message/block lifecycle, indexes, tool JSON, thinking/signatures, stop and usage events |

All nine client/upstream combinations need validation, including the three same-format combinations.

## Code evidence

- `internal/providers/registry.go` constructs `HTTPClient`, which implements `TranslationClient`; `WithManualModels` retains that capability. `Proxy.execute` sends these clients through `executeTranslatedRoute` with no same-format streaming bypass.
- `internal/wire/canonical.go:decodeResponseFor` calls `io.ReadAll` before parsing SSE. `EventStream` holds an entire `[]Event`; the response size is unbounded on this path.
- `decodeSSEFor` splits the complete body into lines, ignores SSE event names and malformed JSON, and does not require a protocol terminal event. A truncated stream can therefore appear successful.
- `decodeChatStreamDelta` inspects only the first choice and returns at most one text or reasoning delta. It does not decode streamed tool calls. `decodeResponsesStreamDelta` interprets any string `delta` as text regardless of event type; function arguments can become assistant text. `decodeAnthropicStreamDelta` only decodes text deltas.
- The three `encode*StreamEvent` functions emit text shapes regardless of canonical event type. `encodeStreamFor` uses `data:` frames and appends `[DONE]` for every protocol; it ignores marshal/write errors. Canonical reasoning, tool, audio, usage, completion and error types do not have complete stream implementations.
- `executeTranslatedRoute` re-encodes buffered partial events on a read failure, also appending the generic terminal sentinel. Upstream data received and data delivered to the client are not distinguished sufficiently for incremental operation.
- The legacy `Proxy.stream` path does forward and flush lines incrementally. It is used by clients without translation support, including the fake provider in `TestProxyDoesNotFailOverAfterStreamBytes`. It accepts EOF as success without validating completion and has incomplete downstream write-failure handling.
- Latest main improves usage extraction, including cache/reasoning details and nested Responses usage. Preserve those fixes; they do not remove buffering or complete the streaming codecs.
- Existing tests include buffered Responses usage decoding and legacy partial-stream retry behavior. They do not establish first-event delivery before upstream completion or full nine-pair protocol conformance. The translation E2E fixture advertises streaming without establishing those properties.

## Required behavior

1. Deliver and flush the first valid event while the upstream response is still open. Memory must not grow with an unbounded event history.
2. Preserve text, tool calls, structured-output text, reasoning, usage, finish/status information and representable multimodal output. Never turn an unsupported semantic event into text or silently drop it.
3. Emit valid lifecycle events for the client format. A failed or truncated stream must never receive a successful terminal event.
4. Allow existing format probing and route retry policy only before client response commitment. After headers or any body bytes are sent, never retry or fail over.
5. Cancel upstream promptly when the client disconnects; persist the terminal attempt/request state and available usage once.
6. Preserve non-streaming behavior, routing order/budgets, learned formats, credential isolation and session metadata on every attempt.

## Architecture

Keep buffered JSON codecs for non-streaming responses. Add per-response streaming decoders and encoders in `internal/wire`; do not make the shared stateless codecs hold mutable stream state.

Suggested boundaries:

```go
type StreamDecoder interface {
    Next(context.Context) (Event, error)
    Close() error
}

type StreamEncoder interface {
    WriteEvent(Event) error
    Close() error // releases resources; never implies successful completion
}
```

Use a synchronous decode → translate → write → flush loop in the proxy. This provides backpressure without an unbounded channel or event slice. Check write and flush errors, using `http.ResponseController` where wrappers support unwrapping. Close upstream on every exit path.

Introduce an SSE frame reader that handles chunk boundaries, UTF-8 splits, CRLF, multiline `data`, event names, comments and blank-line dispatch. Limit individual frames (initial default 1 MiB, configurable) and pre-commit buffering by bytes and time. Reject oversized frames explicitly. Ignore known keepalives; unknown semantic events require a compatibility decision, not silent text conversion.

Extend canonical events with response ID/model, choice/output/content indexes, item ID, call ID, block kind, start/delta/end markers, finish status and structured errors. Store tool argument fragments as strings: a fragment need not be valid standalone JSON. Preserve independent concurrent tool calls. One source frame may yield multiple canonical events.

Maintain only active item state and the aggregate required by the destination protocol. Some terminal Responses events require assembled output: cap that aggregate separately (initial default 16 MiB per response, configurable), fail explicitly on overflow, and do not retain duplicate event history. Test bounded memory against both frame and aggregate limits.

For same-format traffic, use the same incremental transport and lifecycle validation; preserve original frames and provider extensions where possible. Parse a side channel for completion, errors and usage. For different formats, use the canonical event mapping below. Do not let a same-format optimization bypass cancellation, limits or terminal accounting.

## Protocol mapping

| Semantic event | Chat Completions | Responses | Anthropic Messages |
| --- | --- | --- | --- |
| Start | Stable chunk ID/model and initial role | Response created/in-progress and item/content starts as needed | `message_start`, then indexed block starts |
| Text | `delta.content` | Output text deltas with item/output/content identity | Indexed `text_delta` |
| Tool call | Indexed `delta.tool_calls`, ID/name then argument fragments | Function-call item start, argument delta/done, item done | `tool_use` block start, `input_json_delta`, block stop |
| Reasoning | Supported reasoning extension, otherwise incompatibility | Typed reasoning events | Thinking/signature deltas with block identity |
| Usage/finish | Finish-reason chunk; requested usage chunk; `[DONE]` | Done events and terminal completed/incomplete/failed response | Block stops, message delta with stop/usage, `message_stop` |
| Error | Documented error frame then terminate without successful finish | Failure/error lifecycle then terminate | Named error event then terminate |

Responses and Anthropic encoders must emit their own named events and terminal lifecycle; they must not append the Chat `[DONE]` sentinel. Preserve distinct successful, incomplete and failed outcomes. Do not double-emit text from both deltas and final response snapshots.

Handle multimodal and reasoning by explicit source/destination capability. Preserve supported audio/image events and metadata within their native protocol; translate only when the destination can represent them. Preflight known incompatibilities, including requested modalities and structured output. If an unsupported event is discovered after commitment, emit an appropriate stream error and terminate. Encrypted reasoning/signatures must not be reinterpreted or fabricated across protocols. Pin extension fixtures for provider-specific Chat reasoning/audio shapes.

## Delivery, discovery and failure handling

Track actual delivery separately from upstream validation: uncommitted → committed → completed/partial/cancelled. Centralize this result so `ServeHTTP` cannot append a JSON error to an already-started SSE response.

- Before commitment, validate HTTP status/content type and a bounded initial semantic frame. Comments/pings alone do not prove the provider format. Preserve `shouldTryNextFormat` and total attempt budgets for eligible pre-commit failures.
- Learn the provider format after a valid protocol-specific semantic event, not merely an HTTP 200. Transport truncation after validation must not erase a valid learned format.
- After commitment, stop on read/decode/write/flush errors, explicit provider errors, limit violations or cancellation. Never replay buffered output to another provider. Distinguish provider errors from client disconnects in diagnostics.
- EOF without the protocol's terminal marker is a partial failure. A finish-reason chunk may precede usage; do not stop reading prematurely. Treat explicit incomplete Responses status separately from successful completion.
- For `stream: true` with an upstream JSON response, validate the bounded JSON body and synthesize a valid client stream; this provides compatibility but cannot provide upstream token latency. For non-stream requests receiving SSE, aggregate within a size limit and encode JSON, keeping the requested client mode authoritative.
- Preserve existing request/attempt schema and names. Reuse current state/error fields; no migration or new table is required initially. Finalize under a short independent context when request cancellation would otherwise prevent persistence.
- Merge usage by field presence and provider semantics, not summing cumulative snapshots or replacing absent fields with zero. Preserve latest-main cache accounting and `InputTokensNetOfCache`. Retain available partial usage without claiming complete measured billing when final usage is absent.

## Implementation sequence

1. Add incremental SSE parser, event identities and stateful source decoders with fixture tests.
2. Implement three destination lifecycle encoders, typed incompatibility handling, and bounded required aggregation.
3. Integrate the pump in `executeTranslatedRoute`, including same-format preservation, request mode, delivery tracking, cancellation, format learning and usage finalization. Bring the legacy path under the same delivery guarantees.
4. Add the nine-pair integration suite and actual client-consumption tests through gateway middleware; update user documentation only after acceptance passes.

## Acceptance gates

- Test every source/destination pair with a real local HTTP upstream that flushes the first event and blocks on a channel. The client must receive its first semantic event before the test releases upstream completion. Use synchronization and bounded deadlines, not timing-only sleeps or final-body assertions.
- Validate complete text lifecycle with official SDK stream consumers for each client format, including both Anthropic endpoint aliases. Check emitted schema/order independently from our own decoder.
- Cover interleaved tools and fragmented arguments, multiple indexes, Unicode boundaries, reasoning/signatures, structured output, supported media and explicit unsupported-feature failures.
- Cover optional usage, split/cumulative usage, cache and reasoning details, final snapshot deduplication, empty successful output, truncation, malformed frames and provider error events.
- Test failures before first event, after headers, after first text/tool event and at terminal write; assert retry/format-probe counts and persisted request/attempt outcomes. Verify no successful terminator or appended JSON after failure.
- Test disconnect cancellation, slow consumers, frame/aggregate overflow, cleanup and long streams. Assert bounded retained memory and no goroutine leak.
- Run existing wire/proxy/provider/usage/retry tests, gateway integration tests and translation E2E regressions. Existing buffered tests passing is not streaming acceptance.

## Protocol references

- [OpenAI Responses streaming events](https://platform.openai.com/docs/api-reference/responses-streaming): typed response lifecycle and delta events.
- [Anthropic streaming messages](https://platform.claude.com/docs/en/build-with-claude/streaming): named events, indexed block lifecycle, tool fragments, errors and message completion.
- [OpenAI Chat API reference](https://developers.openai.com/api/reference/cli/resources/chat): streaming options. Pin concrete Chat chunk fixtures and SDK versions during implementation.

## Audit validation

Source inspection confirms the buffering and encoder gaps above. `go test ./internal/wire ./internal/proxy ./internal/providers ./internal/usage ./internal/retry` passed for all five packages. These existing tests do not prove incremental delivery or full protocol conformance. No live-provider verification was performed. The only workspace change from this audit is this design document.
