# recorded/ — auto-captured live upstream responses (NEV-59 review fix §2)

This directory is populated by `npm run matrix:record` (see
`matrix.live.spec.ts` + `cmd/mockrecord`): every upstream response from a
full live scenario run (basic + tool-use + streaming per cell, per parent
[NEV-56](/NEV/issues/NEV-56)) is auto-saved here as a mockprovider fixture.

## Capture flow (manual, operator with provider keys)

```sh
docker compose -f docker-compose.e2e-real.yml up -d --build
cd test/e2e
OPENROUTER_API_KEY=sk-... npm run matrix:record
```

Recorders listen on 127.0.0.1:19574-19576, forward to the real providers,
and write one file per upstream response:
- `openrouter-NNN-*.json` — replayable fixture (status + headers + body)
- `*.sse.txt` — raw SSE streams (not directly replayable; convert the
  observed deltas into a `tool-*.json`-style fixture by hand)

Auth headers are stripped at capture time. Prompt text is stored verbatim —
review before promoting.

## Curation flow (recorded → canonical)

1. Run the record pass above (covers all 27 cells + switching).
2. Inspect each file; drop error/polluted responses.
3. Copy the approved basic/tool responses over the canonical fixtures:
   `../tool-chat.json`, `../tool-responses.json`, `../tool-messages.json`,
   `../hop1-killed.json` (keep the `matrix-*` ids and `matrix hello` text
   so `matrix.spec.ts` assertions still match).
4. Run the mock-only matrix to prove the curated fixtures go green:
   `npm run matrix` (zero spend).
5. Commit the curated fixtures; recorded originals may be deleted after.
