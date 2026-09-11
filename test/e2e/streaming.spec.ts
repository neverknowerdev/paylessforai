import { expect, test, type APIRequestContext } from '@playwright/test';

const mockPort = 19474;
const provider = 'streaming-e2e-provider';
const secretLabel = 'streaming-e2e-key';

async function configureProvider(request: APIRequestContext) {
  const credentials = await (await request.get('/api/providers/credentials')).json();
  await Promise.all((credentials.data as Array<{ id: string }>).map(async (credential) => {
    const response = await request.delete(`/api/providers/credentials/${credential.id}`);
    expect(response.ok()).toBeTruthy();
  }));

  const scenario = await request.post(`http://127.0.0.1:${mockPort}/__mock/scenario`, {
    data: {
      models: ['chat', 'responses', 'anthropic'].map((format) => ({
        id: `streaming-e2e-${format}-model`,
        name: `Streaming E2E ${format} Model`,
        prompt_price: '0.000001',
        completion_price: '0.000002',
        context_length: 128000,
        max_completion_tokens: 4096,
        supported_parameters: ['tools', 'response_format'],
        input_modalities: ['text'],
        output_modalities: ['text'],
        supported_features: ['streaming'],
      })),
      response_text: 'streaming works',
      stream: true,
      stream_wait: true,
      input_tokens: 7,
      output_tokens: 3,
    },
  });
  expect(scenario.ok()).toBeTruthy();

  const credential = await request.post('/api/providers/credentials', {
    data: {
      provider,
      label: 'Streaming E2E Provider',
      api_key: 'streaming-e2e-provider-key',
      base_url: `http://127.0.0.1:${mockPort}/streaming-e2e/v1`,
      access_mode: 'api',
    },
  });
  expect(credential.status()).toBe(201);

  const key = await request.post('/api/client-keys', { data: { label: secretLabel, harness: 'Other' } });
  expect(key.status()).toBe(201);
  return (await key.json()).secret as string;
}

async function readUntilFrame(reader: ReadableStreamDefaultReader<Uint8Array>, decoder: TextDecoder, pending: { value: string }) {
  while (!pending.value.includes('\n\n')) {
    const next = await reader.read();
    if (next.done) break;
    pending.value += decoder.decode(next.value, { stream: true });
  }
  const boundary = pending.value.indexOf('\n\n');
  if (boundary < 0) return null;
  const frame = pending.value.slice(0, boundary);
  pending.value = pending.value.slice(boundary + 2);
  return frame;
}

function dataPayload(frame: string) {
  const data = frame.split('\n').find((line) => line.startsWith('data:'))?.slice(5).trim();
  return data && data !== '[DONE]' ? JSON.parse(data) as Record<string, unknown> : data;
}

function textDelta(frame: string) {
  const payload = dataPayload(frame);
  if (!payload || typeof payload === 'string') return '';
  const choices = payload.choices as Array<{ delta?: { content?: string } }> | undefined;
  if (choices?.[0]?.delta?.content) return choices[0].delta.content;
  if (typeof payload.delta === 'string') return payload.delta;
  const delta = payload.delta as { text?: string } | undefined;
  return delta?.text ?? '';
}

test('streams real SSE incrementally for Chat, Responses, and Anthropic clients', async ({ request }) => {
  const secret = await configureProvider(request);
  const cases = [
    { name: 'chat', path: '/v1/chat/completions', body: { model: 'streaming-e2e-chat-model', messages: [{ role: 'user', content: 'hello' }], stream: true } },
    { name: 'responses', path: '/v1/responses', body: { model: 'streaming-e2e-responses-model', input: 'hello', stream: true } },
    { name: 'anthropic', path: '/v1/messages', body: { model: 'streaming-e2e-anthropic-model', max_tokens: 64, messages: [{ role: 'user', content: 'hello' }], stream: true } },
  ] as const;

  for (const testCase of cases) {
    const reset = await request.post(`http://127.0.0.1:${mockPort}/__mock/reset`);
    expect(reset.ok()).toBeTruthy();
    const scenario = await request.post(`http://127.0.0.1:${mockPort}/__mock/scenario`, {
      data: { response_text: 'streaming works', stream: true, stream_wait: true, input_tokens: 7, output_tokens: 3 },
    });
    expect(scenario.ok()).toBeTruthy();

    const response = await fetch(`http://127.0.0.1:19477${testCase.path}`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${secret}`, 'Content-Type': 'application/json' },
      body: JSON.stringify(testCase.body),
    });
    expect(response.status).toBe(200);
    expect(response.headers.get('content-type')).toContain('text/event-stream');
    if (!response.body) throw new Error('streaming response has no body');

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    const pending = { value: '' };
    const firstFrame = await readUntilFrame(reader, decoder, pending);
    expect(firstFrame).toBeTruthy();
    const firstPayload = dataPayload(firstFrame!);
    expect(JSON.stringify(firstPayload)).toContain('stream');

    // The mock provider waits after flushing its first chunk. Receiving this
    // frame proves the gateway did not buffer the upstream response.
    const release = await request.post(`http://127.0.0.1:${mockPort}/__mock/stream/release`);
    expect(release.ok()).toBeTruthy();

    const frames: string[] = [firstFrame!];
    for (;;) {
      const frame = await readUntilFrame(reader, decoder, pending);
      if (frame === null) break;
      frames.push(frame);
      if (frame.includes('[DONE]') || frame.includes('message_stop') || frame.includes('response.completed')) break;
    }
    const body = frames.join('\n\n');
    expect(frames.map(textDelta).join('')).toBe('streaming works');
    if (testCase.name === 'chat') expect(body).toContain('[DONE]');
    if (testCase.name === 'responses') expect(body).toContain('response.completed');
    if (testCase.name === 'anthropic') expect(body).toContain('message_stop');
    await reader.cancel();
  }
});
