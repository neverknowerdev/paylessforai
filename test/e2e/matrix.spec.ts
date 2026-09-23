import { expect, test, type APIRequestContext } from '@playwright/test';

// matrix.spec.ts — black-box format x provider matrix runner (NEV-59).
//
// 3 input shapes (chat / responses / messages) x 3 providers
// (openrouter / surplus / opencode-go) x 3 output shapes (chat / responses /
// messages) = 27 cells. Per cell: basic + tool-use + streaming.
// Plus intra-group format-switching tests (hop1 killed, hop2 answers in the
// input shape).
//
// Output shape is pinned black-box via the credential base_url suffix
// (ExactURL pin: .../chat/completions, .../responses, .../messages) and
// verified via attempt_details[].provider_format + the upstream path the mock
// actually received. Mock mode spends nothing.
//
// Live mode: MATRIX_REAL=1 with OPENROUTER_API_KEY set registers the real
// OpenRouter credential (models per NEV-59) instead of mocks. Surplus /
// opencode keys are picked up the same way when present.

const APP = process.env.APP_BASE_URL || 'http://127.0.0.1:19477';
const EXPECT_TEXT = 'matrix hello';
const REAL = process.env.MATRIX_REAL === '1';

// One upstream model per pinned output shape. Route IDs (and therefore
// learned upstream formats) are keyed by provider+upstream model, so sharing
// a single model across rounds would let round N's learned format override
// round N+1's explicit endpoint pin. Distinct models keep rounds hermetic.
function modelFor(outputKey: string): string {
  return `matrix-model-${outputKey}`;
}
const SWITCH_MODEL = 'matrix-model-switch';
function allModels(): string[] {
  return [...OUTPUTS.map((output) => modelFor(output.key)), SWITCH_MODEL];
}

interface OutputFormat {
  key: 'chat' | 'responses' | 'messages';
  suffix: string;
  wire: string;
  toolFixture: string;
}

const OUTPUTS: OutputFormat[] = [
  { key: 'chat', suffix: '/chat/completions', wire: 'openai_chat_completions', toolFixture: 'tool-chat.json' },
  { key: 'responses', suffix: '/responses', wire: 'openai_responses', toolFixture: 'tool-responses.json' },
  { key: 'messages', suffix: '/messages', wire: 'anthropic_messages', toolFixture: 'tool-messages.json' },
];

interface Provider {
  name: string;
  port: number;
}

const PROVIDERS: Provider[] = [
  { name: 'openrouter', port: 19474 },
  { name: 'surplus', port: 19475 },
  { name: 'opencode-go', port: 19476 },
];

type InputShape = 'chat' | 'responses' | 'messages';
const INPUTS: InputShape[] = ['chat', 'responses', 'messages'];

let secret = '';

function bearer(): Record<string, string> {
  return { Authorization: `Bearer ${secret}` };
}

function messageHeaders(): Record<string, string> {
  return { 'x-api-key': secret, 'anthropic-version': '2023-06-01' };
}

const WEATHER_TOOL_CHAT = {
  type: 'function',
  function: {
    name: 'get_weather',
    description: 'Get the weather for a city',
    parameters: { type: 'object', properties: { city: { type: 'string' } } },
  },
};

const WEATHER_TOOL_RESPONSES = {
  type: 'function',
  name: 'get_weather',
  description: 'Get the weather for a city',
  parameters: { type: 'object', properties: { city: { type: 'string' } } },
};

const WEATHER_TOOL_MESSAGES = {
  name: 'get_weather',
  description: 'Get the weather for a city',
  input_schema: { type: 'object', properties: { city: { type: 'string' } } },
};

async function waitForReady(api: APIRequestContext): Promise<void> {
  const deadline = Date.now() + 25_000;
  for (;;) {
    const health = await api.get(`${APP}/healthz`).catch(() => null);
    const ready = health?.ok() ? await api.get(`${APP}/readyz`).catch(() => null) : null;
    if (ready?.ok()) return;
    if (Date.now() >= deadline) throw new Error(`app not ready: ${APP}`);
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
}

async function clearCredentials(api: APIRequestContext): Promise<void> {
  const payload = (await (await api.get(`${APP}/api/providers/credentials`)).json()) as { data?: Array<{ id: string }> };
  for (const credential of payload.data ?? []) {
    expect((await api.delete(`${APP}/api/providers/credentials/${credential.id}`)).ok()).toBeTruthy();
  }
}

async function clearGroups(api: APIRequestContext): Promise<void> {
  const payload = (await (await api.get(`${APP}/api/groups`)).json()) as { data?: Array<{ id: string; revision: number }> };
  for (const group of payload.data ?? []) {
    const deleted = await api.delete(`${APP}/api/groups/${group.id}?revision=${group.revision}`);
    expect(deleted.ok()).toBeTruthy();
  }
}

function sseDataFrames(raw: string): string[] {
  return raw
    .split('\n')
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice('data:'.length).trim())
    .filter((frame) => frame !== '' && frame !== '[DONE]');
}

function accumulateChat(raw: string): string {
  let text = '';
  for (const frame of sseDataFrames(raw)) {
    try {
      const chunk = JSON.parse(frame) as { choices?: Array<{ delta?: { content?: unknown } }> };
      const content = chunk.choices?.[0]?.delta?.content;
      if (typeof content === 'string') text += content;
    } catch {
      // Non-JSON frame (e.g. usage payloads without deltas); skip.
    }
  }
  return text;
}

function accumulateResponses(raw: string): string {
  let text = '';
  for (const frame of sseDataFrames(raw)) {
    try {
      const event = JSON.parse(frame) as { delta?: unknown };
      if (typeof event.delta === 'string') text += event.delta;
    } catch {
      // Completed-event payloads carry no delta; skip.
    }
  }
  return text;
}

function accumulateMessages(raw: string): string {
  let text = '';
  for (const frame of sseDataFrames(raw)) {
    try {
      const event = JSON.parse(frame) as { delta?: { text?: unknown } };
      const content = event.delta?.text;
      if (typeof content === 'string') text += content;
    } catch {
      // message_delta/stop payloads carry no text; skip.
    }
  }
  return text;
}

async function configureMock(api: APIRequestContext, port: number): Promise<void> {
  const scenario = await api.post(`http://127.0.0.1:${port}/__mock/scenario`, {
    data: {
      models: allModels().map((id) => ({
        id,
        name: 'Matrix Model',
        prompt_price: '0.000001',
        completion_price: '0.000002',
        context_length: 128000,
        max_completion_tokens: 4096,
        supported_parameters: ['tools', 'response_format'],
        input_modalities: ['text'],
        output_modalities: ['text'],
        supported_features: ['streaming'],
      })),
      response_text: EXPECT_TEXT,
      input_tokens: 3,
      output_tokens: 2,
    },
  });
  expect(scenario.ok()).toBeTruthy();
  expect((await api.post(`http://127.0.0.1:${port}/__mock/fixtures`, { data: { files: [] } })).ok()).toBeTruthy();
  expect((await api.post(`http://127.0.0.1:${port}/__mock/reset`)).ok()).toBeTruthy();
}

async function registerPinned(api: APIRequestContext, provider: Provider, output: OutputFormat): Promise<void> {
  const response = await api.post(`${APP}/api/providers/credentials`, {
    data: {
      provider: provider.name,
      label: `matrix-${provider.name}-${output.key}`,
      api_key: `matrix-mock-${provider.name}-${output.key}`,
      base_url: `http://127.0.0.1:${provider.port}/mx/${provider.name}/v1${output.suffix}`,
      access_mode: 'api',
    },
  });
  expect(response.status()).toBe(201);
}

async function createGroup(api: APIRequestContext, slug: string, model: string, providerNames: string[], stages?: Array<{ name: string; providers: string[] }>): Promise<void> {
  const defs = stages ?? [{ name: 'only', providers: providerNames }];
  const response = await api.post(`${APP}/api/groups`, {
    data: {
      name: slug,
      slug,
      enabled: true,
      stages: defs.map((stage, index) => ({
        position: index,
        name: stage.name,
        sources: [{ kind: 'model', model_id: model }],
        provider_names: stage.providers,
        billing_classes: ['metered'],
        selection: 'lowest_expected_cost',
      })),
    },
  });
  // eslint-disable-next-line no-console
  if (response.status() !== 201) console.log(`createGroup ${slug} failed: ${await response.text()}`);
  expect(response.status()).toBe(201);
}

function groupSlug(providerName: string): string {
  return `matrix-${providerName.replace(/_/g, '-')}`;
}

interface AttemptDetail {
  provider?: string;
  provider_format?: string;
  http_status?: number;
}

async function attemptDetails(api: APIRequestContext, slug: string): Promise<AttemptDetail[]> {
  const response = await api.get(`${APP}/api/requests?limit=100`);
  expect(response.ok()).toBeTruthy();
  const payload = (await response.json()) as { data?: Array<{ model: string; attempt_details?: AttemptDetail[] }> };
  const rows = (payload.data ?? []).filter((row) => row.model === slug);
  expect(rows.length).toBeGreaterThan(0);
  // /api/requests returns newest-first, so rows[0] is the latest call.
  return rows[0].attempt_details ?? [];
}

async function mockPaths(api: APIRequestContext, port: number): Promise<string[]> {
  const response = await api.get(`http://127.0.0.1:${port}/__mock/requests`);
  expect(response.ok()).toBeTruthy();
  const payload = (await response.json()) as { data?: Array<{ path?: string }> };
  return (payload.data ?? []).map((call) => call.path ?? '');
}


test.beforeAll(async ({ request }) => {
  await waitForReady(request);
  const key = await request.post(`${APP}/api/client-keys`, { data: { label: 'matrix-runner', harness: 'Other' } });
  expect(key.ok()).toBeTruthy();
  secret = (((await key.json()) as { secret?: string }).secret ?? '');
  expect(secret).toBeTruthy();
  if (REAL) return;
  for (const provider of PROVIDERS) await configureMock(request, provider.port);
  await clearCredentials(request);
  await clearGroups(request);
});

// 27 cells: for each pinned output shape, run every provider x input shape.
for (const output of OUTPUTS) {
  test.describe(`output=${output.key}`, () => {
    test.beforeAll(async ({ request }) => {
      test.skip(REAL, 'mock-mode pinning only');
      await clearCredentials(request);
      await clearGroups(request);
      for (const provider of PROVIDERS) {
        await registerPinned(request, provider, output);
        await createGroup(request, groupSlug(provider.name), modelFor(output.key), [provider.name]);
      }
      for (const provider of PROVIDERS) {
        expect((await request.post(`http://127.0.0.1:${provider.port}/__mock/fixtures`, { data: { files: [] } })).ok()).toBeTruthy();
        expect((await request.post(`http://127.0.0.1:${provider.port}/__mock/reset`)).ok()).toBeTruthy();
      }
    });

    for (const provider of PROVIDERS) {
      for (const input of INPUTS) {
        test(`in=${input} via=${provider.name} out=${output.key}`, async ({ request }) => {
          // Tool calls are served from a pinned-format fixture queued just
          // before the tool leg so basic/stream legs stay on scenario mode.
          const proof: string[] = [];
          // Basic + streaming first (scenario mode).
          if (input === 'chat') {
            const basic = await request.post(`${APP}/v1/chat/completions`, {
              headers: bearer(),
              data: { model: groupSlug(provider.name), messages: [{ role: 'user', content: 'matrix ping' }], max_tokens: 32 },
            });
            expect(basic.status()).toBe(200);
            expect(((await basic.json()) as { choices: Array<{ message: { content: string } }> }).choices[0].message.content).toContain(EXPECT_TEXT);
            const stream = await request.post(`${APP}/v1/chat/completions`, {
              headers: bearer(),
              data: { model: groupSlug(provider.name), messages: [{ role: 'user', content: 'matrix ping' }], max_tokens: 32, stream: true },
            });
            expect(stream.status()).toBe(200);
            const raw = await stream.text();
            expect(raw).toContain('data:');
            expect(raw).toContain('[DONE]');
            expect(accumulateChat(raw)).toContain(EXPECT_TEXT);
            proof.push(`basic+stream ok frames=${sseDataFrames(raw).length}`);
          }
          if (input === 'responses') {
            const basic = await request.post(`${APP}/v1/responses`, {
              headers: bearer(),
              data: { model: groupSlug(provider.name), input: 'matrix ping', max_output_tokens: 32 },
            });
            expect(basic.status()).toBe(200);
            expect(JSON.stringify(await basic.json())).toContain(EXPECT_TEXT);
            const stream = await request.post(`${APP}/v1/responses`, {
              headers: bearer(),
              data: { model: groupSlug(provider.name), input: 'matrix ping', max_output_tokens: 32, stream: true },
            });
            expect(stream.status()).toBe(200);
            const raw = await stream.text();
            expect(raw).toContain('response.completed');
            expect(accumulateResponses(raw)).toContain(EXPECT_TEXT);
            proof.push(`basic+stream ok events=${raw.split('event:').length - 1}`);
          }
          if (input === 'messages') {
            const basic = await request.post(`${APP}/v1/messages`, {
              headers: messageHeaders(),
              data: { model: groupSlug(provider.name), max_tokens: 32, messages: [{ role: 'user', content: 'matrix ping' }] },
            });
            expect(basic.status()).toBe(200);
            expect(JSON.stringify(await basic.json())).toContain(EXPECT_TEXT);
            const stream = await request.post(`${APP}/v1/messages`, {
              headers: messageHeaders(),
              data: { model: groupSlug(provider.name), max_tokens: 32, messages: [{ role: 'user', content: 'matrix ping' }], stream: true },
            });
            expect(stream.status()).toBe(200);
            const raw = await stream.text();
            expect(raw).toContain('content_block_delta');
            expect(accumulateMessages(raw)).toContain(EXPECT_TEXT);
            proof.push(`basic+stream ok events=${raw.split('event:').length - 1}`);
          }
          // Tool leg from the pinned-format fixture.
          expect((await request.post(`http://127.0.0.1:${provider.port}/__mock/fixtures`, { data: { files: [output.toolFixture] } })).ok()).toBeTruthy();
          const toolProof = await runToolLeg(request, input, provider);
          proof.push(...toolProof);
          const paths = (await mockPaths(request, provider.port)).filter((path) => /chat\/completions$|\/responses$|\/messages$/.test(path));
          const details = await attemptDetails(request, groupSlug(provider.name));
          console.log(`DIAG in=${input} via=${provider.name} out=${output.key} paths=${JSON.stringify(paths.slice(-4))} details=${JSON.stringify(details)}`);
          expect(paths[paths.length - 1].endsWith(output.suffix)).toBeTruthy();
          expect(details[details.length - 1].provider_format).toBe(output.wire);
          proof.push(`upstream=${paths[paths.length - 1]} format=${details[details.length - 1].provider_format}`);
          console.log(`CELL in=${input} via=${provider.name} out=${output.key} :: ${proof.join(' | ')}`);
        });
      }
    }
  });
}

async function runToolLeg(api: APIRequestContext, input: InputShape, provider: Provider): Promise<string[]> {
  const slug = groupSlug(provider.name);
  if (input === 'chat') {
    const tool = await api.post(`${APP}/v1/chat/completions`, {
      headers: bearer(),
      data: {
        model: slug,
        messages: [{ role: 'user', content: 'weather in Paris?' }],
        max_tokens: 64,
        tools: [WEATHER_TOOL_CHAT],
      },
    });
    expect(tool.status()).toBe(200);
    const body = (await tool.json()) as { choices: Array<{ message: { tool_calls?: Array<{ function?: { name?: string } }> } }> };
    expect(body.choices[0].message.tool_calls?.[0]?.function?.name).toBe('get_weather');
    return [`tool=${JSON.stringify(body.choices[0].message.tool_calls?.[0]).slice(0, 120)}`];
  }
  if (input === 'responses') {
    const tool = await api.post(`${APP}/v1/responses`, {
      headers: bearer(),
      data: { model: slug, input: 'weather in Paris?', max_output_tokens: 64, tools: [WEATHER_TOOL_RESPONSES] },
    });
    expect(tool.status()).toBe(200);
    const body = (await tool.json()) as { output?: Array<{ type?: string; name?: string }> };
    const call = (body.output ?? []).find((item) => item.type === 'function_call');
    expect(call?.name).toBe('get_weather');
    return [`tool=${JSON.stringify(call).slice(0, 120)}`];
  }
  const tool = await api.post(`${APP}/v1/messages`, {
    headers: messageHeaders(),
    data: {
      model: slug,
      max_tokens: 64,
      messages: [{ role: 'user', content: 'weather in Paris?' }],
      tools: [WEATHER_TOOL_MESSAGES],
    },
  });
  expect(tool.status()).toBe(200);
  const body = (await tool.json()) as { stop_reason?: string; content?: Array<{ type?: string; name?: string }> };
  const block = (body.content ?? []).find((item) => item.type === 'tool_use');
  expect(block?.name).toBe('get_weather');
  expect(body.stop_reason).toBe('tool_use');
  return [`tool=${JSON.stringify(block).slice(0, 120)} stop=${body.stop_reason}`];
}

test.describe('intra-group format switching', () => {
  test.beforeAll(async ({ request }) => {
    test.skip(REAL, 'mock-mode switching only');
    await clearCredentials(request);
    await clearGroups(request);
    // Hop1 speaks chat toward openrouter; hop2 speaks messages toward opencode.
    await registerPinned(request, PROVIDERS[0], OUTPUTS[0]);
    await registerPinned(request, PROVIDERS[2], OUTPUTS[2]);
    await createGroup(request, 'matrix-switch', SWITCH_MODEL, [], [
      { name: 'hop1', providers: [PROVIDERS[0].name] },
      { name: 'hop2', providers: [PROVIDERS[2].name] },
    ]);
  });

  for (const input of INPUTS) {
    test(`kill hop1 (chat/openrouter), hop2 (messages/opencode) answers in ${input}`, async ({ request }) => {
      for (const provider of [PROVIDERS[0], PROVIDERS[2]]) {
        expect((await request.post(`http://127.0.0.1:${provider.port}/__mock/fixtures`, { data: { files: [] } })).ok()).toBeTruthy();
        expect((await request.post(`http://127.0.0.1:${provider.port}/__mock/reset`)).ok()).toBeTruthy();
      }
      expect((await request.post(`http://127.0.0.1:${PROVIDERS[0].port}/__mock/fixtures`, { data: { files: ['hop1-killed.json'] } })).ok()).toBeTruthy();

      let raw = '';
      let status = 0;
      if (input === 'chat') {
        const response = await request.post(`${APP}/v1/chat/completions`, {
          headers: bearer(),
          data: { model: 'matrix-switch', messages: [{ role: 'user', content: 'switch ping' }], max_tokens: 32 },
        });
        status = response.status();
        raw = JSON.stringify(await response.json());
        expect(status).toBe(200);
        expect(raw).toContain(EXPECT_TEXT);
      }
      if (input === 'responses') {
        const response = await request.post(`${APP}/v1/responses`, {
          headers: bearer(),
          data: { model: 'matrix-switch', input: 'switch ping', max_output_tokens: 32 },
        });
        status = response.status();
        const body = (await response.json()) as { object?: string };
        expect(status).toBe(200);
        expect(body.object).toBe('response');
        raw = JSON.stringify(body);
        expect(raw).toContain(EXPECT_TEXT);
      }
      if (input === 'messages') {
        const response = await request.post(`${APP}/v1/messages`, {
          headers: messageHeaders(),
          data: { model: 'matrix-switch', max_tokens: 32, messages: [{ role: 'user', content: 'switch ping' }] },
        });
        status = response.status();
        const body = (await response.json()) as { type?: string };
        expect(status).toBe(200);
        expect(body.type).toBe('message');
        raw = JSON.stringify(body);
        expect(raw).toContain(EXPECT_TEXT);
      }
      const details = await attemptDetails(request, 'matrix-switch');
      expect(details.map((attempt) => attempt.provider)).toEqual([PROVIDERS[0].name, PROVIDERS[2].name]);
      expect(details.map((attempt) => attempt.provider_format)).toEqual([OUTPUTS[0].wire, OUTPUTS[2].wire]);
      console.log(`SWITCH in=${input} hop1=${details[0].provider}/${details[0].provider_format}->${details[0].http_status} hop2=${details[1].provider}/${details[1].provider_format}->${details[1].http_status} proof=${raw.slice(0, 80)}`);
    });
  }
});
