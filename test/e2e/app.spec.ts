import { expect, test } from '@playwright/test';

test('configures providers, creates a client key, and routes an OpenAI request', async ({ page, request }) => {
  await request.post('http://127.0.0.1:19475/__mock/scenario', {
    data: {
      models: [{ id: 'model-a:free', name: 'Model A Free', prompt_price: '0', completion_price: '0', context_length: 128000, max_completion_tokens: 4096, supported_parameters: ['tools', 'response_format'] }],
      response_text: 'mock response',
      failure_count: 1,
    },
  });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'PayLessForAI' })).toBeVisible();

  await page.locator('#provider-name').selectOption('openrouter');
  await page.locator('#provider-label').fill('mock-openrouter');
  await page.locator('#provider-key').fill('mock-key');
  await page.getByRole('button', { name: 'Save credential' }).click();
  await expect(page.locator('#provider-list')).toContainText('openrouter');

  await page.locator('#provider-name').selectOption('surplus');
  await page.locator('#provider-label').fill('mock-surplus');
  await page.locator('#provider-key').fill('mock-key');
  await page.getByRole('button', { name: 'Save credential' }).click();
  await expect(page.locator('#provider-list')).toContainText('surplus');

  await page.locator('#key-label').fill('playwright');
  await page.getByRole('button', { name: 'Create key' }).click();
  const secretText = await page.locator('#new-key').textContent();
  const secret = secretText?.match(/plai_[0-9a-f]+/)?.[0];
  expect(secret).toBeTruthy();

  const response = await request.post('/v1/chat/completions', {
    headers: { Authorization: `Bearer ${secret}` },
    data: { model: 'model-a', messages: [{ role: 'user', content: 'hello' }] },
  });
  expect(response.ok()).toBeTruthy();
  expect((await response.json()).choices[0].message.content).toBe('mock response');
  const openRouterRequests = await (await request.get('http://127.0.0.1:19475/__mock/requests')).json();
  const surplusRequests = await (await request.get('http://127.0.0.1:19476/__mock/requests')).json();
  expect(openRouterRequests.data.some((item: { path: string; body: string }) => item.path.endsWith('/chat/completions') && item.body.includes('model-a:free'))).toBeTruthy();
  expect(surplusRequests.data.some((item: { path: string; body: string }) => item.path.endsWith('/chat/completions') && item.body.includes('model-a'))).toBeTruthy();
  await expect(page.locator('#status')).toContainText('Ready');
});

test('supports Responses and Anthropic Messages contracts', async ({ page, request }) => {
  await page.goto('/');
  await page.locator('#key-label').fill('protocols');
  await page.getByRole('button', { name: 'Create key' }).click();
  const secretText = await page.locator('#new-key').textContent();
  const secret = secretText?.match(/plai_[0-9a-f]+/)?.[0];
  expect(secret).toBeTruthy();

  const responses = await request.post('/v1/responses', {
    headers: { Authorization: `Bearer ${secret}` },
    data: { model: 'model-a', input: 'hello' },
  });
  expect(responses.ok()).toBeTruthy();
  expect((await responses.json()).object).toBe('response');

  const messages = await request.post('/v1/messages', {
    headers: { Authorization: `Bearer ${secret}`, 'anthropic-version': '2023-06-01' },
    data: { model: 'model-a', max_tokens: 32, messages: [{ role: 'user', content: 'hello' }] },
  });
  expect(messages.ok()).toBeTruthy();
  expect((await messages.json()).type).toBe('message');
});
