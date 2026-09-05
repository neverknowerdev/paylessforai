import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: '.', testMatch: 'catalog.spec.ts', workers: 1,
  use: { baseURL: 'http://127.0.0.1:19487' },
  webServer: { command: 'go run ../../cmd/paylessforai-app -data-dir $(mktemp -d /tmp/payless-catalog-ui.XXXXXX) -listen 127.0.0.1:19487', url: 'http://127.0.0.1:19487/readyz', timeout: 120_000 },
});
