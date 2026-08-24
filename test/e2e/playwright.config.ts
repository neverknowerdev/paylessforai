import { defineConfig } from '@playwright/test';

const appURL = 'http://127.0.0.1:19477';

export default defineConfig({
  testDir: '.',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: { baseURL: appURL, trace: 'retain-on-failure' },
  webServer: [
    { command: 'go run ../../cmd/mockprovider -listen 127.0.0.1:19475', url: 'http://127.0.0.1:19475/healthz', reuseExistingServer: true, timeout: 120_000 },
    { command: 'go run ../../cmd/mockprovider -listen 127.0.0.1:19476', url: 'http://127.0.0.1:19476/healthz', reuseExistingServer: true, timeout: 120_000 },
    { command: 'go run ../../cmd/paylessforai -data-dir /tmp/paylessforai-e2e -listen 127.0.0.1:19477 -openrouter-base-url http://127.0.0.1:19475/openrouter/api/v1 -surplus-base-url http://127.0.0.1:19476/surplus/v1', url: appURL + '/readyz', reuseExistingServer: true, timeout: 120_000 },
  ],
});
