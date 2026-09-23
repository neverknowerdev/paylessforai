# NEV-59 — Format x Provider Matrix Report

Runner: `test/e2e/matrix.spec.ts` (`matrix.config.ts`, `npm run matrix`).
Mode: deterministic mocks (openrouter/surplus/opencode-go roles on :19474/75/76),
app on :19477. Zero spend. Full log lines (`CELL`/`SWITCH`) print per-cell proof.

**Result: 30/30 pass** — 27 cells (3 inputs x 3 providers x 3 outputs,
each basic + tool-use + streaming) + 3 intra-group switching tests.

## Matrix

| # | in | via | out | basic | tool | stream | upstream proof |
|---|----|-----|-----|-------|------|--------|----------------|
| 1 | chat | openrouter | chat | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/openrouter/v1/chat/completions` `openai_chat_completions` |
| 2 | responses | openrouter | chat | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 3 | messages | openrouter | chat | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |
| 4 | chat | surplus | chat | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/surplus/v1/chat/completions` `openai_chat_completions` |
| 5 | responses | surplus | chat | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 6 | messages | surplus | chat | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |
| 7 | chat | opencode-go | chat | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/opencode-go/v1/chat/completions` `openai_chat_completions` |
| 8 | responses | opencode-go | chat | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 9 | messages | opencode-go | chat | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |
| 10 | chat | openrouter | responses | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/openrouter/v1/responses` `openai_responses` |
| 11 | responses | openrouter | responses | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 12 | messages | openrouter | responses | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |
| 13 | chat | surplus | responses | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/surplus/v1/responses` `openai_responses` |
| 14 | responses | surplus | responses | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 15 | messages | surplus | responses | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |
| 16 | chat | opencode-go | responses | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/opencode-go/v1/responses` `openai_responses` |
| 17 | responses | opencode-go | responses | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 18 | messages | opencode-go | responses | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |
| 19 | chat | openrouter | messages | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/openrouter/v1/messages` `anthropic_messages` |
| 20 | responses | openrouter | messages | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 21 | messages | openrouter | messages | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |
| 22 | chat | surplus | messages | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/surplus/v1/messages` `anthropic_messages` |
| 23 | responses | surplus | messages | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 24 | messages | surplus | messages | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |
| 25 | chat | opencode-go | messages | ok | `tool_calls/get_weather` | SSE 4 frames + `[DONE]` | `/mx/opencode-go/v1/messages` `anthropic_messages` |
| 26 | responses | opencode-go | messages | `object=response` | `function_call/get_weather` | `response.completed` | same |
| 27 | messages | opencode-go | messages | `type=message` | `tool_use/get_weather` `stop=tool_use` | `content_block_delta` | same |

Tool assertions per the spec: chat=`tool_calls`, responses=`function_call`
item, messages=`tool_use` block with `stop_reason=tool_use`. Output shape is
pinned black-box via the credential `base_url` suffix and verified two ways:
the upstream path the mock received + `attempt_details[].provider_format`.

## Switching (intra-group, hop1 killed)

Group `matrix-switch`: hop1 chat->openrouter, hop2 messages->opencode-go.
hop1 forced 503 (`hop1-killed.json`); hop2 answers in the *input* shape:

- in=chat: hop1 `openrouter/openai_chat_completions->503`, hop2
  `opencode-go/anthropic_messages->200`, body has `choices[].message.content`
  = `matrix hello`.
- in=responses: same hops, body `object=response` containing `matrix hello`.
- in=messages: same hops, body `type=message` containing `matrix hello`.

## Raw proof excerpts

```text
CELL in=messages via=surplus out=responses :: basic+stream ok events=8
  | tool={"id":"toolu_matrix_1","input":{"city":"Paris"},"name":"get_weather","type":"tool_use"} stop=tool_use
  | upstream=/mx/surplus/v1/responses format=openai_responses
SWITCH in=messages hop1=openrouter/openai_chat_completions->503
  hop2=opencode-go/anthropic_messages->200
  proof={"content":[{"text":"matrix hello","type":"text"}],"id":"mock-message","model":"
```

## Bugs found and fixed by this runner

- `internal/wire/anthropic_adapter.go`: `encodeAnthropicResponse` passed the
  canonical finish reason through verbatim, so cross-format tool calls
  returned `stop_reason: "tool_calls"` (chat upstream) or `"stop"`
  (responses upstream) instead of `"tool_use"`. Added
  `anthropicStopReason` mapping (tool calls present -> `tool_use`,
  `stop`/`completed` -> `end_turn`, `length` -> `max_tokens`).
  `go test ./internal/...` and the full default e2e suite (27 passed) stay green.

## Known design note (no change, for CTO/NEV-58)

- Learned upstream format (`model_routes`, keyed `provider:model`) takes
  precedence over a later credential's endpoint pin. The runner keeps rounds
  hermetic with one upstream model per output shape
  (`matrix-model-{chat,responses,messages,switch}`); re-pinning the same
  route ID across credential rotations is left as a routing-policy question.

## Live-provider mode (not yet green, needs keys/routes)

- `MATRIX_REAL=1` + `OPENROUTER_API_KEY` (+ optional surplus/opencode keys)
  switches the runner to real credentials with the NEV-59 model ladder
  (`nex-agi/nex-n2.5-mini:free` -> `nex-agi/nex-n2.5-pro:free` ->
  `openai/gpt-oss-20b`, formats `dots-studio/dots-3-note-preview:free` -> ...).
  Blocked on live route health ([NEV-58](/NEV/issues/NEV-58)); mock mode above
  is the green proof until then.
