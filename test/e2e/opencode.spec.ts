import { expect, test } from '@playwright/test';

const upstream = 'http://127.0.0.1:19474';

test('sends OpenCode sessions through format fallback, retries, and all client protocols and shows the overall failure', async ({ page, request }) => {
  const credentials = await (await request.get('/api/providers/credentials')).json();
  for (const credential of credentials.data) {
    expect((await request.delete(`/api/providers/credentials/${credential.id}`)).ok()).toBeTruthy();
  }
  const keyResponse = await request.post('/api/client-keys', { data: { label: 'opencode-session-e2e', harness: 'Other' } });
  expect(keyResponse.status()).toBe(201);
  const secret = (await keyResponse.json()).secret;

  for (const provider of ['opencode go', 'opencode-go', 'opencode-zen']) {
    const model = `session-${provider.replaceAll(' ', '-')}-${provider.includes(' ') ? 'custom' : 'builtin'}`;
    const scenario = {
      models: [{ id: model, prompt_price: '0.000001', completion_price: '0.000002', context_length: 128000, max_completion_tokens: 4096 }],
      require_opencode_session: true,
      response_text: 'session accepted',
    };
    expect((await request.post(`${upstream}/__mock/scenario`, { data: scenario })).ok()).toBeTruthy();
    expect((await request.post(`${upstream}/__mock/fixtures`, { data: { files: [] } })).ok()).toBeTruthy();
    const saved = await request.post('/api/providers/credentials', { data: {
      provider, label: 'OpenCode session test', api_key: 'mock-opencode-key',
      base_url: `${upstream}/zen/go/v1`, access_mode: 'api',
    } });
    expect(saved.status()).toBe(201);
    expect((await request.post(`${upstream}/__mock/reset`)).ok()).toBeTruthy();
    expect((await request.post(`${upstream}/__mock/fixtures`, { data: { files: ['05-chat-format-error.json', '06-responses-success.json'] } })).ok()).toBeTruthy();

    // No client session header: generate one, retain it across format discovery,
    // and expose exactly the same value in durable request statistics.
    const generated = await request.post('/v1/chat/completions', {
      headers: { Authorization: `Bearer ${secret}` },
      data: { model, messages: [{ role: 'user', content: 'discover OpenCode format' }] },
    });
    expect(generated.status(), await generated.text()).toBe(200);
    expect((await generated.json()).choices[0].message.content).toBe('responses format success');
    let stats = (await (await request.get('/api/requests?limit=100')).json()).data.find((item: { model: string }) => item.model === model);
    expect(stats.session_id).toMatch(/^[0-9a-f]{32}$/);
    let calls = (await (await request.get(`${upstream}/__mock/requests`)).json()).data.filter((item: { method: string; path: string }) => item.method === 'POST' && !item.path.startsWith('/__mock/'));
    expect(calls.map((item: { path: string }) => item.path)).toEqual(['/zen/go/v1/chat/completions', '/zen/go/v1/responses']);
    expect(calls.map((item: { opencode_session: string }) => item.opencode_session)).toEqual([stats.session_id, stats.session_id]);

    // With the provider format persisted, exercise retries and each inbound API.
    for (const protocol of ['chat/completions', 'responses', 'messages']) {
      expect((await request.post(`${upstream}/__mock/reset`)).ok()).toBeTruthy();
      expect((await request.post(`${upstream}/__mock/fixtures`, { data: { files: [] } })).ok()).toBeTruthy();
      expect((await request.post(`${upstream}/__mock/scenario`, { data: { ...scenario, failure_count: 1 } })).ok()).toBeTruthy();
      const session = `${model}-${protocol.replace('/', '-')}`;
      const response = await request.post(`/v1/${protocol}`, {
        headers: { Authorization: `Bearer ${secret}`, [protocol === 'messages' ? 'x-opencode-session' : 'X-PayLess-Chat-Id']: session },
        data: protocol === 'responses' ? { model, input: 'keep session on retry' } : { model, max_tokens: 32, messages: [{ role: 'user', content: 'keep session on retry' }] },
      });
      expect(response.status()).toBe(200);
      stats = (await (await request.get('/api/requests?limit=100')).json()).data.find((item: { session_id: string }) => item.session_id === session);
      expect(stats.attempts).toBe(2);
      calls = (await (await request.get(`${upstream}/__mock/requests`)).json()).data.filter((item: { method: string; path: string }) => item.method === 'POST' && !item.path.startsWith('/__mock/'));
      expect(calls.map((item: { opencode_session: string }) => item.opencode_session)).toEqual([session, session]);
    }

    expect((await request.post(`${upstream}/__mock/scenario`, { data: { ...scenario, status: 404, failure_message: 'No available sellers for this model' } })).ok()).toBeTruthy();
    const failed = await request.post('/v1/chat/completions', {
      headers: { Authorization: `Bearer ${secret}`, 'X-PayLess-Chat-Id': `${model}-failed` },
      data: { model, messages: [{ role: 'user', content: 'all routes fail' }] },
    });
    expect(failed.ok()).toBeFalsy();
    expect(await failed.json()).toMatchObject({ error: { code: 'all_provider_attempts_failed', message: 'all provider attempts failed' } });
    stats = (await (await request.get('/api/requests?limit=100')).json()).data.find((item: { session_id: string }) => item.session_id === `${model}-failed`);
    expect(stats.error_code).toBe('model_not_found');
    expect(stats.error_message).toBe('all provider attempts failed');
    await page.goto('/#requests');
    await page.locator('#refresh-button').click();
    await page.locator(`#requests-table-body tr[data-request-id="${stats.id}"]`).click();
    await expect(page.locator('#request-detail > .modal-note')).toHaveText('Request failed: all provider attempts failed');
    await expect(page.locator('#request-detail')).not.toContainText('Terminal error: model_not_found');
    await expect(page.locator('#request-detail .attempt-list')).toContainText('model_not_found: No available sellers for this model');

    const current = await (await request.get('/api/providers/credentials')).json();
    for (const credential of current.data) {
      expect((await request.delete(`/api/providers/credentials/${credential.id}`)).ok()).toBeTruthy();
    }
  }
});

test('shows planned routes skipped by request capability rules', async ({ page, request }) => {
  const credentials = await (await request.get('/api/providers/credentials')).json();
  for (const credential of credentials.data) {
    expect((await request.delete(`/api/providers/credentials/${credential.id}`)).ok()).toBeTruthy();
  }
  const model = 'skipped-structured-model';
  expect((await request.post(`${upstream}/__mock/scenario`, { data: {
    models: [{ id: model, prompt_price: '0.000001', completion_price: '0.000002', context_length: 128000, max_completion_tokens: 4096 }],
    response_text: 'should not be called',
  } })).ok()).toBeTruthy();
  expect((await request.post(`${upstream}/__mock/fixtures`, { data: { files: [] } })).ok()).toBeTruthy();
  const metered = 'http://127.0.0.1:19476';
  expect((await request.post(`${metered}/__mock/scenario`, { data: {
    models: [{ id: model, prompt_price: '0.000001', completion_price: '0.000002', context_length: 128000, max_completion_tokens: 4096, supported_parameters: ['response_format'] }],
    response_text: 'fallback should fail',
  } })).ok()).toBeTruthy();
  const fallback = await request.post('/api/providers/credentials', { data: {
    provider: 'surplus', label: 'Skipped route fallback', api_key: 'mock-surplus-key',
  } });
  expect(fallback.status(), await fallback.text()).toBe(201);
  expect((await request.post(`${metered}/__mock/scenario`, { data: {
    models: [{ id: model, prompt_price: '0.000001', completion_price: '0.000002', context_length: 128000, max_completion_tokens: 4096, supported_parameters: ['response_format'] }],
    status: 503, failure_message: 'fallback unavailable',
  } })).ok()).toBeTruthy();
  const saved = await request.post('/api/providers/credentials', { data: {
    provider: 'opencode-go', label: 'Skipped route test', api_key: 'mock-opencode-key',
    base_url: `${upstream}/zen/go/v1`, access_mode: 'subscription', subscription_fee_usd: '10',
  } });
  expect(saved.status(), await saved.text()).toBe(201);
  const discovered = (await (await request.get('/api/models')).json()).data.filter((item: { model: string }) => item.model === model);
  expect(discovered, JSON.stringify(discovered)).toEqual(expect.arrayContaining([expect.objectContaining({ provider: 'opencode-go', model })]));
  const keyResponse = await request.post('/api/client-keys', { data: { label: 'skipped-route-e2e', harness: 'Other' } });
  expect(keyResponse.status()).toBe(201);
  const secret = (await keyResponse.json()).secret;
  const response = await request.post('/v1/chat/completions', {
    headers: { Authorization: `Bearer ${secret}` },
    data: { model, messages: [{ role: 'user', content: 'structured please' }], response_format: { type: 'json_object' } },
  });
  expect(response.ok()).toBeFalsy();
  const calls = (await (await request.get(`${upstream}/__mock/requests`)).json()).data.filter((item: { method: string; path: string }) => item.method === 'POST' && !item.path.startsWith('/__mock/'));
  expect(calls.filter((item: { path: string }) => item.path.startsWith('/zen/go/v1/'))).toHaveLength(0);
  const stats = (await (await request.get('/api/requests?limit=100')).json()).data.find((item: { model: string }) => item.model === model);
  expect(stats, JSON.stringify(stats)).toBeTruthy();
  expect(stats.skipped_routes, JSON.stringify(stats)).toEqual([expect.objectContaining({ provider: 'opencode-go', upstream_model: model, state: 'skipped', reason_code: 'missing_capability' })]);

  await page.goto('/#requests');
  await page.locator('#refresh-button').click();
  await page.locator(`#requests-table-body tr[data-request-id="${stats.id}"]`).click();
  await expect(page.locator('#request-detail')).toContainText('Skipped routes (1)');
  await expect(page.locator('#request-detail .skipped-routes')).toContainText('OpenCode Go');
  await expect(page.locator('#request-detail .skipped-routes')).toContainText('missing_capability');
  await expect(page.locator('#request-detail .skipped-routes')).toContainText('route does not support structured output');
  await expect(page.locator('#request-detail .skipped-routes .state-badge.skipped')).toHaveText('skipped');

  const current = await (await request.get('/api/providers/credentials')).json();
  for (const credential of current.data) {
    expect((await request.delete(`/api/providers/credentials/${credential.id}`)).ok()).toBeTruthy();
  }
});
