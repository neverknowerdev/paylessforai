import { expect, test, type APIRequestContext } from '@playwright/test';

const MODEL = 'stub-model';
const EXPECT_TEXT = 'stub hello';
const SESSION_ID = 'stubsession123';
const APP_DEFAULT = 'http://127.0.0.1:9472';

const MOCK_OPENROUTER = process.env.MOCK_OPENROUTER_URL || 'http://127.0.0.1:19474';
const MOCK_SURPLUS = process.env.MOCK_SURPLUS_URL || 'http://127.0.0.1:19475';
const MOCK_OPENCODE = process.env.MOCK_OPENCODE_URL || 'http://127.0.0.1:19476';
const MOCKS = [MOCK_OPENROUTER, MOCK_SURPLUS, MOCK_OPENCODE];

let APP = process.env.APP_BASE_URL || '';
let secret = '';

interface RequestRow {
  session_id?: string;
  model?: string;
  attempts?: number;
}

interface MockCall {
  method?: string;
  path?: string;
}

function bearerHeaders(): Record<string, string> {
  return { Authorization: `Bearer ${secret}` };
}

async function waitForReady(api: APIRequestContext): Promise<void> {
  const urls = [...MOCKS.map((mock) => `${mock}/healthz`), `${APP}/healthz`, `${APP}/readyz`];
  const deadline = Date.now() + 25_000;
  for (;;) {
    const pending: string[] = [];
    for (const url of urls) {
      const response = await api.get(url).catch(() => null);
      if (!response?.ok()) pending.push(url);
    }
    if (pending.length === 0) return;
    if (Date.now() >= deadline) throw new Error(`not ready: ${pending.join(', ')}`);
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
}

async function configureMockScenario(api: APIRequestContext, mock: string): Promise<void> {
  const scenario = await api.post(`${mock}/__mock/scenario`, {
    data: {
      models: [{
        id: MODEL,
        name: 'Stub Model',
        prompt_price: '0.000001',
        completion_price: '0.000002',
        context_length: 128000,
        max_completion_tokens: 4096,
        supported_parameters: ['tools', 'response_format'],
        input_modalities: ['text'],
        output_modalities: ['text'],
        supported_features: ['streaming'],
      }],
      response_text: EXPECT_TEXT,
      input_tokens: 3,
      output_tokens: 2,
    },
  });
  expect(scenario.ok()).toBeTruthy();
  expect((await api.post(`${mock}/__mock/fixtures`, { data: { files: [] } })).ok()).toBeTruthy();
  expect((await api.post(`${mock}/__mock/reset`)).ok()).toBeTruthy();
}

async function registerCredential(api: APIRequestContext, payload: Record<string, string>): Promise<void> {
  const response = await api.post(`${APP}/api/providers/credentials`, { data: payload });
  expect(response.status()).toBe(201);
}

function accumulatedStreamText(raw: string): { frames: number; text: string } {
  const frames = raw
    .split('\n')
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice('data:'.length).trim())
    .filter((frame) => frame !== '' && frame !== '[DONE]');
  let text = '';
  for (const frame of frames) {
    let chunk: { choices?: Array<{ delta?: { content?: unknown }; text?: unknown }> };
    try {
      chunk = JSON.parse(frame) as { choices?: Array<{ delta?: { content?: unknown }; text?: unknown }> };
    } catch {
      continue;
    }
    const choice = chunk.choices?.[0];
    if (typeof choice?.delta?.content === 'string') text += choice.delta.content;
    if (typeof choice?.text === 'string') text += choice.text;
  }
  return { frames: frames.length, text };
}

test.beforeAll(async ({ request }, testInfo) => {
  if (!APP) APP = (testInfo.project?.use as { baseURL?: string } | undefined)?.baseURL || APP_DEFAULT;
  const api = request;
  await waitForReady(api);
  const key = await api.post(`${APP}/api/client-keys`, { data: { label: 'harness-stub', harness: 'Other' } });
  expect(key.ok()).toBeTruthy();
  secret = ((await key.json()) as { secret?: string }).secret ?? '';
  expect(secret).toBeTruthy();
  for (const mock of MOCKS) await configureMockScenario(api, mock);
  const credentials = ((await (await api.get(`${APP}/api/providers/credentials`)).json()) as { data?: Array<{ id: string }> }).data ?? [];
  for (const credential of credentials) expect((await api.delete(`${APP}/api/providers/credentials/${credential.id}`)).ok()).toBeTruthy();
  await registerCredential(api, { provider: 'openrouter', label: 'stub-openrouter', api_key: 'mock-key-not-secret', base_url: `${MOCK_OPENROUTER}/openrouter/api/v1` });
  await registerCredential(api, { provider: 'surplus', label: 'stub-surplus', api_key: 'mock-surplus-not-secret', base_url: `${MOCK_SURPLUS}/surplus/v1` });
  await registerCredential(api, { provider: 'opencode-go', label: 'stub-opencode', api_key: 'mock-opencode-not-secret', base_url: `${MOCK_OPENCODE}/zen/go/v1`, access_mode: 'api' });
});

test('lists stub-model in GET /v1/models', async ({ request }) => {
  const response = await request.get(`${APP}/v1/models`, { headers: bearerHeaders() });
  expect(response.ok()).toBeTruthy();
  const ids = (((await response.json()) as { data?: Array<{ id: string }> }).data ?? []).map((model) => model.id);
  expect(ids).toContain(MODEL);
});

test('chat completions non-stream returns stub hello with request id', async ({ request }) => {
  const response = await request.post(`${APP}/v1/chat/completions`, {
    headers: bearerHeaders(),
    data: { model: MODEL, messages: [{ role: 'user', content: 'hello' }] },
  });
  expect(response.ok()).toBeTruthy();
  expect(((await response.json()) as { choices: Array<{ message: { content: string } }> }).choices[0].message.content).toBe(EXPECT_TEXT);
  const requestId = response.headers()['x-payless-request-id'];
  expect(requestId).toBeTruthy();
});

test('chat completions stream emits SSE deltas with DONE terminator', async ({ request }) => {
  const response = await request.post(`${APP}/v1/chat/completions`, {
    headers: bearerHeaders(),
    data: { model: MODEL, messages: [{ role: 'user', content: 'hello' }], stream: true },
  });
  expect(response.ok()).toBeTruthy();
  const raw = await response.text();
  expect(raw).toContain('data:');
  const { frames, text } = accumulatedStreamText(raw);
  expect(frames).toBeGreaterThan(0);
  expect(text).toContain(EXPECT_TEXT);
  expect(raw).toContain('[DONE]');
});

test('responses non-stream returns a response object with stub text', async ({ request }) => {
  const response = await request.post(`${APP}/v1/responses`, {
    headers: bearerHeaders(),
    data: { model: MODEL, input: 'hello' },
  });
  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as { object?: string };
  expect(body.object).toBe('response');
  expect(JSON.stringify(body)).toContain(EXPECT_TEXT);
});

test('responses stream completes with stub text', async ({ request }) => {
  const response = await request.post(`${APP}/v1/responses`, {
    headers: bearerHeaders(),
    data: { model: MODEL, input: 'hello', stream: true },
  });
  expect(response.ok()).toBeTruthy();
  const raw = await response.text();
  expect(raw).toContain('response.completed');
  expect(raw).toContain(EXPECT_TEXT);
});

for (const path of ['/v1/messages', '/anthropic/v1/messages']) {
  test(`messages ${path} returns a message with stub text`, async ({ request }) => {
    const response = await request.post(`${APP}${path}`, {
      headers: { 'x-api-key': secret, 'anthropic-version': '2023-06-01' },
      data: { model: MODEL, max_tokens: 32, messages: [{ role: 'user', content: 'hello' }] },
    });
    expect(response.ok()).toBeTruthy();
    const body = (await response.json()) as { type?: string };
    expect(body.type).toBe('message');
    expect(JSON.stringify(body)).toContain(EXPECT_TEXT);
  });
}

test('session affinity persists in request stats and upstream saw calls', async ({ request }) => {
  const sessioned = await request.post(`${APP}/v1/chat/completions`, {
    headers: { ...bearerHeaders(), 'x-opencode-session': SESSION_ID },
    data: { model: MODEL, messages: [{ role: 'user', content: 'session check' }] },
  });
  expect(sessioned.status()).toBe(200);
  const stats = await request.get(`${APP}/api/requests?limit=100`);
  expect(stats.ok()).toBeTruthy();
  const rows = (((await stats.json()) as { data?: RequestRow[] }).data ?? []);
  expect(rows.filter((row) => row.session_id === SESSION_ID).length).toBeGreaterThanOrEqual(1);
  expect(rows.filter((row) => row.model === MODEL).length).toBeGreaterThanOrEqual(5);
  expect(rows.filter((row) => row.model === MODEL && (row.attempts ?? 1) >= 1).length).toBeGreaterThanOrEqual(1);
  const upstream = await request.get(`${MOCK_OPENROUTER}/__mock/requests`);
  expect(upstream.ok()).toBeTruthy();
  const calls = (((await upstream.json()) as { data?: MockCall[] }).data ?? []);
  expect(calls.filter((call) => call.method === 'POST' && /chat\/completions|responses|messages/.test(call.path ?? '')).length).toBeGreaterThanOrEqual(1);
});
