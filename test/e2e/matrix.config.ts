import { defineConfig } from '@playwright/test';

// matrix.config.ts — black-box format x provider matrix runner (NEV-59).
// Mock-only, zero spend. Two modes:
//   - local (default): Playwright starts 3 mock upstreams + app via webServer.
//   - docker (MATRIX_EXTERNAL=1): services come from docker-compose.matrix.yml
//     (real harness inside docker); set APP_BASE_URL + MOCK_*_PORT to match.
const appURL = process.env.APP_BASE_URL || 'http://127.0.0.1:19477';
const external = process.env.MATRIX_EXTERNAL === '1';

const openrouterPort = process.env.MOCK_OPENROUTER_PORT || '19474';
const surplusPort = process.env.MOCK_SURPLUS_PORT || '19475';
const opencodePort = process.env.MOCK_OPENCODE_PORT || '19476';

export default defineConfig({
  testDir: '.',
  testMatch: 'matrix.spec.ts',
  timeout: 30_000,
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: { baseURL: appURL, trace: 'retain-on-failure' },
  webServer: external ? [] : [
    { command: `go run ../../cmd/mockprovider -listen 127.0.0.1:${openrouterPort} -fixtures mocks/matrix`, url: `http://127.0.0.1:${openrouterPort}/healthz`, reuseExistingServer: true, timeout: 120_000 },
    { command: `go run ../../cmd/mockprovider -listen 127.0.0.1:${surplusPort} -fixtures mocks/matrix`, url: `http://127.0.0.1:${surplusPort}/healthz`, reuseExistingServer: true, timeout: 120_000 },
    { command: `go run ../../cmd/mockprovider -listen 127.0.0.1:${opencodePort} -fixtures mocks/matrix`, url: `http://127.0.0.1:${opencodePort}/healthz`, reuseExistingServer: true, timeout: 120_000 },
    { command: 'go run ../../cmd/paylessforai-app -data-dir "$PAYLESSFORAI_E2E_DATA_DIR" -listen 127.0.0.1:19477', env: { PAYLESSFORAI_E2E_DATA_DIR: process.env.PAYLESSFORAI_E2E_DATA_DIR || '/tmp/paylessforai-matrix' }, url: appURL + '/readyz', reuseExistingServer: true, timeout: 120_000 },
  ],
});
