# PayLessForAI

PayLessForAI is an LLM gateway that exposes OpenAI- and Anthropic-compatible
endpoints, discovers models and prices from multiple providers, and routes each
request to the cheapest healthy compatible route.

It ships as one dependency-free Go binary, `paylessforai-app`. The app owns
provider credentials, model/pricing discovery, routing, retries, statistics,
and the embedded administration UI on the user's machine. A hosted deployment
will be added later; v1 is intentionally local-only.

## v1 capabilities

- OpenRouter and Surplus Intelligence provider clients.
- OpenRouter `:free` variants and zero-priced models, ranked before paid routes.
- Startup model/catalog discovery with periodic refresh.
- Provider architecture metadata: input/output modalities and supported feature tags,
  surfaced in `/v1/models` and the UI; requests automatically require compatible
  modalities when multimodal content is sent.
- OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages APIs,
  including streaming responses.
- Deterministic, tested route matching and bounded retry/failover behavior.
- Fixed-point estimated pricing from provider catalogs.
- Actual token, cache, reasoning, and provider-reported cost accounting, with
usage-based cost fallback when a provider omits its price. Request statistics also
include the selected provider, upstream model, and durable attempt count so free
route failover is visible. Each request compares the provider-catalog (official)
cost with the actual charged cost and records the dollar and percentage discount
(or overage). Provider failures are stored with a concise parsed message by
default, while the original raw payload remains available from the request
detail view.
- Local client API keys and encrypted provider-credential management.
- Embedded UI for the base URL, keys, provider credentials, and recent request
  statistics.

## Quick start

PayLessForAI currently requires Go 1.26 to build:

```sh
git clone https://github.com/neverknowerdev/paylessforai.git
cd paylessforai
CGO_ENABLED=0 go build -o paylessforai-app ./cmd/paylessforai-app
./paylessforai-app
```

The default listener is `127.0.0.1:9472`. Open
<http://127.0.0.1:9472/> in a browser, add provider credentials, and create a
local client key. The default data directory is the operating-system user
configuration directory under `paylessforai`; override it with `-data-dir`.

The UI is intentionally local-only by default. Provider credentials are stored
encrypted in SQLite, and the generated `master.key` in the data directory is
required to decrypt them. Back up that file if you need to move the installation
to another machine.

## Configure an IDE or API client

After creating a client key in the UI, configure an OpenAI-compatible client
with:

```text
Base URL: http://127.0.0.1:9472/v1
API key:  <the PayLessForAI client key>
```

Anthropic-compatible clients should use `http://127.0.0.1:9472` as their base
URL, send the local key in `x-api-key`, and call `/v1/messages` (the
`/anthropic/v1/messages` alias is also available).

The local client key is separate from the OpenRouter or Surplus API keys
configured in the UI.

## HTTP API

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `GET` | `/v1/models` | Current merged model catalog |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions |
| `POST` | `/v1/responses` | OpenAI Responses |
| `POST` | `/v1/messages` | Anthropic Messages |
| `POST` | `/anthropic/v1/messages` | Anthropic compatibility alias |
| `GET` | `/healthz` | Process health |
| `GET` | `/readyz` | Database readiness |

Inference endpoints require `Authorization: Bearer <client-key>`. Anthropic
Messages also accepts `x-api-key: <client-key>`. Every proxied response includes
an `X-PayLess-Request-ID` header for tracing.

The local UI uses management endpoints under `/api/` for client keys, provider
credentials, request statistics, and status. Keep the listener bound to
loopback unless those endpoints are protected by an external access layer.

## Routing and pricing

At startup and on the configured refresh interval, PayLessForAI fetches provider
model metadata and pricing. OpenRouter uses `/models/user` when an API key is
available and `/models` otherwise; Surplus uses its model catalog. Matching is
based on the requested logical model, protocol, capabilities, limits, health,
and current price.

For a model available through more than one route:

1. Free routes are tried first, including OpenRouter `:free` variants.
2. Paid routes are ranked by estimated request cost.
3. A pre-response rate-limit, timeout, server, or transport failure can retry
   or fail over to another eligible route.
4. Once response bytes have been sent, PayLessForAI does not switch providers;
   it records a partial stream/error instead.

Estimated cost uses fixed-point USD arithmetic and catalog prices. After a
request completes, provider-reported cost is authoritative when present;
otherwise PayLessForAI calculates an actual estimate from token, cache, and
reasoning usage. Request statistics are persisted in SQLite and shown in the UI.
A discount/overage is shown only when both the actual cost and a non-zero
official catalog baseline are available; older rows created before pricing
comparison was introduced, and models with no paid catalog price, are labeled
accordingly.

## Configuration

Useful command-line flags include:

```text
-data-dir                 Database and master-key directory
-listen                   HTTP address (default: 127.0.0.1:9472)
-refresh-interval         Catalog refresh interval (default: 5m)
-openrouter-base-url      OpenRouter API base URL
-surplus-base-url         Surplus API base URL
-read-header-timeout      HTTP request-header timeout
-idle-timeout             HTTP keep-alive timeout
-shutdown-timeout        Graceful shutdown timeout
```

Provider API keys may also be supplied at process startup with:

```sh
PAYLESS_OPENROUTER_API_KEY=... \
PAYLESS_SURPLUS_API_KEY=... \
./paylessforai-app
```

For normal use, adding credentials through the UI is preferred because they are
stored encrypted and can be removed or refreshed without restarting the binary.

## Development and tests

Run the Go checks from the repository root:

```sh
go test ./...
go test -race ./...
go vet ./...
CGO_ENABLED=0 go build ./cmd/paylessforai-app
```

The browser suite starts the real binary and two deterministic mock providers;
it never uses real provider tokens:

```sh
cd test/e2e
npm install
npx playwright install chromium
npm test
```

The same checks run in separate GitHub Actions workflows:

- [Unit tests](.github/workflows/unit-tests.yml)
- [Browser E2E](.github/workflows/e2e.yml)

See the [v1 implementation plan](docs/implementation-plan.md) for provider
discovery details, design decisions, and the remaining roadmap.
