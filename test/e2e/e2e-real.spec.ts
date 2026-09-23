import { expect, request as newRequestContext, test, type APIRequestContext } from '@playwright/test';

// e2e-real.spec.ts — gated real-provider spot check (NEV-46, per NEV-44 §5/§6).
//
// Board decision: ALL providers (openrouter, surplus, opencode-go, opencode-zen),
// manual (workflow_dispatch) + nightly (schedule), spend-capped.
//
// A provider runs ONLY when its key env is set; zero keys -> all tests skip
// (exit 0, zero spend). The app under test is started externally (see
// .github/workflows/e2e-real.yml) and addressed via APP_BASE_URL.
//
// Secrets hygiene: keys, request bodies, and full responses are NEVER logged.
// Failures report only provider name + HTTP status + a redacted error code.

const APP = process.env.APP_BASE_URL || 'http://127.0.0.1:9472';
const MAX_TOKENS = Number(process.env.E2E_REAL_MAX_TOKENS || '16');
const MAX_EXPECTED_COST_PICO = Number(process.env.E2E_REAL_MAX_EXPECTED_COST_PICO_USD || '50000000000');
const MAX_INPUT_PICO = Number(process.env.E2E_REAL_MAX_INPUT_PICO_PER_TOKEN || '5000000');
const MAX_OUTPUT_PICO = Number(process.env.E2E_REAL_MAX_OUTPUT_PICO_PER_TOKEN || '25000000');

interface Provider {
  name: string;
  keyEnv: string;
  modelEnv: string;
  baseURLEnv: string;
}

interface ProviderUnderTest extends Provider {
  key: string;
}

const ALL_PROVIDERS: Provider[] = [
  { name: 'openrouter', keyEnv: 'OPENROUTER_API_KEY', modelEnv: 'E2E_REAL_OPENROUTER_MODEL', baseURLEnv: 'E2E_REAL_OPENROUTER_BASE_URL' },
  { name: 'surplus', keyEnv: 'SURPLUS_API_KEY', modelEnv: 'E2E_REAL_SURPLUS_MODEL', baseURLEnv: 'E2E_REAL_SURPLUS_BASE_URL' },
  { name: 'opencode-go', keyEnv: 'OPENCODE_GO_API_KEY', modelEnv: 'E2E_REAL_OPENCODE_GO_MODEL', baseURLEnv: 'E2E_REAL_OPENCODE_GO_BASE_URL' },
  { name: 'opencode-zen', keyEnv: 'OPENCODE_ZEN_API_KEY', modelEnv: 'E2E_REAL_OPENCODE_ZEN_MODEL', baseURLEnv: 'E2E_REAL_OPENCODE_ZEN_BASE_URL' },
];

function providersUnderTest(): ProviderUnderTest[] {
  return ALL_PROVIDERS.filter((provider) => process.env[provider.keyEnv])
    .map((provider) => ({ ...provider, key: process.env[provider.keyEnv] as string }));
}

// Blank model = auto-pick the cheapest healthy metered route for that provider.
function pinnedModel(provider: Provider): string {
  return process.env[provider.modelEnv] || '';
}

function dryRunBaseURL(provider: Provider): string {
  return process.env[provider.baseURLEnv] || '';
}

const underTest = providersUnderTest();
const SKIP_MESSAGE = 'SKIP: no real provider keys configured (board has not provisioned secret names yet); nothing spent.';

let clientSecret = '';

// Redact anything key-shaped before it can reach the logs; truncate for brevity.
function redact(text: string): string {
  return text
    .replace(/("api_key"[^:]*:[^"]*")[^"]*/g, '$1***REDACTED***')
    .replace(/[Ss]k-[A-Za-z0-9._-]{8,}/g, '***REDACTED***')
    .slice(0, 200);
}

async function diagnostic(response: { status(): number; text(): Promise<string> }): Promise<string> {
  let code = await response.text().catch(() => '');
  try {
    const body = JSON.parse(code) as { error?: { code?: string; message?: string }; code?: string; message?: string };
    code = body.error?.code || body.code || body.error?.message || body.message || code;
  } catch {
    // Keep the raw text; redact + truncate below.
  }
  return `http=${response.status()} err=${redact(String(code))}`;
}

async function waitForReady(api: APIRequestContext): Promise<void> {
  for (let attempt = 0; attempt < 60; attempt++) {
    const health = await api.get(`${APP}/healthz`).catch(() => null);
    const ready = health?.ok() ? await api.get(`${APP}/readyz`).catch(() => null) : null;
    if (ready?.ok()) return;
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
  throw new Error(`app not ready: ${APP}`);
}

async function registerCredential(api: APIRequestContext, provider: ProviderUnderTest): Promise<number> {
  const payload: Record<string, string> = {
    provider: provider.name,
    label: `e2e-real-${provider.name}`,
    api_key: provider.key,
    access_mode: 'api',
  };
  const baseURL = dryRunBaseURL(provider);
  if (baseURL) payload.base_url = baseURL;
  const response = await api.post('/api/providers/credentials', { data: payload });
  expect(response.ok(), `credential rejected for provider=${provider.name} ${await diagnostic(response)}`).toBeTruthy();
  expect(response.status(), `credential rejected for provider=${provider.name} http=${response.status()}`).toBe(201);
  const body = await response.json() as { models_discovered?: number };
  return body.models_discovered ?? 0;
}

interface CatalogRoute {
  provider: string;
  billing_class: string;
  health: string;
  price_available: boolean;
  pricing?: { input?: number; output?: number };
  model: string;
}

async function pickModel(api: APIRequestContext, provider: ProviderUnderTest): Promise<string> {
  const pinned = pinnedModel(provider);
  if (pinned) return pinned;
  const catalog = await api.get('/api/models');
  expect(catalog.ok(), `catalog unreadable ${await diagnostic(catalog)}`).toBeTruthy();
  const routes = ((await catalog.json()) as { data?: CatalogRoute[] }).data ?? [];
  const cheapest = routes
    .filter((route) => route.provider === provider.name
      && route.billing_class === 'metered'
      && route.health === 'healthy'
      && route.price_available === true)
    .sort((a, b) => (a.pricing?.input ?? Number.MAX_SAFE_INTEGER) - (b.pricing?.input ?? Number.MAX_SAFE_INTEGER)
      || (a.pricing?.output ?? Number.MAX_SAFE_INTEGER) - (b.pricing?.output ?? Number.MAX_SAFE_INTEGER))[0];
  expect(cheapest, `provider=${provider.name} has no healthy metered route to auto-pick`).toBeTruthy();
  return (cheapest as CatalogRoute).model;
}

async function createCeilingGroup(api: APIRequestContext, slug: string, name: string, providerName: string, model: string, maxCostPico: number): Promise<void> {
  const response = await api.post('/api/groups', {
    data: {
      name, slug, enabled: true,
      stages: [{
        position: 0, name: 'ceiling',
        sources: [{ kind: 'model', model_id: model }],
        provider_names: [providerName], billing_classes: ['metered'],
        selection: 'lowest_expected_cost',
        maximum_expected_cost_pico_usd: maxCostPico,
        maximum_input_pico_usd_per_token: MAX_INPUT_PICO,
        maximum_output_pico_usd_per_token: MAX_OUTPUT_PICO,
      }],
    },
  });
  expect(response.ok(), `ceiling group rejected for provider=${providerName} ${await diagnostic(response)}`).toBeTruthy();
  expect(response.status(), `ceiling group rejected for provider=${providerName} http=${response.status()}`).toBe(201);
}

function denyErrorCode(body: unknown): string {
  if (typeof body === 'object' && body !== null) {
    const error = (body as { error?: { code?: unknown; message?: unknown }; code?: unknown; message?: unknown }).error;
    const code = (typeof error === 'object' && error !== null ? error.code ?? error.message : error)
      ?? (body as { code?: unknown; message?: unknown }).code
      ?? (body as { message?: unknown }).message;
    if (typeof code === 'string') return code;
  }
  return redact(typeof body === 'string' ? body : JSON.stringify(body));
}

async function denyCheck(api: APIRequestContext, provider: ProviderUnderTest, model: string): Promise<void> {
  const slug = 'e2e-real-ceiling-deny';
  const created = await api.post('/api/groups', {
    data: {
      name: 'e2e-real deny', slug, enabled: true,
      stages: [{
        position: 0, name: 'deny',
        sources: [{ kind: 'model', model_id: model }],
        provider_names: [provider.name], billing_classes: ['metered'],
        selection: 'lowest_expected_cost', maximum_expected_cost_pico_usd: 1,
      }],
    },
  });
  expect(created.status(), `deny group creation failed http=${created.status()}`).toBe(201);
  const response = await api.post('/v1/chat/completions', {
    headers: { Authorization: `Bearer ${clientSecret}` },
    data: { model: slug, messages: [{ role: 'user', content: 'must not send' }], max_tokens: 4 },
  });
  expect(response.status(), `deny group unexpectedly allowed an upstream call (spend-cap breach) for provider=${provider.name}`).not.toBe(200);
  const code = denyErrorCode(await response.json().catch(() => ''));
  expect(code, `deny group wrong error for provider=${provider.name} http=${response.status()} err=${redact(code)}`)
    .toMatch(/group_price_limit_exceeded|no_eligible_route|over_maximum_cost/);
}

interface RequestRow {
  model: string;
  received_at: string;
  estimated_cost_pico_usd?: number;
  actual_cost_pico_usd?: number;
  provider?: string;
  upstream_model?: string;
}

async function cappedCallAndAssertCost(api: APIRequestContext, provider: ProviderUnderTest, slug: string): Promise<void> {
  const response = await api.post('/v1/chat/completions', {
    headers: { Authorization: `Bearer ${clientSecret}` },
    data: { model: slug, messages: [{ role: 'user', content: 'e2e-real ping, reply in 5 words' }], max_tokens: MAX_TOKENS },
  });
  expect(response.ok(), `real call failed for provider=${provider.name} ${await diagnostic(response)} (key/model/credit check needed)`).toBeTruthy();
  expect(response.headers()['x-payless-request-id'], `provider=${provider.name} missing X-PayLess-Request-ID`).toBeTruthy();
  const text = ((await response.json()) as { choices?: Array<{ message?: { content?: string } }> })
    .choices?.[0]?.message?.content ?? '';
  expect(text.length, `provider=${provider.name} empty completion text`).toBeGreaterThan(0);
  const stats = await api.get('/api/requests?limit=25');
  expect(stats.ok(), `requests unreadable ${await diagnostic(stats)}`).toBeTruthy();
  const rows = (((await stats.json()) as { data?: RequestRow[] }).data ?? [])
    .filter((row) => row.model === slug)
    .sort((a, b) => a.received_at.localeCompare(b.received_at));
  const row = rows[rows.length - 1];
  expect(row, `provider=${provider.name} no request row for model=${slug}`).toBeTruthy();
  const estimated = (row as RequestRow).estimated_cost_pico_usd ?? 0;
  const actual = (row as RequestRow).actual_cost_pico_usd ?? 0;
  expect(estimated > 0 || actual > 0,
    `provider=${provider.name} cost>0 assertion failed (estimated=${estimated} actual=${actual})`).toBeTruthy();
}

test.beforeAll(async () => {
  if (underTest.length === 0) {
    console.log(SKIP_MESSAGE);
    return;
  }
  const api = await newRequestContext.newContext({ baseURL: APP });
  try {
    await waitForReady(api);
    const created = await api.post('/api/client-keys', { data: { label: 'e2e-real', harness: 'Other' } });
    expect(created.ok(), `client key creation failed ${await diagnostic(created)}`).toBeTruthy();
    clientSecret = ((await created.json()) as { secret?: string }).secret ?? '';
    expect(clientSecret, 'no .secret in client-keys response').toBeTruthy();
  } finally {
    await api.dispose();
  }
});

test('registers real provider credentials', async ({ request }) => {
  test.skip(underTest.length === 0, SKIP_MESSAGE);
  for (const provider of underTest) {
    const discovered = await registerCredential(request, provider);
    console.log(`ok: credential registered provider=${provider.name} models_discovered=${discovered}`);
  }
});

test('rejects an over-ceiling request with zero upstream spend', async ({ request }) => {
  test.skip(underTest.length === 0, SKIP_MESSAGE);
  const [first] = underTest as [ProviderUnderTest, ...ProviderUnderTest[]];
  await denyCheck(request, first, await pickModel(request, first));
});

for (const provider of underTest) {
  const slug = `e2e-real-${provider.name}`.replace(/_/g, '-');
  test(`routes one capped call through ${provider.name} and records cost`, async ({ request }) => {
    await createCeilingGroup(request, slug, `e2e-real ${provider.name}`, provider.name, await pickModel(request, provider), MAX_EXPECTED_COST_PICO);
    await cappedCallAndAssertCost(request, provider, slug);
  });
}
