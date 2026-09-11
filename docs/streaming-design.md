# Streaming boundaries and follow-up gates

## Current implementation

- `internal/wire/stream_decoder.go` separates SSE framing from protocol decoding.
  Buffered and incremental response reads use the same decoder and terminal checks.
- `internal/wire/stream_encoder.go` owns downstream commitment and framing through
  `StreamWriter`. Responses usage and final snapshots are retained until the end,
  producing one completion event after the output lifecycle.
- `internal/proxy/stream.go` owns transport delivery and finalization. Every
  committed exit persists observed usage and request state. Attempt finalization
  uses a bounded context that survives client cancellation.

The callback event count is not a delivery guarantee: writing headers commits the
response even when the first frame fails. Retry decisions use writer commitment.
Response snapshots must not repeat text already delivered as deltas. Partial
responses and failed upstream lifecycles must not generate a success terminal.

## Verified behavior

Regression tests cover multiline SSE, nested usage, protocol-specific terminal
validation, failed/incomplete Responses events, startup and terminal write
failures, cancellation, truncated bodies, buffered JSON fallback, and durable
partial usage/attempt records. The browser test proves first-text delivery before
upstream completion and checks the entire stream for duplicate completion events.
The mock provider captures its release latch before flushing and stops waiting
when the request is canceled.

## Remaining conformance work

These gaps predate the refactoring and are not established as supported by the
text-streaming tests:

- Preserve all simultaneous deltas and tool calls in a provider chunk. The current
  canonical stream decoders return one event and the Chat decoder selects the
  first tool call. Tool indices, names, IDs, and argument fragments need durable
  per-call assembly and matching downstream item/block start and stop events.
- Model separate reasoning and text blocks, including signatures, rather than
  emitting every Anthropic delta into the initial text block.
- Preserve finish reasons and protocol-specific terminal details, including tool
  handoff and token limits, instead of synthesizing a generic successful ending.
- Complete Responses item IDs, output/content indices, and final item/part
  snapshots, and validate complete lifecycles against actual client SDKs.
- Add explicit audio, image, and other multimodal streaming fixtures across the
  supported protocol pairs; the current text/tool-fragment handling is not proof
  of multimodal stream fidelity.
