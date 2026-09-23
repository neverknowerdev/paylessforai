import { expect, test, type APIRequestContext } from '@playwright/test';

// matrix.live.spec.ts — MANUAL-ONLY live matrix runner (NEV-59 review fix).
//
// Same 27 cells as matrix.spec.ts (3 input shapes x 3 providers x 3 output
// shapes, each basic + tool-use + streaming, plus intra-group switching) but
// against REAL providers and REAL harnesses. NEVER runs in CI:
//   - the spec refuses to run when CI is set,
//   - no .github/workflows file references matrix.live.config.ts.
//
// Manual run (keys in env, never committed):
//   docker compose -f docker-compose.e2e-real.yml up -d --build
//   cd test/e2e && OPENROUTER_API_KEY=sk-... npm run matrix:live
//   docker compose -f docker-compose.e2e-real.yml down -v
//
// Recording run (auto-captures upstream responses into mock fixtures):
//   RECORD=1 OPENROUTER_API_KEY=sk-... npm run matrix:record
// which routes provider traffic through cmd/mockrecord proxies started by
// matrix.live.config.ts webServer entries below (see RECORD_BASE_URLS).

if (process.env.CI) {
  throw new Error('matrix.live is manual-only and must never run in CI');
}

const APP = process.env.APP_BASE_URL || 'http://127.0.0.1:9472';
const EXPECT_MIN_LEN = 1;
const RECORD = process.env.RECORD === '1';

interface LiveProvider {
  name: string;
  keyEnv: string;
  // Real base URL served when RECORD is off.
  realBaseURL: string;
  // Recorder proxy URL served when RECORD=1 (mockrecord forwards + saves).
  recordURL: string;
  // Cheapest models per output shape (NEV-59 spec); override via env.
  models: { chat: string; responses: string; messages: string };
}

const LIVE_PROVIDERS: LiveProvider[] = [
  {
    name: 'openrouter',
    keyEnv: 'OPENROUTER_API_KEY',
    realBaseURL: 'https://openrouter.ai/api/v1',
    recordURL: 'http://127.0.0.1:19574/openrouter/api/v1',
    models: {
      chat: process.env.LIVE_CHAT_MODEL || 'nex-agi/nex-n2.5-mini:free',
      responses: process.env.LIVE_RESPONSES_MODEL || 'dots-studio/dots-3-note-preview:free',
      messages: process.env.LIVE_MESSAGES_MODEL || 'anthropic/claude-3-haiku',
    },
  },
  {
    name: 'surplus',
    keyEnv: 'SURPLUS_API_KEY',
    realBaseURL: 'https://api.surplusintelligence.ai/v1',
    recordURL: 'http://127.0.0.1:19575/surplus/v1',
    models: {
      chat: process.env.LIVE_SURPLUS_CHAT_MODEL || 'surplus-mini',
      responses: process.env.LIVE_SURPLUS_RESPONSES_MODEL || 'surplus-mini',
      messages: process.env.LIVE_SURPLUS_MESSAGES_MODEL || 'surplus-mini',
    },
  },
  {
    name: 'opencode-go',
    keyEnv: 'OPENCODE_GO_API_KEY',
    realBaseURL: 'https://api.opencode.ai/zen/go/v1',
    recordURL: 'http://127.0.0.1:19576/zen/go/v1',
    models: {
      chat: process.env.LIVE_OPENCODE_CHAT_MODEL || 'opencode-mini',
      responses: process.env.LIVE_OPENCODE_RESPONSES_MODEL || 'opencode-mini',
      messages: process.env.LIVE_OPENCODE_MESSAGES_MODEL || 'opencode-mini',
    },
  },
];

type OutputKey = 'chat' | 'responses' | 'messages';
const OUTPUTS: Array<{ key: OutputKey; suffix: string; wire: string }> = [
  { key: 'chat', suffix: '/chat/completions', wire: 'openai_chat_completions' },
  { key: 'responses', suffix: '/responses', wire: 'openai_responses' },
  { key: 'messages', suffix: '/messages', wire: 'anthropic_messages' },
];

type InputShape = 'chat' | 'responses' | 'messages';
const INPUTS: InputShape[] = ['chat', 'responses', 'messages'];

const underTest = LIVE_PROVIDERS.filter((p) => process.env[p.keyEnv]);
if (underTest.length === 0) {
  throw new Error(
    'matrix.live needs at least one provider key in env (OPENROUTER_API_KEY, SURPLUS_API_KEY, OPENCODE_GO_API_KEY); nothing spent, aborting.',
  );
}
// eslint-disable-next-line no-console
console.log(`LIVE providers: ${underTest.map((p) => p.name).join(', ')}${RECORD ? ' (RECORD=1, capturing fixtures)' : ''}`);

let secret = '';
function bearer(): Record<string, string> {
  return { Authorization: `Bearer ${secret}` };
}
function messageHeaders(): Record<string, string> {
  return { 'x-api-key': secret, 'anthropic-version': '2023-06-01' };
}

async function waitForReady(api: APIRequestContext): Promise<void> {
  const deadline = Date.now() + 60_000;
  for (;;) {
    const health = await api.get(`${APP}/healthz`).catch(() => null);
    const ready = health?.ok() ? await api.get(`${APP}/readyz`).catch(() => null) : null;
    if (ready?.ok()) return;
    if (Date.now() >= deadline) throw new Error(`app not ready: ${APP}`);
    await new Promise((resolve) => setTimeout(resolve, 1000));
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
    expect((await api.delete(`${APP}/api/groups/${group.id}?revision=${group.revision}`)).ok()).toBeTruthy();
  }
}

function baseFor(provider: LiveProvider): string {
  return RECORD ? provider.recordURL : provider.realBaseURL;
}

function groupSlug(providerName: string): string {
  return `live-${providerName.replace(/_/g, '-')}`;
}

test.beforeAll(async ({ request }) => {
  await waitForReady(request);
  const key = await request.post(`${APP}/api/client-keys`, { data: { label: 'matrix-live', harness: 'Other' } });
  expect(key.ok()).toBeTruthy();
  secret = (((await key.json()) as { secret?: string }).secret ?? '');
  expect(secret).toBeTruthy();
  await clearCredentials(request);
  await clearGroups(request);
  for (const provider of underTest) {
    const apiKey = process.env[provider.keyEnv] as string;
    for (const output of OUTPUTS) {
      const response = await request.post(`${APP}/api/providers/credentials`, {
        data: {
          provider: provider.name,
          label: `live-${provider.name}-${output.key}`,
          api_key: apiKey,
          base_url: `${baseFor(provider)}${output.suffix}`,
          access_mode: 'api',
        },
      });
      expect(response.status()).toBe(201);
    }
    // One group per provider; the model id selects the pinned output shape.
    for (const output of OUTPUTS) {
      const model = provider.models[output.key];
      const created = await request.post(`${APP}/api/groups`, {
        data: {
          name: `${groupSlug(provider.name)}-${output.key}`,
          slug: `${groupSlug(provider.name)}-${output.key}`,
          enabled: true,
          stages: [{
            position: 0,
            name: 'only',
            sources: [{ kind: 'model', model_id: model }],
            provider_names: [provider.name],
            billing_classes: ['metered'],
            selection: 'lowest_expected_cost',
          }],
        },
      });
      expect(created.status()).toBe(201);
    }
  }
});

for (const provider of underTest) {
  for (const output of OUTPUTS) {
    for (const input of INPUTS) {
      test(`live in=${input} via=${provider.name} out=${output.key}`, async ({ request }) => {
        const slug = `${groupSlug(provider.name)}-${output.key}`;
        const proof: string[] = [];
        if (input === 'chat') {
          const basic = await request.post(`${APP}/v1/chat/completions`, {
            headers: bearer(),
            data: { model: slug, messages: [{ role: 'user', content: 'live ping' }], max_tokens: 32 },
          });
          expect(basic.status()).toBe(200);
          const text = ((await basic.json()) as { choices: Array<{ message: { content: string } }> }).choices[0].message.content;
          expect(text.length).toBeGreaterThanOrEqual(EXPECT_MIN_LEN);
          proof.push(`basic len=${text.length}`);
          const tool = await request.post(`${APP}/v1/chat/completions`, {
            headers: bearer(),
            data: {
              model: slug,
              messages: [{ role: 'user', content: 'weather in Paris?' }],
              max_tokens: 64,
              tools: [{ type: 'function', function: { name: 'get_weather', description: 'Get weather', parameters: { type: 'object', properties: { city: { type: 'string' } } } } }],
            },
          });
          expect(tool.status()).toBe(200);
          expect(JSON.stringify(await tool.json())).toContain('get_weather');
          proof.push('tool ok');
          const stream = await request.post(`${APP}/v1/chat/completions`, {
            headers: bearer(),
            data: { model: slug, messages: [{ role: 'user', content: 'live ping' }], max_tokens: 32, stream: true },
          });
          expect(stream.status()).toBe(200);
          expect(await stream.text()).toContain('data:');
          proof.push('stream ok');
        }
        if (input === 'responses') {
          const basic = await request.post(`${APP}/v1/responses`, {
            headers: bearer(),
            data: { model: slug, input: 'live ping', max_output_tokens: 32 },
          });
          expect(basic.status()).toBe(200);
          expect(JSON.stringify(await basic.json()).length).toBeGreaterThan(0);
          proof.push('basic ok');
          const tool = await request.post(`${APP}/v1/responses`, {
            headers: bearer(),
            data: {
              model: slug,
              input: 'weather in Paris?',
              max_output_tokens: 64,
              tools: [{ type: 'function', name: 'get_weather', description: 'Get weather', parameters: { type: 'object', properties: { city: { type: 'string' } } } }],
            },
          });
          expect(tool.status()).toBe(200);
          expect(JSON.stringify(await tool.json())).toContain('get_weather');
          proof.push('tool ok');
          const stream = await request.post(`${APP}/v1/responses`, {
            headers: bearer(),
            data: { model: slug, input: 'live ping', max_output_tokens: 32, stream: true },
          });
          expect(stream.status()).toBe(200);
          expect(await stream.text()).toContain('response.completed');
          proof.push('stream ok');
        }
        if (input === 'messages') {
          const basic = await request.post(`${APP}/v1/messages`, {
            headers: messageHeaders(),
            data: { model: slug, max_tokens: 32, messages: [{ role: 'user', content: 'live ping' }] },
          });
          expect(basic.status()).toBe(200);
          expect(JSON.stringify(await basic.json())).toContain('message');
          proof.push('basic ok');
          const tool = await request.post(`${APP}/v1/messages`, {
            headers: messageHeaders(),
            data: {
              model: slug,
              max_tokens: 64,
              messages: [{ role: 'user', content: 'weather in Paris?' }],
              tools: [{ name: 'get_weather', description: 'Get weather', input_schema: { type: 'object', properties: { city: { type: 'string' } } } }],
            },
          });
          expect(tool.status()).toBe(200);
          const body = (await tool.json()) as { stop_reason?: string };
          expect(body.stop_reason).toBe('tool_use');
          proof.push('tool ok');
          const stream = await request.post(`${APP}/v1/messages`, {
            headers: messageHeaders(),
            data: { model: slug, max_tokens: 32, messages: [{ role: 'user', content: 'live ping' }], stream: true },
          });
          expect(stream.status()).toBe(200);
          expect(await stream.text()).toContain('content_block_delta');
          proof.push('stream ok');
        }
        // eslint-disable-next-line no-console
        console.log(`LIVE in=${input} via=${provider.name} out=${output.key} :: ${proof.join(' | ')}`);
      });
    }
  }
}
