import { join } from 'node:path';
import { execFileSync } from 'node:child_process';
import { expect, test, type APIRequestContext } from '@playwright/test';

const model = 'translation-matrix-model';
const databasePath = join(process.env.PAYLESSFORAI_E2E_DATA_DIR || '/tmp/paylessforai-e2e', 'paylessforai.db');
const formats = {
  chat: 'openai_chat_completions',
  free: 'openai_responses',
  subscription: 'anthropic_messages',
  metered: 'openai_chat_completions',
};

const providerPorts = {
  free: 19474,
  subscription: 19475,
  metered: 19476,
};

type ProviderName = keyof typeof providerPorts;

const providerDefinitions: Record<ProviderName, { name: string; label: string; key: string; baseURL: string; accessMode: string; fee?: string }> = {
  free: {
    name: 'translation-free',
    label: 'Free route',
    key: 'translation-free-key',
    baseURL: 'http://127.0.0.1:19474/translation/free/v1',
    accessMode: 'api',
  },
  subscription: {
    name: 'translation-subscription',
    label: 'Subscription route',
    key: 'translation-subscription-key',
    baseURL: 'http://127.0.0.1:19475/translation/subscription/v1/messages',
    accessMode: 'subscription',
    fee: '20',
  },
  metered: {
    name: 'translation-metered',
    label: 'Metered route',
    key: 'translation-metered-key',
    baseURL: 'http://127.0.0.1:19476/translation/metered/v1/chat/completions',
    accessMode: 'api',
  },
};

const fixtureCases = {
  unknownFormatFallback: {
    free: ['05-chat-format-error.json', '06-responses-success.json'],
    subscription: [],
    metered: [],
  },
  twoFailedThenMeteredSucceeds: {
    free: ['01-unavailable.json'],
    subscription: ['01-unavailable.json', '01-retry-unavailable.json'],
    metered: ['01-success.json'],
  },
  freeFailsThenSubscriptionSucceeds: {
    free: ['02-unavailable.json'],
    subscription: ['02-success.json'],
    metered: ['02-unavailable.json', '02-retry-unavailable.json'],
  },
  freeSucceeds: {
    free: ['03-success.json'],
    subscription: ['03-unavailable.json', '03-retry-unavailable.json'],
    metered: ['03-unavailable.json', '03-retry-unavailable.json'],
  },
  allProvidersFail: {
    free: ['04-unavailable.json'],
    subscription: ['04-unavailable.json', '04-retry-unavailable.json'],
    metered: ['04-unavailable.json', '04-retry-unavailable.json'],
  },
} satisfies Record<string, Record<ProviderName, string[]>>;

async function setScenario(request: APIRequestContext, port: number, modelID: string, promptPrice: string, completionPrice: string) {
  const response = await request.post(`http://127.0.0.1:${port}/__mock/scenario`, {
    data: {
      models: [{ id: modelID, name: 'Translation Model', prompt_price: promptPrice, completion_price: completionPrice, context_length: 128000, max_completion_tokens: 4096, supported_parameters: ['tools', 'response_format'], input_modalities: ['text'], output_modalities: ['text'], supported_features: ['streaming'] }],
      response_text: 'unused scenario response',
    },
  });
  expect(response.ok()).toBeTruthy();
}

async function clearProviderCredentials(request: APIRequestContext) {
  const response = await request.get('/api/providers/credentials');
  expect(response.ok()).toBeTruthy();
  const payload = await response.json();
  await Promise.all((payload.data as Array<{ id: string }>).map(async (credential) => {
    const deleted = await request.delete(`/api/providers/credentials/${credential.id}`);
    expect(deleted.ok()).toBeTruthy();
  }));
}

async function setFixtures(request: APIRequestContext, fixtures: Record<ProviderName, string[]>) {
  await Promise.all((Object.keys(providerPorts) as ProviderName[]).map(async (provider) => {
    const port = providerPorts[provider];
    const reset = await request.post(`http://127.0.0.1:${port}/__mock/reset`);
    expect(reset.ok()).toBeTruthy();
    const configured = await request.post(`http://127.0.0.1:${port}/__mock/fixtures`, { data: { files: fixtures[provider] } });
    expect(configured.ok()).toBeTruthy();
  }));
}

function readModelRouteFormats(): Array<{ provider: string; format: string }> {
  const raw = execFileSync('sqlite3', [
    '-json',
    databasePath,
    `SELECT provider, COALESCE(format, '') AS format FROM model_routes WHERE model_id = '${model}' ORDER BY provider`,
  ], { encoding: 'utf8' }).trim();
  return raw === '' ? [] : JSON.parse(raw) as Array<{ provider: string; format: string }>;
}

async function latestRequest(request: APIRequestContext) {
  const response = await request.get('/api/requests?limit=100');
  expect(response.ok()).toBeTruthy();
  const payload = await response.json();
  const item = payload.data.find((candidate: { model: string }) => candidate.model === model);
  expect(item).toBeTruthy();
  return item;
}

test('routes one model across free, subscription, and metered formats with durable discovery', async ({ page, request }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'PayLessForAI' })).toBeVisible();
  await clearProviderCredentials(request);

  await setScenario(request, providerPorts.free, `${model}-free`, '0', '0');
  await setScenario(request, providerPorts.subscription, model, '0.000003', '0.000004');
  await setScenario(request, providerPorts.metered, model, '0.000001', '0.000002');

  for (const provider of Object.keys(providerDefinitions) as ProviderName[]) {
    const definition = providerDefinitions[provider];
    const response = await request.post('/api/providers/credentials', {
      data: {
        provider: definition.name,
        label: definition.label,
        api_key: definition.key,
        base_url: definition.baseURL,
        access_mode: definition.accessMode,
        subscription_fee_usd: definition.fee,
      },
    });
    expect(response.status()).toBe(201);
  }

  const catalog = await (await request.get('/api/models')).json();
  const routes = catalog.data.filter((item: { model: string }) => item.model === model);
  expect(routes).toHaveLength(3);
  expect(routes.map((item: { billing_class: string }) => item.billing_class).sort()).toEqual(['free', 'metered', 'subscription']);
  expect(readModelRouteFormats()).toEqual([]);

  const keyResponse = await request.post('/api/client-keys', { data: { label: 'translation-e2e', harness: 'Other' } });
  expect(keyResponse.status()).toBe(201);
  const secret = (await keyResponse.json()).secret as string;

  await setFixtures(request, fixtureCases.unknownFormatFallback);
  const formatFallback = await request.post('/v1/chat/completions', { headers: { Authorization: `Bearer ${secret}` }, data: { model, messages: [{ role: 'user', content: 'discover the provider format' }] } });
  expect(formatFallback.status()).toBe(200);
  expect((await formatFallback.json()).choices[0].message.content).toBe('responses format success');
  const freeRequests = await (await request.get(`http://127.0.0.1:${providerPorts.free}/__mock/requests`)).json();
  const inferencePaths = (freeRequests.data as Array<{ path: string }>).map((item) => item.path).filter((path) => path.endsWith('/chat/completions') || path.endsWith('/responses') || path.endsWith('/messages'));
  expect(inferencePaths).toEqual(['/translation/free/v1/chat/completions', '/translation/free/v1/responses']);
  let stats = await latestRequest(request);
  expect(stats.attempts).toBe(2);
  expect(stats.attempt_details.map((attempt: { provider_format: string; http_status: number }) => [attempt.provider_format, attempt.http_status])).toEqual([[formats.chat, 500], [formats.free, 200]]);
  expect(readModelRouteFormats()).toEqual([{ provider: 'translation-free', format: formats.free }]);

  await setFixtures(request, fixtureCases.twoFailedThenMeteredSucceeds);
  const first = await request.post('/v1/chat/completions', { headers: { Authorization: `Bearer ${secret}` }, data: { model, messages: [{ role: 'user', content: 'two providers fail' }] } });
  expect(first.status()).toBe(200);
  expect((await first.json()).choices[0].message.content).toBe('metered provider success');
  stats = await latestRequest(request);
  expect(stats.attempts).toBe(4);
  expect(stats.attempt_details.map((attempt: { provider: string }) => attempt.provider)).toEqual(['translation-free', 'translation-subscription', 'translation-subscription', 'translation-metered']);
  expect(stats.attempt_details.map((attempt: { provider_format: string }) => attempt.provider_format)).toEqual([formats.free, formats.subscription, formats.subscription, formats.metered]);
  expect(stats.attempt_details.map((attempt: { http_status: number }) => attempt.http_status)).toEqual([503, 503, 503, 200]);
  expect(readModelRouteFormats()).toEqual([
    { provider: 'translation-free', format: formats.free },
    { provider: 'translation-metered', format: formats.metered },
  ]);

  await setFixtures(request, fixtureCases.freeFailsThenSubscriptionSucceeds);
  const second = await request.post('/v1/chat/completions', { headers: { Authorization: `Bearer ${secret}` }, data: { model, messages: [{ role: 'user', content: 'subscription fallback' }] } });
  expect(second.status()).toBe(200);
  expect((await second.json()).choices[0].message.content).toBe('subscription provider success');
  stats = await latestRequest(request);
  expect(stats.attempts).toBe(2);
  expect(stats.attempt_details.map((attempt: { provider: string }) => attempt.provider)).toEqual(['translation-free', 'translation-subscription']);
  expect(stats.attempt_details.map((attempt: { provider_format: string }) => attempt.provider_format)).toEqual([formats.free, formats.subscription]);
  expect(readModelRouteFormats()).toEqual(expect.arrayContaining([
    { provider: 'translation-metered', format: formats.metered },
    { provider: 'translation-subscription', format: formats.subscription },
  ]));

  await setFixtures(request, fixtureCases.freeSucceeds);
  const third = await request.post('/v1/chat/completions', { headers: { Authorization: `Bearer ${secret}` }, data: { model, messages: [{ role: 'user', content: 'free success' }] } });
  expect(third.status()).toBe(200);
  expect((await third.json()).choices[0].message.content).toBe('free provider success');
  stats = await latestRequest(request);
  expect(stats.attempts).toBe(1);
  expect(stats.attempt_details[0]).toMatchObject({ provider: 'translation-free', provider_format: formats.free, http_status: 200 });
  expect(readModelRouteFormats().sort((a, b) => a.provider.localeCompare(b.provider))).toEqual([
    { provider: 'translation-free', format: formats.free },
    { provider: 'translation-metered', format: formats.metered },
    { provider: 'translation-subscription', format: formats.subscription },
  ]);

  await setFixtures(request, fixtureCases.allProvidersFail);
  const failed = await request.post('/v1/chat/completions', { headers: { Authorization: `Bearer ${secret}` }, data: { model, messages: [{ role: 'user', content: 'all unavailable' }] } });
  expect(failed.status()).toBe(503);
  expect(await failed.json()).toMatchObject({
    error: {
      type: 'payless_error',
      code: 'all_provider_attempts_failed',
      message: 'all provider attempts failed',
      attempts: 5,
      errors: [
        { provider: 'translation-free', account: 'Free route', error: 'free provider exhausted' },
        { provider: 'translation-subscription', account: 'Subscription route', error: 'subscription provider exhausted' },
        { provider: 'translation-subscription', account: 'Subscription route', error: 'subscription provider exhausted on retry' },
        { provider: 'translation-metered', account: 'Metered route', error: 'metered provider exhausted' },
        { provider: 'translation-metered', account: 'Metered route', error: 'metered provider exhausted on retry' },
      ],
    },
  });
  stats = await latestRequest(request);
  expect(stats.state).toBe('failed');
  expect(stats.attempt_details.map((attempt: { provider: string; provider_format: string; http_status: number }) => [attempt.provider, attempt.provider_format, attempt.http_status])).toEqual([
    ['translation-free', formats.free, 503],
    ['translation-subscription', formats.subscription, 503],
    ['translation-subscription', formats.subscription, 503],
    ['translation-metered', formats.metered, 503],
    ['translation-metered', formats.metered, 503],
  ]);

  expect(readModelRouteFormats().sort((a, b) => a.provider.localeCompare(b.provider))).toEqual([
    { provider: 'translation-free', format: formats.free },
    { provider: 'translation-metered', format: formats.metered },
    { provider: 'translation-subscription', format: formats.subscription },
  ]);
});
