import { defineConfig } from '@playwright/test';

// matrix.config.ts — black-box format x provider matrix runner (NEV-59).
// Three deterministic mock upstreams (openrouter/surplus/opencode-go roles)
// plus the real app binary. All serve from mocks/matrix fixtures; zero spend.
const appURL = 'http://127.0.0.1:19477';

export default defineConfig({
  testDir: '.',
  testMatch: 'matrix.spec.ts',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: { baseURL: appURL, trace: 'retain-on-failure' },
  webServer: [
    { command: 'go run ../../cmd/mockprovider -listen 127.0.0.1:19474 -fixtures mocks/matrix', url: 'http://127.0.0.1:19474/healthz', reuseExistingServer: true, timeout: 120_000 },
    { command: 'go run ../../cmd/mockprovider -listen 127.0.0.1:19475 -fixtures mocks/matrix', url: 'http://127.0.0.1:19475/healthz', reuseExistingServer: true, timeout: 120_000 },
    { command: 'go run ../../cmd/mockprovider -listen 127.0.0.1:19476 -fixtures mocks/matrix', url: 'http://127.0.0.1:19476/healthz', reuseExistingServer: true, timeout: 120_000 },
    { command: 'go run ../../cmd/paylessforai-app -data-dir "$PAYLESSFORAI_E2E_DATA_DIR" -listen 127.0.0.1:19477', env: { PAYLESSFORAI_E2E_DATA_DIR: process.env.PAYLESSFORAI_E2E_DATA_DIR || '/tmp/paylessforai-matrix' }, url: appURL + '/readyz', reuseExistingServer: true, timeout: 120_000 },
  ],
});
